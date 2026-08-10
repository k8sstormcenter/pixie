// Copyright 2018- The Pixie Authors.
// SPDX-License-Identifier: Apache-2.0

package script

import (
	"sort"
	"strings"
	"testing"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
)

// TestProcessTreeExportColumnsMatchSchema — the strict-sink contract: process_tree.pxl
// must project EXACTLY the forensic_db.process_tree schema columns (+ event_time added
// via df.event_time = df.time_). A mismatch = the sink sends an unknown/missing column
// and the INSERT fails.
func TestProcessTreeExportColumnsMatchSchema(t *testing.T) {
	proj := lastProjection(processTreeScript)
	if len(proj) == 0 {
		t.Fatal("no df[[...]] projection found in process_tree.pxl")
	}
	got := append(append([]string{}, proj...), "event_time")
	want, err := clickhouse.Columns("process_tree")
	if err != nil {
		t.Fatalf("Columns(process_tree): %v", err)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("process_tree export columns != schema columns\n export=%v\n schema=%v", got, want)
	}
}

// TestProcExecCaptureFormMatchesDcSnoop — proc_exec MUST use the SAME verified capture
// form as dc_snoop so process_tree.pid_start/ppid_start equal dc_snoop.pid_start/
// ppid_start, making the forest join exact: %llu (not the signed 64-bit verb),
// curtask->real_parent, RAW group_leader->start_time (NO /10000000 divide). Any of
// those diverging silently breaks the join (evidence would never match the forest).
func TestProcExecCaptureFormMatchesDcSnoop(t *testing.T) {
	s := procExecDeployScript
	if strings.Contains(s, "%lld") {
		t.Error("proc_exec uses percent-lld — must use percent-llu (matches dc_snoop; the signed verb misaligns)")
	}
	if strings.Contains(s, "10000000") {
		t.Error("proc_exec divides start_time — dc_snoop uses RAW group_leader->start_time; the divide breaks the join")
	}
	if strings.Count(s, "real_parent->pid") < 1 {
		t.Error("proc_exec must capture the parent via curtask->real_parent (the dc_snoop-verified form)")
	}
	for _, tok := range []string{
		"tracepoint:syscalls:sys_enter_exec", "pid_start:%llu", "ppid_start:%llu",
		"group_leader->start_time", "exe:%s", "args->filename",
	} {
		if !strings.Contains(s, tok) {
			t.Errorf("proc_exec missing %q", tok)
		}
	}
	// printf must carry each raw identity field the export/schema reads.
	for _, c := range []string{"time_", "pid", "pid_start", "ppid", "ppid_start", "exe"} {
		if !strings.Contains(s, c+":") {
			t.Errorf("proc_exec printf missing field %q", c)
		}
	}
	// Budget guard: the exec-tracepoint printf must stay within 8 conversion specifiers.
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "printf(\"time_:") {
			if n := strings.Count(line, "%"); n > 8 {
				t.Errorf("proc_exec printf has %d args (>8 over budget): %s", n, strings.TrimSpace(line))
			}
		}
	}
}

// TestProcessTreeRegistered — proc_exec must be an AE-deployed tracepoint and
// ch-process_tree a registered retention export, else the table is never populated.
func TestProcessTreeRegistered(t *testing.T) {
	var hasTP bool
	for _, tp := range DesiredTracepoints() {
		if tp.Name == "proc_exec" && tp.Table == "proc_exec" {
			hasTP = true
		}
	}
	if !hasTP {
		t.Error("proc_exec not in DesiredTracepoints — the AE will not deploy it")
	}
	var hasExport bool
	for _, p := range DarkVectorPresets() {
		if p.Name == "ch-process_tree" {
			hasExport = true
			if !strings.Contains(p.Script, "px.export") || !strings.Contains(p.Script, "process_tree") {
				t.Error("ch-process_tree preset does not export to process_tree")
			}
		}
	}
	if !hasExport {
		t.Error("ch-process_tree not in DarkVectorPresets — process_tree is never written")
	}
}

// TestProcessTreeResolvesPodViaProcessStats — the runc-attribution mechanism: the
// export MUST merge process_stats (Pixie's cgroup->pod resolution) so a process
// spawned via runc into a pod gets its OWN pod, not the host runc parent's.
func TestProcessTreeResolvesPodViaProcessStats(t *testing.T) {
	for _, tok := range []string{
		"px.DataFrame(table='process_stats'",
		"proc.pod = proc.ctx['pod']",
		"left_on=['pid'], right_on=['pid']",
	} {
		if !strings.Contains(processTreeScript, tok) {
			t.Errorf("process_tree.pxl missing pod-resolution step %q", tok)
		}
	}
}
