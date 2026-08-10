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
// identity inline (curtask->real_parent via a $tk intermediate) so every dcache
// event carries ppid/pcomm and a pid-reuse-stable start time (group_leader->
// start_time) for both the process and its parent — the process-forest edge.
//
// This is the exact structure of the last image that CAPTURED (9fd0ca430, 49132
// rows): $tk intermediate, real_parent, raw group_leader->start_time, 9 args incl
// pcomm. The ONLY change is the u64 start fields use %llu, not %lld: the 64-bit
// 'lld' verb is not handled by Pixie's tracepoint printf output parser and
// misaligns every field after it, which is why ppid parsed as 0 despite being a
// correct %d. %llu is the proven u64 verb (time_ uses it). A prior "simplify" to
// inline casts / ->parent / a /10000000 divide made the program capture ZERO rows
// (ae_reconcile read 49132 -> 0), so this test pins the capturing form.
func TestDcSnoopTracepointCapturesParent(t *testing.T) {
	for _, tok := range []string{
		"ppid:", "pcomm:", "pid_start:", "ppid_start:", "group_leader->start_time",
	} {
		if !strings.Contains(dcSnoopDeployScript, tok) {
			t.Errorf("dc_snoop_deploy.pxl missing %q — parent/identity capture incomplete", tok)
		}
	}
	// Both probe blocks (kprobe:lookup_fast + kretprobe:d_lookup) must carry the
	// parent pid via the capturing real_parent form.
	if n := strings.Count(dcSnoopDeployScript, "real_parent->pid"); n < 2 {
		t.Errorf("dc_snoop_deploy.pxl: real_parent->pid must appear in BOTH probe blocks, got %d", n)
	}
	// The %lld regression must not creep back — it is not in Pixie's tracepoint
	// printf output parser and silently zeroes every field after it (that was the
	// ppid=0 bug). The u64 start fields must use %llu.
	if strings.Contains(dcSnoopDeployScript, "%lld") {
		t.Error("dc_snoop_deploy.pxl uses percent-lld — misaligns output; use percent-llu for u64 start fields")
	}
	if strings.Count(dcSnoopDeployScript, "pid_start:%llu") < 2 || strings.Count(dcSnoopDeployScript, "ppid_start:%llu") < 2 {
		t.Error("dc_snoop_deploy.pxl: pid_start/ppid_start must use percent-llu in both probe blocks")
	}
	// A /10000000 divide inside the printf args coincided with zero capture — keep
	// the raw start_time (the proven-capturing form).
	if strings.Contains(dcSnoopDeployScript, "10000000") {
		t.Error("dc_snoop_deploy.pxl divides start_time — the proven-capturing form uses raw group_leader->start_time")
	}
	// Tracepoint fields must be a superset of the raw (non-enriched) schema columns
	// the export reads straight from the table.
	for _, c := range []string{"time_", "pid", "pid_start", "ppid", "ppid_start", "comm", "pcomm", "t", "file"} {
		if !strings.Contains(dcSnoopDeployScript, c+":") {
			t.Errorf("dc_snoop_deploy.pxl printf missing field %q", c)
		}
	}
}

// TestDcSnoopAncestryFilterJoinsParentNamespace — the export must resolve the
// parent's namespace via a process_stats join keyed on ppid, and the resolved
// parent_namespace must be a TEMP column (used only for the drop, never projected
// to the sink — else the INSERT gets an unknown column). The actual drops are
// injected from env by presets.go and asserted in presets_test.go.
func TestDcSnoopAncestryFilterJoinsParentNamespace(t *testing.T) {
	for _, tok := range []string{
		"par = px.DataFrame(table='process_stats'",
		"par.parent_namespace = par.ctx['namespace']",
		"par.ppid = px.upid_to_pid(par.upid)",
		"left_on=['ppid'], right_on=['ppid']",
		"# __DC_SNOOP_PARENT_EXCLUSION__",
	} {
		if !strings.Contains(dcSnoopScript, tok) {
			t.Errorf("dc_snoop.pxl ancestry filter missing %q", tok)
		}
	}
	// parent_namespace must NOT reach the sink — it is not a schema column.
	if proj := lastProjection(dcSnoopScript); contains(proj, "parent_namespace") {
		t.Error("parent_namespace leaked into the final projection — sink would reject the INSERT")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
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
