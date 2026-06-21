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
//  1. Subscribe to a Trigger that produces kubescape.Event values.
//  2. For each event, derive the workload anomaly.Target + AnomalyHash,
//     look up the in-memory active set for this hostname, and either
//     open a new active row or extend an existing one (t_end ← now+after).
//  3. Persist the resulting AttributionRow to ClickHouse via Sink.
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
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/pxl"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/reconcile"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
)

// Trigger is the source of new kubescape events.
type Trigger interface {
	Subscribe(ctx context.Context) (<-chan kubescape.Event, error)
}

// Sink writes attribution rows to ClickHouse and, on boot, can fetch
// still-active rows so the controller can rehydrate after a crash.
// WritePixieRows is the rev-1 fallback path for environments where
// the cloud's retention plugin can't reach the in-cluster CH (so the
// operator queries pixie itself and pushes rows directly).
type Sink interface {
	Write(ctx context.Context, rows []sink.AttributionRow) error
	QueryActive(ctx context.Context, hostname string) ([]sink.AttributionRow, error)
	WritePixieRows(ctx context.Context, table string, rows []map[string]any) error
}

// PixieQuerier is the rev-1 path's executor: take a PxL string and
// return the resulting rows. nil disables operator-side pixie pushes
// (rev-2 default — the cloud's plugin handles it).
type PixieQuerier interface {
	Query(ctx context.Context, pxl string) ([]map[string]any, error)
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

	// Rec records per-pull read/wrote counts for the FILTER fan-out path
	// (ADAPTIVE_RECONCILE). nil → reconcile.Nop{} in New (instrument off).
	Rec reconcile.Recorder

	// Before / After form the time window: t_start = event_time - Before,
	// t_end = max(t_end, now + After). Both default to 5 min.
	Before time.Duration
	After  time.Duration

	// PushPixieTables, when non-empty alongside a non-nil Pixie querier,
	// makes the controller query pixie for every named table on each
	// fresh anomaly window and push the result directly to
	// forensic_db.<table>. Used in environments where the cloud's
	// retention plugin can't reach the in-cluster CH service.
	PushPixieTables []string

	// PushRefreshInterval — how often pushPixieRows re-queries pixie
	// while the attribution window is still active. The first query
	// covers [t_start, now]; subsequent queries cover only the new
	// per-table slice [last_upper[table], now] so we don't duplicate
	// rows. Zero (the natural Go default for unset env vars) is
	// rewritten to 30s in defaulted(). To DISABLE periodic re-fan-out
	// (single-shot mode, which loses pixie traffic that arrives after
	// the kubescape event) set this to a NEGATIVE duration — pick -1
	// to be unambiguous.
	PushRefreshInterval time.Duration

	// === Throughput-protection knobs ===
	//
	// At high anomaly rates (many concurrent active hashes), the default
	// pushPixieRows behavior — N parallel PxL queries per hash, no
	// global cap — can DoS the vizier-query-broker (observed: 90% of
	// queries DeadlineExceeded at 180s under 4× sweep load). The three
	// knobs below are independent throttles; all default to 0 (= legacy
	// unbounded behavior preserved).
	//
	// MaxParallelQueriesPerHash caps concurrent goroutines INSIDE one
	// pushPixieRows pass. 0 = no cap (current). Recommended 3-5 for
	// load-protective deployments.
	MaxParallelQueriesPerHash int

	// MaxInflightQueriesGlobal caps concurrent PxL queries across all
	// pushPixieRows goroutines (every hash). 0 = no cap (current).
	// Recommended 20-50 — sized to broker capacity.
	MaxInflightQueriesGlobal int

	// EmptyResultSkipAfterN: after this many consecutive 0-row returns
	// for the same (pod, table) pair, skip that pair on subsequent
	// passes for EmptyResultSkipTTL. 0 = disabled (current). A pgsql
	// pod that never speaks HTTP returns 0 on every http_events
	// query; skipping eliminates that waste.
	EmptyResultSkipAfterN int

	// EmptyResultSkipTTL controls how long a (pod, table) stays in the
	// negative cache. 0 = disabled (current). When the TTL expires the
	// pair is retried, so a pod that newly starts a protocol
	// self-heals within at most TTL seconds.
	EmptyResultSkipTTL time.Duration

	// OnAttribution, when non-nil, is called for every event after
	// the attribution row has been computed (whether the row is new
	// or an extension). The rev-3 streaming path uses this to feed
	// its ActiveSet without touching controller internals.
	//
	// Contract:
	//   - Called from controller.handle's goroutine.
	//   - Synchronous; do NOT block. Callbacks that need to do work
	//     should hand off to a goroutine + buffered channel internally.
	//   - tEnd is the post-event t_end (= now + After for new rows,
	//     or the extended value for existing ones).
	OnAttribution func(namespace, pod string, tEnd time.Time)

	// OnPrune, when non-nil, is called for each hash evicted by
	// PruneExpired with the (namespace, pod) of the evicted row.
	// Used by the rev-3 streaming path to shrink its ActiveSet.
	// Same contract as OnAttribution: synchronous, non-blocking.
	OnPrune func(namespace, pod string)
}

