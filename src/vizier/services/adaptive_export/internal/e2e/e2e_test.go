// Copyright 2018- The Pixie Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package e2e wires the real Trigger + real Sink (both HTTP-backed)
// to a stub ClickHouse in-process and exercises the full
// kubescape→attribution path end-to-end. This is the highest-fidelity
// test that runs in `go test`. Real-cluster validation lives on the
// lab.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/controller"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/trigger"
)

// stubClickHouse emulates ClickHouse's HTTP interface: GET responds
// with a fixed kubescape_logs JSONEachRow body; POST records the
// INSERT body for later assertion.
type stubClickHouse struct {
	mu          sync.Mutex
	kubescape   []map[string]any
	insertedSQL []string
	insertBody  [][]byte
}

func (s *stubClickHouse) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")
	switch r.Method {
	case http.MethodGet:
		if !strings.Contains(q, "FROM forensic_db.kubescape_logs") {
			http.Error(w, "unexpected SELECT: "+q, 400)
			return
		}
		if !strings.Contains(q, "hostname = 'node-1'") {
			http.Error(w, "missing hostname filter: "+q, 400)
			return
		}
		s.mu.Lock()
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		for _, row := range s.kubescape {
			_ = enc.Encode(row)
		}
		s.mu.Unlock()
		w.WriteHeader(200)
		_, _ = w.Write(buf.Bytes())
	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.insertedSQL = append(s.insertedSQL, q)
		s.insertBody = append(s.insertBody, body)
		s.mu.Unlock()
		w.WriteHeader(200)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (s *stubClickHouse) bodies() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.insertBody))
	for i, b := range s.insertBody {
		out[i] = append([]byte{}, b...)
	}
	return out
}

func canonicalKubescapeRow() map[string]any {
	return map[string]any{
		"RuleID":                "R1005",
		"RuntimeK8sDetails":     `{"podName":"redis-578d5dc9bd-kjj78","podNamespace":"redis"}`,
		"RuntimeProcessDetails": `{"processTree":{"pid":106040,"comm":"redis-server"}}`,
		"event_time":            "1744477360303026359",
		"hostname":              "node-1",
	}
}

// TestE2E_PushFlow_AttributionRowArrives — full chain: stub-CH serves a
// kubescape row → real Trigger discovers and parses → real Controller
// computes hash + opens active row → real Sink HTTP-POSTs INSERT to
// adaptive_attribution. Assert the resulting body carries the right hash.
func TestE2E_PushFlow_AttributionRowArrives(t *testing.T) {
	stub := &stubClickHouse{kubescape: []map[string]any{canonicalKubescapeRow()}}
	srv := httptest.NewServer(http.HandlerFunc(stub.handle))
	defer srv.Close()

	trg, err := trigger.New(trigger.Config{
		Endpoint:     srv.URL,
		Hostname:     "node-1",
		PollInterval: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("trigger.New: %v", err)
	}
	snk, err := sink.New(sink.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("sink.New: %v", err)
	}
	cfg := controller.Config{Hostname: "node-1", Before: time.Minute, After: time.Minute}
	ctl := controller.New(trg, snk, cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ctl.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("controller did not stop within 2s of cancel")
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(stub.bodies()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	bodies := stub.bodies()
	if len(bodies) == 0 {
		t.Fatalf("no INSERTs reached stub-CH within 2s")
	}

	wantHash := string(anomaly.Hash(anomaly.Target{
		PID: 106040, Comm: "redis-server",
		Pod: "redis-578d5dc9bd-kjj78", Namespace: "redis",
	}))
	matched := false
	for _, b := range bodies {
		if strings.Contains(string(b), `"anomaly_hash":"`+wantHash+`"`) &&
			strings.Contains(string(b), `"hostname":"node-1"`) &&
			strings.Contains(string(b), `"namespace":"redis"`) &&
			strings.Contains(string(b), `"pid":106040`) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("no INSERT body had the expected attribution shape; bodies=\n%s", joinBodies(bodies))
	}
}

func joinBodies(bs [][]byte) string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = string(b)
	}
	return strings.Join(out, "\n---\n")
}
