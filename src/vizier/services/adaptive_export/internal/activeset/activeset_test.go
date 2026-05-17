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

package activeset

import (
	"sync"
	"testing"
	"time"
)

func TestUpsertEmitsAddedDelta(t *testing.T) {
	s := New()
	ch := s.Subscribe(4)
	s.Upsert(Key{Namespace: "ns", Pod: "p1"}, time.Now().Add(5*time.Minute))
	select {
	case d := <-ch:
		if len(d.Added) != 1 || d.Added[0].Pod != "p1" {
			t.Fatalf("expected added=[p1], got %+v", d)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("no delta")
	}
}

func TestUpsertExtendDoesNotEmitDelta(t *testing.T) {
	s := New()
	ch := s.Subscribe(4)
	k := Key{Namespace: "ns", Pod: "p1"}
	t0 := time.Now()
	s.Upsert(k, t0.Add(1*time.Minute))
	<-ch // drain initial add
	s.Upsert(k, t0.Add(5*time.Minute))
	select {
	case d := <-ch:
		t.Fatalf("unexpected delta on pure extension: %+v", d)
	case <-time.After(100 * time.Millisecond):
		// good
	}
}

func TestRemoveEmitsRemovedDelta(t *testing.T) {
	s := New()
	ch := s.Subscribe(4)
	k := Key{Namespace: "ns", Pod: "p1"}
	s.Upsert(k, time.Now().Add(1*time.Minute))
	<-ch
	s.Remove(k)
	select {
	case d := <-ch:
		if len(d.Removed) != 1 || d.Removed[0].Pod != "p1" {
			t.Fatalf("expected removed=[p1], got %+v", d)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("no delta")
	}
}

func TestPruneExpiredBatchesRemovals(t *testing.T) {
	s := New()
	ch := s.Subscribe(4)
	now := time.Now()
	s.Upsert(Key{Pod: "a"}, now.Add(-time.Minute)) // already expired
	s.Upsert(Key{Pod: "b"}, now.Add(time.Minute))  // still active
	s.Upsert(Key{Pod: "c"}, now.Add(-time.Second)) // already expired
	// drain the three add deltas
	for i := 0; i < 3; i++ {
		<-ch
	}
	removed := s.PruneExpired(now)
	if len(removed) != 2 {
		t.Fatalf("expected 2 removals, got %d (%v)", len(removed), removed)
	}
	select {
	case d := <-ch:
		if len(d.Removed) != 2 {
			t.Fatalf("expected single delta with 2 removals, got %+v", d)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("no delta from PruneExpired")
	}
}

func TestSnapshotReturnsCurrentMembers(t *testing.T) {
	s := New()
	s.Upsert(Key{Namespace: "n1", Pod: "p1"}, time.Now().Add(time.Minute))
	s.Upsert(Key{Namespace: "n2", Pod: "p2"}, time.Now().Add(time.Minute))
	keys, v := s.Snapshot()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if v == 0 {
		t.Fatalf("version should have advanced")
	}
}

func TestSubscriberOverflowDropsOldest(t *testing.T) {
	s := New()
	ch := s.Subscribe(2) // tiny buffer
	for i := 0; i < 10; i++ {
		s.Upsert(Key{Pod: string(rune('a' + i))}, time.Now().Add(time.Minute))
	}
	// We expect at most buffer-size deltas to survive — the rest were dropped.
	collected := 0
	for {
		select {
		case <-ch:
			collected++
		case <-time.After(50 * time.Millisecond):
			if collected == 0 {
				t.Fatalf("got zero deltas; broadcast is broken")
			}
			if collected > 2 {
				t.Fatalf("got %d deltas from a 2-buffer channel; drop-oldest broken", collected)
			}
			return
		}
	}
}

func TestConcurrentUpsertsAreSafe(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Upsert(Key{Pod: string(rune('a' + (i % 26)))}, time.Now().Add(time.Minute))
		}()
	}
	wg.Wait()
	if s.Size() == 0 {
		t.Fatalf("size 0 after 50 concurrent upserts")
	}
}

func TestRenderKey(t *testing.T) {
	if got := (Key{Namespace: "n", Pod: "p"}).Render(); got != "n/p" {
		t.Fatalf("render = %q, want n/p", got)
	}
	if got := (Key{Pod: "p"}).Render(); got != "p" {
		t.Fatalf("render(no ns) = %q, want p", got)
	}
}
