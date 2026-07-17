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

//go:build integration
// +build integration

package sink_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	chpkg "px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
)

// Live integration tests for the operator's ClickHouse write path.
// Driven against a real ClickHouse reachable at INTEGRATION_CH_ENDPOINT.
// Skipped if unset.

func env(t *testing.T) (endpoint, user, pass string) {
	t.Helper()
	endpoint = os.Getenv("INTEGRATION_CH_ENDPOINT")
	if endpoint == "" {
		t.Skip("INTEGRATION_CH_ENDPOINT not set; skipping live ClickHouse test")
	}
	return endpoint, os.Getenv("INTEGRATION_CH_USER"), os.Getenv("INTEGRATION_CH_PASSWORD")
}

func ensureSchema(t *testing.T, endpoint, user, pass string) {
	t.Helper()
	a, err := chpkg.NewApplier(endpoint, user, pass)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.Apply(ctx); err != nil {
		t.Fatalf("Apply (precondition): %v", err)
	}
}

func chCount(t *testing.T, endpoint, user, pass, query string) int {
	t.Helper()
	q := url.Values{}
	q.Set("query", query)
	req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/?"+q.Encode(), nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		t.Fatalf("count HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var n int
	fmt.Sscanf(strings.TrimSpace(string(body)), "%d", &n)
	return n
}

// TestSinkWriteAttribution_Live exercises Write() — the operator's only
// production write surface (forensic_db.adaptive_attribution). One row
// per arriving anomaly; ReplacingMergeTree(t_end) collapses re-inserts.
func TestSinkWriteAttribution_Live(t *testing.T) {
	endpoint, user, pass := env(t)
	ensureSchema(t, endpoint, user, pass)

	s, err := sink.New(sink.Config{
		Endpoint: endpoint,
		Username: user,
		Password: pass,
	})
	if err != nil {
		t.Fatalf("sink.New: %v", err)
	}

	// Unique anomaly_hash per test run — keeps assertions decoupled
	// from any pre-existing rows.
	tag := fmt.Sprintf("aw-test-%d", time.Now().UnixNano())
	sum := sha256.Sum256([]byte(tag))
	hash := anomaly.AnomalyHash(fmt.Sprintf("%x", sum[:8]))

	now := time.Now().UTC()
	row := sink.AttributionRow{
		AnomalyHash: hash,
		Namespace:   "redis",
		Pod:         "redis-test",
		Comm:        "redis-server",
		PID:         1234,
		Hostname:    tag, // unique hostname → unique row
		TStart:      now.Add(-5 * time.Minute),
		TEnd:        now.Add(5 * time.Minute),
		LastSeen:    now,
		LastRuleID:  "R1005",
		NAnomalies:  1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.Write(ctx, []sink.AttributionRow{row}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := chCount(t, endpoint, user, pass,
		fmt.Sprintf("SELECT count() FROM forensic_db.adaptive_attribution WHERE hostname='%s'", tag))
	if got != 1 {
		t.Errorf("adaptive_attribution count for hostname=%s: got %d, want 1", tag, got)
	}
}

// TestSinkWritePixieRows_Live exercises WritePixieRows() against every
// pixie observation table the operator owns. This is the precise bug
// surface the user reported — silent INSERT failures here mean the
// per-table fan-out writes nothing and the analyst sees empty tables.
//
// One row per table, with a unique hostname per run so subsequent runs
// don't have to reset the cluster.
func TestSinkWritePixieRows_Live(t *testing.T) {
	endpoint, user, pass := env(t)
	ensureSchema(t, endpoint, user, pass)

	s, err := sink.New(sink.Config{
		Endpoint: endpoint,
		Username: user,
		Password: pass,
	})
	if err != nil {
		t.Fatalf("sink.New: %v", err)
	}

	tag := fmt.Sprintf("aw-pix-%d", time.Now().UnixNano())
	now := time.Now().UTC()

	for _, table := range chpkg.PixieTables() {
		// Per-table timeout so a slow early table can't starve later
		// ones — a shared budget across the whole loop makes this live
		// test unnecessarily flaky on a loaded CH (CodeRabbit
		// r-#68/sink/integration_test.go).
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		row := minimalRowFor(table, tag, now)
		if err := s.WritePixieRows(ctx, table, []map[string]any{row}); err != nil {
			t.Errorf("WritePixieRows(%s): %v", table, err)
			cancel()
			continue
		}
		ident := table
		if strings.Contains(table, ".") {
			ident = "`" + table + "`"
		}
		got := chCount(t, endpoint, user, pass,
			fmt.Sprintf("SELECT count() FROM forensic_db.%s WHERE hostname='%s'", ident, tag))
		if got < 1 {
			t.Errorf("table %s after WritePixieRows: count=%d, want >=1", table, got)
		}
		cancel()
	}
}

// minimalRowFor returns the minimum-viable row map for a pixie
// observation table — only the columns the schema marks NOT NULL and
// that don't have DEFAULT clauses. The remaining columns get CH
// defaults (0 / "" / now).
func minimalRowFor(table, hostname string, t time.Time) map[string]any {
	base := map[string]any{
		"time_":      t.Format("2006-01-02 15:04:05.000000000"),
		"upid":       "0:0:0",
		"hostname":   hostname,
		"event_time": t.Format("2006-01-02 15:04:05.000"),
		"namespace":  "default",
		"pod":        "test-pod",
	}
	// Some pixie tables use slightly different column shapes — provide
	// the strict-minimum extras to avoid CH MissingColumn errors.
	switch table {
	case "http_events":
		base["resp_status"] = 200
		base["latency"] = 0
		base["remote_port"] = 0
		base["local_port"] = 0
	case "dns_events":
		base["remote_port"] = 53
		base["local_port"] = 0
		base["latency"] = 0
	case "redis_events", "mysql_events", "pgsql_events", "cql_events", "mongodb_events",
		"amqp_events", "mux_events", "tls_events":
		base["latency"] = 0
		base["remote_port"] = 0
		base["local_port"] = 0
	case "http2_messages.beta":
		base["remote_port"] = 0
		base["local_port"] = 0
	case "kafka_events.beta":
		base["latency"] = 0
		base["remote_port"] = 0
		base["local_port"] = 0
	}
	return base
}
