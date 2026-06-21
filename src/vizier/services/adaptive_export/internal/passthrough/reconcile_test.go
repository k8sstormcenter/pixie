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

package passthrough

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/reconcile"
	sinkpkg "px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
)

// capRec captures every reconcile.Row for assertions.
type capRec struct{ rows []reconcile.Row }

func (c *capRec) Record(_ context.Context, r reconcile.Row) { c.rows = append(c.rows, r) }

// tableQuerier returns a fixed row count per pixie table, keyed by the
// `table='X'` token QueryFor embeds in the PxL. An entry of -1 means the
// query itself fails (to exercise the read-error branch).
type tableQuerier struct{ n map[string]int }

func (q tableQuerier) Query(_ context.Context, src string) ([]map[string]any, error) {
	for tbl, n := range q.n {
		if strings.Contains(src, "table='"+tbl+"'") {
			if n < 0 {
				return nil, errors.New("boom")
			}
			rows := make([]map[string]any, n)
			for i := range rows {
				rows[i] = map[string]any{"time_": int64(i)}
			}
			return rows, nil
		}
	}
	return nil, nil
}

// failSink fails WritePixieRows for tables in `fail`, succeeds otherwise.
type failSink struct{ fail map[string]bool }

func (s failSink) WritePixieRows(_ context.Context, table string, _ []map[string]any) error {
	if s.fail[table] {
		return errors.New("sink down")
	}
	return nil
}

// TestTick_ReconcileRecordsReadVsWrote is the scientific check of the
// passthrough write-fidelity instrument: for every table pulled in a tick,
// exactly one reconcile.Row must be emitted, and its (ReadCount, WroteCount)
// must reflect the actual read/write outcome — the basis for localizing
// loss to query (read<pem) vs sink (wrote<read).
func TestTick_ReconcileRecordsReadVsWrote(t *testing.T) {
	rec := &capRec{}
	loop := New(
		tableQuerier{n: map[string]int{
			"http_events":  3,  // read 3, write ok → wrote 3
			"dns_events":   0,  // read 0, write skipped → wrote 0
			"conn_stats":   5,  // read 5, sink fails → wrote 0
			"pgsql_events": -1, // query fails → read 0, wrote 0
		}},
		failSink{fail: map[string]bool{"conn_stats": true}},
		Config{
			Window:   60 * time.Second,
			Tables:   []string{"http_events", "dns_events", "conn_stats", "pgsql_events"},
			Rec:      rec,
			Hostname: "node-test",
		},
	)
	loop.tick(context.Background())

	got := map[string][2]int64{}
	for _, r := range rec.rows {
		if r.Mode != "passthrough" {
			t.Fatalf("Mode=%q want passthrough", r.Mode)
		}
		if r.Hostname != "node-test" {
			t.Fatalf("Hostname=%q want node-test", r.Hostname)
		}
		if !r.WinEnd.After(r.WinStart) {
			t.Fatalf("table %s: WinEnd %v not after WinStart %v", r.Table, r.WinEnd, r.WinStart)
		}
		got[r.Table] = [2]int64{r.ReadCount, r.WroteCount}
	}
	want := map[string][2]int64{
		"http_events":  {3, 3}, // read==wrote: clean
		"dns_events":   {0, 0}, // empty: read 0
		"conn_stats":   {5, 0}, // SINK DROP: read 5, wrote 0  ← R6 signal
		"pgsql_events": {0, 0}, // query error: read 0
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %d tables, want %d (rows=%+v)", len(got), len(want), rec.rows)
	}
	for tbl, w := range want {
		if got[tbl] != w {
			t.Errorf("table %s: (read,wrote)=%v want %v", tbl, got[tbl], w)
		}
	}

	// conn_stats must show read>wrote — the exact shape a sink-drop bug
	// produces, which a count-only check would miss.
	if r := got["conn_stats"]; r[0] <= r[1] {
		t.Errorf("conn_stats read(%d) must exceed wrote(%d) on sink failure", r[0], r[1])
	}
}

// TestNew_DefaultsRecorderToNop proves the instrument is OFF (no panic on a
// nil Recorder) unless explicitly wired.
func TestNew_DefaultsRecorderToNop(t *testing.T) {
	loop := New(tableQuerier{n: map[string]int{"http_events": 1}}, failSink{},
		Config{Window: time.Second, Tables: []string{"http_events"}})
	// Must not panic with Rec unset.
	loop.tick(context.Background())
}

