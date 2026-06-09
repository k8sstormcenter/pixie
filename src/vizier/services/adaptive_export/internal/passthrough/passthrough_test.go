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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
)

type fakeQuerier struct {
	mu    sync.Mutex
	calls []string // PxL sources received
	row   map[string]any
	err   error
}

func (f *fakeQuerier) Query(_ context.Context, src string) ([]map[string]any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, src)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return []map[string]any{f.row}, nil
}

type fakeSink struct {
	mu      sync.Mutex
	writes  map[string]int // table → row count
	failFor string
}

func newFakeSink() *fakeSink { return &fakeSink{writes: map[string]int{}} }

func (f *fakeSink) WritePixieRows(_ context.Context, table string, rows []map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFor == table {
		return errors.New("fakeSink: forced failure")
	}
	f.writes[table] += len(rows)
	return nil
}

// TestLoop_DefaultsTablesToPixieTables — when Config.Tables is unset, the
// loop must walk every clickhouse.PixieTables() entry. This is the contract
// the A/B measurement depends on (a missing table silently drops a column
// from the capture-fraction matrix).
func TestLoop_DefaultsTablesToPixieTables(t *testing.T) {
	q := &fakeQuerier{row: map[string]any{"upid": "x", "time_": time.Now()}}
	s := newFakeSink()
	l := New(q, s, Config{Window: 1 * time.Second, Refresh: 1 * time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.tick(ctx)

	expected := clickhouse.PixieTables()
	if len(s.writes) != len(expected) {
		t.Fatalf("wrote %d tables, want %d", len(s.writes), len(expected))
	}
	for _, want := range expected {
		if s.writes[want] != 1 {
			t.Fatalf("table %q: wrote %d rows, want 1", want, s.writes[want])
		}
	}
}

// TestLoop_EmitsEmptyTargetPxL — the firehose semantics require the PxL
// to omit the namespace/pod predicates entirely. The whole A/B
// experiment is meaningful only if the EVERYTHING phase truly does NOT
// filter rows.
func TestLoop_EmitsEmptyTargetPxL(t *testing.T) {
	q := &fakeQuerier{row: map[string]any{"upid": "x", "time_": time.Now()}}
	s := newFakeSink()
	l := New(q, s, Config{Window: 1 * time.Second, Refresh: 1 * time.Hour})

	l.tick(context.Background())

	for _, src := range q.calls {
		// pxl.QueryFor with empty Target writes neither "df.namespace ==" nor
		// "df.pod ==" predicates. If either appears, the loop is silently
		// filtering and the A/B comparison is invalid.
		if strings.Contains(src, "df.namespace ==") {
			t.Fatalf("passthrough PxL contains namespace filter — A/B invariant broken:\n%s", src)
		}
		if strings.Contains(src, "df.pod ==") {
			t.Fatalf("passthrough PxL contains pod filter — A/B invariant broken:\n%s", src)
		}
	}
}

// TestLoop_TickContinuesPastTableFailure — a single table failing
// (query error OR sink error) must not block subsequent tables in the
// same tick. Otherwise a transient pixie 500 on http_events would
// silently drop conn_stats, redis_events, etc. from that window.
func TestLoop_TickContinuesPastTableFailure(t *testing.T) {
	q := &fakeQuerier{row: map[string]any{"upid": "x", "time_": time.Now()}}
	s := newFakeSink()
	s.failFor = "http_events" // sink rejects the first table
	l := New(q, s, Config{
		Window:  1 * time.Second,
		Refresh: 1 * time.Hour,
		Tables:  []string{"http_events", "conn_stats", "dns_events"},
	})

	l.tick(context.Background())

	if s.writes["http_events"] != 0 {
		t.Fatalf("http_events should NOT have written: %d rows", s.writes["http_events"])
	}
	if s.writes["conn_stats"] != 1 || s.writes["dns_events"] != 1 {
		t.Fatalf("tables after the failure should still write: conn_stats=%d dns_events=%d",
			s.writes["conn_stats"], s.writes["dns_events"])
	}
}

// TestLoop_RunFiresImmediately — the first tick must happen on Run
// entry (not after one Refresh). Otherwise a 30s default Refresh would
// add 30s of "AE-FILTER" baseline mixing into the EVERYTHING phase's
// first window when the operator boots into passthrough mode.
func TestLoop_RunFiresImmediately(t *testing.T) {
	q := &fakeQuerier{row: map[string]any{"upid": "x", "time_": time.Now()}}
	s := newFakeSink()
	l := New(q, s, Config{
		Window:  1 * time.Second,
		Refresh: 1 * time.Hour, // ensure the test fails if we wait for the ticker
		Tables:  []string{"http_events"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()

	// Poll briefly — Run's immediate tick should land within ms.
	deadline := time.After(2 * time.Second)
	for {
		s.mu.Lock()
		got := s.writes["http_events"]
		s.mu.Unlock()
		if got == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("first tick did not fire within 2s; got %d writes", got)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// TestNew_AppliesDefaults — Window/Refresh = 0 fall back to 30s, Tables
// = nil falls back to clickhouse.PixieTables(). Production cmd/main.go
// reads optional env knobs into Config; an unset env yields a zero
// duration and we must not crash with a zero ticker.
func TestNew_AppliesDefaults(t *testing.T) {
	l := New(&fakeQuerier{}, newFakeSink(), Config{})
	if l.cfg.Window != 30*time.Second {
		t.Fatalf("default Window = %v, want 30s", l.cfg.Window)
	}
	if l.cfg.Refresh != 30*time.Second {
		t.Fatalf("default Refresh = %v, want 30s", l.cfg.Refresh)
	}
	if got, want := len(l.cfg.Tables), len(clickhouse.PixieTables()); got != want {
		t.Fatalf("default Tables count = %d, want %d", got, want)
	}
}

// TestLoop_RespectsContext — a cancelled context mid-tick should stop
// further table queries (we don't want a 2-min stall on SIGTERM when
// the loop has 13 tables × N-second pixie roundtrip queued up).
func TestLoop_RespectsContext(t *testing.T) {
	var calls atomic.Int32
	q := &slowQuerier{calls: &calls}
	s := newFakeSink()
	l := New(q, s, Config{
		Window:  1 * time.Second,
		Refresh: 1 * time.Hour,
		Tables:  []string{"a", "b", "c", "d", "e"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before tick starts
	l.tick(ctx)
	// All tables should be skipped because ctx.Err() != nil at top of loop.
	if calls.Load() != 0 {
		t.Fatalf("expected 0 querier calls after cancel, got %d", calls.Load())
	}
}

type slowQuerier struct{ calls *atomic.Int32 }

func (s *slowQuerier) Query(_ context.Context, _ string) ([]map[string]any, error) {
	s.calls.Add(1)
	return nil, nil
}
