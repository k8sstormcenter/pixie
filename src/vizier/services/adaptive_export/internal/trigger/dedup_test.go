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

package trigger

import "testing"

func TestDedupLRU_AddContains(t *testing.T) {
	d := newDedupLRU(8)
	if d.Contains("a") {
		t.Fatalf("empty LRU claims to contain a")
	}
	d.Add("a", 100)
	d.Add("b", 200)
	if !d.Contains("a") || !d.Contains("b") {
		t.Fatalf("added fingerprints not found")
	}
	if d.Len() != 2 {
		t.Fatalf("Len = %d, want 2", d.Len())
	}
	// Duplicate Add is a no-op (no double-entry, no reorder).
	d.Add("a", 100)
	if d.Len() != 2 {
		t.Fatalf("duplicate Add changed Len to %d", d.Len())
	}
}

func TestDedupLRU_CapacityEvictsOldestInsertion(t *testing.T) {
	d := newDedupLRU(3)
	d.Add("a", 1)
	d.Add("b", 2)
	d.Add("c", 3)
	d.Add("d", 4) // over capacity → "a" (oldest insertion) evicted
	if d.Contains("a") {
		t.Fatalf("oldest entry not evicted at capacity")
	}
	for _, fp := range []string{"b", "c", "d"} {
		if !d.Contains(fp) {
			t.Fatalf("entry %q evicted unexpectedly", fp)
		}
	}
	if d.Len() != 3 {
		t.Fatalf("Len = %d, want 3", d.Len())
	}
}

func TestDedupLRU_EvictBelow(t *testing.T) {
	d := newDedupLRU(8)
	d.Add("a", 100)
	d.Add("b", 200)
	d.Add("c", 300)
	d.EvictBelow(250)
	if d.Contains("a") || d.Contains("b") {
		t.Fatalf("entries below floor survived EvictBelow")
	}
	if !d.Contains("c") {
		t.Fatalf("entry at/above floor was evicted")
	}
	// EvictBelow stops at the first entry >= floor (prefix semantics):
	// a late arrival (low evn inserted AFTER a higher one) survives —
	// documented as harmless.
	d.Add("late", 50)
	d.EvictBelow(250)
	if !d.Contains("late") {
		t.Fatalf("late-arrival entry behind a newer one should survive prefix eviction")
	}
}

func TestDedupLRU_ZeroCapacityIsSafe(t *testing.T) {
	d := newDedupLRU(0) // clamped to 1
	d.Add("a", 1)
	if !d.Contains("a") {
		t.Fatalf("single entry not retained")
	}
	d.Add("b", 2)
	if d.Contains("a") || !d.Contains("b") {
		t.Fatalf("capacity-1 eviction wrong: a=%v b=%v", d.Contains("a"), d.Contains("b"))
	}
}