func (c *Config) defaulted() Config {
	out := *c
	if out.Before == 0 {
		out.Before = 5 * time.Minute
	}
	if out.After == 0 {
		out.After = 5 * time.Minute
	}
	// Zero → fall through to the 30s default. NEGATIVE values are
	// preserved so callers can explicitly request single-shot mode
	// (see PushRefreshInterval doc above).
	if out.PushRefreshInterval == 0 {
		out.PushRefreshInterval = 30 * time.Second
	}
	return out
}

// Controller is the live orchestrator. One instance per operator process.
type Controller struct {
	trig    Trigger
	sink    Sink
	clock   Clock
	cfg     Config
	querier PixieQuerier // nil disables operator-side pixie pushes

	mu     sync.Mutex
	active map[anomaly.AnomalyHash]*sink.AttributionRow
	// inFlight tracks hashes whose pushPixieRows goroutine is currently
	// running. handle() re-launches the goroutine when the previous one
	// has exited (window expired between bursts), so a hash that already
	// exists in `active` but is no longer being actively fanned-out
	// gets refreshed protocol-table writes on the next alert. Without
	// this, the goroutine only spawns on the very first event for a
	// hash and subsequent bursts silently stop populating per-table
	// rows even though attribution keeps updating in CH.
	inFlight map[anomaly.AnomalyHash]bool

	// globalSem is the buffered channel that implements the
	// MaxInflightQueriesGlobal throttle. nil → no global cap.
	globalSem chan struct{}

	// emptyCacheMu guards emptyStreak and emptySkipUntil. Both are keyed
	// by "ns|pod|table" — namespace must be part of the key, otherwise
	// same-named pods in different namespaces share suppression state.
	emptyCacheMu   sync.Mutex
	emptyStreak    map[string]int       // consecutive 0-row returns
	emptySkipUntil map[string]time.Time // skip this (ns,pod,table) until this time
}

// New wires a Controller. nil clock falls through to RealClock.
// nil querier disables the rev-1 push path (controller will only
// write attribution rows; expects cloud's retention plugin to write
// pixie tables).
func New(trig Trigger, snk Sink, cfg Config, clk Clock) *Controller {
	if clk == nil {
		clk = RealClock{}
	}
	defaulted := cfg.defaulted()
	if defaulted.Rec == nil {
		defaulted.Rec = reconcile.Nop{}
	}
	c := &Controller{
		trig:           trig,
		sink:           snk,
		clock:          clk,
		cfg:            defaulted,
		active:         map[anomaly.AnomalyHash]*sink.AttributionRow{},
		inFlight:       map[anomaly.AnomalyHash]bool{},
		emptyStreak:    map[string]int{},
		emptySkipUntil: map[string]time.Time{},
	}
	if defaulted.MaxInflightQueriesGlobal > 0 {
		c.globalSem = make(chan struct{}, defaulted.MaxInflightQueriesGlobal)
	}
	return c
}

