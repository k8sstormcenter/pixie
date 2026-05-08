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

package trigger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const canonicalRowJSON = `{"RuleID":"R1005","RuntimeK8sDetails":"{\"podName\":\"redis-578d5dc9bd-kjj78\",\"podNamespace\":\"redis\"}","RuntimeProcessDetails":"{\"processTree\":{\"pid\":106040,\"comm\":\"redis-server\"}}","event_time":"1744477360303026359","hostname":"node-1"}`

// TestTrigger_Polls_HostnameAndWatermark — query carries WHERE hostname=… AND event_time>… .
func TestTrigger_Polls_HostnameAndWatermark(t *testing.T) {
	var lastQuery string
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		lastQuery = r.URL.Query().Get("query")
		if calls == 1 {
			_, _ = w.Write([]byte(canonicalRowJSON + "\n"))
			return
		}
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()
	tr, err := New(Config{Endpoint: srv.URL, Hostname: "node-1", PollInterval: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)
	select {
	case ev := <-ch:
		if ev.Target.Pod != "redis-578d5dc9bd-kjj78" {
			t.Fatalf("Pod = %q", ev.Target.Pod)
		}
		if ev.Target.PID != 106040 {
			t.Fatalf("PID = %d", ev.Target.PID)
		}
		if ev.Hostname != "node-1" {
			t.Fatalf("Hostname = %q", ev.Hostname)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for first event")
	}
	// Wait for at least one more poll so we can assert watermark.
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(lastQuery, "hostname = 'node-1'") {
		t.Fatalf("query missing hostname filter: %q", lastQuery)
	}
	if !strings.Contains(lastQuery, "event_time >= 1744477360303026359") {
		t.Fatalf("watermark didn't advance to inclusive boundary: %q", lastQuery)
	}
}

// TestTrigger_RequiresHostname — defensive: refuses empty hostname.
func TestTrigger_RequiresHostname(t *testing.T) {
	if _, err := New(Config{Endpoint: "http://x", Hostname: ""}); err == nil {
		t.Fatalf("empty Hostname not rejected")
	}
}

// TestTrigger_ContextCancellationClosesChannel — clean shutdown.
func TestTrigger_ContextCancellationClosesChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	tr, _ := New(Config{Endpoint: srv.URL, Hostname: "node-1", PollInterval: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := tr.Subscribe(ctx)
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("channel produced after cancel")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("channel not closed within 300ms of cancel")
	}
}

// TestTrigger_HTTPErrorContinues — transient 5xx → retry, system stable.
func TestTrigger_HTTPErrorContinues(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(canonicalRowJSON + "\n"))
	}))
	defer srv.Close()
	tr, _ := New(Config{Endpoint: srv.URL, Hostname: "node-1", PollInterval: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)
	select {
	case ev := <-ch:
		if ev.Target.Comm == "" {
			t.Fatalf("got empty Target after recovery")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("trigger did not recover from transient HTTP 503")
	}
}

// TestTrigger_DedupesAtWatermarkBoundary — same-event_time rows that
// arrive in a later poll than they were already observed must NOT be
// re-emitted. Distinct rows at the same boundary timestamp must still
// be emitted (only the duplicate is suppressed).
func TestTrigger_DedupesAtWatermarkBoundary(t *testing.T) {
	const distinctRowJSON = `{"RuleID":"R0006","RuntimeK8sDetails":"{\"podName\":\"redis-578d5dc9bd-kjj78\",\"podNamespace\":\"redis\"}","RuntimeProcessDetails":"{\"processTree\":{\"pid\":222222,\"comm\":\"redis-cli\"}}","event_time":"1744477360303026359","hostname":"node-1"}`
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		switch n {
		case 1:
			// First poll emits the canonical row.
			_, _ = w.Write([]byte(canonicalRowJSON + "\n"))
		case 2:
			// Second poll: server "re-discovers" the SAME row at the
			// boundary timestamp PLUS one DISTINCT row at the same
			// event_time. The trigger must suppress the duplicate
			// fingerprint and pass through the distinct one.
			_, _ = w.Write([]byte(canonicalRowJSON + "\n" + distinctRowJSON + "\n"))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	defer srv.Close()

	tr, _ := New(Config{Endpoint: srv.URL, Hostname: "node-1", PollInterval: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	// Collect events for ~250 ms — long enough for at least 3 polls.
	deadline := time.Now().Add(250 * time.Millisecond)
	var got []uint64 // PIDs we observed
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			got = append(got, ev.Target.PID)
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Expect exactly 2 events: PID 106040 (canonical, emitted once
	// even though server returned it twice) and PID 222222 (distinct
	// row at same boundary, emitted exactly once).
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (canonical + distinct, no dup); pids=%v", len(got), got)
	}
	canonicalSeen, distinctSeen := 0, 0
	for _, pid := range got {
		switch pid {
		case 106040:
			canonicalSeen++
		case 222222:
			distinctSeen++
		}
	}
	if canonicalSeen != 1 {
		t.Fatalf("canonical row emitted %dx, want 1 (dedup failed)", canonicalSeen)
	}
	if distinctSeen != 1 {
		t.Fatalf("distinct same-event_time row emitted %dx, want 1 (over-aggressive dedup)", distinctSeen)
	}
}

// TestTrigger_RejectsInvalidIdentifiers — defensive: SQL injection via
// Database/Table config is refused at construction time.
func TestTrigger_RejectsInvalidIdentifiers(t *testing.T) {
	for _, bad := range []string{
		"forensic_db; DROP TABLE alerts",
		"db with space",
		"123starts_with_digit",
		"backtick`injection",
		"forensic_db.kubescape_logs", // dotted not allowed for this table param
	} {
		_, err := New(Config{Endpoint: "http://x", Hostname: "node-1", Database: bad})
		if err == nil {
			t.Errorf("New accepted bad Database %q; expected error", bad)
		}
		_, err = New(Config{Endpoint: "http://x", Hostname: "node-1", Table: bad})
		if err == nil {
			t.Errorf("New accepted bad Table %q; expected error", bad)
		}
	}
}

// TestTrigger_BadRowSkipped — incomplete kubescape row is skipped, good rows still arrive.
func TestTrigger_BadRowSkipped(t *testing.T) {
	bad := `{"RuleID":"","RuntimeK8sDetails":"","RuntimeProcessDetails":"","event_time":"1","hostname":"node-1"}` + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bad + canonicalRowJSON + "\n"))
	}))
	defer srv.Close()
	tr, _ := New(Config{Endpoint: srv.URL, Hostname: "node-1", PollInterval: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)
	select {
	case ev := <-ch:
		if ev.Target.Comm != "redis-server" {
			t.Fatalf("got Comm %q; bad row leaked through", ev.Target.Comm)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("good row not received after bad-row skip")
	}
}
