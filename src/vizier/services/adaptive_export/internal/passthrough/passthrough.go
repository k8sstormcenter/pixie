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

// Package passthrough is the firehose-mode counterpart to the anomaly-gated
// adaptive write path. When enabled, a single background loop queries every
// pixie observation table with an empty Target (no ns/pod predicate),
// covering the configured rolling window, and writes the result via the
// existing sink. The intent is one-shot A/B measurement: compare the
// row-count + on-disk byte volume of forensic_db tables under ADAPTIVE_PASSTHROUGH=1
// (Phase EVERYTHING) vs ADAPTIVE_PASSTHROUGH=0 (Phase AE-FILTER) under the
// same load + window, yielding the AE capture fraction per table.
//
// This package is intentionally minimal: no anomaly gate, no ActiveSet, no
// trigger. It reuses the same QueryFor / Adapter / Sink wiring as the rest
// of AE so the bytes-per-row shape is comparable across phases.
package passthrough

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/pxl"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/reconcile"
)

// excludedTables are dropped from the firehose table set: tables that are
// declared builtin but are not materialised on every cluster, so a
// passthrough pull against them returns a "Table not found" compilation
// error every tick (pure log spam, zero rows). http2_messages.beta is the
// known offender. Removing it here keeps the schema/DDL lists (which still
// own the table when it DOES exist) untouched.
var excludedTables = map[string]bool{
	"http2_messages.beta": true,
}

// querier matches the cmd-side pixieAdapter wrapper (returns
// []map[string]any instead of pixieapi.Row) so the loop is decoupled
// from pxapi internals + trivially fakeable in tests.
type querier interface {
	Query(ctx context.Context, src string) ([]map[string]any, error)
}

// sink writes rows for a specific pixie table to forensic_db.<table>.
type sink interface {
	WritePixieRows(ctx context.Context, table string, rows []map[string]any) error
}

// Config carries the env-derived knobs. Window: the rolling lookback the
// loop's PxL covers each refresh. Refresh: cadence between loop iterations.
// Tables: which pixie tables to firehose (defaults to clickhouse.PixieTables()
// when nil/empty).
type Config struct {
	Window  time.Duration
	Refresh time.Duration
	// QueryTimeout bounds a single table's pixie query (entlein/dx#7). The
	// firehose pull used to bound query+write by Refresh, which is far too tight
	// for a heavy protocol: pgsql_events carries full SQL text and its
	// socket_tracer parse is expensive, so the ExecuteScript deadline-exceeded and
	// pgsql_events landed 0 rows in forensic_db. Decoupled from Refresh and
	// defaulted generous (matches the OrderQuery path's 180s budget).
	QueryTimeout time.Duration
	Tables       []string
	// Rec records per-pull read/wrote counts (ADAPTIVE_RECONCILE). nil →
	// defaulted to reconcile.Nop{} in New (instrument off).
	Rec reconcile.Recorder
	// Hostname is the node name stamped on reconcile rows.
	Hostname string
	// Compiled selects the firehose query path. When true (the default
	// wired by cmd/main.go), per-table PxL is precompiled ONCE at New and
	// all tables are pulled CONCURRENTLY per tick. When false, the legacy
	// path is used: QueryFor rebuilds each table's PxL every tick and the
	// tables are walked serially. The env var ADAPTIVE_PASSTHROUGH_COMPILED
	// (cmd/main.go) flips this — set it to "false" to revert.
	Compiled bool
}

// Loop is the passthrough goroutine.
type Loop struct {
	q   querier
	s   sink
	cfg Config
	// tmpl holds the precompiled per-table PxL templates (table → fmt
	// template with two %d time-bound verbs). Populated in New only when
	// cfg.Compiled; nil otherwise.
	tmpl map[string]string
}

// New constructs a Loop. Caller-provided querier+sink must already be
// wired (cmd/main.go builds both unconditionally when ADAPTIVE_PASSTHROUGH
// is enabled).
func New(q querier, s sink, cfg Config) *Loop {
	if cfg.Window <= 0 {
		cfg.Window = 30 * time.Second
	}
	if cfg.Refresh <= 0 {
		cfg.Refresh = 30 * time.Second
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 150 * time.Second // #7: heavy pgsql pull needs headroom
	}
	if len(cfg.Tables) == 0 {
		cfg.Tables = clickhouse.PixieTables()
	}
	// Drop tables that aren't materialised on this cluster (e.g.
	// http2_messages.beta) so they don't error every tick.
	cfg.Tables = filterExcluded(cfg.Tables)
	if cfg.Rec == nil {
		cfg.Rec = reconcile.Nop{}
	}
	l := &Loop{q: q, s: s, cfg: cfg}
	if cfg.Compiled {
		// Precompile each table's PxL once. The window is fixed for the
		// lifetime of the loop, so only the per-tick time bounds vary.
		l.tmpl = make(map[string]string, len(cfg.Tables))
		for _, table := range cfg.Tables {
			t, err := pxl.CompilePassthrough(table, cfg.Window)
			if err != nil {
				// A non-builtin table can't be compiled; skip it rather
				// than fail construction (matches the per-table tolerance
				// of the run loop).
				log.WithError(err).WithField("table", table).
					Warn("ADAPTIVE_PASSTHROUGH: precompile skipped")
				continue
			}
			l.tmpl[table] = t
		}
	}
	return l
}

