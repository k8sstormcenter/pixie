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
	"sync/atomic"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
)

// AttributionNotifier decouples the controller's per-event callback
// (controller.handle) from ActiveSet writes. Without this shim, a
// stalled ActiveSet subscriber (e.g. a slow Supervisor under load)
// could back-pressure controller.handle and stall trigger consumption
// — i.e. lose the operator's main invariant: kubescape events are
// processed in time.
//
// Contract:
//   - Submit / SubmitRemove NEVER block. They drop on buffer overflow
//     and bump DroppedCount.
//   - One Run goroutine consumes the buffer and applies to ActiveSet.
//   - Filtered (host-pid / empty pod) events are counted separately so
//     drops vs filters can be distinguished in metrics.
type AttributionNotifier struct {
	set *activeset.ActiveSet
	cfg NotifierConfig
	in  chan notifyEvent

	dropped  atomic.Int64
	filtered atomic.Int64
}

// NotifierConfig tunes the notifier. Zero → safe defaults.
type NotifierConfig struct {
	// BufferSize is the input chan capacity. 0 → 1024 default.
	// Larger absorbs longer consumer stalls; smaller fails faster.
	// Producer drops the OLDEST event on overflow (we'd rather lose
	// stale activations than fresh ones).
	BufferSize int
}

func (c NotifierConfig) defaulted() NotifierConfig {
	if c.BufferSize <= 0 {
		c.BufferSize = 1024
	}
	return c
}

// notifyEvent is the discriminated-union we send across the buffer.
type notifyEvent struct {
	key    activeset.Key
	tEnd   time.Time
	remove bool
}

// NewAttributionNotifier wires a notifier. Call Run(ctx) to start
// the consumer goroutine.
func NewAttributionNotifier(set *activeset.ActiveSet, cfg NotifierConfig) *AttributionNotifier {
	c := cfg.defaulted()
	return &AttributionNotifier{
		set: set,
		cfg: c,
		in:  make(chan notifyEvent, c.BufferSize),
	}
}

// Submit hands an upsert to the notifier. Never blocks. Drops oldest
// on overflow + bumps DroppedCount. Host-pid (empty Pod) events are
// filtered here so the ActiveSet never sees them.
func (n *AttributionNotifier) Submit(key activeset.Key, tEnd time.Time) {
	if key.Pod == "" {
		n.filtered.Add(1)
		return
	}
	n.send(notifyEvent{key: key, tEnd: tEnd})
}

// SubmitRemove hands a removal. Same non-blocking contract as Submit.
func (n *AttributionNotifier) SubmitRemove(key activeset.Key) {
	if key.Pod == "" {
		n.filtered.Add(1)
		return
	}
	n.send(notifyEvent{key: key, remove: true})
}

// send is the non-blocking enqueue with drop-oldest semantics.
func (n *AttributionNotifier) send(e notifyEvent) {
	select {
	case n.in <- e:
	default:
		// Drop the OLDEST event then retry. If retry still fails
		// (consumer drained between the two operations and another
		// producer raced in), count this submit as dropped.
		select {
		case <-n.in:
			n.dropped.Add(1)
		default:
		}
		select {
		case n.in <- e:
		default:
			n.dropped.Add(1)
		}
	}
}

// Run owns one goroutine; drains the buffer until ctx cancellation.
// Best-effort drain on shutdown — anything remaining in the buffer
// after ctx.Done is dropped.
func (n *AttributionNotifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-n.in:
			if e.remove {
				n.set.Remove(e.key)
			} else {
				n.set.Upsert(e.key, e.tEnd)
			}
		}
	}
}

// DroppedCount returns the number of events lost to buffer overflow.
// Use this as a backpressure signal — non-zero means the consumer
// can't keep up.
func (n *AttributionNotifier) DroppedCount() int64 { return n.dropped.Load() }

// FilteredCount returns the number of events filtered (empty pod).
func (n *AttributionNotifier) FilteredCount() int64 { return n.filtered.Load() }

// SubmitFromController is a tiny convenience wrapper that matches
// the controller.Config.OnAttribution signature exactly, for
// idiomatic wiring in main.go:
//
//	ctlCfg.OnAttribution = notifier.SubmitFromController
func (n *AttributionNotifier) SubmitFromController(namespace, pod string, tEnd time.Time) {
	n.Submit(activeset.Key{Namespace: namespace, Pod: pod}, tEnd)
}

// RemoveFromController matches controller.Config.OnPrune signature.
func (n *AttributionNotifier) RemoveFromController(namespace, pod string) {
	n.SubmitRemove(activeset.Key{Namespace: namespace, Pod: pod})
}

// (Backpressure logging was deliberately not wired internally to
// avoid coupling the notifier to a particular log cadence. Callers
// observe via DroppedCount() and log on their own schedule.)
