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

package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestApply_ExecutesEveryOperatorOwnedTable — Apply POSTs one DDL per
// table in OperatorOwnedTables, in order. None of the kubescape tables
// (alerts, kubescape_logs) are touched — those belong to the soc installer.
func TestApply_ExecutesEveryOperatorOwnedTable(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	a, err := NewApplier(srv.URL, "", "")
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	if err := a.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// 1 CREATE DATABASE + len(OperatorOwnedTables) CREATE TABLE calls.
	if got, want := len(bodies), len(OperatorOwnedTables)+1; got != want {
		t.Fatalf("Apply made %d calls, want %d", got, want)
	}
	if !strings.Contains(bodies[0], "CREATE DATABASE IF NOT EXISTS forensic_db") {
		t.Fatalf("first DDL must create the database; got: %s", bodies[0])
	}
	// Spot-check that the SECOND call is for the first OperatorOwnedTables entry,
	// and that the LAST call is for the last OperatorOwnedTables entry (robust to
	// new operator-owned tables being appended, e.g. dx_evidence_graph).
	if !strings.Contains(bodies[1], "forensic_db."+OperatorOwnedTables[0]) {
		t.Fatalf("second DDL not for %s; got: %s", OperatorOwnedTables[0], bodies[1])
	}
	lastTable := OperatorOwnedTables[len(OperatorOwnedTables)-1]
	if !strings.Contains(bodies[len(bodies)-1], "forensic_db."+lastTable) {
		t.Fatalf("last DDL not for %s; got: %s", lastTable, bodies[len(bodies)-1])
	}
	// And ensure no kubescape DDL leaked through. Match the CREATE TABLE form,
	// not a bare mention: the order-UUID views (dx_kubescape_anomalies,
	// dx_src__kubescape_logs) legitimately SELECT FROM forensic_db.kubescape_logs,
	// so a substring check on the table name alone flags reading it as creating it.
	// Ownership is about who issues the CREATE TABLE, which is still never AE.
	for _, b := range bodies {
		for _, ks := range []string{"alerts", "kubescape_logs"} {
			if strings.Contains(b, "CREATE TABLE IF NOT EXISTS forensic_db."+ks) {
				t.Fatalf("operator's Apply must not create kubescape tables; got:\n%s", b)
			}
		}
	}
}

// TestApply_FailsFastOnHTTPError — if any CREATE returns non-2xx,
// Apply returns immediately without attempting later tables.
func TestApply_FailsFastOnHTTPError(t *testing.T) {
	// atomic.Int32 because httptest's handler runs on its own goroutine
	// while the test goroutine reads `calls` after Apply returns —
	// without atomic the -race detector flags a data race even though
	// the goroutines are happens-before-ordered by Apply's HTTP response.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("ddl exploded"))
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	a, err := NewApplier(srv.URL, "", "")
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	if err := a.Apply(context.Background()); err == nil {
		t.Fatalf("expected error from Apply on HTTP 500")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Apply continued past first failure; calls = %d", got)
	}
}

// tableForQuery extracts the table name from a system.columns query
// like "...AND table='http_events' FORMAT JSONEachRow".
func tableForQuery(q string) string {
	const marker = "table='"
	i := strings.Index(q, marker)
	if i < 0 {
		return ""
	}
	rest := q[i+len(marker):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// TestVerifyPixieSchema_DetectsMissingColumns — defensive guard.
// On rig 6a25c85c (PR #47 schema-loss report), http_events was created
// by a hand-maintained stopgap that DIDN'T include req_path /
// req_headers / etc. — the columns AE's writer puts into JSONEachRow
// posts. The old VerifyPixieSchema only checked namespace/pod/hostname/
// time_, so it passed; the writer's 22 unknown fields then got silently
// dropped by CH at default settings. The expanded contract verifies
// EVERY column AE expects per table is present in CH (the writer ⇔
// schema contract). This test reproduces the rig 6a25c85c shape:
// http_events comes back with the 4 operator-required columns but
// missing the data columns the writer fills.
func TestVerifyPixieSchema_DetectsMissingColumns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return only the operator-required columns for the first pixie
		// table iterated; that's the regression shape — looks "valid"
		// to the old checker but fails the writer-column union.
		table := tableForQuery(r.URL.Query().Get("query"))
		if table == "http_events" {
			_, _ = w.Write([]byte(`{"name":"time_"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"upid"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"namespace"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"pod"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"hostname"}` + "\n"))
			return
		}
		// Other tables (won't be reached) — fully populated.
		cols, _ := Columns(table)
		for _, c := range cols {
			fmt.Fprintf(w, "{\"name\":%q}\n", c)
		}
	}))
	defer srv.Close()
	a, err := NewApplier(srv.URL, "", "")
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	err = a.VerifyPixieSchema(context.Background())
	if err == nil {
		t.Fatalf("expected SchemaDriftError; got nil")
	}
	var drift *SchemaDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("err type = %T, want *SchemaDriftError", err)
	}
	if drift.Table != "http_events" {
		t.Fatalf("first drift = %q, want http_events", drift.Table)
	}
	// Spot-check that several of the data columns the writer fills are
	// flagged missing — that's the new coverage vs the old 4-column
	// check.
	for _, want := range []string{"req_path", "req_headers", "resp_status", "latency"} {
		if !contains(drift.Missing, want) {
			t.Errorf("Missing should include %q (writer-column drift); got %v", want, drift.Missing)
		}
	}
}

