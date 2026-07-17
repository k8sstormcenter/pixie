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

package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/kubescape"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
)

// ---------- fakes ----------

type fakeTrigger struct {
	ch  chan kubescape.Event
	err error
}

func newFakeTrigger() *fakeTrigger { return &fakeTrigger{ch: make(chan kubescape.Event, 16)} }

func (f *fakeTrigger) Subscribe(_ context.Context) (<-chan kubescape.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ch, nil
}

func (f *fakeTrigger) push(ev kubescape.Event) { f.ch <- ev }
func (f *fakeTrigger) close()                  { close(f.ch) }

type fakeSink struct {
	mu       sync.Mutex
	writes   []sink.AttributionRow
	preload  []sink.AttributionRow
	werr     error
	qerr     error
	attempts int // every Write call increments, even when werr fires
}

func (f *fakeSink) WritePixieRows(_ context.Context, _ string, _ []map[string]any) error {
	return nil
}

func (f *fakeSink) Write(_ context.Context, rows []sink.AttributionRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.werr != nil {
		return f.werr
	}
	f.writes = append(f.writes, rows...)
	return nil
}

func (f *fakeSink) writeAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

func (f *fakeSink) QueryActive(_ context.Context, hostname string) ([]sink.AttributionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.qerr != nil {
		return nil, f.qerr
	}
	out := make([]sink.AttributionRow, 0, len(f.preload))
	for _, r := range f.preload {
		if r.Hostname == hostname {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSink) snapshot() []sink.AttributionRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sink.AttributionRow{}, f.writes...)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// ---------- helpers ----------

var canonicalEventTime = time.Unix(0, 1744477360303026359).UTC()

func canonicalEvent() kubescape.Event {
	return kubescape.Event{
		Target: anomaly.Target{
			PID: 106040, Comm: "redis-server",
			Pod: "redis-578d5dc9bd-kjj78", Namespace: "redis",
		},
		EventTime: 1744477360303026359,
		RuleID:    "R1005",
		Hostname:  "node-1",
	}
}

func anotherTargetEvent() kubescape.Event {
	ev := canonicalEvent()
	ev.Target.PID = 999999
	ev.RuleID = "R0006"
	return ev
}

func waitFor(t *testing.T, what string, deadline time.Duration, ok func() bool) {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func runController(t *testing.T, c *Controller, trig *fakeTrigger) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	return func() {
		trig.close()
		cancel()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatalf("controller did not stop within 1s")
		}
	}
}

func defaultCfg() Config {
	return Config{Hostname: "node-1", Before: 5 * time.Minute, After: 5 * time.Minute}
}

// ---------- tests ----------

// TestController_NewWindow_FirstAnomalyOnTarget — first event on a hash
// produces one Sink write with t_start = event - Before, t_end = now + After.
func TestController_NewWindow_FirstAnomalyOnTarget(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime.Add(time.Second)}
	c := New(trig, snk, defaultCfg(), clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	waitFor(t, "first write", 200*time.Millisecond, func() bool { return len(snk.snapshot()) > 0 })
	got := snk.snapshot()[0]
	wantHash := anomaly.Hash(canonicalEvent().Target)
	if got.AnomalyHash != wantHash {
		t.Fatalf("hash = %q, want %q", got.AnomalyHash, wantHash)
	}
	if got.PID != 106040 || got.Comm != "redis-server" || got.Namespace != "redis" {
		t.Fatalf("identity wrong: %+v", got)
	}
	if got.Hostname != "node-1" {
		t.Fatalf("Hostname = %q", got.Hostname)
	}
	wantStart := canonicalEventTime.Add(-5 * time.Minute)
	if !got.TStart.Equal(wantStart) {
		t.Fatalf("TStart = %v, want %v", got.TStart, wantStart)
	}
	wantEnd := clk.Now().Add(5 * time.Minute)
	if !got.TEnd.Equal(wantEnd) {
		t.Fatalf("TEnd = %v, want %v", got.TEnd, wantEnd)
	}
	if got.NAnomalies != 1 || got.LastRuleID != "R1005" {
		t.Fatalf("LastRuleID/NAnomalies wrong: %+v", got)
	}
}

