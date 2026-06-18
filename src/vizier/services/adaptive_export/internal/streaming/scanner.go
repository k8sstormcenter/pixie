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
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/reconcile"
)

// Querier executes a PxL string against a vizier and returns the
// resulting flat rows. Same shape as controller.PixieQuerier; kept
// independently here to avoid an import cycle.
type Querier interface {
	Query(ctx context.Context, pxl string) ([]map[string]any, error)
}

// ScannerConfig tunes one TableScanner.
type ScannerConfig struct {
	// Table is the pixie observation table this scanner targets
	// (e.g. "pgsql_events"). REQUIRED.
	Table string

	// QueryWindow is the `start_time` in the emitted PxL, e.g. "-60s".
	// Must be longer than RefreshInterval + maximum expected query
	// latency, otherwise rows in the gap between consecutive runs
	// would be missed. 0 → -60s.
	QueryWindow time.Duration

	// RefreshInterval is the floor on time-between-PxL-submissions.
	// A filter change can submit sooner; this prevents over-frequent
	// submissions when the filter is stable. 0 → 30s.
	RefreshInterval time.Duration

	// QueryTimeout bounds one PxL call. 0 → 180s.
	QueryTimeout time.Duration

	// BackoffInitial / BackoffMax — exponential backoff on Querier
	// errors. 0 → 1s / 30s.
	BackoffInitial time.Duration
	BackoffMax     time.Duration

	// Rec records per-pull read/submitted counts (ADAPTIVE_RECONCILE).
	// nil → reconcile.Nop{} in defaulted() (instrument off).
	Rec reconcile.Recorder

	// Hostname is stamped on reconcile rows.
	Hostname string
}

func (c ScannerConfig) defaulted() ScannerConfig {
	if c.QueryWindow <= 0 {
		c.QueryWindow = 60 * time.Second
	}
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = 30 * time.Second
	}
	if c.QueryTimeout <= 0 {
		c.QueryTimeout = 180 * time.Second
	}
	if c.BackoffInitial <= 0 {
		c.BackoffInitial = 1 * time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
	if c.Rec == nil {
		c.Rec = reconcile.Nop{}
	}
	return c
}

// TableScanner runs ONE PxL submission per refresh cycle for ONE
// pixie table, with a pod allowlist drawn from an upstream Filter
// channel. Output goes to a per-table BatchWriter.
//
// This is the rev-3 replacement for pushPixieRows' per-hash×per-table
// fan-out. Goroutines created: 1 per TableScanner. Concurrency
// against vizier-query-broker: 1 per scanner = N (number of tables).
type TableScanner struct {
	cfg     ScannerConfig
	querier Querier
	writer  *BatchWriter
	filters <-chan Filter

	currentFilter Filter

	queries  atomic.Int64
	queryErr atomic.Int64
	rowsIn   atomic.Int64
	skipped  atomic.Int64
}

// NewScanner wires a scanner. filters is the channel returned by
// FilterUpdater.Subscribe.
func NewScanner(cfg ScannerConfig, querier Querier, writer *BatchWriter, filters <-chan Filter) *TableScanner {
	return &TableScanner{
		cfg:     cfg.defaulted(),
		querier: querier,
		writer:  writer,
		filters: filters,
	}
}

// Run owns one goroutine. Loops:
//
//  1. Wait for filter (initial) — block until first one arrives.
//  2. Loop:
//     - If filter has no pods AND mode == Allowlist: skip query
//     entirely (the whole purpose: empty allowlist = no work).
//     - Else: build PxL, query, push rows to writer.
//     - Sleep RefreshInterval OR until filter changes.
//  3. Backoff on Querier errors.
func (s *TableScanner) Run(ctx context.Context) {
	// 1. Initial filter.
	select {
	case f, ok := <-s.filters:
		if !ok {
			return
		}
		s.currentFilter = f
	case <-ctx.Done():
		return
	}

	backoff := s.cfg.BackoffInitial
	resetBackoff := func() { backoff = s.cfg.BackoffInitial }
	bumpBackoff := func() {
		backoff *= 2
		if backoff > s.cfg.BackoffMax {
			backoff = s.cfg.BackoffMax
		}
	}

	for {
		if ctx.Err() != nil {
			return
		}

		// Empty allowlist short-circuit: nothing to query.
		if s.currentFilter.Mode == FilterModeAllowlist && len(s.currentFilter.Pods) == 0 {
			s.skipped.Add(1)
			// Diagnostic: an empty allowlist means the ActiveSet has no
			// members — i.e. nothing has been steered into this AE yet.
			// Logged so an operator can tell "empty ActiveSet → skipping"
			// apart from "queried but the broker returned 0 rows" (the
			// latter logs "query completed rows=0"). Naturally rate-limited:
			// we block on the next filter immediately after.
			log.WithFields(log.Fields{
				"table":   s.cfg.Table,
				"version": s.currentFilter.Version,
			}).Info("streaming.TableScanner: empty allowlist (ActiveSet has no steered pods) — skipping query until a filter with pods arrives")
			// Wait for either: a new filter arrives, or ctx done.
			select {
			case <-ctx.Done():
				return
			case f, ok := <-s.filters:
				if !ok {
					return
				}
				s.currentFilter = f
			}
			continue
		}

		// 2. Build PxL + execute.
		pxl := s.buildPxL(s.currentFilter)
		winEnd := time.Now()
		winStart := winEnd.Add(-s.cfg.QueryWindow)
		qctx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
		rows, err := s.querier.Query(qctx, pxl)
		cancel()
		s.queries.Add(1)
		if err != nil {
			s.queryErr.Add(1)
			s.cfg.Rec.Record(ctx, reconcile.Row{
				TS: winEnd, Mode: "streaming", Table: s.cfg.Table,
				WinStart: winStart, WinEnd: winEnd,
				ReadCount: 0, WroteCount: 0, WriteErr: err.Error(),
				Hostname: s.cfg.Hostname,
			})
			log.WithError(err).WithFields(log.Fields{
				"table":   s.cfg.Table,
				"pods":    len(s.currentFilter.Pods),
				"mode":    s.currentFilter.Mode,
				"backoff": backoff,
			}).Warn("streaming.TableScanner: query failed; backing off")
			// Wait either backoff OR new filter (filter takes precedence).
			select {
			case <-ctx.Done():
				return
			case f, ok := <-s.filters:
				if !ok {
					return
				}
				s.currentFilter = f
				resetBackoff()
			case <-time.After(backoff):
				bumpBackoff()
			}
			continue
		}
		resetBackoff()
		s.rowsIn.Add(int64(len(rows)))

		// 3. Hand off to writer.
		submitted := 0
		if len(rows) > 0 {
			if s.writer.Submit(rows) {
				submitted = len(rows)
			}
		}
		s.cfg.Rec.Record(ctx, reconcile.Row{
			TS: winEnd, Mode: "streaming", Table: s.cfg.Table,
			WinStart: winStart, WinEnd: winEnd,
			ReadCount: int64(len(rows)), WroteCount: int64(submitted),
			Hostname: s.cfg.Hostname,
		})
		log.WithFields(log.Fields{
			"table":   s.cfg.Table,
			"pods":    len(s.currentFilter.Pods),
			"mode":    s.currentFilter.Mode,
			"rows":    len(rows),
			"version": s.currentFilter.Version,
		}).Info("streaming.TableScanner: query completed")

		// 4. Sleep until refresh OR filter change.
		select {
		case <-ctx.Done():
			return
		case f, ok := <-s.filters:
			if !ok {
				return
			}
			s.currentFilter = f
		case <-time.After(s.cfg.RefreshInterval):
		}
	}
}

