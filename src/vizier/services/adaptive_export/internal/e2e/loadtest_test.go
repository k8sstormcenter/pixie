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

// L1 — hermetic load-test layer for the AE write surface.
//
// This is the deterministic, in-process counterpart to the live (L3) rig
// experiments in /home/croedig/pixie/aeload. It exercises the SAME real
// Trigger + Controller + Sink chain as e2e_test.go, but feeds Pixie's data
// plane from a MOCK PixieQuerier returning a CANNED row set. Both the kubescape
// trigger fixture and the Pixie capture are therefore fully controlled, so the
// AE write surface — control plane (adaptive_attribution) AND data plane
// (per-protocol-table rows + bytes) — is a pure function of the inputs.
//
// Reproducibility is proven by running the whole chain REPS times and asserting
// that every per-table row count, byte total, and the attribution count is
// identical across all reps (std = 0 / a single distinct value). Single-pull is
// forced via PushRefreshInterval = -1 (single-shot), the same effect the L3
// config achieves on the rig — so the non-deduping MergeTree protocol tables
// never get duplicate re-inserts.
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/controller"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/trigger"
)

// newStubServer starts an httptest server backed by the stub-CH handler.
func newStubServer(s *stubClickHouse) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(s.handle))
}

// sqls returns a copy of the recorded INSERT query strings, index-aligned with
// bodies().
func (s *stubClickHouse) sqls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.insertedSQL))
	copy(out, s.insertedSQL)
	return out
}

// fixedClock pins now() so the window math is identical every rep.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// cannedQuerier is the mock Pixie data plane: it returns a fixed number of
// fixed rows per protocol table, parsed from the table name embedded in the
// PxL (px.DataFrame(table='<t>')). Everything else returns 0 rows — exactly how
// a silent pod looks to real Pixie.
type cannedQuerier struct {
	perTable map[string]int // table -> row count to synthesize
}

var tableInPxL = regexp.MustCompile(`table='([^']+)'`)

func (q *cannedQuerier) Query(_ context.Context, pxl string) ([]map[string]any, error) {
	m := tableInPxL.FindStringSubmatch(pxl)
	if m == nil {
		return nil, fmt.Errorf("cannedQuerier: no table in pxl: %s", pxl)
	}
	n := q.perTable[m[1]]
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		// Deterministic, fully-specified row. encoding/json sorts map keys,
		// so the serialized bytes are byte-identical every rep.
		rows = append(rows, map[string]any{
			"time_":     1744477360303026359 + int64(i),
			"namespace": "aeload",
			"pod":       "aeload/gen-l1",
			"req_path":  fmt.Sprintf("/ping/%d", i),
			"table":     m[1],
		})
	}
	return rows, nil
}

// counts holds the per-rep measurement of what reached "ClickHouse".
type counts struct {
	rowsByTable  map[string]int
	bytesByTable map[string]int
	attribution  int
}

// measure parses the stub-CH insert bodies into per-table row/byte counts.
func measure(sqls []string, bodies [][]byte) counts {
	c := counts{rowsByTable: map[string]int{}, bytesByTable: map[string]int{}}
	insertRe := regexp.MustCompile(`INSERT INTO forensic_db\.(\w+) FORMAT JSONEachRow`)
	for i, q := range sqls {
		m := insertRe.FindStringSubmatch(q)
		if m == nil {
			continue
		}
		table := m[1]
		body := bodies[i]
		nrows := 0
		for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				nrows++
			}
		}
		if table == "adaptive_attribution" {
			c.attribution += nrows
			continue
		}
		c.rowsByTable[table] += nrows
		c.bytesByTable[table] += len(body)
	}
	return c
}

// runOnce drives the full Trigger→Controller→Sink chain against a fresh stub-CH
// serving exactly one kubescape row, with the canned Pixie data plane, and
// returns the measured AE write surface.
func runOnce(t *testing.T, perTable map[string]int) counts {
	t.Helper()
	stub := &stubClickHouse{kubescape: []map[string]any{canonicalKubescapeRow()}}
	srv := newStubServer(stub)
	defer srv.Close()

	trg, err := trigger.New(trigger.Config{
		Endpoint:     srv.URL,
		Hostname:     "node-1",
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("trigger.New: %v", err)
	}
	snk, err := sink.New(sink.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("sink.New: %v", err)
	}

	tables := make([]string, 0, len(perTable))
	for tn := range perTable {
		tables = append(tables, tn)
	}
	cfg := controller.Config{
		Hostname:            "node-1",
		Before:              time.Minute,
		After:               time.Minute,
		PushPixieTables:     tables,
		PushRefreshInterval: -1, // single-shot: exactly one pull, no MergeTree dup inflation
	}
	clk := fixedClock{t: time.Unix(1744477370, 0)} // > event_time, so window is open
	ctl := controller.New(trg, snk, cfg, clk).WithPixieQuerier(&cannedQuerier{perTable: perTable})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ctl.Run(ctx); close(done) }()

	// Wait until the attribution row AND all expected protocol-table inserts
	// have landed (or timeout). Expected protocol inserts = one per table with
	// a non-zero canned count.
	wantTables := 0
	for _, n := range perTable {
		if n > 0 {
			wantTables++
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c := measure(stub.sqls(), stub.bodies())
		if c.attribution >= 1 && len(c.rowsByTable) >= wantTables {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("controller did not stop within 2s")
	}
	return measure(stub.sqls(), stub.bodies())
}

// TestLoad_DataPlaneExactReproducible_L1 — the hermetic reproducibility proof.
func TestLoad_DataPlaneExactReproducible_L1(t *testing.T) {
	const reps = 100
	perTable := map[string]int{
		"http_events":  100,
		"dns_events":   100,
		"pgsql_events": 100,
	}

	var first counts
	for rep := 0; rep < reps; rep++ {
		got := runOnce(t, perTable)

		// Per-rep exactness: write surface == canned input (write ⊇ read with
		// equality) + exactly one attribution row.
		for tbl, want := range perTable {
			if got.rowsByTable[tbl] != want {
				t.Fatalf("rep %d: %s rows = %d, want %d", rep, tbl, got.rowsByTable[tbl], want)
			}
		}
		if got.attribution != 1 {
			t.Fatalf("rep %d: adaptive_attribution rows = %d, want 1", rep, got.attribution)
		}
		if len(got.rowsByTable) != len(perTable) {
			t.Fatalf("rep %d: unexpected tables written: %v", rep, keysOf(got.rowsByTable))
		}

		if rep == 0 {
			first = got
			continue
		}
		// Cross-rep exactness: identical rows AND bytes => std = 0 => CV = 0.
		for tbl := range perTable {
			if got.rowsByTable[tbl] != first.rowsByTable[tbl] {
				t.Fatalf("rep %d: %s row count drifted: %d != %d (rep 0)", rep, tbl, got.rowsByTable[tbl], first.rowsByTable[tbl])
			}
			if got.bytesByTable[tbl] != first.bytesByTable[tbl] {
				t.Fatalf("rep %d: %s byte total drifted: %d != %d (rep 0)", rep, tbl, got.bytesByTable[tbl], first.bytesByTable[tbl])
			}
		}
	}
	t.Logf("L1 reproducible across %d reps: http=%d(%dB) dns=%d(%dB) pgsql=%d(%dB) attribution=%d",
		reps,
		first.rowsByTable["http_events"], first.bytesByTable["http_events"],
		first.rowsByTable["dns_events"], first.bytesByTable["dns_events"],
		first.rowsByTable["pgsql_events"], first.bytesByTable["pgsql_events"],
		first.attribution)
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
