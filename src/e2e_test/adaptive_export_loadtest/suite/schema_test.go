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

package aeloadsuite

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// tsKind is how a timestamp column encodes nanoseconds.
type tsKind int

const (
	uint64Nanos tsKind = iota // raw unix-epoch NANOSECONDS in a UInt64
	dt64Nanos                 // DateTime64(9)
)

func (k tsKind) String() string {
	if k == uint64Nanos {
		return "UInt64 (unix ns)"
	}
	return "DateTime64(9)"
}

// tsCol is one timestamp column and the nanosecond encoding it MUST have.
type tsCol struct {
	table, column string
	kind          tsKind
}

// protocolTables — the Pixie socket_tracer tables; each carries time_ (Pixie
// native TIME64NS) + a derived event_time, both nanoseconds.
var protocolTables = []string{
	"http_events", "http2_messages.beta", "dns_events", "conn_stats",
	"pgsql_events", "redis_events", "mysql_events", "cql_events",
	"mongodb_events", "kafka_events.beta", "amqp_events", "mux_events", "tls_events",
}

// timestampFixtures — EVERY timestamp column in forensic_db and the nanosecond
// encoding it must have, one entry per (table, column). The whole system is
// nanoseconds: raw epoch columns are UInt64 unix-ns, everything else is
// DateTime64(9). New tables/columns get added here; TestNoCoarserTimestamps is
// the dynamic backstop for anything missed.
func timestampFixtures() []tsCol {
	f := []tsCol{
		// soc-owned inputs
		{"kubescape_logs", "event_time", uint64Nanos},
		{"alerts", "timestamp", dt64Nanos},
		{"alerts", "ingest_time", dt64Nanos},
		// AE trigger cursor + write bookkeeping
		{"trigger_watermark", "watermark", uint64Nanos},
		{"trigger_watermark", "updated_at", dt64Nanos},
		{"adaptive_attribution", "t_start", dt64Nanos},
		{"adaptive_attribution", "t_end", dt64Nanos},
		{"adaptive_attribution", "last_seen", dt64Nanos},
		{"ae_reconcile", "ts", dt64Nanos},
		{"ae_reconcile", "win_start", dt64Nanos},
		{"ae_reconcile", "win_end", dt64Nanos},
		// dx evidence
		{"dx_attack_graph", "event_time", uint64Nanos},
	}
	for _, t := range protocolTables {
		f = append(f, tsCol{t, "time_", dt64Nanos}, tsCol{t, "event_time", dt64Nanos})
	}
	return f
}

// columnType returns the ClickHouse type of forensic_db.<table>.<column>, or "".
func (e Env) columnType(t *testing.T, table, column string) string {
	return strings.TrimSpace(e.Query(t, fmt.Sprintf(
		"SELECT type FROM system.columns WHERE database='forensic_db' AND table='%s' AND name='%s'",
		table, column)))
}

// TestEveryTimestampIsNanoseconds asserts, per (table, column) fixture, that the
// timestamp column exists and is nanosecond-precision — DateTime64(9), or a
// UInt64 unix-ns epoch. This is the per-table schema contract.
func TestEveryTimestampIsNanoseconds(t *testing.T) {
	e := RequireLiveEnv(t)
	for _, f := range timestampFixtures() {
		f := f
		t.Run(f.table+"."+f.column, func(t *testing.T) {
			typ := e.columnType(t, f.table, f.column)
			require.NotEmptyf(t, typ, "%s.%s not found in forensic_db", f.table, f.column)
			switch f.kind {
			case dt64Nanos:
				require.Truef(t, strings.HasPrefix(typ, "DateTime64(9"),
					"%s.%s = %s, want %s (nanoseconds)", f.table, f.column, typ, f.kind)
			case uint64Nanos:
				require.Equalf(t, "UInt64", typ,
					"%s.%s = %s, want %s", f.table, f.column, typ, f.kind)
			}
		})
	}
}

// TestNoCoarserTimestamps is the dynamic backstop: NO DateTime column anywhere in
// forensic_db may be coarser than nanoseconds (catches DateTime, DateTime64(0..8)
// on any table/column not in the fixtures).
func TestNoCoarserTimestamps(t *testing.T) {
	e := RequireLiveEnv(t)
	bad := strings.TrimSpace(e.Query(t,
		"SELECT table || '.' || name || ' = ' || type FROM system.columns "+
			"WHERE database='forensic_db' AND type LIKE 'DateTime%' "+
			"AND type NOT LIKE 'DateTime64(9%' ORDER BY table, name FORMAT TSV"))
	require.Emptyf(t, bad, "non-nanosecond DateTime column(s) in forensic_db:\n%s", bad)
}
