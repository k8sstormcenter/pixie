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

package streaming

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
)

// recordingQuerier captures every PxL string + lets the test inject
// a per-call row count. Useful for verifying that the PxL the scanner
// emits actually carries the whitelist the test set up upstream.
type recordingQuerier struct {
	mu       sync.Mutex
	queries  []string
	rowsFunc func(pxl string) []map[string]any
}

func (r *recordingQuerier) Query(_ context.Context, pxl string) ([]map[string]any, error) {
	r.mu.Lock()
	r.queries = append(r.queries, pxl)
	r.mu.Unlock()
	if r.rowsFunc == nil {
		return nil, nil
	}
	return r.rowsFunc(pxl), nil
}

func (r *recordingQuerier) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.queries))
	copy(out, r.queries)
	return out
}

// countingWriter is a SinkWriter that just counts rows landed
// per-table — proxies an integration-grade check without standing
// up a real CH.
type countingWriter struct {
	mu      sync.Mutex
	perTable map[string]int64
	calls   atomic.Int64
}

func newCountingWriter() *countingWriter {
	return &countingWriter{perTable: map[string]int64{}}
}

func (w *countingWriter) WritePixieRows(_ context.Context, table string, rows []map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.perTable[table] += int64(len(rows))
	w.calls.Add(1)
	return nil
}

func (w *countingWriter) count(table string) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.perTable[table]
}

// TestIntegration_NotifierToScannerWhitelistFlow — exercises the
// whole rev-3 pipeline minus pixie:
//
//   AttributionNotifier.Submit
//     → ActiveSet.Upsert
//       → FilterUpdater (debounce)
//         → TableScanner.buildPxL (whitelist embedded)
//           → recordingQuerier (verify PxL contains pod names)
//             → BatchWriter (verify rows reach sink)
//
// The whole chain runs against fake pixie + fake sink so we can
// assert on PxL strings + row counts deterministically.
func TestIntegration_NotifierToScannerWhitelistFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Wire up the chain.
	set := activeset.New()
	notif := NewAttributionNotifier(set, NotifierConfig{BufferSize: 128})
	updater := NewUpdater(set, UpdaterConfig{Debounce: 20 * time.Millisecond})
	q := &recordingQuerier{
		rowsFunc: func(pxl string) []map[string]any {
			// Return 3 rows iff the whitelist contains "wantpod"; else 0.
			if strings.Contains(pxl, "wantpod") {
				return []map[string]any{{"a": 1}, {"a": 2}, {"a": 3}}
			}
			return nil
		},
	}
	w := newCountingWriter()
	writer := NewBatchWriter("pgsql_events", w, WriterConfig{
		BatchEvery: 50 * time.Millisecond,
		BatchRows:  1000,
	})
	scanner := NewScanner(ScannerConfig{
		Table:           "pgsql_events",
		RefreshInterval: 30 * time.Millisecond,
		QueryTimeout:    500 * time.Millisecond,
	}, q, writer, updater.Subscribe())

	// Spin everything up.
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); notif.Run(ctx) }()
	go func() { defer wg.Done(); updater.Run(ctx) }()
	go func() { defer wg.Done(); writer.Run(ctx) }()
	go func() { defer wg.Done(); scanner.Run(ctx) }()

	// Push two pods through the controller-facing API.
	notif.Submit(activeset.Key{Namespace: "n", Pod: "wantpod"}, time.Now().Add(time.Minute))
	notif.Submit(activeset.Key{Namespace: "n", Pod: "other"}, time.Now().Add(time.Minute))

	// Wait for the writer to land non-zero rows.
	deadline := time.Now().Add(2 * time.Second)
	for w.count("pgsql_events") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := w.count("pgsql_events")
	if got < 3 {
		t.Fatalf("expected ≥3 rows written for pgsql_events, got %d", got)
	}

	// Assert the PxL carried BOTH pods.
	found := q.all()
	if len(found) == 0 {
		t.Fatalf("no PxL queries captured")
	}
	last := found[len(found)-1]
	if !strings.Contains(last, "wantpod") || !strings.Contains(last, "other") {
		t.Fatalf("last PxL missing one of the pods:\n%s", last)
	}

	cancel()
	wg.Wait()
}

