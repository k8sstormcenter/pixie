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
	"regexp"
	"testing"
)

// dt64Millis matches a DateTime64(3) COLUMN TYPE (millisecond scale). It does NOT
// match the toDateTime64(x, 3) function form (that reads "DateTime64(time_…"), so
// only real millisecond column declarations trip it.
var dt64Millis = regexp.MustCompile(`DateTime64\(3[,)]`)

// dt64Nanos matches a DateTime64(9) column type (nanosecond scale).
var dt64Nanos = regexp.MustCompile(`DateTime64\(9[,)]`)

// TestPixieTablesUseNanosecondTimestamps is the ONE-unit guardrail (contracts
// C1 + C16): every pixie observation table stores its timestamps as
// DateTime64(9) — nanoseconds — and NEVER DateTime64(3) milliseconds. A single
// millisecond column silently corrupts the invariant shared by three writers of
// these tables: the AE HTTP write path, the retention-plugin native ClickHouse
// export sink (which emits event_time = time_ as DateTime64(9) precisely to avoid
// its own millisecond auto-append), and dx/soc joins that read event_time as
// nanoseconds. kubescape_logs (unix-ns UInt64 INPUT) and alerts (kubescape's own
// millisecond alert stream) are not pixie observation tables and are excluded
// from PixieTables() by construction — this test locks that boundary.
func TestPixieTablesUseNanosecondTimestamps(t *testing.T) {
	for _, tbl := range PixieTables() {
		ddl, err := DDL(tbl)
		if err != nil {
			t.Fatalf("DDL(%q): %v", tbl, err)
		}
		if dt64Millis.MatchString(ddl) {
			t.Errorf("%s declares a DateTime64(3) millisecond column — pixie observation tables MUST use DateTime64(9) nanoseconds (contracts C1/C16). No millisecond timestamps.", tbl)
		}
		if !dt64Nanos.MatchString(ddl) {
			t.Errorf("%s has no DateTime64(9) timestamp column — expected nanosecond time_ and event_time.", tbl)
		}
	}
}

// TestNoMillisecondTimestampReintroducedAnywhere is a coarser backstop: outside
// the two documented millisecond consumers (kubescape alerts + the kubescape_logs
// magnitude-normalizer), no forensic_db table the operator owns as a pixie
// observation table may carry a DateTime64(3) column. Guards against a future
// table being added with the wrong scale.
func TestNoMillisecondTimestampReintroducedAnywhere(t *testing.T) {
	for _, tbl := range PixieTables() {
		ddl, err := DDL(tbl)
		if err != nil {
			t.Fatalf("DDL(%q): %v", tbl, err)
		}
		if dt64Millis.MatchString(ddl) {
			t.Fatalf("pixie table %s reintroduced a millisecond timestamp — the one-unit (nanosecond) invariant is broken", tbl)
		}
	}
}
