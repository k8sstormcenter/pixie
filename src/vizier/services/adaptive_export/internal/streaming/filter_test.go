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
		MaxWhitelistSize: 3,
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
