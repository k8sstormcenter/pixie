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
	mu      sync.Mutex
	writes  []sink.AttributionRow
	preload []sink.AttributionRow
	werr    error
	qerr    error
}

func (f *fakeSink) WritePixieRows(_ context.Context, _ string, _ []map[string]any) error {
	return nil
}

func (f *fakeSink) Write(_ context.Context, rows []sink.AttributionRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.werr != nil {
		return f.werr
	}
	f.writes = append(f.writes, rows...)
	return nil
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

	clk.advance(2 * time.Minute) // past t_end (now+1min)
	if r := c.PruneExpired(); r != 1 {
		t.Fatalf("PruneExpired removed %d, want 1", r)
	}
	if c.Active() != 0 {
		t.Fatalf("Active after prune = %d, want 0", c.Active())
	}
}

// TestController_SinkErrorNonFatal — controller does not crash on Sink.Write error.
func TestController_SinkErrorNonFatal(t *testing.T) {
	trig := newFakeTrigger()
	snk := &fakeSink{werr: errors.New("ch unreachable")}
	clk := &fakeClock{t: canonicalEventTime}
	c := New(trig, snk, defaultCfg(), clk)
	stop := runController(t, c, trig)
	defer stop()

	trig.push(canonicalEvent())
	// Wait for the handler to process the event (no fixed sleep).
	waitFor(t, "active=1 despite sink error", 200*time.Millisecond, func() bool { return c.Active() == 1 })
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