// TestController_Coalesce_SecondAnomalySameHash — second event on the
// same target reuses the same row, increments n_anomalies, extends t_end.
func TestController_Coalesce_SecondAnomalySameHash(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime.Add(time.Second)}
	c := New(trig, snk, defaultCfg(), clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	waitFor(t, "first write", 200*time.Millisecond, func() bool { return len(snk.snapshot()) >= 1 })

	clk.advance(2 * time.Minute) // 2 minutes pass; t_end should reset to now+5min
	ev2 := canonicalEvent()
	ev2.RuleID = "R0006"
	ev2.EventTime = uint64(canonicalEventTime.Add(2 * time.Minute).UnixNano())
	trig.push(ev2)
	waitFor(t, "second write", 200*time.Millisecond, func() bool { return len(snk.snapshot()) >= 2 })

	if c.Active() != 1 {
		t.Fatalf("Active = %d, want 1 (must coalesce on same hash)", c.Active())
	}
	got := snk.snapshot()[1]
	if got.NAnomalies != 2 {
		t.Fatalf("NAnomalies = %d, want 2", got.NAnomalies)
	}
	if got.LastRuleID != "R0006" {
		t.Fatalf("LastRuleID = %q, want R0006", got.LastRuleID)
	}
	wantEnd := clk.Now().Add(5 * time.Minute)
	if !got.TEnd.Equal(wantEnd) {
		t.Fatalf("TEnd = %v, want %v (must extend on coalesce)", got.TEnd, wantEnd)
	}
}

// TestController_NeverShrinksTEnd — out-of-order arrivals or repeats
// must not regress t_end backward.
func TestController_NeverShrinksTEnd(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}
	c := New(trig, snk, defaultCfg(), clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	waitFor(t, "first", 200*time.Millisecond, func() bool { return len(snk.snapshot()) >= 1 })
	originalEnd := snk.snapshot()[0].TEnd

	// fake clock REWINDS — pathological but defensive
	clk.advance(-time.Hour)
	trig.push(canonicalEvent())
	waitFor(t, "second", 200*time.Millisecond, func() bool { return len(snk.snapshot()) >= 2 })
	got := snk.snapshot()[1]
	if !got.TEnd.Equal(originalEnd) {
		t.Fatalf("TEnd regressed: was %v, now %v", originalEnd, got.TEnd)
	}
}

// TestController_NewWindowForColdTarget — different target opens a 2nd
// active row, preserving the first.
func TestController_NewWindowForColdTarget(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}
	c := New(trig, snk, defaultCfg(), clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	trig.push(anotherTargetEvent())
	waitFor(t, "two active", 300*time.Millisecond, func() bool { return c.Active() == 2 })
}

// TestController_Rehydrate_FromSink — boot reads still-active rows.
func TestController_Rehydrate_FromSink(t *testing.T) {
	trig := newFakeTrigger()
	t0 := canonicalEventTime
	preload := []sink.AttributionRow{
		{AnomalyHash: "h1", Hostname: "node-1", PID: 1, Comm: "x", TStart: t0, TEnd: t0.Add(10 * time.Minute), LastSeen: t0, NAnomalies: 5},
		{AnomalyHash: "h2", Hostname: "node-OTHER", PID: 2, Comm: "y", TStart: t0, TEnd: t0.Add(10 * time.Minute), LastSeen: t0, NAnomalies: 1},
	}
	snk := &fakeSink{preload: preload}
	clk := &fakeClock{t: t0}
	c := New(trig, snk, defaultCfg(), clk)

	if err := c.Rehydrate(context.Background()); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if c.Active() != 1 {
		t.Fatalf("Active after rehydrate = %d, want 1 (must filter by hostname)", c.Active())
	}
}

// TestController_PruneExpired — entries past their t_end drop out.
func TestController_PruneExpired(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}
	c := New(trig, snk, Config{Hostname: "node-1", Before: time.Minute, After: time.Minute}, clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	waitFor(t, "active=1", 200*time.Millisecond, func() bool { return c.Active() == 1 })

	// PruneExpired() now waits for TEnd + 2*After (the grace period that
	// prevents racing same-hash alerts arriving right after a prune from
	// spawning fresh pushPixieRows goroutines that re-scan the slice).
	// With Before=After=1m the row's TEnd is now+1m, so we need to advance
	// past now+1m+2*1m = now+3m.
	clk.advance(3*time.Minute + time.Second) // past t_end + 2*After grace
	if r := c.PruneExpired(); r != 1 {
		t.Fatalf("PruneExpired removed %d, want 1", r)
	}
	if c.Active() != 0 {
		t.Fatalf("Active after prune = %d, want 0", c.Active())
	}
}