// TestIntegration_EmptyActiveSetSkipsAllQueries — when nothing is
// active, the scanner must NOT issue queries at all.
func TestIntegration_EmptyActiveSetSkipsAllQueries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	set := activeset.New()
	updater := NewUpdater(set, UpdaterConfig{Debounce: 10 * time.Millisecond})
	q := &recordingQuerier{rowsFunc: func(string) []map[string]any { return nil }}
	w := newCountingWriter()
	writer := NewBatchWriter("redis_events", w, WriterConfig{BatchEvery: 50 * time.Millisecond})
	scanner := NewScanner(ScannerConfig{Table: "redis_events", RefreshInterval: 30 * time.Millisecond}, q, writer, updater.Subscribe())

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); updater.Run(ctx) }()
	go func() { defer wg.Done(); writer.Run(ctx) }()
	go func() { defer wg.Done(); scanner.Run(ctx) }()

	<-ctx.Done()
	wg.Wait()

	if len(q.all()) != 0 {
		t.Fatalf("scanner issued %d queries against empty active set; expected 0", len(q.all()))
	}
}

// TestIntegration_PrunePropagatesToScannerWhitelist — when the
// controller's prune fires, the scanner's next PxL must omit the
// pruned pod.
func TestIntegration_PrunePropagatesToScannerWhitelist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	set := activeset.New()
	notif := NewAttributionNotifier(set, NotifierConfig{BufferSize: 64})
	updater := NewUpdater(set, UpdaterConfig{Debounce: 20 * time.Millisecond})
	q := &recordingQuerier{}
	w := newCountingWriter()
	writer := NewBatchWriter("http_events", w, WriterConfig{BatchEvery: 50 * time.Millisecond})
	scanner := NewScanner(ScannerConfig{Table: "http_events", RefreshInterval: 30 * time.Millisecond}, q, writer, updater.Subscribe())

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); notif.Run(ctx) }()
	go func() { defer wg.Done(); updater.Run(ctx) }()
	go func() { defer wg.Done(); writer.Run(ctx) }()
	go func() { defer wg.Done(); scanner.Run(ctx) }()

	notif.Submit(activeset.Key{Pod: "soon-pruned"}, time.Now().Add(time.Minute))
	// Wait for first query.
	waitForQueryContaining(t, q, "soon-pruned", time.Second)
	// Snapshot query count BEFORE Remove so we can measure post-Remove queries.
	preCount := len(q.all())
	notif.SubmitRemove(activeset.Key{Pod: "soon-pruned"})
	// Give the prune propagation a generous window (debounce 20ms +
	// scanner refresh interval 30ms + a few cycles).
	time.Sleep(300 * time.Millisecond)
	// Count queries issued AFTER the Remove that still contain the
	// pruned pod — must be zero. (Empty-whitelist branch in the
	// scanner skips queries entirely, so the legitimate post-prune
	// state shows up as "no new queries added at all", or as new
	// queries containing OTHER pods.)
	postQueries := q.all()[preCount:]
	for _, pxl := range postQueries {
		if strings.Contains(pxl, "soon-pruned") {
			cancel()
			wg.Wait()
			t.Fatalf("scanner issued a post-prune query containing the removed pod:\n%s", pxl)
		}
	}
	cancel()
	wg.Wait()
}

// waitForQueryContaining polls the recorder until a query containing
// `needle` appears OR timeout fires.
func waitForQueryContaining(t *testing.T, q *recordingQuerier, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, pxl := range q.all() {
			if strings.Contains(pxl, needle) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no query containing %q within %v; captured: %v", needle, timeout, q.all())
}
