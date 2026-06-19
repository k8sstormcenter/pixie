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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OperatorOwnedTables is the subset of KnownTables the adaptive_export
// operator creates on boot. Kubescape tables (alerts, kubescape_logs)
// are NOT here — they are owned by the soc/tree/clickhouse-lab
// installer. Order matters: adaptive_attribution last so it does not
// reference any pixie table during creation (it does not, but the
// invariant is cheap to keep).
var OperatorOwnedTables = []string{
	// 12 pixie socket_tracer tables — created BEFORE Pixie's retention
	// plugin gets a chance to auto-DDL them (which would omit our
	// namespace + pod columns and break analyst JOINs).
	"http_events",
	"http2_messages.beta",
	"dns_events",
	"redis_events",
	"mysql_events",
	"pgsql_events",
	"cql_events",
	"mongodb_events",
	"kafka_events.beta",
	"amqp_events",
	"mux_events",
	"tls_events",
	// conn_stats — pixie observation table; created in the
	// same boot pass as the others so Apply (here) and Verify (KnownTables
	// in ddl.go) can't drift. The drift was a real regression: aeprod3/4/5
	// shipped with this list at 14 entries while ddl.go's KnownTables had 15,
	// so Apply created 14 tables on fresh install and Verify failed at boot
	// with "conn_stats schema drift, missing columns". Locked down by
	// TestOperatorOwnedTables_CoversAllPixieTables in apply_test.go.
	"conn_stats",
	// operator's write targets.
	"adaptive_attribution",
	"trigger_watermark",
	// per-pull write-fidelity instrument (ADAPTIVE_RECONCILE). Created on
	// boot so a reconcile run has a target without manual DDL. Not a pixie
	// table → not in PixieTables(), so VerifyPixieSchema ignores it.
	"ae_reconcile",
	// dx evidence-graph edge list — created on boot so the Pixie
	// dx_evidence_graph UI (px.DataFrame clickhouse_dsn) has a real,
	// globally-registered table to read. dx emits edges, AE persists.
	// Not a pixie socket_tracer table → not in PixieTables().
	"dx_attack_graph",
	// rule-ins-only VIEW over dx_attack_graph; created AFTER it (depends on it).
	"dx_attack_graph_malicious",
}

// Applier applies operator-owned DDL to a ClickHouse cluster over the
// HTTP interface (default 8123). Used at boot.
type Applier struct {
	endpoint string
	user     string
	pass     string
	client   *http.Client
}

