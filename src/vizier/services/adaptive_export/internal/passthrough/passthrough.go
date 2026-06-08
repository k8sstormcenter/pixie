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
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/pxl"
)

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
	Tables  []string
}

// Loop is the passthrough goroutine.
type Loop struct {
	q   querier
	s   sink
	cfg Config
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
	if len(cfg.Tables) == 0 {
		cfg.Tables = clickhouse.PixieTables()
	}
	return &Loop{q: q, s: s, cfg: cfg}
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

// tick runs one passthrough sweep across every configured table.
func (l *Loop) tick(ctx context.Context) {
	now := time.Now()
	sliceStart := now.Add(-l.cfg.Window)
	sliceEnd := now

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
			continue
		}
		rows, err := l.q.Query(ctx, src)
		if err != nil {
			log.WithError(err).WithField("table", table).Warn("ADAPTIVE_PASSTHROUGH: pixie query failed")
			continue
		}
		if len(rows) == 0 {
			log.WithField("table", table).Debug("ADAPTIVE_PASSTHROUGH: 0 rows")
			continue
		}
		if err := l.s.WritePixieRows(ctx, table, rows); err != nil {
			log.WithError(err).WithFields(log.Fields{
				"table": table,
				"rows":  len(rows),
			}).Warn("ADAPTIVE_PASSTHROUGH: sink write failed")
			continue
		}
		log.WithFields(log.Fields{
			"table": table,
			"rows":  len(rows),
		}).Info("ADAPTIVE_PASSTHROUGH: rows written")
	}
}
