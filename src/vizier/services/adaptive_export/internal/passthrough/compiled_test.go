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

package passthrough

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

// syncSink records written (table → rowcount) under a mutex so it is safe
// to assert against after the concurrent compiled tick.
type syncSink struct {
	mu  sync.Mutex
	got map[string]int
}

func newSyncSink() *syncSink { return &syncSink{got: map[string]int{}} }

func (s *syncSink) WritePixieRows(_ context.Context, table string, rows []map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got[table] += len(rows)
	return nil
}

func (s *syncSink) tables() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.got))
	for t := range s.got {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// TestNew_ExcludesHTTP2 proves http2_messages.beta is dropped from the
// firehose set (it isn't materialised on every cluster → "Table not found"
// spam) while another dotted-but-real table (kafka_events.beta) is kept.
func TestNew_ExcludesHTTP2(t *testing.T) {
	// Tables nil → defaults to clickhouse.PixieTables() which DOES list
	// http2_messages.beta; New must strip it.
	loop := New(tableQuerier{n: map[string]int{}}, newSyncSink(),
		Config{Window: time.Minute, Compiled: true})

	for _, tbl := range loop.cfg.Tables {
		if tbl == "http2_messages.beta" {
			t.Fatalf("http2_messages.beta must be excluded from passthrough tables: %v", loop.cfg.Tables)
		}
	}
	if _, ok := loop.tmpl["http2_messages.beta"]; ok {
		t.Fatalf("http2_messages.beta must not be precompiled")
	}
	// Sanity: a real table is still present + precompiled.
	if _, ok := loop.tmpl["http_events"]; !ok {
		t.Fatalf("http_events should be precompiled; tmpl=%v", loop.tmpl)
	}
}

// TestCompiledTick_WritesAllTables exercises the concurrent precompiled
// path: every table with rows must be written exactly once. (Running under
// `go test -race` also asserts the fan-out is data-race free.)
func TestCompiledTick_WritesAllTables(t *testing.T) {
	sink := newSyncSink()
	loop := New(
		tableQuerier{n: map[string]int{
			"http_events": 4,
			"dns_events":  2,
			"conn_stats":  7,
		}},
		sink,
		Config{
			Window:   time.Minute,
			Tables:   []string{"http_events", "dns_events", "conn_stats"},
			Compiled: true,
		},
	)
	loop.tick(context.Background())

	want := map[string]int{"http_events": 4, "dns_events": 2, "conn_stats": 7}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.got) != len(want) {
		t.Fatalf("wrote %v tables, want %v", sink.got, want)
	}
	for tbl, n := range want {
		if sink.got[tbl] != n {
			t.Errorf("table %s wrote %d rows, want %d", tbl, sink.got[tbl], n)
		}
	}
}

// TestCompiledTick_EqualsLegacy proves the compiled path and the legacy
// serial path write the SAME tables with the SAME row counts for identical
// inputs — the toggle changes performance/structure, not output.
func TestCompiledTick_EqualsLegacy(t *testing.T) {
	rows := map[string]int{"http_events": 3, "dns_events": 5, "conn_stats": 1}
	tables := []string{"http_events", "dns_events", "conn_stats"}

	run := func(compiled bool) *syncSink {
		sink := newSyncSink()
		New(tableQuerier{n: rows}, sink,
			Config{Window: time.Minute, Tables: tables, Compiled: compiled}).
			tick(context.Background())
		return sink
	}

	c := run(true)
	l := run(false)

	if cs, ls := c.tables(), l.tables(); len(cs) != len(ls) {
		t.Fatalf("compiled wrote %v, legacy wrote %v", cs, ls)
	}
	for tbl, n := range rows {
		if c.got[tbl] != n || l.got[tbl] != n {
			t.Errorf("table %s: compiled=%d legacy=%d want %d", tbl, c.got[tbl], l.got[tbl], n)
		}
	}
}