// NewApplier validates the endpoint and returns a ready Applier.
func NewApplier(endpoint, user, pass string) (*Applier, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("clickhouse: empty endpoint")
	}
	// Reject anything that isn't an absolute http/https URL — net/http will
	// otherwise interpret things like "localhost:8123" as a relative path
	// and fail much later with a confusing "missing protocol scheme" deep
	// inside the first request.
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("clickhouse: invalid endpoint %q (must be absolute http/https URL)", endpoint)
	}
	return &Applier{
		endpoint: strings.TrimRight(endpoint, "/"),
		user:     user,
		pass:     pass,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Apply ensures forensic_db exists, then runs CREATE TABLE IF NOT
// EXISTS for every OperatorOwnedTables entry in declared order.
// Idempotent. Returns the first error encountered without continuing —
// callers should treat schema apply as a precondition for the rest of
// boot.
func (a *Applier) Apply(ctx context.Context) error {
	if err := a.execute(ctx, "CREATE DATABASE IF NOT EXISTS forensic_db"); err != nil {
		return fmt.Errorf("apply: create database forensic_db: %w", err)
	}
	for _, table := range OperatorOwnedTables {
		ddl, err := DDL(table)
		if err != nil {
			return fmt.Errorf("apply: get DDL for %s: %w", table, err)
		}
		if err := a.execute(ctx, ddl); err != nil {
			return fmt.Errorf("apply: create %s: %w", table, err)
		}
	}
	return nil
}

// WriteAttackGraph inserts dx evidence-graph edges into
// forensic_db.dx_attack_graph. jsonEachRow is newline-delimited JSON objects
// whose keys are the column names (JSONEachRow; unknown keys are skipped,
// missing columns default). No-op on empty input.
func (a *Applier) WriteAttackGraph(ctx context.Context, jsonEachRow []byte) error {
	if len(jsonEachRow) == 0 {
		return nil
	}
	return a.execute(ctx, "INSERT INTO forensic_db.dx_attack_graph FORMAT JSONEachRow\n"+string(jsonEachRow))
}

// execute POSTs a single DDL statement to ClickHouse via the HTTP
// query endpoint. Non-2xx responses surface as Go errors.
func (a *Applier) execute(ctx context.Context, sql string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.endpoint+"/", strings.NewReader(sql))
	if err != nil {
		return err
	}
	if a.user != "" {
		req.SetBasicAuth(a.user, a.pass)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// SchemaDriftError is returned by VerifyPixieSchema when a pixie
// observation table is missing one or more of the operator-required
// columns. errors.Is-friendly.
type SchemaDriftError struct {
	Table   string
	Missing []string
}

func (e *SchemaDriftError) Error() string {
	return fmt.Sprintf("clickhouse: pixie table %q schema drift, missing columns: %s",
		e.Table, strings.Join(e.Missing, ", "))
}

// requiredPixieColumns are the columns every pixie observation table
// MUST have for adaptive_attribution JOINs to work. namespace + pod are
// our additions over Pixie's auto-DDL; hostname + time_ are Pixie's own
// canonical columns we depend on.
var requiredPixieColumns = []string{"namespace", "pod", "hostname", "time_"}

// VerifyPixieSchema queries system.columns for each pixie observation
// table and confirms EVERY column AE writes for that table is present
// in CH. This is the **writer ⇔ schema contract** test (the T1 in
// the operator's PR #47 schema-loss report on 2026-06-07).
//
// The earlier shape of this function only checked the 4
// operator-required columns (namespace/pod/hostname/time_) — a table
// could be hand-created with those four plus a different subset of
// data columns and pass verification, while AE's writer would post
// JSON containing the column names schema.sql says the table should
// have. The result on rig 6a25c85c: CH silently dropped 22 of 24
// columns into nothing because they were "unknown fields"
// (input_format_skip_unknown_fields default = 1), AE's
// summaryWroteFewerThan saw written_rows=0 / rows_sent=259 only AFTER
// the data was lost, and the controller hot-looped on the rejection.
//
// The expanded contract: for every table in PixieTables(), CH's
// actual column set must be a superset of clickhouse.Columns(table) —
// i.e. the canonical column list parsed out of schema.sql, which IS
// the single source of truth.
//
// Returns the FIRST drift detected as *SchemaDriftError. Callers
// usually want to log loudly and refuse to start so the misconfig
// is visible — silently continuing leaves the table with a schema
// the AE writer can't actually populate.
func (a *Applier) VerifyPixieSchema(ctx context.Context) error {
	for _, table := range PixieTables() {
		actual, err := a.tableColumns(ctx, table)
		if err != nil {
			return fmt.Errorf("verify %s: %w", table, err)
		}
		// The canonical column shape AE expects (schema.sql).
		want, err := Columns(table)
		if err != nil {
			return fmt.Errorf("verify %s: load expected columns: %w", table, err)
		}
		// Operator-required + canonical union, deduped.
		need := make([]string, 0, len(want)+len(requiredPixieColumns))
		seen := map[string]bool{}
		for _, c := range want {
			if !seen[c] {
				seen[c] = true
				need = append(need, c)
			}
		}
		for _, c := range requiredPixieColumns {
			if !seen[c] {
				seen[c] = true
				need = append(need, c)
			}
		}
		var missing []string
		for _, w := range need {
			if !contains(actual, w) {
				missing = append(missing, w)
			}
		}
		if len(missing) > 0 {
			return &SchemaDriftError{Table: table, Missing: missing}
		}
	}
	return nil
}

// tableColumns lists the column names of forensic_db.<table> as
// reported by system.columns.
func (a *Applier) tableColumns(ctx context.Context, table string) ([]string, error) {
	q := url.Values{}
	q.Set("query", fmt.Sprintf(
		"SELECT name FROM system.columns WHERE database='forensic_db' AND table=%s FORMAT JSONEachRow",
		quoteCH(table)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint+"/?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if a.user != "" {
		req.SetBasicAuth(a.user, a.pass)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	type row struct {
		Name string `json:"name"`
	}
	var out []string
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var r row
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("parse system.columns row: %w", err)
		}
		out = append(out, r.Name)
	}
	return out, nil
}

func quoteCH(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
	return "'" + r + "'"
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
