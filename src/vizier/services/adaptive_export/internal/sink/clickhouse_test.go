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

package sink

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

func canonicalAttribution() AttributionRow {
	t0 := time.Unix(0, 1744477360303026359).UTC()
	return AttributionRow{
		AnomalyHash: anomaly.Hash(anomaly.Target{
			PID: 106040, Comm: "redis-server",
			Pod: "redis-578d5dc9bd-kjj78", Namespace: "redis",
		}),
		Namespace:  "redis",
		Pod:        "redis-578d5dc9bd-kjj78",
		Comm:       "redis-server",
		PID:        106040,
		Hostname:   "node-1",
		TStart:     t0.Add(-5 * time.Minute),
		TEnd:       t0.Add(5 * time.Minute),
		LastSeen:   t0,
		LastRuleID: "R1005",
		NAnomalies: 1,
	}
}

// TestSink_Write_PostsCorrectQueryAndBody — INSERT targets the right
// table; body is one JSON object per line with all attribution fields.
func TestSink_Write_PostsCorrectQueryAndBody(t *testing.T) {
	var gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	row := canonicalAttribution()
	if err := s.Write(context.Background(), []AttributionRow{row}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "INSERT INTO forensic_db.adaptive_attribution FORMAT JSONEachRow"
	if gotQuery != want {
		t.Fatalf("query = %q, want %q", gotQuery, want)
	}
	for _, needle := range []string{
		`"anomaly_hash":"` + string(row.AnomalyHash) + `"`,
		`"namespace":"redis"`,
		`"pod":"redis-578d5dc9bd-kjj78"`,
		`"comm":"redis-server"`,
		`"pid":106040`,
		`"hostname":"node-1"`,
		`"last_rule_id":"R1005"`,
		`"n_anomalies":1`,
	} {
		if !strings.Contains(gotBody, needle) {
			t.Fatalf("body missing %q; body=%s", needle, gotBody)
		}
	}
	if !strings.Contains(gotBody, `"t_start":"2025-04-12 16:57:40.303026359"`) {
		t.Fatalf("t_start not formatted as DateTime64 string; body=%s", gotBody)
	}
}

// TestSink_Write_EmptyBatch — no HTTP call.
func TestSink_Write_EmptyBatch(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("Write empty: %v", err)
	}
	if called {
		t.Fatalf("empty Write made an HTTP call")
	}
}

// TestSink_Write_HTTPErrorPropagates — non-2xx returns Go error.
func TestSink_Write_HTTPErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("clickhouse exploded"))
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	err := s.Write(context.Background(), []AttributionRow{canonicalAttribution()})
	if err == nil {
		t.Fatalf("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error should mention 503: %v", err)
	}
}

// TestSink_QueryActive_BuildsCorrectSQL — boot rehydration query.
func TestSink_QueryActive_BuildsCorrectSQL(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"anomaly_hash":"abc","namespace":"redis","pod":"redis-x","comm":"redis-server","pid":106040,"hostname":"node-1","t_start_ns":"1744477060303026359","t_end_ns":"1744477660303026359","last_seen_ns":"1744477360303026359","last_rule_id":"R1005","n_anomalies":1}` + "\n"))
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	rows, err := s.QueryActive(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("QueryActive: %v", err)
	}
	if !strings.Contains(seenQuery, "FROM forensic_db.adaptive_attribution FINAL") {
		t.Fatalf("missing FINAL: %q", seenQuery)
	}
	if !strings.Contains(seenQuery, "hostname = 'node-1'") {
		t.Fatalf("missing hostname filter: %q", seenQuery)
	}
	if !strings.Contains(seenQuery, "t_end > now64(9)") {
		t.Fatalf("missing t_end > now64 filter: %q", seenQuery)
	}
	if len(rows) != 1 || rows[0].AnomalyHash != "abc" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].PID != 106040 {
		t.Fatalf("PID = %d", rows[0].PID)
	}
	if rows[0].TStart.UnixNano() != 1744477060303026359 {
		t.Fatalf("TStart wrong: %v", rows[0].TStart)
	}
}

// TestSink_QueryActive_RequiresHostname — defensive guard.
func TestSink_QueryActive_RequiresHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	if _, err := s.QueryActive(context.Background(), ""); err == nil {
		t.Fatalf("empty hostname should error")
	}
}

// TestSink_QuoteEscape — single quotes in hostname survive injection-safely.
func TestSink_QuoteEscape(t *testing.T) {
	if got := quoteCH("o'malley"); got != `'o\'malley'` {
		t.Fatalf("quoteCH = %q, want 'o\\'malley'", got)
	}
}

