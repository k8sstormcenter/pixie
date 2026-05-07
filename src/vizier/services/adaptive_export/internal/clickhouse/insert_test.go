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
	"errors"
	"strings"
	"testing"
)

// TestColumns_AdaptiveAttribution — the operator's only write target.
// Column list must match the DDL exactly so the sink can append values
// in the right positional order.
func TestColumns_AdaptiveAttribution(t *testing.T) {
	cols, err := Columns("adaptive_attribution")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	want := []string{
		"anomaly_hash", "namespace", "pod", "comm", "pid",
		"hostname", "t_start", "t_end", "last_seen",
		"last_rule_id", "n_anomalies",
	}
	if len(cols) != len(want) {
		t.Fatalf("Columns(adaptive_attribution) length %d, want %d; got %v", len(cols), len(want), cols)
	}
	for i, c := range want {
		if cols[i] != c {
			t.Fatalf("col[%d] = %q, want %q (full=%v)", i, cols[i], c, cols)
		}
	}
}

// TestColumns_PixieTablesIncludeNamespaceAndPod — every pixie table's
// column list contains namespace + pod (the JOIN keys against
// adaptive_attribution).
func TestColumns_PixieTablesIncludeNamespaceAndPod(t *testing.T) {
	for _, table := range PixieTables() {
		t.Run(table, func(t *testing.T) {
			cols, err := Columns(table)
			if err != nil {
				t.Fatalf("Columns(%q): %v", table, err)
			}
			if !contains(cols, "namespace") {
				t.Fatalf("%s missing namespace; cols=%v", table, cols)
			}
			if !contains(cols, "pod") {
				t.Fatalf("%s missing pod; cols=%v", table, cols)
			}
			if contains(cols, "anomaly_hash") || contains(cols, "anomaly_hashes") {
				t.Fatalf("%s must not carry hash inline; cols=%v", table, cols)
			}
		})
	}
}

// TestInsertSQL_AdaptiveAttribution — the canonical INSERT used by the sink.
func TestInsertSQL_AdaptiveAttribution(t *testing.T) {
	sql, err := InsertSQL("adaptive_attribution")
	if err != nil {
		t.Fatalf("InsertSQL: %v", err)
	}
	if !strings.HasPrefix(sql, "INSERT INTO forensic_db.adaptive_attribution (") {
		t.Fatalf("bad prefix: %q", sql)
	}
	if !strings.HasSuffix(sql, ") VALUES") {
		t.Fatalf("bad suffix: %q", sql)
	}
}

// TestInsertSQL_DottedTablesBacktickQuoted — INSERT statements for
// dotted ClickHouse identifiers must wrap the name in backticks.
func TestInsertSQL_DottedTablesBacktickQuoted(t *testing.T) {
	for _, table := range []string{"http2_messages.beta", "kafka_events.beta"} {
		t.Run(table, func(t *testing.T) {
			sql, err := InsertSQL(table)
			if err != nil {
				t.Fatalf("InsertSQL(%q): %v", table, err)
			}
			if !strings.Contains(sql, "INSERT INTO forensic_db.`"+table+"` (") {
				t.Fatalf("dotted table %q not backtick-quoted: %q", table, sql)
			}
		})
	}
}

// TestInsertSQL_Unknown — defensive contract.
func TestInsertSQL_Unknown(t *testing.T) {
	for _, bad := range []string{"", "evil; DROP TABLE"} {
		_, err := InsertSQL(bad)
		if !errors.Is(err, ErrUnknownTable) {
			t.Fatalf("InsertSQL(%q) → %v, want ErrUnknownTable", bad, err)
		}
	}
}

