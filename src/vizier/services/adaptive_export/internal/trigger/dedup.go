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

import "container/list"

// dedupLRU is a bounded, insertion-ordered set of row fingerprints,
// each tagged with the row's normalized event_time (nanos). It is the
// #97 (F8/AE-9) extension of the old single-boundary `seenAtBoundary`
// map: with a bounded lookback the trigger re-fetches every row in
// [watermark-Lookback, watermark] on each poll, so dedup must cover the
// whole window, not just the exact watermark boundary.
//
// Eviction is two-fold:
//   - EvictBelow(floor): entries whose event_time has slid below the
//     lookback floor can never be returned by the SELECT again, so they
//     are dropped eagerly to keep the set at ~window size.
//   - capacity: Add evicts the OLDEST INSERTION when over max, bounding
//     memory even if the window holds more rows than expected. An
//     in-window entry evicted by capacity may cause one duplicate emit —
//     the documented trade-off for bounded memory (size it >= the max
//     rows per window; default 4*PollLimit).
//
// Not goroutine-safe; owned by the single poll loop.
type dedupLRU struct {
	max   int
	ll    *list.List // front = oldest insertion
	items map[string]*list.Element
}

type dedupEntry struct {
	fp  string
	evn uint64 // normalized event_time (nanos)
}

func newDedupLRU(capacity int) *dedupLRU {
	if capacity <= 0 {
		capacity = 1
	}
	return &dedupLRU{max: capacity, ll: list.New(), items: map[string]*list.Element{}}
}

// Contains reports whether fp was Added and not yet evicted.
func (d *dedupLRU) Contains(fp string) bool {
	_, ok := d.items[fp]
	return ok
}

// Add records fp with its normalized event_time. No-op if already
// present. Evicts oldest insertions while over capacity.
func (d *dedupLRU) Add(fp string, evn uint64) {
	if _, ok := d.items[fp]; ok {
		return
	}
	d.items[fp] = d.ll.PushBack(dedupEntry{fp: fp, evn: evn})
	for d.ll.Len() > d.max {
		d.removeElement(d.ll.Front())
	}
}

// EvictBelow drops entries with evn < floor, popping from the oldest
// insertion. Insertion order tracks the poll's ORDER BY event_time, so
// in the common case this removes exactly the expired prefix. A late
// arrival (low evn inserted after a higher one) may survive behind a
// newer entry until capacity eviction — harmless: Contains on an
// expired fp only suppresses a row the SELECT can no longer return.
func (d *dedupLRU) EvictBelow(floor uint64) {
	for e := d.ll.Front(); e != nil; {
		if e.Value.(dedupEntry).evn >= floor {
			return
		}
		next := e.Next()
		d.removeElement(e)
		e = next
	}
}

// Len returns the number of live entries.
func (d *dedupLRU) Len() int { return d.ll.Len() }

func (d *dedupLRU) removeElement(e *list.Element) {
	delete(d.items, e.Value.(dedupEntry).fp)
	d.ll.Remove(e)
}
