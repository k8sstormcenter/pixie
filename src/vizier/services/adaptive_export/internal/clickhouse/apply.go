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
	// operator's only write target.
	"adaptive_attribution",
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
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("clickhouse: invalid endpoint %q: %w", endpoint, err)
	}
	return &Applier{
		endpoint: strings.TrimRight(endpoint, "/"),
		user:     user,
		pass:     pass,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Apply runs CREATE TABLE IF NOT EXISTS for every OperatorOwnedTables
// entry, in declared order. Idempotent. Returns the first error
// encountered without continuing — callers should treat schema apply
// as a precondition for the rest of boot.
func (a *Applier) Apply(ctx context.Context) error {
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
// table and confirms the operator-required columns are present. Used
// as a defensive guard against Pixie's retention plugin having
// auto-created a table BEFORE our Apply ran (e.g., operator was
// installed onto a cluster where the plugin had already been running
// with its own minimal DDL).
//
// Returns the FIRST drift detected as *SchemaDriftError. Callers
// usually want to log loudly and refuse to start so the misconfig
// is visible — silently continuing leaves the table with a schema
// the analyst-side JOINs can't cope with.
func (a *Applier) VerifyPixieSchema(ctx context.Context) error {
	for _, table := range PixieTables() {
		cols, err := a.tableColumns(ctx, table)
		if err != nil {
			return fmt.Errorf("verify %s: %w", table, err)
		}
		var missing []string
		for _, want := range requiredPixieColumns {
			if !contains(cols, want) {
				missing = append(missing, want)
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
