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

import "testing"

// darkVectorTables is the set of bpftrace + native-profiler tables. Every entry
// is a dark-vector observation table whose rows are attributed to a workload via
// the process_stats pid-merge (or, for stack_trace, via upid). Kept in lockstep
// with pxl.darkVectorTables (+ stack_trace, the profiler) — TestDarkVectorSetMatchesPixieTables
// guards that this list stays a subset of the operator-owned pixie tables.
var darkVectorTables = []string{
	"dc_snoop", "creds_change", "stack_trace",
}

// TestDarkVectorTablesHaveFullMetadata enforces attribution CONSISTENCY: every
// dark-vector / profiler table carries the identical full k8s metadata set
// (namespace, pod, container, hostname), so any dark-vector event is attributable
// to its workload uniformly. A table missing one of these is an inconsistency
// that dx projection + forensic joins would silently drop — this was the concrete
// gap in the dx_* tracepoint tables before they were removed (no tracepoint was
// ever deployed for them, so they could only ever return zero rows).
func TestDarkVectorTablesHaveFullMetadata(t *testing.T) {
	required := []string{"namespace", "pod", "container", "hostname"}
	for _, tbl := range darkVectorTables {
		cols, err := Columns(tbl)
		if err != nil {
			t.Fatalf("Columns(%q): %v", tbl, err)
		}
		have := make(map[string]bool, len(cols))
		for _, c := range cols {
			have[c] = true
		}
		for _, req := range required {
			if !have[req] {
				t.Errorf("dark-vector table %s is missing metadata column %q (cols=%v) — every dark table must carry namespace/pod/container/hostname for uniform workload attribution", tbl, req, cols)
			}
		}
	}
}

// TestDarkVectorTablesCarryProcessIdentity — a dark-vector row must name the
// process it came from: comm (tracepoint tables) or upid (the profiler). Without
// it the pid/comm-filterable evidence base has nothing to filter on.
func TestDarkVectorTablesCarryProcessIdentity(t *testing.T) {
	for _, tbl := range darkVectorTables {
		cols, err := Columns(tbl)
		if err != nil {
			t.Fatalf("Columns(%q): %v", tbl, err)
		}
		var comm, upid bool
		for _, c := range cols {
			switch c {
			case "comm":
				comm = true
			case "upid":
				upid = true
			}
		}
		if !comm && !upid {
			t.Errorf("dark-vector table %s carries no process identity (need comm or upid) cols=%v", tbl, cols)
		}
	}
}

// TestDarkVectorSetMatchesPixieTables keeps darkVectorTables honest: every dark
// table is an operator-owned pixie observation table (so the metadata + nanosecond
// guards above actually cover it). Catches a dark table being renamed/removed in
// PixieTables() without updating this set.
func TestDarkVectorSetMatchesPixieTables(t *testing.T) {
	pixie := make(map[string]bool)
	for _, p := range PixieTables() {
		pixie[p] = true
	}
	for _, d := range darkVectorTables {
		if !pixie[d] {
			t.Errorf("dark-vector table %q is not in PixieTables() — it would escape the nanosecond + metadata guards", d)
		}
	}
}
