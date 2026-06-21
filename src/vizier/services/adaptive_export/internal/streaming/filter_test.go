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
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
)

func TestFilterUpdater_DebouncesMultipleDeltas(t *testing.T) {
	set := activeset.New()
	u := NewUpdater(set, UpdaterConfig{Debounce: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	ch := u.Subscribe()

	// Drain the initial snapshot (empty).
	<-ch

	// Bombard with 10 distinct upserts inside the debounce window.
	for i := 0; i < 10; i++ {
		set.Upsert(activeset.Key{Pod: string(rune('a' + i))}, time.Now().Add(time.Minute))
	}

	// Wait one debounce window + slack and count how many filter
	// emissions arrived. Should be exactly one — the coalesced one.
	deadline := time.After(300 * time.Millisecond)
	count := 0
	var lastF Filter
	collecting := true
	for collecting {
		select {
		case f := <-ch:
			count++
			lastF = f
		case <-deadline:
			collecting = false
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 coalesced filter emission, got %d", count)
	}
	if len(lastF.Pods) != 10 {
		t.Fatalf("expected 10 pods in coalesced filter, got %d", len(lastF.Pods))
	}
}

func TestFilterUpdater_FallsBackToUnfilteredOnSizeCap(t *testing.T) {
	set := activeset.New()
	u := NewUpdater(set, UpdaterConfig{
		Debounce:         20 * time.Millisecond,
		MaxAllowlistSize: 3,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	ch := u.Subscribe()
	<-ch // initial empty

	for i := 0; i < 5; i++ {
		set.Upsert(activeset.Key{Pod: string(rune('a' + i))}, time.Now().Add(time.Minute))
	}
	select {
	case f := <-ch:
		if f.Mode != FilterModeUnfiltered {
			t.Fatalf("expected unfiltered mode (5 > cap 3), got %v", f.Mode)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("no filter emission")
	}
}

// TestFilterUpdater_CapBoundary_AtLimit — exactly MaxAllowlistSize
// pods MUST stay in allowlist mode (not flip to unfiltered).
func TestFilterUpdater_CapBoundary_AtLimit(t *testing.T) {
	set := activeset.New()
	u := NewUpdater(set, UpdaterConfig{
		Debounce:         10 * time.Millisecond,
		MaxAllowlistSize: 3,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	ch := u.Subscribe()
	<-ch
	for i := 0; i < 3; i++ {
		set.Upsert(activeset.Key{Pod: string(rune('a' + i))}, time.Now().Add(time.Minute))
	}
	f := waitForFilter(t, ch, 300*time.Millisecond)
	if f.Mode != FilterModeAllowlist {
		t.Fatalf("at exactly cap=3, expected allowlist, got %v", f.Mode)
	}
	if len(f.Pods) != 3 {
		t.Fatalf("expected 3 pods in allowlist, got %d", len(f.Pods))
	}
}

// TestFilterUpdater_CapBoundary_OneOverLimit — cap+1 pods MUST flip
// to unfiltered. This is the exact boundary just above the cap.
func TestFilterUpdater_CapBoundary_OneOverLimit(t *testing.T) {
	set := activeset.New()
	u := NewUpdater(set, UpdaterConfig{
		Debounce:         10 * time.Millisecond,
		MaxAllowlistSize: 3,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	ch := u.Subscribe()
	<-ch
	for i := 0; i < 4; i++ {
		set.Upsert(activeset.Key{Pod: string(rune('a' + i))}, time.Now().Add(time.Minute))
	}
	f := waitForFilter(t, ch, 300*time.Millisecond)
	if f.Mode != FilterModeUnfiltered {
		t.Fatalf("at cap+1=4, expected unfiltered, got %v with %d pods", f.Mode, len(f.Pods))
	}
}

// TestFilterUpdater_CapBoundary_RecoversAfterShrink — going from
// unfiltered (set was huge) back to a small set MUST switch back to
// allowlist mode. Without this, a transient burst that hit the cap
// would force unfiltered mode forever.
func TestFilterUpdater_CapBoundary_RecoversAfterShrink(t *testing.T) {
	set := activeset.New()
	u := NewUpdater(set, UpdaterConfig{
		Debounce:         10 * time.Millisecond,
		MaxAllowlistSize: 3,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	ch := u.Subscribe()
	<-ch

	// Burst above cap.
	for i := 0; i < 10; i++ {
		set.Upsert(activeset.Key{Pod: string(rune('a' + i))}, time.Now().Add(time.Minute))
	}
	f := waitForFilter(t, ch, 300*time.Millisecond)
	if f.Mode != FilterModeUnfiltered {
		t.Fatalf("expected unfiltered after burst, got %v", f.Mode)
	}
	// Shrink back below cap.
	for i := 3; i < 10; i++ {
		set.Remove(activeset.Key{Pod: string(rune('a' + i))})
	}
	// Drain any intermediate filters; verify the LATEST emission is
	// back to allowlist mode.
	deadline := time.Now().Add(500 * time.Millisecond)
	last := f
	for time.Now().Before(deadline) {
		select {
		case last = <-ch:
		case <-time.After(100 * time.Millisecond):
		}
		if last.Mode == FilterModeAllowlist {
			return // recovered
		}
	}
	t.Fatalf("did not recover to allowlist mode after shrink; last mode=%v pods=%d",
		last.Mode, len(last.Pods))
}

// TestFilterUpdater_CapDisabled_AllowsAnySize — when MaxAllowlistSize <= 0
// the cap is disabled and even very large sets stay in allowlist mode.
func TestFilterUpdater_CapDisabled_AllowsAnySize(t *testing.T) {
	set := activeset.New()
	u := NewUpdater(set, UpdaterConfig{
		Debounce:         10 * time.Millisecond,
		MaxAllowlistSize: -1, // explicit disable
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	ch := u.Subscribe()
	<-ch
	for i := 0; i < 100; i++ {
		set.Upsert(activeset.Key{Pod: string(rune('a'+i%26)) + string(rune('a'+i/26))}, time.Now().Add(time.Minute))
	}
	f := waitForFilter(t, ch, 300*time.Millisecond)
	if f.Mode != FilterModeAllowlist {
		t.Fatalf("with cap disabled (=-1), expected allowlist; got %v", f.Mode)
	}
}

// waitForFilter polls ch until a filter shows up, returning it.
func waitForFilter(t *testing.T, ch <-chan Filter, timeout time.Duration) Filter {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(timeout):
		t.Fatalf("no filter within %v", timeout)
		return Filter{}
	}
}

func TestFilterUpdater_InitialSnapshotIsSeeded(t *testing.T) {
	set := activeset.New()
	set.Upsert(activeset.Key{Namespace: "n", Pod: "p1"}, time.Now().Add(time.Minute))
	u := NewUpdater(set, UpdaterConfig{Debounce: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	ch := u.Subscribe()
	select {
	case f := <-ch:
		if len(f.Pods) != 1 || f.Pods[0].Pod != "p1" {
			t.Fatalf("initial snapshot wrong: %+v", f)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("no initial filter")
	}
}
