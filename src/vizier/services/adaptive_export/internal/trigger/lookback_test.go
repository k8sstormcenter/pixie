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

// Bounded-lookback + wall-clock poison-clamp tests (#97 / F8 / AE-9).
// No live ClickHouse: a stub HTTP server implements the trigger's
// JSONEachRow contract INCLUDING the `>= <bound>` watermark predicate,
// so re-poll semantics (the essence of lookback) are exercised for real.

package trigger

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeCH is a stub ClickHouse HTTP endpoint that stores rows and, like
// the real server, only returns rows whose NORMALIZED event_time is >=
// the bound parsed out of the trigger's SELECT.
type fakeCH struct {
	mu   sync.Mutex
	rows []fakeRow
	srv  *httptest.Server
}

type fakeRow struct {
	eventTime uint64 // raw, unit-ambiguous — exactly like production
	ruleID    string
	pid       int
}

var boundRE = regexp.MustCompile(`>= (\d+) ORDER`)

func newFakeCH(t *testing.T) *fakeCH {
	f := &fakeCH{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		m := boundRE.FindStringSubmatch(q)
		if m == nil {
			t.Errorf("query without >= bound: %q", q)
			w.WriteHeader(400)
			return
		}
		bound, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			t.Errorf("unparseable bound in query %q: %v", q, err)
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		var out []fakeRow
		for _, row := range f.rows {
			if normalizeEventTimeNanos(row.eventTime) >= bound {
				out = append(out, row)
			}
		}
		f.mu.Unlock()
		sort.Slice(out, func(i, j int) bool {
			return normalizeEventTimeNanos(out[i].eventTime) < normalizeEventTimeNanos(out[j].eventTime)
		})
		for _, row := range out {
			fmt.Fprintf(w,
				`{"RuleID":%q,"RuntimeK8sDetails":"{\"podName\":\"p-1\",\"podNamespace\":\"ns\"}","RuntimeProcessDetails":"{\"processTree\":{\"pid\":%d,\"comm\":\"c\"}}","event_time":"%d","hostname":"node-1"}`+"\n",
				row.ruleID, row.pid, row.eventTime)
		}
	}))
	return f
}

func (f *fakeCH) add(r fakeRow) {
	f.mu.Lock()
	f.rows = append(f.rows, r)
	f.mu.Unlock()
}

func (f *fakeCH) close() { f.srv.Close() }

// testBase is a fixed "now" for deterministic clamp behavior:
// 2026-05-29T… ≈ 1.7805e9 seconds.
const testBase = uint64(1_780_500_000)

func fixedNow() time.Time { return time.Unix(int64(testBase), 0) }

