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
// fails. This is the coupling that adding ppid/pid_start/ppid_start could have
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
// identity inline (curtask->parent) so every dcache event carries ppid and a
// pid-reuse-stable start time (group_leader->start_time) for both the process and
// its parent — the process-forest edge. Uses the exec_snoop-proven form: ->parent
// (not ->real_parent) and %d ints (%lld is not in Pixie's tracepoint printf subset;
// it misaligned every field after it, which is why ppid parsed as 0).
//
// CRITICAL: the printf must stay within the 8-argument budget (5 numeric + 3
// strings), the same profile as the proven exec_snoop tracepoint. A 9th arg makes
// bpftrace reject the program ("printf: Too many arguments for format string"), the
// tracepoint flaps FAILED<->RUNNING, and dc_snoop captures ZERO rows — which is
// exactly what a captured parent comm (%s) cost us. So this asserts the arg budget.
func TestDcSnoopTracepointCapturesParent(t *testing.T) {
	for _, tok := range []string{
		"ppid:", "pid_start:", "ppid_start:", "group_leader->start_time",
	} {
		if !strings.Contains(dcSnoopDeployScript, tok) {
			t.Errorf("dc_snoop_deploy.pxl missing %q — parent/identity capture incomplete", tok)
		}
	}
	// Both probe blocks (kprobe:lookup_fast + kretprobe:d_lookup) must carry the
	// parent pid via curtask->parent (the proven form; ->real_parent + %lld was the
	// field-misalignment bug that captured ppid=0).
	if n := strings.Count(dcSnoopDeployScript, "->parent->pid"); n < 2 {
		t.Errorf("dc_snoop_deploy.pxl: ->parent->pid must appear in BOTH probe blocks, got %d", n)
	}
	// The %lld regression must not creep back — it silently zeroes every field after
	// the first %lld.
	if strings.Contains(dcSnoopDeployScript, "%lld") {
		t.Error("dc_snoop_deploy.pxl uses percent-lld — not in Pixie's tracepoint printf subset; use percent-d")
	}
	// Parent comm must NOT be captured — it was the 9th arg that broke the program.
	if strings.Contains(dcSnoopDeployScript, "parent->comm") || strings.Contains(dcSnoopDeployScript, "pcomm:") {
		t.Error("dc_snoop_deploy.pxl captures parent comm — that 9th printf arg exceeds bpftrace's budget and zeroes capture")
	}
	// Enforce the 8-arg printf budget: each probe's printf format must have at most
	// 8 conversion specifiers. A 9th (or more) is rejected by bpftrace at compile.
	for _, line := range strings.Split(dcSnoopDeployScript, "\n") {
		if !strings.Contains(line, "printf(\"time_:") {
			continue
		}
		if n := strings.Count(line, "%"); n > 8 {
			t.Errorf("dc_snoop_deploy.pxl printf has %d args (>8, over bpftrace's budget): %s", n, strings.TrimSpace(line))
		}
	}
	// Tracepoint fields must be a superset of the raw (non-enriched) schema columns
	// the export reads straight from the table (no parent comm).
	for _, c := range []string{"time_", "pid", "pid_start", "ppid", "ppid_start", "comm", "t", "file"} {
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