// WithPixieQuerier wires the rev-1 path. Returns the receiver for
// chaining. Idempotent — call before Run.
func (c *Controller) WithPixieQuerier(q PixieQuerier) *Controller {
	c.querier = q
	return c
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
	var resume []sink.AttributionRow
	for i := range rows {
		row := rows[i]
		c.active[row.AnomalyHash] = &row
		// Rev-1: a restart restored the window but no pushPixieRows goroutine —
		// without this, post-restart Pixie data is silently missed until another
		// event for the same hash arrives (CodeRabbit). Re-arm the fan-out for
		// each restored window, mirroring handle()'s spawn (in-flight guarded).
		if c.querier != nil && len(c.cfg.PushPixieTables) > 0 && !c.inFlight[row.AnomalyHash] {
			c.inFlight[row.AnomalyHash] = true
			resume = append(resume, row)
		}
	}
	c.mu.Unlock()
	for i := range resume {
		r := resume[i]
		go func() {
			defer func() {
				c.mu.Lock()
				delete(c.inFlight, r.AnomalyHash)
				c.mu.Unlock()
			}()
			c.pushPixieRows(ctx, r)
		}()
	}
	log.WithFields(log.Fields{"rehydrated": len(rows), "resumed": len(resume)}).
		Info("controller: active set restored")
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
	tEvent := eventTimeToTime(ev.EventTime)

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
	// Decide AND mark inFlight under the same mutex acquisition so two
	// rapid events for the same hash can't both decide to spawn.
	spawn := c.querier != nil && len(c.cfg.PushPixieTables) > 0 && !c.inFlight[hash]
	if spawn {
		c.inFlight[hash] = true
	}
	c.mu.Unlock()

	if err := c.sink.Write(ctx, []sink.AttributionRow{snapshot}); err != nil {
		// Attribution persistence failed → do NOT fan out, or we'd write Pixie
		// rows with no persisted attribution anchor (orphaned rows, CodeRabbit).
		// Non-fatal (system-stability rule): release the reserved in-flight slot
		// and return; a later event for the same hash retries.
		log.WithError(err).Warn("controller: sink write failed — skipping fan-out")
		if spawn {
			c.mu.Lock()
			delete(c.inFlight, hash)
			c.mu.Unlock()
		}
		return
	}
	if c.cfg.OnAttribution != nil {
		c.cfg.OnAttribution(snapshot.Namespace, snapshot.Pod, snapshot.TEnd)
	}
	// Rev-1 path: query pixie for the [t_start, t_end) slice of every
	// PushPixieTables table for this (namespace, pod) and write rows
	// directly to CH. Done in a goroutine so the controller doesn't
	// block on PxL execution (each query can take hundreds of ms;
	// N tables sequentially would stall the trigger). Re-spawned on
	// every event whose hash currently has no in-flight goroutine
	// (covers both brand-new hashes and hashes whose previous
	// pushPixieRows exited because the window had quieted down).
	if spawn {
		go func() {
			defer func() {
				c.mu.Lock()
				delete(c.inFlight, hash)
				c.mu.Unlock()
			}()
			c.pushPixieRows(ctx, snapshot)
		}()
	}
}