// TestController_SinkErrorNonFatal — controller does not crash on
// Sink.Write error AND rolls back the in-memory attribution row so a
// failed persist doesn't leave a phantom anchor that pushPixieRows
// could fan out against (CodeRabbit r-#68/controller/controller.go).
// The rollback contract is: on first event for a hash with write
// failure → c.active[hash] is NOT added.
func TestController_SinkErrorNonFatal(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{werr: errors.New("ch unreachable")}
	clk := &fakeClock{t: canonicalEventTime}
	c := New(trig, snk, defaultCfg(), clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	// Wait until the handler has actually called Write (and got the
	// error). Then assert rollback: active stays at 0.
	waitFor(t, "handler processed sink error", 200*time.Millisecond,
		func() bool { return snk.writeAttempts() >= 1 })
	if got := c.Active(); got != 0 {
		t.Fatalf("Active()=%d after sink error; want 0 (rollback contract)", got)
	}
}

// TestController_RestartMidStream_Aborts — context cancel terminates Run.
func TestController_RestartMidStream_Aborts(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}
	c := New(trig, snk, defaultCfg(), clk)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()

	trig.push(canonicalEvent())
	waitFor(t, "controller picked up event", 200*time.Millisecond, func() bool { return c.Active() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("controller did not abort within 300ms of cancel")
	}
}

// ────────────────────────────────────────────────────────────────
// Callbacks (rev-3 streaming hook): OnAttribution + OnPrune
// ────────────────────────────────────────────────────────────────

type attrCall struct {
	ns, pod string
	tEnd    time.Time
}

// TestController_OnAttribution_FiresPerEvent — every kubescape
// event (new or extension) triggers exactly one OnAttribution.
func TestController_OnAttribution_FiresPerEvent(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}

	var mu sync.Mutex
	var calls []attrCall
	cfg := defaultCfg()
	cfg.OnAttribution = func(ns, pod string, tEnd time.Time) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, attrCall{ns, pod, tEnd})
	}
	c := New(trig, snk, cfg, clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	trig.push(canonicalEvent()) // extension on same hash
	trig.push(canonicalEvent())
	waitFor(t, "3 attribution callbacks", 300*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 3
	})
	mu.Lock()
	defer mu.Unlock()
	for _, c := range calls {
		if c.pod == "" {
			t.Fatalf("callback received empty pod: %+v", c)
		}
		if c.tEnd.IsZero() {
			t.Fatalf("callback received zero tEnd: %+v", c)
		}
	}
}

// TestController_OnAttribution_NilIsNoop — nil callback must not crash.
func TestController_OnAttribution_NilIsNoop(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}
	cfg := defaultCfg()
	cfg.OnAttribution = nil // explicit
	c := New(trig, snk, cfg, clk)
	stop := runController(t, c, trig)
	defer stop()
	trig.push(canonicalEvent())
	waitFor(t, "event landed", 200*time.Millisecond, func() bool { return c.Active() == 1 })
	// No assertion needed beyond not panicking.
}