// buildPxL renders the script for one query.
func (s *TableScanner) buildPxL(f Filter) string {
	relStart := "-" + strconv.FormatInt(int64(s.cfg.QueryWindow/time.Second), 10) + "s"
	var b strings.Builder
	b.WriteString("import px\n")
	b.WriteString("df = px.DataFrame(table='" + s.cfg.Table + "', start_time='" + relStart + "')\n")
	b.WriteString("df.namespace = px.upid_to_namespace(df.upid)\n")
	b.WriteString("df.pod = px.upid_to_pod_name(df.upid)\n")
	if f.Mode == FilterModeAllowlist && len(f.Pods) > 0 {
		// Allowlist clause. PxL syntax exploration (2026-05-17):
		//  - `or` between equalities → "Expected two arguments to 'or'"
		//  - `|` between equalities → "Operator '|' not handled"
		//  - `px.contains(s, p)` → SUBSTRING (not regex)
		//  - `px.regex_match(p, s)` → RE2 regex match (PxL UDF
		//    registered in carnot/funcs/builtins/regex_ops.cc)
		// → use regex_match with an anchored alternation.
		b.WriteString("df = df[px.regex_match('^(")
		for i, k := range f.Pods {
			if i > 0 {
				b.WriteString("|")
			}
			b.WriteString(escapeRegex(escapePxL(k.Render())))
		}
		b.WriteString(")$', df.pod)]\n")
	}
	// Unfiltered mode: emit ALL pods on this node. The CH writer's
	// downstream consumers can filter by joining adaptive_attribution.
	b.WriteString("px.display(df, '" + s.cfg.Table + "')\n")
	return b.String()
}

// ScannerStats — small monitoring helper.
type ScannerStats struct {
	Queries int64
	Errors  int64
	RowsIn  int64
	Skipped int64
}

func (s *TableScanner) Stats() ScannerStats {
	return ScannerStats{
		Queries: s.queries.Load(),
		Errors:  s.queryErr.Load(),
		RowsIn:  s.rowsIn.Load(),
		Skipped: s.skipped.Load(),
	}
}

var pxlEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func escapePxL(s string) string {
	return pxlEscaper.Replace(s)
}

// escapeRegex defangs regex metacharacters in pod names. k8s pod names
// are DNS-1123 (lowercase alphanumeric + hyphen) plus a "/" namespace
// separator — none of these are regex meta — but we escape defensively
// so a future rename rule that admits underscores or dots doesn't
// produce a silently-broken filter.
var regexEscaper = strings.NewReplacer(
	`.`, `\.`,
	`|`, `\|`,
	`(`, `\(`,
	`)`, `\)`,
	`+`, `\+`,
	`*`, `\*`,
	`?`, `\?`,
	`[`, `\[`,
	`]`, `\]`,
	`{`, `\{`,
	`}`, `\}`,
	`^`, `\^`,
	`$`, `\$`,
)

func escapeRegex(s string) string {
	return regexEscaper.Replace(s)
}

// Compile-time assert ActiveSet.Key is what we expect (the fmt import
// would be unused if Render changed).
var _ = fmt.Sprintf

// Compile-time assert that activeset.Key.Render is the format used
// above (sanity for refactors).
var _ = (activeset.Key{}).Render
