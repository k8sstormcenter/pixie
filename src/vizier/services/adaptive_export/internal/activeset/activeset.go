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

// Package activeset owns the "currently being streamed" pod set for
// the rev-3 adaptive-write streaming path. One ActiveSet per
// operator process.
//
// Why it exists: rev-2's pushPixieRows fan-out gated streaming
// per-(hash, table); the fan-out spawned an O(active_hashes × tables)
// concurrency tree that DoS'd vizier-query-broker under load. Rev-3
// inverts the relationship: ONE PxL submission per table per refresh,
// embedding a whitelist drawn from this ActiveSet. The set is keyed
// per-pod, not per-hash, because pixie events have no hash dimension
// — multiple anomaly hashes on the same pod share one stream slot.
//
// Membership is computed from kubescape attribution: a pod is in the
// set iff there is at least one anomaly-attribution row for it whose
// t_end is in the future.
package activeset

import (
	"sync"
	"time"
)

// Key identifies one pod in the set. "namespace/pod" matches what
// `px.upid_to_pod_name` returns inside PxL, so embedding Keys verbatim
// into a PxL whitelist filter requires no transformation.
type Key struct {
	Namespace string
	Pod       string
}

// Render returns the "namespace/pod" form used in PxL whitelists.
// Pod-only Keys (empty Namespace) render as bare "pod" — kept for
// host-pid edge cases though those don't currently reach a stream.
func (k Key) Render() string {
	if k.Namespace == "" {
		return k.Pod
	}
	return k.Namespace + "/" + k.Pod
}

// Delta describes a change to the set. Subscribers receive deltas
// to know when to re-evaluate stream submissions. Both slices may
// be non-empty in a single delta when concurrent upserts and prunes
// land in the same delivery window.
type Delta struct {
	Added   []Key
	Removed []Key
	Version uint64 // monotonic; matches the post-delta version of the set
}

// ActiveSet is a goroutine-safe, version-counted pod set with
// fan-out delta delivery.
type ActiveSet struct {
	mu      sync.Mutex
	members map[Key]time.Time // pod → t_end (when the active window expires absent further extension)
	version uint64

	// subs are independent buffered channels — one per subscriber.
	// Buffered so a slow consumer can't block an upserter; oldest
	// delta is dropped on overflow (subscriber observes a version
	// skip and is expected to re-snapshot).
	subsMu sync.Mutex
	subs   []chan Delta
}

// New returns an empty ActiveSet.
func New() *ActiveSet {
	return &ActiveSet{
		members: map[Key]time.Time{},
	}
}

// Upsert sets or extends a pod's t_end. Idempotent — if the pod is
// already present with a >= t_end, no delta is emitted (caller-side
// dedup of trivial extensions; saves debouncer churn).
func (s *ActiveSet) Upsert(k Key, tEnd time.Time) {
	s.mu.Lock()
	prev, existed := s.members[k]
	if existed && !tEnd.After(prev) {
		s.mu.Unlock()
		return // no-op extension; quietly skip
	}
	s.members[k] = tEnd
	s.version++
	v := s.version
	s.mu.Unlock()

	if !existed {
		s.broadcast(Delta{Added: []Key{k}, Version: v})
	}
	// extension (existed && tEnd > prev) doesn't change membership;
	// no delta needed — subscribers don't care about t_end shifts of
	// already-present pods.
}

// Remove drops a pod. No-op if not present. Always emits a delta on
// real removals so subscribers can shrink whitelists.
func (s *ActiveSet) Remove(k Key) {
	s.mu.Lock()
	if _, ok := s.members[k]; !ok {
		s.mu.Unlock()
		return
	}
	delete(s.members, k)
	s.version++
	v := s.version
	s.mu.Unlock()
	s.broadcast(Delta{Removed: []Key{k}, Version: v})
}

// PruneExpired removes every pod whose t_end is at or before `at`.
// Returns the removed keys for caller-side logging. Emits ONE delta
// containing all removals so subscribers re-evaluate once.
func (s *ActiveSet) PruneExpired(at time.Time) []Key {
	s.mu.Lock()
	var removed []Key
	for k, tEnd := range s.members {
		if !tEnd.After(at) {
			removed = append(removed, k)
			delete(s.members, k)
		}
	}
	if len(removed) == 0 {
		s.mu.Unlock()
		return nil
	}
	s.version++
	v := s.version
	s.mu.Unlock()
	s.broadcast(Delta{Removed: removed, Version: v})
	return removed
}

// Snapshot returns the current set + version atomically. Caller owns
// the returned slice — safe to mutate. Use this on subscription to
// build the initial whitelist before listening for deltas.
func (s *ActiveSet) Snapshot() ([]Key, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Key, 0, len(s.members))
	for k := range s.members {
		out = append(out, k)
	}
	return out, s.version
}

// Size returns the current membership count (test + metric helper).
func (s *ActiveSet) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.members)
}

// Subscribe returns a channel of deltas. Buffer size sets the
// tolerance for slow consumers; the channel drops oldest deltas on
// overflow and subscribers MUST re-snapshot if they detect a version
// gap. Channel is closed when ctx-equivalent shutdown is signalled
// via Unsubscribe.
func (s *ActiveSet) Subscribe(buffer int) <-chan Delta {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan Delta, buffer)
	s.subsMu.Lock()
	s.subs = append(s.subs, ch)
	s.subsMu.Unlock()
	return ch
}

// Unsubscribe removes and closes a previously-returned channel.
// Idempotent (no error on unknown chan).
func (s *ActiveSet) Unsubscribe(ch <-chan Delta) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for i, c := range s.subs {
		// compare on the directional alias — Go permits this implicit conversion
		if (<-chan Delta)(c) == ch {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			close(c)
			return
		}
	}
}

// broadcast attempts to send to every subscriber non-blockingly. On
// buffer overflow the OLDEST delta is dropped so the most recent
// state-change always reaches the subscriber (it'll re-snapshot if
// the version gap matters). This is the contract: subscribers MUST
// tolerate dropped deltas + use Snapshot to reconcile.
func (s *ActiveSet) broadcast(d Delta) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, c := range s.subs {
		select {
		case c <- d:
		default:
			// Drop oldest by draining one then sending.
			select {
			case <-c:
			default:
			}
			select {
			case c <- d:
			default:
			}
		}
	}
}
