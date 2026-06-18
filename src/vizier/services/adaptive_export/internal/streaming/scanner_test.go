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
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
)

// fakeQuerier captures PxL strings and returns a canned row set.
type fakeQuerier struct {
	mu      sync.Mutex
	queries []string
	rows    []map[string]any
}

func (f *fakeQuerier) Query(ctx context.Context, pxl string) ([]map[string]any, error) {
	f.mu.Lock()
	f.queries = append(f.queries, pxl)
	f.mu.Unlock()
	return f.rows, nil
}

// failingQuerier always returns err.
type failingQuerier struct {
	err  error
	mu   sync.Mutex
	hits int
}

func (f *failingQuerier) Query(ctx context.Context, pxl string) ([]map[string]any, error) {
	f.mu.Lock()
	f.hits++
	f.mu.Unlock()
	return nil, f.err
}

// flipFlopQuerier alternates success / failure per call.
type flipFlopQuerier struct {
	mu       sync.Mutex
	idx      int
	results  [][]map[string]any
	failures []bool
}

func (f *flipFlopQuerier) Query(ctx context.Context, pxl string) ([]map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.idx % len(f.failures)
	f.idx++
	if f.failures[i] {
		return nil, errors.New("simulated failure")
	}
	return f.results[i], nil
}

// fakeWriter counts WritePixieRows invocations.
type fakeWriter struct {
	count atomic.Int64
}

func (f *fakeWriter) WritePixieRows(ctx context.Context, table string, rows []map[string]any) error {
	f.count.Add(int64(len(rows)))
	return nil
}

func TestScanner_BuildsPxLWithAllowlistOR(t *testing.T) {
	cfg := ScannerConfig{Table: "pgsql_events"}.defaulted()
	s := &TableScanner{cfg: cfg}
	f := Filter{
		Mode: FilterModeAllowlist,
		Pods: []activeset.Key{
			{Namespace: "n1", Pod: "a"},
			{Namespace: "n2", Pod: "b"},
		},
	}
	pxl := s.buildPxL(f)
	if !strings.Contains(pxl, "table='pgsql_events'") {
		t.Fatalf("pxl missing table: %s", pxl)
	}
	if !strings.Contains(pxl, "n1/a") {
		t.Fatalf("pxl missing first pod in regex: %s", pxl)
	}
	if !strings.Contains(pxl, "n2/b") {
		t.Fatalf("pxl missing second pod in regex: %s", pxl)
	}
	if !strings.Contains(pxl, "px.regex_match") || !strings.Contains(pxl, "df.pod)") {
		t.Fatalf("pxl missing px.regex_match call: %s", pxl)
	}
	if !strings.Contains(pxl, "^(") || !strings.Contains(pxl, ")$") {
		t.Fatalf("pxl missing anchored alternation: %s", pxl)
	}
}

func TestScanner_UnfilteredModeOmitsAllowlist(t *testing.T) {
	cfg := ScannerConfig{Table: "http_events"}.defaulted()
	s := &TableScanner{cfg: cfg}
	f := Filter{Mode: FilterModeUnfiltered}
	pxl := s.buildPxL(f)
	if strings.Contains(pxl, "df.pod ==") {
		t.Fatalf("unfiltered mode should not emit pod filter: %s", pxl)
	}
}

func TestScanner_EmptyAllowlistSkipsQuery(t *testing.T) {
	q := &fakeQuerier{rows: nil}
	w := NewBatchWriter("pgsql_events", &fakeWriter{}, WriterConfig{BatchEvery: time.Hour})
	filtCh := make(chan Filter, 4)
	filtCh <- Filter{Mode: FilterModeAllowlist, Pods: nil} // empty
	cfg := ScannerConfig{Table: "pgsql_events", RefreshInterval: 100 * time.Millisecond}
	sc := NewScanner(cfg, q, w, filtCh)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx)
	sc.Run(ctx)
	st := sc.Stats()
	if st.Queries != 0 {
		t.Fatalf("expected 0 queries on empty allowlist, got %d", st.Queries)
	}
	if st.Skipped == 0 {
		t.Fatalf("expected skipped > 0")
	}
}