// TestController_OnPrune_FiresWithKeyDetails — PruneExpired must
// emit one OnPrune callback per evicted hash, with ns + pod set.
func TestController_OnPrune_FiresWithKeyDetails(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}
	var mu sync.Mutex
	var pruned []attrCall
	cfg := Config{
		Hostname: "node-1", Before: time.Minute, After: time.Minute,
		OnPrune: func(ns, pod string) {
			mu.Lock()
			defer mu.Unlock()
			pruned = append(pruned, attrCall{ns: ns, pod: pod})
		},
	}
	c := New(trig, snk, cfg, clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	waitFor(t, "active=1", 200*time.Millisecond, func() bool { return c.Active() == 1 })
	clk.advance(3*time.Minute + time.Second) // past t_end + 2*After grace
	if r := c.PruneExpired(); r != 1 {
		t.Fatalf("PruneExpired removed %d, want 1", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pruned) != 1 {
		t.Fatalf("OnPrune fired %d times, want 1", len(pruned))
	}
	if pruned[0].pod == "" {
		t.Fatalf("OnPrune called with empty pod: %+v", pruned[0])
	}
}

// TestController_OnPrune_NilIsNoop — nil callback must not crash
// the prune loop.
func TestController_OnPrune_NilIsNoop(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}
	cfg := Config{Hostname: "node-1", Before: time.Minute, After: time.Minute}
	cfg.OnPrune = nil // explicit
	c := New(trig, snk, cfg, clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	waitFor(t, "active=1", 200*time.Millisecond, func() bool { return c.Active() == 1 })
	clk.advance(3*time.Minute + time.Second)
	_ = c.PruneExpired()
	// No panic = pass.
}

// TestController_OnPrune_OnlyFiresWhenLastHashOnPodGone — multiple
// anomaly hashes can share a single (namespace, pod) when distinct
// PID×comm combinations on the same pod each get their own
// kubescape rule firing. Real-world example (sweep observation):
// pgsql-server has hashes for processes `postgres`, `pg_isready`,
// and `runc:[2:INIT]` — three hashes, one pod.
//
// The streaming layer is pod-keyed, so OnPrune(ns, pod) must only
// fire when the LAST hash for that pod is evicted. Premature firing
// would stop the per-pod stream while other hashes are still active.
// CR feedback (controller.go:156) caught this; see comment thread.
func TestController_OnPrune_OnlyFiresWhenLastHashOnPodGone(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}

	var mu sync.Mutex
	var prunedPods []string
	cfg := Config{
		Hostname: "node-1", Before: time.Minute, After: time.Minute,
		OnPrune: func(ns, pod string) {
			mu.Lock()
			defer mu.Unlock()
			prunedPods = append(prunedPods, ns+"/"+pod)
		},
	}
	c := New(trig, snk, cfg, clk)
	stop := runController(t, c, trig)
	defer stop()

	// Two events on the SAME pod but with different (PID, Comm) so
	// anomaly.Hash returns two distinct hashes.
	mkEvent := func(pid uint64, comm string) kubescape.Event {
		return kubescape.Event{
			Target: anomaly.Target{
				PID: pid, Comm: comm, Pod: "pgsql-server-x", Namespace: "px",
			},
			EventTime: uint64(canonicalEventTime.UnixNano()),
			RuleID:    "R1", Hostname: "node-1",
		}
	}
	trig.push(mkEvent(100, "postgres"))
	trig.push(mkEvent(200, "pg_isready"))
	waitFor(t, "two distinct hashes active", 300*time.Millisecond, func() bool {
		return c.Active() == 2
	})

	// Advance past TEnd + 2*After so BOTH hashes are evictable.
	clk.advance(3*time.Minute + time.Second)
	if r := c.PruneExpired(); r != 2 {
		t.Fatalf("PruneExpired removed %d, want 2 hashes", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prunedPods) != 1 {
		t.Fatalf("OnPrune fired %d times for one pod with 2 hashes; want 1. Calls: %v",
			len(prunedPods), prunedPods)
	}
	if prunedPods[0] != "px/pgsql-server-x" {
		t.Fatalf("wrong pod pruned: %q", prunedPods[0])
	}
}

// TestController_OnPrune_DoesNotFireWhileOtherHashesActive — inverse
// case: only ONE hash on a pod expires; OnPrune must NOT fire for
// that pod because other hashes for the same pod remain active.
func TestController_OnPrune_DoesNotFireWhileOtherHashesActive(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}

	var mu sync.Mutex
	var prunedPods []string
	cfg := Config{
		Hostname: "node-1", Before: time.Minute, After: time.Minute,
		OnPrune: func(ns, pod string) {
			mu.Lock()
			defer mu.Unlock()
			prunedPods = append(prunedPods, ns+"/"+pod)
		},
	}
	c := New(trig, snk, cfg, clk)
	stop := runController(t, c, trig)
	defer stop()

	mkEvent := func(pid uint64) kubescape.Event {
		return kubescape.Event{
			Target: anomaly.Target{
				PID: pid, Comm: "c", Pod: "samepod", Namespace: "ns",
			},
			EventTime: uint64(canonicalEventTime.UnixNano()),
			RuleID:    "R1", Hostname: "node-1",
		}
	}
	trig.push(mkEvent(100))
	waitFor(t, "1 hash", 300*time.Millisecond, func() bool { return c.Active() == 1 })

	// Advance time so first hash's TEnd is in the past but not yet
	// past the 2*After grace. Then push second hash on the same pod.
	clk.advance(2 * time.Minute)
	trig.push(mkEvent(200))
	waitFor(t, "2 hashes", 300*time.Millisecond, func() bool { return c.Active() == 2 })

	// Advance to where the FIRST hash is past grace (3m after its
	// creation) but the SECOND is still alive (its TEnd is at
	// canonical+3m; grace would be +5m). Total clock progression
	// from canonical: 2m + 1m + 1s = 3m1s.
	clk.advance(time.Minute + time.Second)
	removed := c.PruneExpired()
	if removed != 1 {
		t.Fatalf("PruneExpired removed %d, want 1 (only the old hash)", removed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prunedPods) != 0 {
		t.Fatalf("OnPrune fired for a pod that still has 1 active hash; calls: %v", prunedPods)
	}
}

// TestController_OnAttribution_NotHeldUnderMutex — a slow callback
// must NOT block PruneExpired's progress (the controller must not
// be holding its own mutex while invoking user code).
//
// We arrange a synchronous OnPrune that blocks until we signal,
// then call PruneExpired in a goroutine and confirm that we can
// independently call Active() (which acquires the same mutex)
// without deadlocking.
func TestController_OnPrune_DoesNotHoldMutex(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{}
	clk := &fakeClock{t: canonicalEventTime}

	pruneInCallback := make(chan struct{})
	release := make(chan struct{})

	cfg := Config{
		Hostname: "node-1", Before: time.Minute, After: time.Minute,
		OnPrune: func(ns, pod string) {
			close(pruneInCallback)
			<-release
		},
	}
	c := New(trig, snk, cfg, clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	waitFor(t, "active=1", 200*time.Millisecond, func() bool { return c.Active() == 1 })

	clk.advance(3*time.Minute + time.Second)

	pruneDone := make(chan struct{})
	go func() {
		_ = c.PruneExpired()
		close(pruneDone)
	}()

	// Wait until the prune is inside the callback.
	select {
	case <-pruneInCallback:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("OnPrune did not fire within 500ms")
	}

	// Active() acquires the same mutex; if PruneExpired holds it
	// across the callback, this blocks forever.
	activeDone := make(chan int, 1)
	go func() { activeDone <- c.Active() }()

	select {
	case n := <-activeDone:
		if n != 0 {
			t.Fatalf("expected Active=0 (eviction happened before callback), got %d", n)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Active() blocked — PruneExpired is holding the mutex across user callback")
	}

	close(release)
	<-pruneDone
}

// TestEmptyResultSkip_NamespaceIsolation — the negative cache must
// not let one namespace's empty-streak suppress queries for a same-
// named pod in a different namespace. Two pods named "api" in "ns-a"
// vs "ns-b" sharing a single PEM node previously collided because
// the cache key was just "pod|table".
func TestEmptyResultSkip_NamespaceIsolation(t *testing.T) {
	clk := &fakeClock{t: canonicalEventTime}
	c := New(newFakeTrigger(), &fakeSink{}, Config{
		Hostname:              "node-1",
		Before:                time.Minute,
		After:                 time.Minute,
		EmptyResultSkipAfterN: 2,
		EmptyResultSkipTTL:    5 * time.Minute,
	}, clk)

	const table = "stirling_http_events"
	// Drive ns-a/api to N empty results — should arm the skip cache for ns-a/api only.
	for i := 0; i < 2; i++ {
		c.noteQueryResult("ns-a", "api", table, 0)
	}
	if !c.shouldSkipEmpty("ns-a", "api", table) {
		t.Fatalf("ns-a/api should be skip-armed after 2 empties")
	}
	if c.shouldSkipEmpty("ns-b", "api", table) {
		t.Fatalf("ns-b/api was wrongly suppressed by ns-a/api's empty streak " +
			"(skip cache key conflates namespaces)")
	}
}
