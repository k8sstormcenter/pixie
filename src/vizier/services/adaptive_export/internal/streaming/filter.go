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

// Package streaming implements the rev-3 push-flow: long-running
// PxL submissions per pixie table, with a pod allowlist derived from
// the ActiveSet. See .local/adaptive-write-rev3-plan.md for the full
// architectural rationale.
package streaming

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
)

// FilterMode selects how the embedded PxL allowlist is constructed.
type FilterMode int

const (
	// FilterModeAllowlist embeds an explicit pod list in the PxL
	// `df = df[df.pod.in_([...])]` clause. Optimal while the set is
	// small.
	FilterModeAllowlist FilterMode = iota

	// FilterModeUnfiltered emits the script WITHOUT a pod filter —
	// the stream returns ALL pods on this node. Used when the active
	// set exceeds MaxAllowlistSize: the PxL script-size limit + parse
	// cost would dominate; we prefer to pull everything and filter
	// in the operator's CH writer. Memory-speed filtering beats
	// linear-in-N PxL parse cost.
	FilterModeUnfiltered
)

// String for log output.
func (m FilterMode) String() string {
	switch m {
	case FilterModeAllowlist:
		return "allowlist"
	case FilterModeUnfiltered:
		return "unfiltered"
	default:
		return "unknown"
	}
}

// Filter is the immutable snapshot that a TableScanner uses to
// produce one PxL submission.
type Filter struct {
	Mode    FilterMode
	Pods    []activeset.Key // populated iff Mode == Allowlist
	Version uint64          // ActiveSet version this filter was derived from
}

// UpdaterConfig tunes the FilterUpdater.
type UpdaterConfig struct {
	// Debounce coalesces multiple ActiveSet deltas into one filter
	// emission. With many concurrent activations (e.g. cluster-wide
	// incident), this caps re-submission rate at 1 / Debounce per
	// TableScanner. 0 → 1 second default.
	Debounce time.Duration

	// MaxAllowlistSize is the threshold at which we switch to
	// FilterModeUnfiltered. 0 → 500 default. -1 disables the cap
	// (allowlist always; PxL parse cost is yours to own).
	MaxAllowlistSize int

	// SubscribeBuffer is the per-subscriber delta buffer size on the
	// underlying ActiveSet subscription. 0 → 32 default.
	SubscribeBuffer int
}

func (c UpdaterConfig) defaulted() UpdaterConfig {
	if c.Debounce <= 0 {
		c.Debounce = 1 * time.Second
	}
	if c.MaxAllowlistSize == 0 {
		c.MaxAllowlistSize = 500
	}
	if c.SubscribeBuffer <= 0 {
		c.SubscribeBuffer = 32
	}
	return c
}

// FilterUpdater bridges ActiveSet → TableScanner. It subscribes to
// ActiveSet deltas, debounces them, and emits a coalesced Filter on
// its output channel. Run() owns one goroutine.
type FilterUpdater struct {
	set *activeset.ActiveSet
	cfg UpdaterConfig

	// deltaCh is the underlying ActiveSet subscription, established
	// at construction (not in Run) so callers can deterministically
	// Upsert into `set` after NewUpdater returns and know those
	// upserts will be delivered. Without this, Run's goroutine
	// might not have subscribed to the set yet when the first
	// Upsert lands → silent drop.
	deltaCh <-chan activeset.Delta

	mu     sync.Mutex
	subs   []chan Filter
	closed bool
}

// NewUpdater wires an updater AND establishes its ActiveSet
// subscription. Call Run(ctx) to start its goroutine.
func NewUpdater(set *activeset.ActiveSet, cfg UpdaterConfig) *FilterUpdater {
	d := cfg.defaulted()
	return &FilterUpdater{
		set:     set,
		cfg:     d,
		deltaCh: set.Subscribe(d.SubscribeBuffer),
	}
}

// Subscribe returns a buffered channel that receives a fresh Filter
// after each debounce window in which one or more deltas landed.
// Plus one initial Filter representing the current snapshot, so a
// subscriber can build its first PxL submission without waiting.
//
// Channel is closed when ctx (from Run) is cancelled.
func (u *FilterUpdater) Subscribe() <-chan Filter {
	u.mu.Lock()
	defer u.mu.Unlock()
	ch := make(chan Filter, 4)
	if !u.closed {
		// Seed with the current snapshot so first PxL submission
		// doesn't have to wait for a delta to arrive.
		ch <- u.computeFilter()
	}
	u.subs = append(u.subs, ch)
	return ch
}

// Run owns the FilterUpdater goroutine until ctx is cancelled.
//
// Lifecycle:
//
//	deltaCh = set.Subscribe(buffer)
//	for {
//	    select {
//	    case <-ctx.Done(): close subs; return
//	    case <-deltaCh: schedule a fire at now+Debounce (idempotent)
//	    case <-fireTimer: compute filter; broadcast to subs
//	    }
//	}
//
// The fire-timer is rearmed only when a delta arrives; in steady
// state with no deltas, this goroutine is dormant.
func (u *FilterUpdater) Run(ctx context.Context) {
	defer u.closeSubs()
	defer u.set.Unsubscribe(u.deltaCh)

	var pendingTimer *time.Timer
	var pendingC <-chan time.Time
	arm := func() {
		if pendingTimer != nil {
			return // already scheduled
		}
		pendingTimer = time.NewTimer(u.cfg.Debounce)
		pendingC = pendingTimer.C
	}
	disarm := func() {
		if pendingTimer != nil {
			pendingTimer.Stop()
			pendingTimer = nil
			pendingC = nil
		}
	}

	for {
		select {
		case <-ctx.Done():
			disarm()
			return

		case _, ok := <-u.deltaCh:
			if !ok {
				// ActiveSet shutdown: disarm any pending timer so its
				// goroutine doesn't outlive Run trying to send on
				// pendingC (CodeRabbit r3379377645).
				disarm()
				return
			}
			arm()

		case <-pendingC:
			disarm()
			f := u.computeFilter()
			u.broadcast(f)
			log.WithFields(log.Fields{
				"mode":    f.Mode,
				"pods":    len(f.Pods),
				"version": f.Version,
			}).Info("streaming.FilterUpdater: emitted filter")
		}
	}
}

// computeFilter snapshots the ActiveSet and decides whether to embed
// an allowlist or fall back to unfiltered mode based on size.
func (u *FilterUpdater) computeFilter() Filter {
	keys, version := u.set.Snapshot()
	if u.cfg.MaxAllowlistSize > 0 && len(keys) > u.cfg.MaxAllowlistSize {
		return Filter{Mode: FilterModeUnfiltered, Version: version}
	}
	return Filter{Mode: FilterModeAllowlist, Pods: keys, Version: version}
}

// broadcast non-blockingly delivers to every subscriber. Subscribers
// that fall behind get the OLDEST filter dropped — the newest state
// always reaches them (their PxL re-submission is what matters; old
// filter versions are stale by construction).
func (u *FilterUpdater) broadcast(f Filter) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, ch := range u.subs {
		select {
		case ch <- f:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- f:
			default:
			}
		}
	}
}

func (u *FilterUpdater) closeSubs() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	for _, ch := range u.subs {
		close(ch)
	}
	u.subs = nil
}