// TestVerifyPixieSchema_AllPresent — happy path. The mock server returns
// the FULL schema.sql column shape for each table, so VerifyPixieSchema
// confirms the writer ⇔ schema contract holds and returns nil.
func TestVerifyPixieSchema_AllPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		table := tableForQuery(r.URL.Query().Get("query"))
		cols, err := Columns(table)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		for _, c := range cols {
			fmt.Fprintf(w, "{\"name\":%q}\n", c)
		}
	}))
	defer srv.Close()
	a, err := NewApplier(srv.URL, "", "")
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	if err := a.VerifyPixieSchema(context.Background()); err != nil {
		t.Fatalf("VerifyPixieSchema: %v", err)
	}
}

// TestNewApplier_RejectsBadEndpoint — defensive contract.
func TestNewApplier_RejectsBadEndpoint(t *testing.T) {
	if _, err := NewApplier("", "", ""); err == nil {
		t.Fatalf("empty endpoint not rejected")
	}
	if _, err := NewApplier("http://%zz", "", ""); err == nil {
		t.Fatalf("malformed endpoint not rejected")
	}
}

// TestOperatorOwnedTables_DoesNotIncludeKubescape — structural guard:
// the operator never owns kubescape tables.
func TestOperatorOwnedTables_DoesNotIncludeKubescape(t *testing.T) {
	for _, x := range []string{"alerts", "kubescape_logs"} {
		if contains(OperatorOwnedTables, x) {
			t.Fatalf("%q must not be in OperatorOwnedTables (it belongs to the soc installer)", x)
		}
	}
}

// TestOperatorOwnedTables_TrailingOperatorTables — ordering guard.
// pixie observation tables come first (so they exist before the retention
// plugin can auto-DDL them with the wrong schema), then the operator's
// own write targets in declared order.
func TestOperatorOwnedTables_TrailingOperatorTables(t *testing.T) {
	want := []string{
		"adaptive_attribution", "trigger_watermark", "ae_reconcile", "dx_evidence_graph", "dx_evidence_manifest", "dx_order_seeds", "dx_order_records", "dx_orders", "dx_order_edges",
		"dx_anomaly_orders", "dx_kubescape_anomalies", "dx_src__kubescape_logs", "dx_src__stack_trace", "dx_ord__conn_stats", "dx_ord__redis_events", "dx_ord__http_events", "dx_ord__dns_events", "dx_ord__pgsql_events", "dx_ord__mysql_events", "dx_ord__dc_snoop", "dx_ord__stack_trace", "dx_kubescape_mitre", "dx_src__kubescape_mitre", "dx_orders_win", "dx_dns_resolve",
	}
	got := OperatorOwnedTables[len(OperatorOwnedTables)-len(want):]
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("OperatorOwnedTables tail = %v, want %v", got, want)
		}
	}
}

// TestOperatorOwnedTables_CoversAllPixieTables — drift guard between the
// boot-time Apply (OperatorOwnedTables, this file) and the verify path
// that uses ddl.go's KnownTables / PixieTables. aeprod3/4/5 shipped with
// the two lists out of sync: ddl.go's PixieTables() included "conn_stats"
// (re-added in commit a54a1f6d3) but OperatorOwnedTables
// did not, so Apply created 14 tables and Verify expected 15 — AE fatal'd
// at boot with `pixie table schema drift detected … conn_stats schema
// drift, missing columns`. Anyone adding a new pixie observation table in
// the future MUST add it to both lists; this test fails loudly otherwise.
func TestOperatorOwnedTables_CoversAllPixieTables(t *testing.T) {
	owned := map[string]bool{}
	for _, n := range OperatorOwnedTables {
		owned[n] = true
	}
	var missing []string
	for _, p := range PixieTables() {
		if !owned[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("PixieTables() not covered by OperatorOwnedTables: %v "+
			"(adding a pixie table requires updating BOTH apply.go OperatorOwnedTables "+
			"and ddl.go KnownTables+PixieTables — drift causes the boot-time schema "+
			"verify to fail with \"missing columns\")", missing)
	}
}