// pushPixieRows fans out per-table PxL queries and writes the results
// to forensic_db.<table>. One goroutine per anomaly window. The first
// pass covers [t_start, now]; subsequent passes (every
// PushRefreshInterval) cover only the new slice [last_upper, now] so
// pixie traffic that arrives AFTER the initial kubescape event still
// makes it into CH. Loop exits when the (possibly extended) t_end is
// in the past or ctx is cancelled. All failures are logged + non-fatal.
func (c *Controller) pushPixieRows(ctx context.Context, initial sink.AttributionRow) {
	target := anomaly.Target{
		PID:       initial.PID,
		Comm:      initial.Comm,
		Pod:       initial.Pod,
		Namespace: initial.Namespace,
	}
	log.WithFields(log.Fields{
		"hash":    initial.AnomalyHash,
		"pod":     initial.Pod,
		"comm":    initial.Comm,
		"tables":  len(c.cfg.PushPixieTables),
		"refresh": c.cfg.PushRefreshInterval,
		"t_start": initial.TStart,
		"t_end":   initial.TEnd,
	}).Info("pushPixieRows: starting fan-out")

	// Per-table watermark of pixie data we've already pulled for THIS
	// hash. We advance a table's cursor only after BOTH the query AND
	// the sink-write succeed; failures keep the cursor in place so the
	// next pass retries the same slice instead of dropping it.
	lastUpper := make(map[string]time.Time, len(c.cfg.PushPixieTables))
	for _, t := range c.cfg.PushPixieTables {
		lastUpper[t] = initial.TStart
	}
	pass := 0
	for {
		if ctx.Err() != nil {
			return
		}
		// Re-snapshot the active row each iteration so we pick up t_end
		// extensions from concurrent kubescape events (extending the
		// window beyond the initial t_end). COPY the row out of the
		// shared pointer before releasing the mutex — handle() mutates
		// the same struct, so reading TEnd after Unlock would race.
		c.mu.Lock()
		live, exists := c.active[initial.AnomalyHash]
		var current sink.AttributionRow
		if exists {
			current = *live
		}
		c.mu.Unlock()
		if !exists {
			log.WithField("hash", initial.AnomalyHash).
				Info("pushPixieRows: window closed (active entry gone)")
			return
		}
		now := c.clock.Now()
		if !current.TEnd.After(now) {
			log.WithFields(log.Fields{
				"hash":  initial.AnomalyHash,
				"t_end": current.TEnd,
			}).Info("pushPixieRows: fan-out complete (window expired)")
			return
		}

		pass++
		// Fan out the per-table PxL queries IN PARALLEL. The serial
		// rev-1 loop spent 1.5-5s per refresh waiting for the 9 tables
		// that return 0 rows for this pod (a redis-server pod only ever
		// has data in redis_events; the other 9 queries are pure
		// latency tax). Parallel cuts the per-pass wall time to roughly
		// max(query_time) instead of sum(query_times). Each goroutine
		// runs an independent Pixie RPC; the cloud's PassThroughProxy
		// fans them across vizier-query-broker fine in our measurements
		// (10 simultaneous in-flight queries → ~250-700ms wall vs
		// ~3-5s serial).
		type tableResult struct {
			table    string
			sliceEnd time.Time
			rows     int
			err      error
		}
		results := make(chan tableResult, len(c.cfg.PushPixieTables))
		var wg sync.WaitGroup
		// Per-hash concurrency limiter (knob #1: MaxParallelQueriesPerHash).
		// nil → unbounded (legacy behavior preserved).
		var perHashSem chan struct{}
		if c.cfg.MaxParallelQueriesPerHash > 0 {
			perHashSem = make(chan struct{}, c.cfg.MaxParallelQueriesPerHash)
		}
		for _, table := range c.cfg.PushPixieTables {
			if ctx.Err() != nil {
				break
			}
			// Knob #3: negative-cache skip. Pods that have returned 0
			// rows for this table N times in a row are skipped for TTL.
			// Self-heals when TTL expires.
			if c.shouldSkipEmpty(initial.Namespace, initial.Pod, table) {
				continue
			}
			sliceStart := lastUpper[table]
			sliceEnd := now
			if !sliceEnd.After(sliceStart) {
				continue // tiny / inverted slice — skip
			}
			q, err := pxl.QueryFor(table, target, sliceStart, sliceEnd, now)
			if err != nil {
				log.WithError(err).WithField("table", table).Warn("controller: QueryFor")
				continue
			}
			wg.Add(1)
			go func(table, q string, sliceStart, sliceEnd time.Time) {
				defer wg.Done()
				// Per-pull reconciliation (ADAPTIVE_RECONCILE): record what
				// this goroutine READ from Pixie vs WROTE to CH for this
				// (pod, table, window), on EVERY return path. Deferred so a
				// sem-cancel / query error / sink error all still emit a row
				// — the reconcile run needs the failures, not just successes.
				var readCount, wroteCount int
				var recErr string
				defer func() {
					c.cfg.Rec.Record(ctx, reconcile.Row{
						TS:         now,
						Mode:       "filter",
						Table:      table,
						Namespace:  initial.Namespace,
						Pod:        initial.Pod,
						WinStart:   sliceStart,
						WinEnd:     sliceEnd,
						ReadCount:  int64(readCount),
						WroteCount: int64(wroteCount),
						WriteErr:   recErr,
						Hostname:   c.cfg.Hostname,
					})
				}()
				// Acquire per-hash slot, then optional global slot.
				// Order matters: per-hash is cheap and local; global
				// gates network. Releasing in reverse order avoids the
				// pathological case where a stuck global slot pins a
				// per-hash slot for an unrelated table.
				if perHashSem != nil {
					select {
					case perHashSem <- struct{}{}:
					case <-ctx.Done():
						recErr = ctx.Err().Error()
						results <- tableResult{table: table, err: ctx.Err()}
						return
					}
					defer func() { <-perHashSem }()
				}
				if c.globalSem != nil {
					select {
					case c.globalSem <- struct{}{}:
					case <-ctx.Done():
						recErr = ctx.Err().Error()
						results <- tableResult{table: table, err: ctx.Err()}
						return
					}
					defer func() { <-c.globalSem }()
				}
				qctx, cancel := context.WithTimeout(ctx, 180*time.Second)
				rows, qerr := c.querier.Query(qctx, q)
				cancel()
				if qerr != nil {
					recErr = qerr.Error()
					results <- tableResult{table: table, err: qerr}
					return
				}
				// Update negative cache: 0 rows bumps streak, ≥1 row resets.
				c.noteQueryResult(initial.Namespace, initial.Pod, table, len(rows))
				nrows := len(rows)
				readCount = nrows
				if nrows > 0 {
					// Bound the sink write with its own timeout. Without
					// this, a stalled CH HTTP write would hold the table
					// goroutine forever, wg.Wait() would block the entire
					// pass, and refreshes for the active window would stop
					// — symptoms documented in our session as "fan-out
					// started, no error, no push" rows in the operator log.
					wctx, wcancel := context.WithTimeout(ctx, 60*time.Second)
					werr := c.sink.WritePixieRows(wctx, table, rows)
					wcancel()
					if werr != nil {
						recErr = werr.Error()
						results <- tableResult{table: table, err: werr}
						return
					}
					wroteCount = nrows
					log.WithFields(log.Fields{
						"table": table,
						"rows":  nrows,
						"hash":  initial.AnomalyHash,
						"pass":  pass,
					}).Info("pushed pixie rows for active anomaly window")
				}
				results <- tableResult{table: table, sliceEnd: sliceEnd, rows: nrows}
			}(table, q, sliceStart, sliceEnd)
		}
		wg.Wait()
		close(results)
		for r := range results {
			if r.err != nil {
				// Distinguish query vs sink errors for the operator log
				log.WithError(r.err).WithField("table", r.table).Warn("controller: pixie query or sink")
				continue // do NOT advance lastUpper — retry next pass
			}
			lastUpper[r.table] = r.sliceEnd
		}

		// Refresh interval treats negative as "single-shot" so callers
		// can opt out via the dedicated negative sentinel; the default
		// is 30s, set in defaulted(). Zero is reserved for "use default"
		// to keep the env-parsing layer simple (env unset → 0 → default).
		if c.cfg.PushRefreshInterval < 0 {
			log.WithField("hash", initial.AnomalyHash).
				Info("pushPixieRows: fan-out complete (single-shot mode)")
			return
		}
		if !sleepOrCancel(ctx, c.cfg.PushRefreshInterval) {
			return
		}
	}
}

