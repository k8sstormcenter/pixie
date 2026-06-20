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
	"strings"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/reconcile"
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
