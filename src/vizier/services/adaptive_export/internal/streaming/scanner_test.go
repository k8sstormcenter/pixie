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

// fakeQuerier captures PxL strings and returns a canned row set.
type fakeQuerier struct {
	mu       sync.Mutex
	queries  []string
	rows     []map[string]any
	failures int32
}

func (f *fakeQuerier) Query(ctx context.Context, pxl string) ([]map[string]any, error) {
	f.mu.Lock()
	f.queries = append(f.queries, pxl)
	f.mu.Unlock()
	return f.rows, nil
}

// fakeWriter counts WritePixieRows invocations.
type fakeWriter struct {
	count atomic.Int64
}

func (f *fakeWriter) WritePixieRows(ctx context.Context, table string, rows []map[string]any) error {
	f.count.Add(int64(len(rows)))
	return nil
}

func TestScanner_BuildsPxLWithWhitelistOR(t *testing.T) {
	cfg := ScannerConfig{Table: "pgsql_events"}.defaulted()
	s := &TableScanner{cfg: cfg}
	f := Filter{
		Mode: FilterModeWhitelist,
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

func TestScanner_UnfilteredModeOmitsWhitelist(t *testing.T) {
	cfg := ScannerConfig{Table: "http_events"}.defaulted()
	s := &TableScanner{cfg: cfg}
	f := Filter{Mode: FilterModeUnfiltered}
	pxl := s.buildPxL(f)
	if strings.Contains(pxl, "df.pod ==") {
		t.Fatalf("unfiltered mode should not emit pod filter: %s", pxl)
	}
}

func TestScanner_EmptyWhitelistSkipsQuery(t *testing.T) {
	q := &fakeQuerier{rows: nil}
	w := NewBatchWriter("pgsql_events", &fakeWriter{}, WriterConfig{BatchEvery: time.Hour})
	filtCh := make(chan Filter, 4)
	filtCh <- Filter{Mode: FilterModeWhitelist, Pods: nil} // empty
	cfg := ScannerConfig{Table: "pgsql_events", RefreshInterval: 100 * time.Millisecond}
	sc := NewScanner(cfg, q, w, filtCh)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go w.Run(ctx)
	sc.Run(ctx)
	st := sc.Stats()
	if st.Queries != 0 {
		t.Fatalf("expected 0 queries on empty whitelist, got %d", st.Queries)
	}
	if st.Skipped == 0 {
		t.Fatalf("expected skipped > 0")
	}
}

func TestScanner_QueriesOnNonEmptyFilter(t *testing.T) {
	q := &fakeQuerier{rows: []map[string]any{{"time_": time.Now(), "pod": "n/p"}}}
	fw := &fakeWriter{}
	w := NewBatchWriter("pgsql_events", fw, WriterConfig{BatchEvery: 50 * time.Millisecond})
	filtCh := make(chan Filter, 4)
	filtCh <- Filter{Mode: FilterModeWhitelist, Pods: []activeset.Key{{Pod: "p"}}}
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