// shouldSkipEmpty reports whether (namespace, pod, table) is currently
// in the negative cache. Returns false when knob #3 is disabled.
func (c *Controller) shouldSkipEmpty(namespace, pod, table string) bool {
	if c.cfg.EmptyResultSkipAfterN <= 0 || c.cfg.EmptyResultSkipTTL <= 0 {
		return false
	}
	key := namespace + "|" + pod + "|" + table
	c.emptyCacheMu.Lock()
	defer c.emptyCacheMu.Unlock()
	until, ok := c.emptySkipUntil[key]
	if !ok {
		return false
	}
	if c.clock.Now().Before(until) {
		return true
	}
	// TTL expired — clear it so the next call retries the query and
	// can re-arm the cache from observed results.
	delete(c.emptySkipUntil, key)
	delete(c.emptyStreak, key)
	return false
}

// noteQueryResult updates the negative cache after a successful pixie
// query. 0 rows bumps the streak; ≥1 row resets it. Once the streak
// reaches the configured N, the (namespace, pod, table) triple is
// skipped for TTL.
func (c *Controller) noteQueryResult(namespace, pod, table string, nrows int) {
	if c.cfg.EmptyResultSkipAfterN <= 0 || c.cfg.EmptyResultSkipTTL <= 0 {
		return
	}
	c.emptyCacheMu.Lock()
	defer c.emptyCacheMu.Unlock()
	key := namespace + "|" + pod + "|" + table
	if nrows > 0 {
		delete(c.emptyStreak, key)
		delete(c.emptySkipUntil, key)
		return
	}
	c.emptyStreak[key]++
	if c.emptyStreak[key] >= c.cfg.EmptyResultSkipAfterN {
		c.emptySkipUntil[key] = c.clock.Now().Add(c.cfg.EmptyResultSkipTTL)
	}
}

