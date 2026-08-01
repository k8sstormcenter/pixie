/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package controller

// Tests for the CHUNKED + adaptively-subdividing ordered capture path — the
// durable fix for heavy tables (dc_snoop) losing the per-query deadline race under
// the OrderExportAll fan-out. Each chunk is a both-sides bounded pixie query; a
// chunk that still times out under contention is halved down to orderMinChunk.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/reconcile"
)

// countingQuerier counts Query calls and can fail the first failN calls with a
// configurable error (to exercise adaptive subdivision) or fail every call.
type countingQuerier struct {
	mu      sync.Mutex
	calls   int
	rows    []map[string]any
	failN   int   // fail the first failN calls, then succeed
	failAll bool  // fail every call
	failErr error // error to return on a failed call
}

func (q *countingQuerier) Query(context.Context, string) ([]map[string]any, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls++
	if q.failAll || q.calls <= q.failN {
		return nil, q.failErr
	}
	return q.rows, nil
}

func (q *countingQuerier) callCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls
}

// recordingRec captures every reconcile Row so a test can assert the ordered path
// records exactly ONE aggregated row per table (not one per chunk).
type recordingRec struct {
	mu   sync.Mutex
	rows []reconcile.Row
}

func (r *recordingRec) Record(_ context.Context, row reconcile.Row) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, row)
}

func chunkCtl(snk Sink, q PixieQuerier, rec reconcile.Recorder, chunk time.Duration) *Controller {
	cfg := defaultCfg()
	cfg.OrderChunk = chunk
	cfg.Rec = rec
	c := New(newFakeTrigger(), snk, cfg, &fakeClock{t: canonicalEventTime})
	if q != nil {
		c = c.WithPixieQuerier(q)
	}
	return c
}

var deadlineErr = errors.New("rpc error: code = DeadlineExceeded desc = context deadline exceeded")

// A wide window is walked in OrderChunk-sized slices: one pixie query per chunk,
// each writing its rows. 180s window / 60s chunk = 3 bounded queries.
func TestOrderQueryChunksWideWindow(t *testing.T) {
	snk := newRecordingSink()
	q := &countingQuerier{rows: []map[string]any{{"comm": "whoami"}}}
	end := canonicalEventTime
	start := end.Add(-180 * time.Second)
	if err := chunkCtl(snk, q, reconcile.Nop{}, 60*time.Second).
		OrderQuery(oqTarget, "dc_snoop", start, end, "qid-w"); err != nil {
		t.Fatalf("OrderQuery: %v", err)
	}
	if got := q.callCount(); got != 3 {
		t.Errorf("want 3 chunk queries for a 180s/60s window, got %d", got)
	}
	if got := snk.count("dc_snoop"); got != 3 {
		t.Errorf("want 3 rows written (one per chunk), got %d", got)
	}
}

// The ordered path records exactly ONE reconcile row per table, aggregating the
// per-chunk read/wrote counts — a forensic dump reads per-table, not per-chunk.
func TestOrderQuerySingleReconcileRowPerTable(t *testing.T) {
	snk := newRecordingSink()
	rec := &recordingRec{}
	q := &countingQuerier{rows: []map[string]any{{"comm": "cat"}}}
	end := canonicalEventTime
	start := end.Add(-120 * time.Second) // 2 chunks
	if err := chunkCtl(snk, q, rec, 60*time.Second).
		OrderQuery(oqTarget, "dc_snoop", start, end, "qid-r"); err != nil {
		t.Fatalf("OrderQuery: %v", err)
	}
	if len(rec.rows) != 1 {
		t.Fatalf("want 1 aggregated reconcile row, got %d", len(rec.rows))
	}
	if rec.rows[0].ReadCount != 2 || rec.rows[0].WroteCount != 2 {
		t.Errorf("want aggregated read=2 wrote=2 across chunks, got read=%d wrote=%d",
			rec.rows[0].ReadCount, rec.rows[0].WroteCount)
	}
	if rec.rows[0].WriteErr != "" {
		t.Errorf("clean capture must record no error, got %q", rec.rows[0].WriteErr)
	}
}

// A chunk that fails with a TRANSIENT (deadline) error is retried as narrower
// half-spans and recovers — the flaky-capture fix. The querier fails only its first
// call, so the initial full-chunk query subdivides and the halves succeed.
func TestCaptureSpanSubdividesOnTransientError(t *testing.T) {
	snk := newRecordingSink()
	q := &countingQuerier{rows: []map[string]any{{"comm": "getent"}}, failN: 1, failErr: deadlineErr}
	end := canonicalEventTime
	start := end.Add(-8 * time.Second) // single 60s chunk covers it → one initial query
	if err := chunkCtl(snk, q, reconcile.Nop{}, 60*time.Second).
		OrderQuery(oqTarget, "dc_snoop", start, end, "qid-t"); err != nil {
		t.Fatalf("transient failure must recover via subdivision, got %v", err)
	}
	// call 1 (8s span) fails → split into two 4s halves (calls 2 & 3), both succeed.
	if got := q.callCount(); got != 3 {
		t.Errorf("want 3 calls (1 failed + 2 half-span retries), got %d", got)
	}
	if got := snk.count("dc_snoop"); got != 2 {
		t.Errorf("want 2 half-span writes after subdivision, got %d", got)
	}
}

// A NON-transient error (e.g. a missing dark-vector table) surfaces immediately —
// no wasteful subdivision. Exactly one query per chunk, error returned.
func TestCaptureSpanDoesNotSplitNonTransient(t *testing.T) {
	snk := newRecordingSink()
	q := &countingQuerier{failAll: true, failErr: errors.New("table 'dx_bpf' not found")}
	end := canonicalEventTime
	start := end.Add(-30 * time.Second) // < one chunk → single chunk
	err := chunkCtl(snk, q, reconcile.Nop{}, 60*time.Second).
		OrderQuery(oqTarget, "dx_bpf", start, end, "qid-n")
	if err == nil {
		t.Fatal("non-transient error must surface")
	}
	if got := q.callCount(); got != 1 {
		t.Errorf("non-transient error must NOT subdivide; want 1 call, got %d", got)
	}
}

// A persistently-timing-out span subdivides down to orderMinChunk and then surfaces
// the error instead of looping forever — the recursion terminates at the floor.
func TestCaptureSpanTerminatesAtMinChunk(t *testing.T) {
	snk := newRecordingSink()
	q := &countingQuerier{failAll: true, failErr: deadlineErr}
	end := canonicalEventTime
	start := end.Add(-4 * time.Second) // 4s → 2s → 1s (floor), bounded call count
	err := chunkCtl(snk, q, reconcile.Nop{}, 60*time.Second).
		OrderQuery(oqTarget, "dc_snoop", start, end, "qid-f")
	if err == nil {
		t.Fatal("a span that never succeeds must ultimately surface the error")
	}
	// 4s→(2s,2s)→each (1s,1s): calls = 1 + 2 + 4 = 7, finite. Assert it stayed bounded.
	if got := q.callCount(); got == 0 || got > 15 {
		t.Errorf("subdivision must terminate at orderMinChunk with a bounded call count, got %d", got)
	}
}
