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

// Package clickhouse owns the canonical ClickHouse DDL for the
// forensic_db tables that adaptive_export reads (kubescape_logs) and
// the 12 socket_tracer tables Pixie's retention plugin writes (which
// the operator joins against via forensic_db.adaptive_attribution).
//
// schema.sql is the single source of truth. The operator never invents
// SQL — it always extracts statements verbatim from the embedded copy.
package clickhouse

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

//go:embed schema.sql
var canonicalSchema string

// KnownTables enumerates every forensic_db table the operator is aware
// of, in the order they appear in schema.sql. Backtick-quoted table
// names (those containing dots, e.g. "http2_messages.beta") are listed
// here without backticks; DDL() reinjects them.
var KnownTables = []string{
	// non-pixie
	"alerts",
	"kubescape_logs",
	// 12 socket_tracer pixie observation tables
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
	// conn_stats — re-added to rev-2 schema; counts per
	// (remote_addr, remote_port, protocol) on each retention-script pull.
	"conn_stats",
	"dc_snoop",
	"creds_change",
	"stack_trace",
	"dx_vfs_events",
	"dx_unlink",
	"dx_dlookup",
	"dx_mprotect",
	"dx_bpf",
	"dx_ptrace",
	// operator-owned attribution table
	"adaptive_attribution",
	// operator-owned persistent trigger cursor
	"trigger_watermark",
	// operator-owned per-pull write-fidelity instrument (ADAPTIVE_RECONCILE).
	// NOT a pixie table — absent from PixieTables().
	"ae_reconcile",
	// operator-owned dx evidence-graph edge list (read by the Pixie
	// dx_evidence_graph UI via clickhouse_dsn). NOT a pixie table.
	"dx_evidence_graph",
	// rule-ins-only VIEW over dx_evidence_graph (condition != ''); the
	// dx_evidence_graph UI reads this by default so benign rows are filtered
	// in ClickHouse, not pulled. Must follow dx_evidence_graph (depends on it).
	"dx_evidence_graph_malignant",
	// operator-owned dx §9 completeness manifest — one row per verdict naming
	// the evidence rows dx consulted (POST /dx/evidence_manifest). NOT a pixie
	// table. Independent of dx_evidence_graph.
	"dx_evidence_manifest",
	// operator-owned dx per-referral order seeds (#136 evidence-loss fix). dx
	// INSERTs one row per anomaly so dx_anomaly_orders can window every uniqueID.
	// NOT a pixie table.
	"dx_order_seeds",
	// order-UUID consulted-records bridge (#136 stamping): the records dx consulted
	// per primary kubescape log, stamped with its order_id. dx INSERTs. NOT a pixie
	// table.
	"dx_order_records",
	// NEW identity-model tables (added alongside dx_order_seeds/records). NOT pixie tables.
	"dx_orders",
	"dx_order_edges",
	// order-UUID pre-correlation views (#136) read by the px/dx_evidence_graph
	// dashboard. VIEWS, created after their base tables (kubescape_logs ensured
	// first). NOT pixie tables. Order matches schema.sql (appended at the end).
	"dx_anomaly_orders",
	"dx_kubescape_anomalies",
	"dx_src__kubescape_logs",
	"dx_src__redis_events",
	"dx_src__conn_stats",
	"dx_src__http_events",
	"dx_src__dns_events",
	"dx_src__pgsql_events",
	"dx_src__mysql_events",
	"dx_src__dc_snoop",
	"dx_src__stack_trace",
	// NEW identity-model join view.
	"dx_ord__conn_stats",
	"dx_ord__redis_events",
	"dx_ord__http_events",
	"dx_ord__dns_events",
	"dx_ord__pgsql_events",
	"dx_ord__mysql_events",
	"dx_ord__dc_snoop",
	"dx_ord__stack_trace",
	// MITRE ATT&CK enrichment + per-order window views (px/dx_evidence_graph).
	"dx_kubescape_mitre",
	"dx_src__kubescape_mitre",
	"dx_orders_win",
	// dc_snoop passthrough (PxL unique_id inference).
	"dx_base__dc_snoop",
}

// ErrUnknownTable is returned by DDL / Columns when asked for a table
// not in KnownTables.
var ErrUnknownTable = errors.New("clickhouse: unknown table")

// DDL returns the canonical CREATE TABLE statement for the named table,
// extracted from the embedded schema.sql.
func DDL(table string) (string, error) {
	if !isKnown(table) {
		return "", fmt.Errorf("%w: %q", ErrUnknownTable, table)
	}
	// ClickHouse identifiers containing a dot must be backtick-quoted.
	// Build the right header for the lookup.
	identifier := table
	if strings.Contains(table, ".") {
		identifier = "`" + table + "`"
	}
	start := -1
	for _, kw := range []string{"CREATE TABLE IF NOT EXISTS forensic_db.", "CREATE VIEW IF NOT EXISTS forensic_db."} {
		if start = strings.Index(canonicalSchema, kw+identifier); start >= 0 {
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("%w: %q registered in KnownTables but not present in embedded schema.sql", ErrUnknownTable, table)
	}
	rest := canonicalSchema[start:]
	semi := strings.Index(rest, ";")
	if semi < 0 {
		return "", fmt.Errorf("malformed schema.sql: no terminating ';' after %q", table)
	}
	return rest[:semi+1], nil
}

// PixieTables returns the subset of KnownTables that are pixie
// socket_tracer observation tables (the JOIN targets for
// adaptive_attribution).
func PixieTables() []string {
	return []string{
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
		"conn_stats",
		"dc_snoop",
		"creds_change",
		"stack_trace",
		"dx_vfs_events",
		"dx_unlink",
		"dx_dlookup",
		"dx_mprotect",
		"dx_bpf",
		"dx_ptrace",
	}
}

func isKnown(name string) bool {
	for _, t := range KnownTables {
		if t == name {
			return true
		}
	}
	return false
}
