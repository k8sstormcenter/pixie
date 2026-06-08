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

// TestDDL_ReturnsCanonicalForKnownTables — every table named in
// KnownTables can be extracted as a complete CREATE TABLE statement.
func TestDDL_ReturnsCanonicalForKnownTables(t *testing.T) {
	for _, name := range KnownTables {
		t.Run(name, func(t *testing.T) {
			ddl, err := DDL(name)
			if err != nil {
				t.Fatalf("DDL(%q): %v", name, err)
			}
			if !strings.HasPrefix(ddl, "CREATE TABLE IF NOT EXISTS forensic_db.") {
				t.Fatalf("DDL(%q) wrong prefix: %q", name, ddl[:minInt(70, len(ddl))])
			}
			if !strings.HasSuffix(ddl, ";") {
				t.Fatalf("DDL(%q) does not terminate with ';'", name)
			}
		})
	}
}

// TestDDL_PixieTablesIncludeNamespaceAndPod — every pixie table must
// declare namespace + pod columns (used by attribution JOINs).
func TestDDL_PixieTablesIncludeNamespaceAndPod(t *testing.T) {
	for _, name := range PixieTables() {
		t.Run(name, func(t *testing.T) {
			ddl, err := DDL(name)
			if err != nil {
				t.Fatalf("DDL(%q): %v", name, err)
			}
			if !strings.Contains(ddl, "namespace") {
				t.Fatalf("%s missing namespace column", name)
			}
			if !strings.Contains(ddl, "pod") {
				t.Fatalf("%s missing pod column", name)
			}
		})
	}
}

// TestDDL_PixieTables_NoAnomalyHashColumn — pixie observation tables
// MUST NOT carry the hash inline; attribution is via JOIN.
func TestDDL_PixieTables_NoAnomalyHashColumn(t *testing.T) {
	for _, name := range PixieTables() {
		t.Run(name, func(t *testing.T) {
			ddl, err := DDL(name)
			if err != nil {
				t.Fatalf("DDL(%q): %v", name, err)
			}
			if strings.Contains(ddl, "anomaly_hash") || strings.Contains(ddl, "anomaly_hashes") {
				t.Fatalf("pixie table %q must not carry anomaly_hash column; got:\n%s", name, ddl)
			}
		})
	}
}

// TestDDL_AdaptiveAttribution_HasExpectedColumns — the attribution
// table is the operator's only write target.
func TestDDL_AdaptiveAttribution_HasExpectedColumns(t *testing.T) {
	ddl, err := DDL("adaptive_attribution")
	if err != nil {
		t.Fatalf("DDL: %v", err)
	}
	for _, c := range []string{
		"anomaly_hash", "namespace", "pod", "comm", "pid",
		"hostname", "t_start", "t_end", "last_seen",
	} {
		if !strings.Contains(ddl, c) {
			t.Fatalf("adaptive_attribution missing column %q; got:\n%s", c, ddl)
		}
	}
	if !strings.Contains(ddl, "ReplacingMergeTree(t_end)") {
		t.Fatalf("adaptive_attribution must use ReplacingMergeTree(t_end); got:\n%s", ddl)
	}
}

// TestDDL_KubescapeLogs_PreservesAnomalyHash — kubescape_logs keeps its
// existing anomaly_hash DEFAULT ” column for pipeline compat.
func TestDDL_KubescapeLogs_PreservesAnomalyHash(t *testing.T) {
	ddl, err := DDL("kubescape_logs")
	if err != nil {
		t.Fatalf("DDL: %v", err)
	}
	if !strings.Contains(ddl, "anomaly_hash") {
		t.Fatalf("kubescape_logs lost anomaly_hash column: %s", ddl)
	}
}

// TestDDL_UnknownTable_ErrUnknownTable — defensive contract.
func TestDDL_UnknownTable_ErrUnknownTable(t *testing.T) {
	for _, bad := range []string{"", "no_such_table", "process_events"} {
		_, err := DDL(bad)
		if !errors.Is(err, ErrUnknownTable) {
			t.Fatalf("DDL(%q) → %v, want ErrUnknownTable", bad, err)
		}
	}
}

// TestDDL_DottedTableName_BacktickQuoted — schema.sql backtick-quotes
// dotted ClickHouse identifiers.
func TestDDL_DottedTableName_BacktickQuoted(t *testing.T) {
	for _, name := range []string{"http2_messages.beta", "kafka_events.beta"} {
		t.Run(name, func(t *testing.T) {
			ddl, err := DDL(name)
			if err != nil {
				t.Fatalf("DDL(%q): %v", name, err)
			}
			if !strings.Contains(ddl, "`"+name+"`") {
				t.Fatalf("dotted table %q must be backtick-quoted; got:\n%s", name, ddl)
			}
		})
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