// sleepOrCancel returns true on normal sleep completion, false if ctx cancelled.
func sleepOrCancel(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Active returns the count of in-memory active hashes (test helper).
func (c *Controller) Active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

// SnapshotActive returns a fresh QueryActive against CH. Exposed so
// callers (e.g. main.go) can seed the streaming ActiveSet at boot
// without having to know about Sink internals.
func (c *Controller) SnapshotActive(ctx context.Context) ([]sink.AttributionRow, error) {
	return c.sink.QueryActive(ctx, c.cfg.Hostname)
}

// eventTimeToTime converts forensic_db.kubescape_logs.event_time (UInt64)
// into a time.Time, auto-detecting the unit. Vector's kubescape sink in
// the soc lab writes unix SECONDS (~1.7e9), but other deployments may
// emit millis (~1.7e12) or nanos (~1.7e18) per kubescape's own field
// conventions. Magnitude check picks the unit so we don't silently
// misinterpret the same UInt64 across pipeline variants.
func eventTimeToTime(et uint64) time.Time {
	switch {
	case et < 1e10:
		return time.Unix(int64(et), 0).UTC() // seconds
	case et < 1e13:
		return time.Unix(0, int64(et)*int64(time.Millisecond)).UTC() // millis
	default:
		return time.Unix(0, int64(et)).UTC() // nanos
	}
}

// PruneExpired removes from the in-memory active set every entry whose
// t_end has been in the past longer than a grace period. ClickHouse's
// ReplacingMergeTree handles table-side cleanup; this just keeps the
// operator's RAM bounded.
//
// The grace period (2 * cfg.After by default) bridges the gap between
// the prune timer and the next detection cycle: without it, a
// same-hash alert arriving milliseconds after a prune ran would spawn
// a fresh pushPixieRows goroutine, re-scanning the slice from
// initial.TStart and wasting Pixie query budget on data we already
// scanned. Empirically (2026-05-15) the un-graced prune accounted for
// 100% of pushPixieRows goroutine exits, none reached the natural
// "window expired" path — the prune kept racing reactivation.
//
// Caller invokes on a periodic timer.
func (c *Controller) PruneExpired() int {
	now := c.clock.Now()
	grace := 2 * c.cfg.After
	// Collect under the lock; fire callbacks AFTER releasing so we
	// don't hold the controller mutex across user code.
	//
	// IMPORTANT (rev-3 streaming correctness): c.active is keyed by
	// anomaly hash, but the streaming layer (ActiveSet) is keyed by
	// (namespace, pod). One pod can host multiple distinct hashes
	// (e.g. pgsql-server has hashes for postgres, pg_isready, runc:
	// [2:INIT] processes). Firing OnPrune for every evicted hash
	// would prematurely stop streaming for a pod that still has
	// other active hashes. So: compute the set of pods that have
	// NO remaining active hashes after this prune, and only fire
	// OnPrune for those.
	type podKey struct{ namespace, pod string }
	prunedHashes := 0
	var pruned []podKey
	c.mu.Lock()
	// Pass 1: delete expired hashes and remember which pods THEY
	// belonged to.
	candidatePods := map[podKey]struct{}{}
	for h, row := range c.active {
		if !row.TEnd.Add(grace).After(now) {
			candidatePods[podKey{row.Namespace, row.Pod}] = struct{}{}
			delete(c.active, h)
			prunedHashes++
		}
	}
	// Pass 2: from candidatePods, remove any pod that STILL has at
	// least one surviving hash in c.active. What's left is the set
	// of pods that lost their LAST hash — these get OnPrune.
	for _, row := range c.active {
		delete(candidatePods, podKey{row.Namespace, row.Pod})
	}
	for pk := range candidatePods {
		pruned = append(pruned, pk)
	}
	c.mu.Unlock()
	if c.cfg.OnPrune != nil {
		for _, k := range pruned {
			c.cfg.OnPrune(k.namespace, k.pod)
		}
	}
	return prunedHashes
}
