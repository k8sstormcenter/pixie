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
	"errors"
	"fmt"
	"strings"
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

	// QueryLag holds the per-table watermark this far behind wall-clock:
	// each pass queries up to now-QueryLag, not now. Sparse tables (dns_events,
	// dc_snoop) emit events that socket_tracer/stirling flush a few seconds
	// after they occur; without a lag the watermark advances past an event's
	// time_ before it is queryable, so it is skipped forever. Continuous tables
	// (conn_stats) always have fresh post-watermark rows so they never notice —
	// which is why sparse tables lost ALL rows while continuous ones exported
	// fully. Defaulted to 30s in defaulted(); 0 keeps the legacy (lossy) behavior
	// only if set negative is not used — env ADAPTIVE_QUERY_LAG_SEC overrides.
	QueryLag time.Duration

	// DisableSelfSteer, when true, stops the kubescape trigger from spawning its own
	// pushPixieRows fan-out — the AE then exports ONLY what a control client (dx)
	// orders via /export/start (OrderExportAll) or /query (OrderQuery). Set by
	// EXPORT_MODE=never in main.go. Inverted bool so the zero value preserves the
	// legacy self-steering behavior (and existing tests that build Config directly).
	DisableSelfSteer bool

	// ExportAllFloor bounds how often the control-surface steer-all (OrderExportAll)
	// re-captures the SAME target. dx fires StartExport per referral (~1s floor), so
	// without this a sustained attack floods the broker with redundant full-table
	// captures over overlapping windows. Defaulted to 30s in defaulted().
	ExportAllFloor time.Duration

	// OrderChunk is the sub-window span the ordered path (OrderQuery) walks the
	// capture window in. Each chunk is a both-sides bounded pixie query, so no single
	// query re-scans the whole (up to 600s) window on the node-local PEM — the fix for
	// heavy tables (dc_snoop) losing the per-query deadline race under the
	// OrderExportAll fan-out. A chunk that still times out under contention is
	// adaptively halved down to orderMinChunk. Defaulted to defaultOrderChunk in
	// defaulted(); env ADAPTIVE_ORDER_CHUNK_SEC overrides.
	OrderChunk time.Duration

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
	if out.QueryLag == 0 {
		out.QueryLag = 30 * time.Second
	}
	if out.ExportAllFloor == 0 {
		out.ExportAllFloor = 30 * time.Second
	}
	if out.OrderChunk == 0 {
		out.OrderChunk = defaultOrderChunk
	}
	return out
}

const (
	// defaultOrderChunk is the sub-window the ordered path walks the capture window
	// in. 60s over the 600s control lookback = 10 bounded queries per table, each
	// cheap enough to complete well inside the 180s deadline even when 20 tables
	// fan out concurrently against one node-local PEM.
	defaultOrderChunk = 60 * time.Second
	// orderMinChunk is the floor for adaptive subdivision: a span this small that
	// still fails is surfaced rather than split further (a 1s window that can't be
	// captured is a real error, not contention).
	orderMinChunk = 1 * time.Second
)

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

	exportAllMu sync.Mutex
	exportAllAt map[string]time.Time // per-target floor for OrderExportAll (steer-all)
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
		exportAllAt:    map[string]time.Time{},
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

