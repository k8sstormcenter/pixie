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

// Package controller orchestrates the adaptive-write push flow on a
// single node:
//
//   1. Subscribe to a Trigger that produces kubescape.Event values.
//   2. For each event, derive the workload anomaly.Target + AnomalyHash,
//      look up the in-memory active set for this hostname, and either
//      open a new active row or extend an existing one (t_end ← now+after).
//   3. Persist the resulting AttributionRow to ClickHouse via Sink.
//
// The controller does NOT execute PxL itself, does NOT write pixie
// observation rows, and does NOT manage retention scripts. Pixie's
// retention plugin (driven by user-defined PxL scripts in the UI)
// owns those concerns. Operator's only output is forensic_db.adaptive_attribution.
package controller

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/kubescape"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
)

// Trigger is the source of new kubescape events.
type Trigger interface {
	Subscribe(ctx context.Context) (<-chan kubescape.Event, error)
}

// Sink writes attribution rows to ClickHouse and, on boot, can fetch
// still-active rows so the controller can rehydrate after a crash.
type Sink interface {
	Write(ctx context.Context, rows []sink.AttributionRow) error
	QueryActive(ctx context.Context, hostname string) ([]sink.AttributionRow, error)
}

// Clock abstracts time for tests.
type Clock interface {
	Now() time.Time
}

// RealClock is the production Clock.
type RealClock struct{}

// Now returns time.Now().
func (RealClock) Now() time.Time { return time.Now() }

// Config tunes the controller. Zero values fall through to safe defaults.
type Config struct {
	// Hostname is the node-local key. REQUIRED.
	Hostname string

	// Before / After form the time window: t_start = event_time - Before,
	// t_end = max(t_end, now + After). Both default to 5 min.
	Before time.Duration
	After  time.Duration
}

func (c *Config) defaulted() Config {
	out := *c
	if out.Before == 0 {
		out.Before = 5 * time.Minute
	}
	if out.After == 0 {
		out.After = 5 * time.Minute
	}
	return out
}

// Controller is the live orchestrator. One instance per operator process.
type Controller struct {
	trig  Trigger
	sink  Sink
	clock Clock
	cfg   Config

	mu     sync.Mutex
	active map[anomaly.AnomalyHash]*sink.AttributionRow
}

// New wires a Controller. nil clock falls through to RealClock.
func New(trig Trigger, snk Sink, cfg Config, clk Clock) *Controller {
	if clk == nil {
		clk = RealClock{}
	}
	return &Controller{
		trig:   trig,
		sink:   snk,
		clock:  clk,
		cfg:    cfg.defaulted(),
		active: map[anomaly.AnomalyHash]*sink.AttributionRow{},
	}
}

// Rehydrate populates the in-memory active set from ClickHouse so a
// restarted operator picks up where it left off. Idempotent. Call
// once at boot before Run.
func (c *Controller) Rehydrate(ctx context.Context) error {
	rows, err := c.sink.QueryActive(ctx, c.cfg.Hostname)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range rows {
		row := rows[i]
		c.active[row.AnomalyHash] = &row
	}
	log.WithField("rehydrated", len(rows)).Info("controller: active set restored")
	return nil
}

// Run subscribes to the trigger and processes events until ctx is
// cancelled or the trigger closes its channel. Returns ctx.Err() on
// cancellation or nil on graceful trigger shutdown.
func (c *Controller) Run(ctx context.Context) error {
	ch, err := c.trig.Subscribe(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			c.handle(ctx, ev)
		}
	}
}

// handle processes one event: open or extend the attribution row,
// then persist to ClickHouse. Errors from Sink.Write are logged but
// not fatal — system stability rule.
func (c *Controller) handle(ctx context.Context, ev kubescape.Event) {
	hash := anomaly.Hash(ev.Target)
	now := c.clock.Now()
	tEvent := time.Unix(0, int64(ev.EventTime)).UTC()

	c.mu.Lock()
	row, exists := c.active[hash]
	if !exists {
		row = &sink.AttributionRow{
			AnomalyHash: hash,
			Namespace:   ev.Target.Namespace,
			Pod:         ev.Target.Pod,
			Comm:        ev.Target.Comm,
			PID:         ev.Target.PID,
			Hostname:    c.cfg.Hostname,
			TStart:      tEvent.Add(-c.cfg.Before),
			TEnd:        now.Add(c.cfg.After),
			LastSeen:    tEvent,
			LastRuleID:  ev.RuleID,
			NAnomalies:  1,
		}
		c.active[hash] = row
	} else {
		// Extend t_end if the new now+after is later. Never shrink.
		if proposed := now.Add(c.cfg.After); proposed.After(row.TEnd) {
			row.TEnd = proposed
		}
		// Update last_seen if this event's timestamp is more recent.
		if tEvent.After(row.LastSeen) {
			row.LastSeen = tEvent
		}
		row.LastRuleID = ev.RuleID
		row.NAnomalies++
	}
	snapshot := *row
	c.mu.Unlock()

	if err := c.sink.Write(ctx, []sink.AttributionRow{snapshot}); err != nil {
		log.WithError(err).Warn("controller: sink write failed")
	}
}

// Active returns the count of in-memory active hashes (test helper).
func (c *Controller) Active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

// PruneExpired removes from the in-memory active set every entry whose
// t_end is in the past. ClickHouse's ReplacingMergeTree handles
// table-side cleanup; this just keeps the operator's RAM bounded.
// Caller invokes on a periodic timer.
func (c *Controller) PruneExpired() int {
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for h, row := range c.active {
		if !row.TEnd.After(now) {
			delete(c.active, h)
			removed++
		}
	}
	return removed
}