// TestScanner_BackoffOnRepeatedErrors — after a Query error, the
// scanner must back off (NOT hot-loop). After K consecutive
// failures, the per-retry interval must be ≥ a measurable threshold.
func TestScanner_BackoffOnRepeatedErrors(t *testing.T) {
	q := &failingQuerier{err: errors.New("simulated broker outage")}
	w := NewBatchWriter("pgsql_events", &fakeWriter{}, WriterConfig{BatchEvery: 50 * time.Millisecond})
	filtCh := make(chan Filter, 4)
	filtCh <- Filter{Mode: FilterModeAllowlist, Pods: []activeset.Key{{Pod: "p"}}}
	cfg := ScannerConfig{
		Table:           "pgsql_events",
		RefreshInterval: 100 * time.Second, // huge — backoff must dominate, not refresh
		QueryTimeout:    100 * time.Millisecond,
		BackoffInitial:  50 * time.Millisecond,
		BackoffMax:      200 * time.Millisecond,
	}
	sc := NewScanner(cfg, q, w, filtCh)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	go w.Run(ctx)
	sc.Run(ctx)
	st := sc.Stats()
	// In 1 second with backoff = 50/100/200/200 → expected attempts ≤ ~10.
	// Without backoff (hot-loop), we'd see thousands.
	if st.Errors > 20 {
		t.Fatalf("scanner appears to be hot-looping on errors: %d in 1s (expected ≤ 20)", st.Errors)
	}
	if st.Errors < 2 {
		t.Fatalf("scanner did not retry after error: %d (expected ≥ 2)", st.Errors)
	}
}

// TestScanner_BackoffResetsOnSuccess — once a query succeeds, the
// backoff state must reset so the next failure waits BackoffInitial
// (not BackoffMax).
func TestScanner_BackoffResetsOnSuccess(t *testing.T) {
	q := &flipFlopQuerier{
		results: [][]map[string]any{
			nil, // first call fails
			{{"x": 1}},
			nil, // third call fails again
		},
		failures: []bool{true, false, true},
	}
	w := NewBatchWriter("pgsql_events", &fakeWriter{}, WriterConfig{BatchEvery: 1 * time.Hour})
	filtCh := make(chan Filter, 4)
	filtCh <- Filter{Mode: FilterModeAllowlist, Pods: []activeset.Key{{Pod: "p"}}}
	cfg := ScannerConfig{
		Table:           "pgsql_events",
		RefreshInterval: 10 * time.Millisecond,
		QueryTimeout:    100 * time.Millisecond,
		BackoffInitial:  50 * time.Millisecond,
		BackoffMax:      400 * time.Millisecond,
	}
	sc := NewScanner(cfg, q, w, filtCh)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	go w.Run(ctx)
	sc.Run(ctx)
	st := sc.Stats()
	// Without backoff reset, a stuck-at-Max scanner would hit fewer
	// retries (waiting BackoffMax=400ms = 0 retries in 250ms after
	// first error). With reset, success → 50ms → fail → 100ms etc.
	// — more retries fit in the window.
	//
	// Concrete: after each "fail | success | fail | success ..." cycle,
	// backoff stays at the initial value, so retries are FAST. We
	// expect ≥ 3 queries and ≥ 2 errors in 250 ms.
	if st.Queries < 3 {
		t.Fatalf("scanner did fewer queries than expected; queries=%d errors=%d (backoff may not be resetting)", st.Queries, st.Errors)
	}
	if st.Errors < 2 {
		t.Fatalf("expected ≥ 2 errors, got %d", st.Errors)
	}
}

func TestScanner_QueriesOnNonEmptyFilter(t *testing.T) {
	q := &fakeQuerier{rows: []map[string]any{{"time_": time.Now(), "pod": "n/p"}}}
	fw := &fakeWriter{}
	w := NewBatchWriter("pgsql_events", fw, WriterConfig{BatchEvery: 50 * time.Millisecond})
	filtCh := make(chan Filter, 4)
	filtCh <- Filter{Mode: FilterModeAllowlist, Pods: []activeset.Key{{Pod: "p"}}}
	cfg := ScannerConfig{Table: "pgsql_events", RefreshInterval: 50 * time.Millisecond, QueryTimeout: 1 * time.Second}
	sc := NewScanner(cfg, q, w, filtCh)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go w.Run(ctx)
	sc.Run(ctx)
	if sc.Stats().Queries == 0 {
		t.Fatalf("expected at least one query")
	}
	if fw.count.Load() == 0 {
		t.Fatalf("writer received no rows; expected at least 1")
	}
}
