// Copyright 2018- The Pixie Authors.
// SPDX-License-Identifier: Apache-2.0

package script

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
)

var (
	projRe = regexp.MustCompile(`df = df\[\[([^\]]*)\]\]`)
	quotRe = regexp.MustCompile(`'([^']+)'`)
)

// lastProjection returns the column names in the final `df = df[['a','b',...]]`
// projection of an export script.
func lastProjection(script string) []string {
	m := projRe.FindAllStringSubmatch(script, -1)
	if len(m) == 0 {
		return nil
	}
	var cols []string
	for _, q := range quotRe.FindAllStringSubmatch(m[len(m)-1][1], -1) {
		cols = append(cols, q[1])
	}
	return cols
}

// TestDcSnoopExportColumnsMatchSchema is the strict-sink contract: the columns the
// dc_snoop retention export projects — plus event_time (added via df.event_time =
// df.time_) — must be EXACTLY the forensic_db.dc_snoop schema columns. A mismatch
// means the OTel/ClickHouse sink sends an unknown or missing column and the INSERT
// fails. This is the coupling that adding ppid/pcomm/pid_start/ppid_start could have
// silently broken, and it guards the steered path too (queryfor auto-carries the
// tracepoint columns, so schema == export == tracepoint-derived).
func TestDcSnoopExportColumnsMatchSchema(t *testing.T) {
	proj := lastProjection(dcSnoopScript)
	if len(proj) == 0 {
		t.Fatal("no df[[...]] projection found in dc_snoop.pxl")
	}
	got := append(append([]string{}, proj...), "event_time")
	want, err := clickhouse.Columns("dc_snoop")
	if err != nil {
		t.Fatalf("Columns(dc_snoop): %v", err)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dc_snoop export columns != schema columns\n export=%v\n schema=%v", got, want)
	}
}

// TestDcSnoopTracepointCapturesParent — the bpftrace program must emit the parent
// identity inline (curtask->real_parent) so every dcache event carries ppid/pcomm
// and a pid-reuse-stable start time (group_leader->start_time) for both the process
// and its parent — the process-forest edge.
func TestDcSnoopTracepointCapturesParent(t *testing.T) {
	for _, tok := range []string{
		"real_parent", "ppid:", "pcomm:", "pid_start:", "ppid_start:", "group_leader->start_time",
	} {
		if !strings.Contains(dcSnoopDeployScript, tok) {
			t.Errorf("dc_snoop_deploy.pxl missing %q — parent/identity capture incomplete", tok)
		}
	}
	// Both probe blocks (kprobe:lookup_fast + kretprobe:d_lookup) must carry it.
	if n := strings.Count(dcSnoopDeployScript, "real_parent->pid"); n < 2 {
		t.Errorf("dc_snoop_deploy.pxl: real_parent->pid must appear in BOTH probe blocks, got %d", n)
	}
	// Tracepoint fields must be a superset of the raw (non-enriched) schema columns
	// the export reads straight from the table.
	for _, c := range []string{"time_", "pid", "pid_start", "ppid", "ppid_start", "comm", "pcomm", "t", "file"} {
		if !strings.Contains(dcSnoopDeployScript, c+":") {
			t.Errorf("dc_snoop_deploy.pxl printf missing field %q", c)
		}
	}
}

// TestDcSnoopCollapseKeepsRepeats — the path-walk collapse must (a) exist and be
// keyed by basename so distinct files never merge, and (b) preserve every real
// repeat with its exact timestamp: it filters to the max-length row per micro-
// window (no count/dedup aggregation), so two identical accesses survive as two
// rows.
func TestDcSnoopCollapseKeepsRepeats(t *testing.T) {
	for _, tok := range []string{
		"px.length(df.file)", "px.replace('.*/'", "px.bin(df.time_", "'leaf'", "flen_max", "df.flen == df.flen_max",
	} {
		if !strings.Contains(dcSnoopScript, tok) {
			t.Errorf("dc_snoop.pxl collapse missing %q", tok)
		}
	}
	// Must NOT fold repeats into counts — that would drop the exact timestamps.
	if strings.Contains(dcSnoopScript, "px.count") {
		t.Errorf("dc_snoop.pxl must NOT aggregate repeats into counts (keep exact timestamps)")
	}
}