// TestTick_ReconcileCatchesCHSilentDrop — the production-meaningful
// counterpart to TestTick_ReconcileRecordsReadVsWrote: replaces the
// in-process fake sink with a real sink.ClickHouseHTTP pointed at an
// httptest server that mimics CH's X-ClickHouse-Summary silent-drop
// shape (200 OK + written_rows=0 in the header). The loop must see
// the silent drop as an error (sink.summaryWroteFewerThan returns
// non-nil) and record WroteCount=0, ReadCount=N. This is the EXACT
// regression an R6 (sink-layer loss) reconcile run must detect; the
// fake-sink test only proves the wiring, this test proves the chain
// works end-to-end.
func TestTick_ReconcileCatchesCHSilentDrop(t *testing.T) {
	const (
		table = "http_events"
		nRows = 5
	)
	// Counter so we can assert the loop actually called the sink once
	// (one tick × one table = one POST).
	var posts atomic.Int32
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		// Emulate CH's silent-drop response: 200 OK with summary that
		// says "0 rows written" despite a non-empty body. AE's sink
		// turns this into a Go error via summaryWroteFewerThan.
		w.Header().Set("X-ClickHouse-Summary", `{"written_rows":"0"}`)
		w.WriteHeader(http.StatusOK)
	}))
	defer ch.Close()

	s, err := sinkpkg.New(sinkpkg.Config{Endpoint: ch.URL, Database: "forensic_db"})
	if err != nil {
		t.Fatalf("sink.New: %v", err)
	}
	rec := &capRec{}
	loop := New(
		tableQuerier{n: map[string]int{table: nRows}},
		s,
		Config{
			Window:   60 * time.Second,
			Tables:   []string{table},
			Rec:      rec,
			Hostname: "node-test",
		},
	)
	loop.tick(context.Background())

	if posts.Load() != 1 {
		t.Fatalf("CH endpoint hit %d times, want 1", posts.Load())
	}
	if len(rec.rows) != 1 {
		t.Fatalf("recorded %d reconcile rows, want 1", len(rec.rows))
	}
	row := rec.rows[0]
	if row.Table != table {
		t.Fatalf("Table=%q want %q", row.Table, table)
	}
	if row.ReadCount != int64(nRows) {
		t.Fatalf("ReadCount=%d, want %d (read from querier)", row.ReadCount, nRows)
	}
	if row.WroteCount != 0 {
		t.Fatalf("WroteCount=%d, want 0 (CH silent-drop must land here, not at %d)", row.WroteCount, nRows)
	}
	if !strings.Contains(row.WriteErr, "silent drop") && !strings.Contains(row.WriteErr, "written_rows") {
		t.Fatalf("WriteErr=%q, want CH silent-drop attribution", row.WriteErr)
	}
}

// TestTick_ReconcileAttributesCHFailureCorrectly — the dual to
// CHSilentDrop: when CH returns an actual 5xx, the loop must record
// the same (read=N, wrote=0) shape with a different WriteErr. Proves
// the loop's read-count vs wrote-count split is sink-error-agnostic
// (it's the COUNT that matters for R6 attribution, not the specific
// failure mode).
func TestTick_ReconcileAttributesCHFailureCorrectly(t *testing.T) {
	const (
		table = "dns_events"
		nRows = 7
	)
	ch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Memory limit exceeded"))
	}))
	defer ch.Close()

	s, err := sinkpkg.New(sinkpkg.Config{Endpoint: ch.URL, Database: "forensic_db"})
	if err != nil {
		t.Fatalf("sink.New: %v", err)
	}
	rec := &capRec{}
	loop := New(
		tableQuerier{n: map[string]int{table: nRows}},
		s,
		Config{
			Window:   60 * time.Second,
			Tables:   []string{table},
			Rec:      rec,
			Hostname: "node-test",
		},
	)
	loop.tick(context.Background())

	if len(rec.rows) != 1 {
		t.Fatalf("recorded %d reconcile rows, want 1", len(rec.rows))
	}
	row := rec.rows[0]
	if row.ReadCount != int64(nRows) || row.WroteCount != 0 {
		t.Fatalf("got (read,wrote)=(%d,%d) want (%d,0)", row.ReadCount, row.WroteCount, nRows)
	}
	if !strings.Contains(row.WriteErr, "500") && !strings.Contains(row.WriteErr, "Memory") {
		t.Fatalf("WriteErr=%q, want 500/Memory attribution", row.WriteErr)
	}
}