// OrderQuery runs ONE control-ordered (target, table, window) query and writes the
// result through AE's normal sink — the dx→AE write⊇read path behind the control
// surface's POST /query. Unlike pushPixieRows (one goroutine per kubescape-anomaly
// window, driven by the Trigger), this is a single-shot forensic capture for a
// table dx EXPLICITLY consulted to reach a verdict. It is how the evidence dx read
// — e.g. the jndi-in-http the PEM bench found at triage — lands in forensic_db even
// when no kubescape anomaly opened a window for that pod (entlein/dx#93). Reuses the
// same QueryFor → querier.Query → sink.WritePixieRows path + globalSem + reconcile
// accounting as the anomaly-driven push. Satisfies control.queryRunner.
func (c *Controller) OrderQuery(target anomaly.Target, table string, start, end time.Time, queryID string) error {
	if c.querier == nil {
		return errors.New("controller: no pixie querier (operator-side push disabled)")
	}
	now := c.clock.Now()
	chunk := c.cfg.OrderChunk
	if chunk <= 0 {
		chunk = defaultOrderChunk
	}
	// Walk the window oldest→newest in fixed chunks. Each chunk is a both-sides
	// bounded pixie query (QueryFor stamps end_time), so no single query re-scans the
	// whole window on the node-local PEM — the flaky-capture fix. captureSpan halves
	// any chunk that still times out under fan-out contention. Chunks run
	// sequentially per table, so OrderExportAll's per-table concurrency (20 tables)
	// is unchanged while each table now issues cheap bounded queries instead of one
	// firehose. ReplacingMergeTree makes the overlapping/retried spans idempotent.
	var readTotal, wroteTotal int
	var firstErr error
	for s := start; s.Before(end); s = s.Add(chunk) {
		e := s.Add(chunk)
		if e.After(end) {
			e = end
		}
		qid := fmt.Sprintf("%s:%d-%d", queryID, s.Unix(), e.Unix())
		r, w, err := c.captureSpan(target, table, s, e, qid)
		readTotal += r
		wroteTotal += w
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	recErr := ""
	if firstErr != nil {
		recErr = firstErr.Error()
	}
	// One reconcile row per table, aggregating every chunk — the read/wrote counts a
	// forensic dump reads stay per-table, not per-chunk.
	c.cfg.Rec.Record(context.Background(), reconcile.Row{
		TS: now, Mode: "ordered", Table: table,
		Namespace: target.Namespace, Pod: target.Pod,
		WinStart: start, WinEnd: end,
		ReadCount: int64(readTotal), WroteCount: int64(wroteTotal),
		WriteErr: recErr, Hostname: c.cfg.Hostname,
	})
	return firstErr
}

// captureSpan captures [start,end) for one table, subdividing on a transient
// (deadline/overload) failure down to orderMinChunk. A span that times out under
// PEM contention is retried as two half-spans — each scans less data at the source
// (QueryFor bounds end_time), so a dense window that blows the 180s deadline as one
// query completes as several small ones. Idempotent: overlapping/retried spans
// dedupe in the ReplacingMergeTree evidence tables. Non-transient errors (e.g. a
// missing dark-vector table) surface immediately without wasteful splitting.
func (c *Controller) captureSpan(target anomaly.Target, table string, start, end time.Time, queryID string) (readCount, wroteCount int, err error) {
	r, w, e := c.orderQuerySlice(target, table, start, end, queryID)
	if e == nil || !isRetriableSpanErr(e) || end.Sub(start) <= orderMinChunk {
		return r, w, e
	}
	log.WithError(e).WithFields(log.Fields{
		"table": table, "pod": target.Pod, "span": end.Sub(start).String(),
	}).Warn("ordered capture: transient failure, subdividing span")
	mid := start.Add(end.Sub(start) / 2)
	r1, w1, e1 := c.captureSpan(target, table, start, mid, queryID+".l")
	r2, w2, e2 := c.captureSpan(target, table, mid, end, queryID+".r")
	if e1 != nil {
		return r1 + r2, w1 + w2, e1
	}
	return r1 + r2, w1 + w2, e2
}

// orderQuerySlice runs ONE bounded (target, table, [start,end)) capture: query
// pixie, write the rows, return the read/wrote counts. It records NO reconcile row —
// the OrderQuery driver aggregates across chunks and records once. globalSem still
// bounds broker load per slice. Background ctx with per-op timeouts mirrors
// pushPixieRows: a control-ordered capture completes independently of any anomaly
// window's lifecycle.
func (c *Controller) orderQuerySlice(target anomaly.Target, table string, start, end time.Time, queryID string) (readCount, wroteCount int, err error) {
	now := c.clock.Now()
	q, qerr := pxl.QueryFor(table, target, start, end, now)
	if qerr != nil {
		return 0, 0, qerr
	}
	if c.globalSem != nil {
		c.globalSem <- struct{}{}
		defer func() { <-c.globalSem }()
	}
	qctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	rows, qerr := c.querier.Query(qctx, q)
	cancel()
	if qerr != nil {
		return 0, 0, qerr
	}
	if len(rows) == 0 {
		return 0, 0, nil // nothing to persist; the driver's reconcile row still records the read
	}
	wctx, wcancel := context.WithTimeout(context.Background(), 60*time.Second)
	werr := c.sink.WritePixieRows(wctx, table, rows)
	wcancel()
	if werr != nil {
		return len(rows), 0, werr
	}
	log.WithFields(log.Fields{
		"table": table, "rows": len(rows), "pod": target.Pod, "query_id": queryID,
	}).Info("ordered pixie rows written to forensic_db (dx→AE /query)")
	return len(rows), len(rows), nil
}

// isRetriableSpanErr reports whether a slice error is a transient overload/timeout
// worth retrying as a narrower span (vs. a structural error like a missing table,
// which no amount of subdivision fixes). Covers ctx deadlines and the gRPC status
// strings the pixie querier surfaces (DeadlineExceeded / ResourceExhausted /
// Unavailable), which do NOT satisfy errors.Is(context.DeadlineExceeded).
func isRetriableSpanErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, m := range []string{
		"deadline", "timeout", "exceeded", "resourceexhausted",
		"resource exhausted", "unavailable", "context canceled", "context cancelled",
	} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// OrderExportAll runs a one-shot OrderQuery for EVERY configured pixie table for
// the target/window — the control-surface "steer-all" path. A control client
// (dx) asks AE to capture the COMPLETE evidence set for an anomaly's pod, with NO
// per-table relevance decision: dx filters only to (namespace, pod), AE grabs
// everything that could be relevant. Tables run concurrently (each OrderQuery
// takes the globalSem itself, so MaxInflightQueriesGlobal still bounds broker
// load), best-effort — a per-table error is logged and skipped so one slow/empty
// table can't block the rest. The deterministic query_id makes overlapping
// anomalies on the same pod idempotent (same target+table+window → same id).
func (c *Controller) OrderExportAll(target anomaly.Target, start, end time.Time) {
	if c.querier == nil || len(c.cfg.PushPixieTables) == 0 {
		return
	}
	// Per-target floor: dx sends StartExport on EVERY referral (its own floor is
	// ~1s), so a sustained attack fires OrderExportAll many times per second for
	// the SAME pod. Each call is a full 20-table capture over a rolling ~600s
	// window that already overlaps the previous one, so re-running them just floods
	// the broker (globalSem saturates, nothing completes). Collapse the burst: one
	// full capture per target per ExportAllFloor — the rolling window still covers
	// every event.
	tk := target.Namespace + "/" + target.Pod
	c.exportAllMu.Lock()
	if last, ok := c.exportAllAt[tk]; ok && c.clock.Now().Sub(last) < c.cfg.ExportAllFloor {
		c.exportAllMu.Unlock()
		return
	}
	c.exportAllAt[tk] = c.clock.Now()
	c.exportAllMu.Unlock()
	log.WithFields(log.Fields{
		"pod": target.Pod, "namespace": target.Namespace, "tables": len(c.cfg.PushPixieTables),
	}).Info("OrderExportAll: dx-steered full-evidence capture for anomaly pod")
	var wg sync.WaitGroup
	for _, table := range c.cfg.PushPixieTables {
		wg.Add(1)
		go func(table string) {
			defer wg.Done()
			qid := fmt.Sprintf("steerall:%s/%s:%s:%d-%d",
				target.Namespace, target.Pod, table, start.Unix(), end.Unix())
			if err := c.OrderQuery(target, table, start, end, qid); err != nil {
				log.WithError(err).WithFields(log.Fields{"table": table, "pod": target.Pod}).
					Warn("OrderExportAll: table export failed (skipped)")
			}
		}(table)
	}
	wg.Wait()
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
		if !c.cfg.DisableSelfSteer && c.querier != nil && len(c.cfg.PushPixieTables) > 0 && !c.inFlight[row.AnomalyHash] {
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
	// Save the pre-mutation snapshot so we can roll back if the sink
	// write fails (CodeRabbit r-#68/controller/controller.go). Without
	// this, on write error we'd keep the extended TEnd/NAnomalies/
	// LastSeen in c.active and an already-running pushPixieRows would
	// re-snapshot them and fan out data based on an attribution row
	// that never actually landed in CH.
	var prevRow sink.AttributionRow
	if exists {
		prevRow = *row
	}
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
	spawn := !c.cfg.DisableSelfSteer && c.querier != nil && len(c.cfg.PushPixieTables) > 0 && !c.inFlight[hash]
	if spawn {
		c.inFlight[hash] = true
	}
	c.mu.Unlock()

	if err := c.sink.Write(ctx, []sink.AttributionRow{snapshot}); err != nil {
		// Attribution persistence failed → do NOT fan out, or we'd write Pixie
		// rows with no persisted attribution anchor (orphaned rows, CodeRabbit).
		// Non-fatal (system-stability rule): release the reserved in-flight slot,
		// ROLL BACK the in-memory mutation so an already-running pushPixieRows
		// for this hash doesn't keep extending its window on a phantom
		// attribution, and return; a later event for the same hash retries.
		log.WithError(err).Warn("controller: sink write failed — skipping fan-out")
		c.mu.Lock()
		if exists {
			*c.active[hash] = prevRow
		} else {
			delete(c.active, hash)
		}
		if spawn {
			delete(c.inFlight, hash)
		}
		c.mu.Unlock()
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
			// Trail the watermark by QueryLag so sparse late-flushed rows are
			// still queryable when this slice runs. QueryFor's `now` (for its
			// relative start_time pad) stays real-now so the DataFrame window
			// still covers the slice.
			sliceEnd := now.Add(-c.cfg.QueryLag)
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
