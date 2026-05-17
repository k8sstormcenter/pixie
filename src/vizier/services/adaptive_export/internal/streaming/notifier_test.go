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
	"sync"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
)

// TestNotifier_NeverBlocksCaller — the synchronous callback path
// (controller.handle → cfg.OnAttribution → activeset.Upsert) must
// not block the caller even when the consuming end is slow.
//
// The current design exposes Upsert as a fast in-mem mutation, but
// once we wire a Notifier between controller and ActiveSet, the
// Notifier MUST guarantee bounded latency on the producer side.
func TestNotifier_CallerReturnsImmediatelyEvenIfConsumerStalls(t *testing.T) {
	set := activeset.New()
	// Deliberately no ctx / Run here — we want a stalled consumer
	// to prove producer never blocks.

	n := NewAttributionNotifier(set, NotifierConfig{BufferSize: 32})
	// Start the goroutine but DON'T let it drain — simulate stall
	// by NOT calling Run. The producer-side call MUST still return.
	// (We never start n.Run here on purpose.)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		// Submit MORE events than the buffer can hold.
		n.Submit(activeset.Key{Pod: "p"}, time.Now().Add(time.Minute))
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("1000 Submit() calls took %v — producer is blocking on a stalled consumer", elapsed)
	}
	// Sanity: at least some events were dropped (since we never started Run).
	if n.DroppedCount() == 0 {
		t.Fatalf("expected DroppedCount > 0 with no consumer, got 0")
	}
}

// TestNotifier_DeliversEventsWhenConsumerKeepsUp — happy path.
// We submit slowly enough vs a generously-sized buffer that the
// consumer trivially keeps up. Tests the basic delivery contract
// without measuring the buffer's drop semantics (that's covered by
// TestNotifier_DroppedCountAccurate).
func TestNotifier_DeliversEventsWhenConsumerKeepsUp(t *testing.T) {
	set := activeset.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Buffer >> burst so no drops are forced; throttle the submit
	// loop so the consumer gets scheduled between sends.
	n := NewAttributionNotifier(set, NotifierConfig{BufferSize: 1024})
	go n.Run(ctx)

	tEnd := time.Now().Add(5 * time.Minute)
	for i := 0; i < 50; i++ {
		n.Submit(activeset.Key{Pod: "p" + string(rune('a'+(i%26)))}, tEnd)
		if i%5 == 0 {
			// Yield so the consumer can drain — production callers
			// (controller.handle) naturally have inter-event gaps.
			time.Sleep(time.Microsecond)
		}
	}
	// Wait until consumer drains.
	deadline := time.Now().Add(500 * time.Millisecond)
	for set.Size() < 26 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if set.Size() != 26 {
		t.Fatalf("expected 26 distinct pods, got %d", set.Size())
	}
	if n.DroppedCount() != 0 {
		t.Fatalf("expected 0 drops with buffer>>burst, got %d", n.DroppedCount())
	}
}

// TestNotifier_SubmitConcurrentlySafe — the producer path must be
// safe under concurrent callers (controller has only one goroutine
// in handle, but the contract should be conservative).
func TestNotifier_SubmitConcurrentlySafe(t *testing.T) {
	set := activeset.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := NewAttributionNotifier(set, NotifierConfig{BufferSize: 256})
	go n.Run(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				n.Submit(activeset.Key{Pod: string(rune('a' + (i % 26)))}, time.Now().Add(time.Minute))
			}
		}()
	}
	wg.Wait()
	// Allow drain.
	deadline := time.Now().Add(500 * time.Millisecond)
	for set.Size() < 26 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if set.Size() == 0 {
		t.Fatalf("no pods landed in ActiveSet under concurrent Submit")
	}
}

// TestNotifier_RunStopsOnCtxCancel — must drain + return promptly
// on ctx cancellation.
func TestNotifier_RunStopsOnCtxCancel(t *testing.T) {
	set := activeset.New()
	ctx, cancel := context.WithCancel(context.Background())
	n := NewAttributionNotifier(set, NotifierConfig{BufferSize: 16})
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Run did not return within 500ms of ctx cancel")
	}
}

// TestNotifier_RemoveDeliveredAsRemoval — the Notifier must
// distinguish Upsert vs Remove events.
func TestNotifier_RemoveDeliveredAsRemoval(t *testing.T) {
	set := activeset.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := NewAttributionNotifier(set, NotifierConfig{BufferSize: 4})
	go n.Run(ctx)

	k := activeset.Key{Pod: "p1"}
	n.Submit(k, time.Now().Add(time.Minute))
	// drain
	deadline := time.Now().Add(300 * time.Millisecond)
	for set.Size() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if set.Size() != 1 {
		t.Fatalf("upsert didn't land")
	}
	n.SubmitRemove(k)
	deadline = time.Now().Add(300 * time.Millisecond)
	for set.Size() == 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if set.Size() != 0 {
		t.Fatalf("remove didn't land")
	}
}

// TestNotifier_DroppedCountAccurate — overflow accounting.
func TestNotifier_DroppedCountAccurate(t *testing.T) {
	set := activeset.New()
	n := NewAttributionNotifier(set, NotifierConfig{BufferSize: 4})
	// Don't run the consumer.
	const submits = 100
	for i := 0; i < submits; i++ {
		n.Submit(activeset.Key{Pod: "p"}, time.Now())
	}
	if got := n.DroppedCount(); got < int64(submits-4-1) { // allow ±1 slack on buffer count
		t.Fatalf("expected ~%d drops, got %d", submits-4, got)
	}
}

// TestNotifier_HostPidEntriesAreFiltered — host-pid events (empty
// Pod) cannot be streamed and must be dropped at the Notifier so the
// ActiveSet never accumulates pod-less rows.
func TestNotifier_HostPidEntriesAreFiltered(t *testing.T) {
	set := activeset.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := NewAttributionNotifier(set, NotifierConfig{BufferSize: 8})
	go n.Run(ctx)
	n.Submit(activeset.Key{Pod: ""}, time.Now().Add(time.Minute))
	n.Submit(activeset.Key{Pod: "real"}, time.Now().Add(time.Minute))
	deadline := time.Now().Add(300 * time.Millisecond)
	for set.Size() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if set.Size() != 1 {
		t.Fatalf("expected 1 entry (only real), got %d", set.Size())
	}
	if n.FilteredCount() < 1 {
		t.Fatalf("expected at least 1 filtered, got %d", n.FilteredCount())
	}
}

// staticAtomicCheck — make sure Stats accessors don't panic on
// a freshly-constructed notifier (no Run yet).
func TestNotifier_StatsOnFreshInstance(t *testing.T) {
	set := activeset.New()
	n := NewAttributionNotifier(set, NotifierConfig{})
	if n.DroppedCount() != 0 || n.FilteredCount() != 0 {
		t.Fatalf("fresh notifier should report zero counters")
	}
}