// newLookbackTrigger builds a trigger against the fake server with the
// #97 config (300s lookback) and a pinned wall clock.
func newLookbackTrigger(t *testing.T, f *fakeCH, hostname string, lookback time.Duration) *ClickHouseHTTP {
	t.Helper()
	tr, err := New(Config{
		Endpoint:     f.srv.URL,
		Hostname:     hostname,
		PollInterval: 20 * time.Millisecond,
		Lookback:     lookback,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.now = fixedNow // deterministic poison clamp
	return tr
}

// TestTrigger_LookbackCapturesLateArrivalExactlyOnce — T2: a row that
// lands BELOW the watermark but inside the lookback window is processed
// exactly once (no drop, no duplicate over many re-polls), and a row
// below watermark-lookback stays dropped (the documented bound). Also
// asserts ae_trigger_below_watermark_total increments (T3).
func TestTrigger_LookbackCapturesLateArrivalExactlyOnce(t *testing.T) {
	f := newFakeCH(t)
	defer f.close()
	f.add(fakeRow{eventTime: testBase, ruleID: "R1", pid: 111}) // head row → watermark = testBase

	belowBefore := testutil.ToFloat64(metricBelowWatermark)

	tr := newLookbackTrigger(t, f, "node-lb", 300*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	// Wait for the head row so the watermark is at testBase.
	select {
	case ev := <-ch:
		if ev.Target.PID != 111 {
			t.Fatalf("first event PID = %d, want 111", ev.Target.PID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for head row")
	}

	// Late arrival 60s below the watermark (inside the 300s window) and
	// one 400s below (outside the window).
	f.add(fakeRow{eventTime: testBase - 60, ruleID: "R2", pid: 222})
	f.add(fakeRow{eventTime: testBase - 400, ruleID: "R3", pid: 333})

	got := map[uint64]int{}
	deadline := time.Now().Add(400 * time.Millisecond) // ~20 re-polls of the same window
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			got[ev.Target.PID]++
		case <-time.After(20 * time.Millisecond):
		}
	}
	if got[222] != 1 {
		t.Errorf("late-arrival row emitted %d times, want exactly 1 (T2)", got[222])
	}
	if got[333] != 0 {
		t.Errorf("row below watermark-lookback emitted %d times, want 0 (documented bound)", got[333])
	}
	if got[111] != 0 {
		t.Errorf("head row re-emitted %d times after initial delivery (window dedup failed)", got[111])
	}
	if delta := testutil.ToFloat64(metricBelowWatermark) - belowBefore; delta < 1 {
		t.Errorf("ae_trigger_below_watermark_total delta = %v, want >= 1", delta)
	}
}

// TestTrigger_PoisonRowDoesNotHalt — T1 (the F8 non-halt guarantee): a
// row carrying the real E8 poison timestamp (1.78e18-style far-future
// vs the pinned clock) is clamp-rejected from advancing the watermark,
// the reject metric increments, the watermark gauge stays wall-clock-
// bounded, and SUBSEQUENT seconds rows are still processed — no manual
// watermark reset needed.
func TestTrigger_PoisonRowDoesNotHalt(t *testing.T) {
	// The exact leftover value from loadtest E8's poisoned watermark.
	// Normalized it stays 1.781559e18 ns ≈ 12 days past the pinned
	// clock (1.7805e9 s) — beyond the 1h MaxSkew.
	const poisonET = uint64(1781559619170395824)

	f := newFakeCH(t)
	defer f.close()
	f.add(fakeRow{eventTime: testBase - 10, ruleID: "R1", pid: 111})

	rejBefore := testutil.ToFloat64(metricEventTimeRejected)

	tr := newLookbackTrigger(t, f, "node-poison", 300*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	select {
	case ev := <-ch:
		if ev.Target.PID != 111 {
			t.Fatalf("first event PID = %d, want 111", ev.Target.PID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for head row")
	}

	// Inject the poison row; it is emitted once (real anomaly, mangled
	// timestamp) but must not advance the cursor.
	f.add(fakeRow{eventTime: poisonET, ruleID: "RPOISON", pid: 666})
	select {
	case ev := <-ch:
		if ev.Target.PID != 666 {
			t.Fatalf("expected poison row emission, got PID %d", ev.Target.PID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("poison row was dropped entirely; want emitted-once-without-advance")
	}
	if delta := testutil.ToFloat64(metricEventTimeRejected) - rejBefore; delta < 1 {
		t.Errorf("ae_trigger_event_time_rejected_total delta = %v, want >= 1", delta)
	}

	// THE F8 guarantee: a fresh seconds row AFTER the poison must flow.
	// Under the old strict HWM the cursor sat at 1.78e18 and this row
	// was below it forever (25/25 ticks at n_anomalies=0 in E8).
	f.add(fakeRow{eventTime: testBase + 5, ruleID: "R2", pid: 222})
	var got222 int
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) && got222 == 0 {
		select {
		case ev := <-ch:
			if ev.Target.PID == 222 {
				got222++
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	if got222 != 1 {
		t.Fatalf("post-poison seconds row emitted %d times, want 1 (T1 non-halt)", got222)
	}

	// Watermark gauge stays wall-clock-bounded: it advanced to the real
	// row (testBase+5 s), NOT to the poison value.
	wantWM := float64(normalizeEventTimeNanos(testBase + 5))
	if got := testutil.ToFloat64(metricWatermarkNS.WithLabelValues("kubescape_logs", "node-poison")); got != wantWM {
		t.Errorf("ae_trigger_watermark_ns = %v, want %v (wall-clock-bounded, not poison)", got, wantWM)
	}
}

// TestTrigger_PoisonPersistedWatermarkSelfRecovers — the E8 recovery
// scenario without the manual ALTER TABLE … DELETE: a pre-fix deployment
// left a far-future watermark behind; on start the trigger clamps it to
// wall-clock and fresh rows flow again.
func TestTrigger_PoisonPersistedWatermarkSelfRecovers(t *testing.T) {
	const poisonWM = uint64(1781559619170395824)

	f := newFakeCH(t)
	defer f.close()
	f.add(fakeRow{eventTime: testBase, ruleID: "R1", pid: 111})

	tr := newLookbackTrigger(t, f, "node-recover", 300*time.Second)
	tr.cfg.InitialWatermark = poisonWM // simulates the poisoned persisted cursor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	select {
	case ev := <-ch:
		if ev.Target.PID != 111 {
			t.Fatalf("recovered event PID = %d, want 111", ev.Target.PID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("fresh row not delivered — poisoned persisted watermark was not clamped (still halted)")
	}
}

// TestTrigger_LookbackZeroIsStrictHWM — T4: LOOKBACK=0 preserves the
// legacy strict high-water-mark exactly — the poll bound IS the
// watermark (no window subtraction) and a below-watermark row stays
// dropped. (The monotonic happy path itself is pinned by the existing
// clickhouse_test.go suite, which runs with the zero-value Lookback.)
func TestTrigger_LookbackZeroIsStrictHWM(t *testing.T) {
	f := newFakeCH(t)
	defer f.close()
	f.add(fakeRow{eventTime: testBase, ruleID: "R1", pid: 111})

	tr := newLookbackTrigger(t, f, "node-strict", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	select {
	case ev := <-ch:
		if ev.Target.PID != 111 {
			t.Fatalf("first event PID = %d, want 111", ev.Target.PID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for head row")
	}

	// A late arrival below the watermark: with strict HWM the SELECT
	// bound equals the watermark, so it is never fetched again → dropped.
	f.add(fakeRow{eventTime: testBase - 60, ruleID: "R2", pid: 222})
	got := map[uint64]int{}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			got[ev.Target.PID]++
		case <-time.After(20 * time.Millisecond):
		}
	}
	if got[222] != 0 {
		t.Errorf("strict mode emitted a below-watermark row %d times; want 0 (legacy behavior)", got[222])
	}
	if got[111] != 0 {
		t.Errorf("strict mode re-emitted the boundary row %d times; want 0", got[111])
	}
}