// filterExcluded returns tables with the excludedTables entries removed,
// preserving order.
func filterExcluded(tables []string) []string {
	out := tables[:0:0]
	for _, t := range tables {
		if excludedTables[t] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// rec emits one passthrough reconciliation row (best-effort; Nop when the
// instrument is off).
func (l *Loop) rec(ctx context.Context, table string, winStart, winEnd time.Time, read, wrote int, errStr string) {
	l.cfg.Rec.Record(ctx, reconcile.Row{
		TS:         time.Now(),
		Mode:       "passthrough",
		Table:      table,
		WinStart:   winStart,
		WinEnd:     winEnd,
		ReadCount:  int64(read),
		WroteCount: int64(wrote),
		WriteErr:   errStr,
		Hostname:   l.cfg.Hostname,
	})
}

// Run blocks until ctx is cancelled. On each refresh tick the loop walks
// the configured tables, queries pixie for the window [now-Window, now)
// with no ns/pod filter, and writes the resulting rows. Individual table
// failures are logged but never break the loop — passthrough is a
// best-effort measurement workload, not the durable write path.
func (l *Loop) Run(ctx context.Context) {
	log.WithFields(log.Fields{
		"window":  l.cfg.Window,
		"refresh": l.cfg.Refresh,
		"tables":  l.cfg.Tables,
	}).Info("ADAPTIVE_PASSTHROUGH: firehose loop starting")

	// Fire immediately so the first window doesn't have to wait `Refresh`.
	l.tick(ctx)

	t := time.NewTicker(l.cfg.Refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("ADAPTIVE_PASSTHROUGH: firehose loop stopped")
			return
		case <-t.C:
			l.tick(ctx)
		}
	}
}

// tick runs one passthrough sweep across every configured table. When
// cfg.Compiled (the default) all tables are pulled CONCURRENTLY using the
// precompiled templates; otherwise they are walked serially with QueryFor
// rebuilt per tick (legacy path, kept for rollback via the env var).
func (l *Loop) tick(ctx context.Context) {
	now := time.Now()
	sliceStart := now.Add(-l.cfg.Window)
	sliceEnd := now

	if l.cfg.Compiled {
		l.tickConcurrent(ctx, sliceStart, sliceEnd)
		return
	}
	for _, table := range l.cfg.Tables {
		if ctx.Err() != nil {
			return
		}
		// Empty Target: namespace+pod predicates are SKIPPED inside
		// QueryFor, so the PxL DataFrame returns ALL rows in the window.
		// This is the bypass that makes the A/B measurement meaningful.
		src, err := pxl.QueryFor(table, anomaly.Target{}, sliceStart, sliceEnd, now)
		if err != nil {
			log.WithError(err).WithField("table", table).Warn("ADAPTIVE_PASSTHROUGH: QueryFor failed")
			l.rec(ctx, table, sliceStart, sliceEnd, 0, 0, err.Error())
			continue
		}
		l.pull(ctx, table, src, sliceStart, sliceEnd)
	}
}

// tickConcurrent fires every table's precompiled query at once and waits
// for all to finish. Per-table failures are isolated inside pull, so one
// table's error never affects another.
func (l *Loop) tickConcurrent(ctx context.Context, sliceStart, sliceEnd time.Time) {
	var wg sync.WaitGroup
	for _, table := range l.cfg.Tables {
		if ctx.Err() != nil {
			break
		}
		tmpl, ok := l.tmpl[table]
		if !ok {
			// Non-builtin table skipped at precompile time. Record the
			// failure so the reconcile row count matches the legacy
			// (non-compiled) path, which records one row per table per
			// tick unconditionally (CodeRabbit r-#68/passthrough.go).
			l.rec(ctx, table, sliceStart, sliceEnd, 0, 0, "pxl: precompile skipped (non-builtin table)")
			continue
		}
		src := pxl.Render(tmpl, sliceStart, sliceEnd)
		wg.Add(1)
		go func(table, src string) {
			defer wg.Done()
			l.pull(ctx, table, src, sliceStart, sliceEnd)
		}(table, src)
	}
	wg.Wait()
}

// pull runs one table's query, writes the rows, and records the reconcile
// row. It is safe for concurrent use across distinct tables: the querier,
// sink, and recorder are all pool/HTTP-backed and concurrency-safe, and
// each call touches a different forensic_db.<table>.
func (l *Loop) pull(ctx context.Context, table, src string, sliceStart, sliceEnd time.Time) {
	// Bound this table's external query+write+record so a hung dependency can't
	// stall the whole sweep or delay shutdown (CodeRabbit). Derived per-table
	// from the parent ctx; covers both the serial and concurrent tick paths.
	ctx, cancel := context.WithTimeout(ctx, l.cfg.QueryTimeout)
	defer cancel()
	rows, err := l.q.Query(ctx, src)
	if err != nil {
		log.WithError(err).WithField("table", table).Warn("ADAPTIVE_PASSTHROUGH: pixie query failed")
		l.rec(ctx, table, sliceStart, sliceEnd, 0, 0, err.Error())
		return
	}
	if len(rows) == 0 {
		log.WithField("table", table).Debug("ADAPTIVE_PASSTHROUGH: 0 rows")
		l.rec(ctx, table, sliceStart, sliceEnd, 0, 0, "")
		return
	}
	if err := l.s.WritePixieRows(ctx, table, rows); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"table": table,
			"rows":  len(rows),
		}).Warn("ADAPTIVE_PASSTHROUGH: sink write failed")
		l.rec(ctx, table, sliceStart, sliceEnd, len(rows), 0, err.Error())
		return
	}
	log.WithFields(log.Fields{
		"table": table,
		"rows":  len(rows),
	}).Info("ADAPTIVE_PASSTHROUGH: rows written")
	l.rec(ctx, table, sliceStart, sliceEnd, len(rows), len(rows), "")
}
