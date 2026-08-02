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

package pxl

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// ErrUnknownTable is returned by QueryFor for a table not in BuiltinTables.
var ErrUnknownTable = errors.New("pxl: unknown pixie table")

// pxSetMaxRows raises Pixie's per-table result cap via the query-broker's
// own `#px:set` query flag (parsed from the script — see
// src/vizier/services/query_broker/controllers/query_flags.go, default
// max_output_rows_per_table = 10000). Without it the planner's
// add_limit_to_batch_result_sink_rule silently truncates any px.display to
// 10000 rows, so a wide firehose window (or a very busy pod) loses the
// excess at the read. 1e6 is far above any realistic AE window. See
// memory project-ae-passthrough-10k-cap.
const pxSetMaxRows = "#px:set max_output_rows_per_table=1000000\n"

// QueryFor returns a PxL script that selects rows from `table` for the
// (namespace, pod) of `t`, time-bounded to [sliceStart, sliceEnd). The
// `now` argument lets us compute a relative `start_time=` for
// px.DataFrame (PxL rejects ISO-string absolute bounds; we use a
// generously-padded relative bound and post-filter precisely with
// px.int64_to_time on the time_ column).
func QueryFor(table string, t anomaly.Target, sliceStart, sliceEnd, now time.Time) (string, error) {
	if !IsBuiltin(table) {
		return "", fmt.Errorf("%w: %q", ErrUnknownTable, table)
	}
	// pad covers (now - sliceStart) plus a 30s safety margin. When
	// sliceStart is in the future (caller bug), now.Sub is negative and
	// we'd ask pixie for a positive-only relative start; clamp to 30s.
	pad := now.Sub(sliceStart) + 30*time.Second
	if pad < 30*time.Second {
		pad = 30 * time.Second
	}
	relStart := "-" + strconv.FormatInt(int64(pad/time.Second), 10) + "s"

	var b strings.Builder
	b.WriteString(pxSetMaxRows)
	b.WriteString("import px\n")
	// Bound the PEM source scan on BOTH sides. Without an end_time the planner
	// scans [sliceStart, now] for EVERY query, so an old ordered window (e.g. the
	// 600s control lookback) re-materializes the whole span on the node-local PEM;
	// under the OrderExportAll fan-out the heavy tables (dc_snoop) then blow the
	// per-query deadline and drop out (the flaky-capture RCA). relEndBound caps the
	// scan at ~sliceEnd; the exact upper bound is still trimmed by the df.time_ <
	// sliceEnd nanos filter below, so nothing real is clipped.
	dfArgs := "table='" + pixieSourceFor(table) + "', start_time='" + relStart + "'"
	if relEnd := relEndBound(now, sliceEnd); relEnd != "" {
		dfArgs += ", end_time='" + relEnd + "'"
	}
	b.WriteString("df = px.DataFrame(" + dfArgs + ")\n")
	b.WriteString("df = df[df.time_ >= px.int64_to_time(" + strconv.FormatInt(sliceStart.UnixNano(), 10) + ")]\n")
	b.WriteString("df = df[df.time_ <  px.int64_to_time(" + strconv.FormatInt(sliceEnd.UnixNano(), 10) + ")]\n")
	// Native tables: px.upid_to_pod_name returns "<namespace>/<pod>" (carnot:
	// metadata_ops.h UPIDToPodNameUDF::Exec → absl::Substitute("$0/$1", ns, name)),
	// not the bare pod name. Dark-vector tracepoint tables (pid-keyed) resolve pod
	// via a process_stats pid-merge instead and yield a BARE pod name (dx#126).
	if table == "stack_trace" {
		// stack_trace is the CANONICAL native continuous profiler (stack_traces.beta,
		// upid-keyed — NOT a pid tracepoint, so NOT a dark-vector pid-merge). Resolve
		// pod/namespace/container/hostname exactly like the export preset
		// (script/presets/stack_trace.pxl) and stamp event_time = time_ so the CH
		// stack_trace row is complete. df.ctx['pod'] is the NAMESPACED "<ns>/<pod>"
		// key (verified live), so the pod filter is namespaced — same as the native
		// upid_to_pod_name path below.
		b.WriteString("df.namespace = df.ctx['namespace']\n")
		b.WriteString("df.pod = df.ctx['pod']\n")
		b.WriteString("df.container = df.ctx['container']\n")
		b.WriteString("df.hostname = px.upid_to_node_name(df.upid)\n")
		b.WriteString("df.event_time = df.time_\n")
		if t.Namespace != "" {
			b.WriteString("df = df[df.namespace == '" + escapePxL(t.Namespace) + "']\n")
		}
		if t.Pod != "" {
			if t.Namespace != "" {
				b.WriteString("df = df[df.pod == '" + escapePxL(t.Namespace+"/"+t.Pod) + "']\n")
			} else {
				b.WriteString("df = df[px.regex_match('^[^/]+/" + escapePxL(regexp.QuoteMeta(t.Pod)) + "$', df.pod)]\n")
			}
		}
	} else if IsDarkVector(table) {
		// Dark-vector tracepoints emit a RAW kernel pid. The malignant transient
		// pids an incident actually produces — an attack's whoami/cat/getent
		// children — are too short-lived to land in process_stats, so their
		// pod/namespace resolves BLANK; a pod (or even namespace) filter drops
		// exactly the evidence, which is why the dark tables came back empty.
		// The AE is node-local (pem-direct → the node's own PEM), so the query is
		// already scoped to the alert's node.
		//
		// ORDER MATTERS: drop the infra/self comms FIRST (env-driven, no recompile),
		// THEN do the process_stats pid-merge. The node's dark stream is huge
		// (Formatter/vector/runc/... thousands of rows per window); merging every
		// one against process_stats is the query that timed out and silently
		// dropped dc_snoop. Filtering comm first shrinks the merge to the handful
		// of workload rows (bash/redis/whoami/cat), so the dark capture completes.
		b.WriteString(darkCommExclusion(table))
		b.WriteString(PodEnrichPxL(table))
		// AFTER the pid-merge resolves df.namespace: drop infra/system namespaces
		// (blank-namespace transient workload rows are KEPT — see darkCommExclusion
		// note; the attack's short-lived children resolve blank). This mirrors the
		// shipped cron preset (script/presets dc_snoop.pxl __DC_SNOOP_EXCLUSION__);
		// the OrderExportAll path was missing it, so infra pods' dcache churn
		// (ConfigReloader/iptables/CNI/host daemons) flooded every dc_snoop capture.
		b.WriteString(darkNamespaceExclusion())
	} else {
		b.WriteString(PodEnrichPxL(table))
		if t.Namespace != "" {
			b.WriteString("df = df[df.namespace == '" + escapePxL(t.Namespace) + "']\n")
		}
		if t.Pod != "" {
			if t.Namespace != "" {
				// upid_to_pod_name is "<ns>/<pod>" — exact equality on the namespaced key.
				b.WriteString("df = df[df.pod == '" + escapePxL(t.Namespace+"/"+t.Pod) + "']\n")
			} else {
				// Pod-only fallback: df.pod is "<ns>/<pod>", so a bare-pod
				// equality always misses. Regex-anchor "<any-ns>/<pod>".
				b.WriteString("df = df[px.regex_match('^[^/]+/" + escapePxL(regexp.QuoteMeta(t.Pod)) + "$', df.pod)]\n")
			}
		}
	}
	b.WriteString("px.display(df, '" + table + "')\n")
	return b.String(), nil
}

// relEndBound returns a RELATIVE end_time ("-<n>s") that caps the PEM's source
// scan at ~sliceEnd, or "" when sliceEnd is at/after now (scan to the live edge).
// The gap is floored to whole seconds so the source window ends slightly LATER
// than sliceEnd and never clips real rows — the precise upper bound is enforced by
// the df.time_ < sliceEnd nanos post-filter. This is the load lever behind the
// chunked ordered path: each chunk materializes only its own span instead of
// [chunkStart, now].
func relEndBound(now, sliceEnd time.Time) string {
	gap := now.Sub(sliceEnd)
	if gap < time.Second {
		return "" // at/after now → default end_time (scan to now)
	}
	return "-" + strconv.FormatInt(int64(gap/time.Second), 10) + "s"
}

// pixieSourceFor returns the Pixie table a builtin is sourced FROM when it
// differs from the ClickHouse table it is written TO. stack_trace is written to
// CH as 'stack_trace' but sourced from the CANONICAL native continuous profiler
// 'stack_traces.beta' — the always-on Pixie profiler, NOT an AE-invented table.
// (Dotted-name DataFrames compile fine in a direct query; verified live.)
func pixieSourceFor(table string) string {
	if table == "stack_trace" {
		return "stack_traces.beta"
	}
	return table
}

// darkVectorHasComm lists the dark-vector tables that carry a `comm` column, so
// the infra-comm exclusion only emits for those (stack_trace is upid-only).
var darkVectorHasComm = map[string]bool{
	"dc_snoop": true, "creds_change": true, "dx_vfs_events": true,
	"dx_unlink": true, "dx_dlookup": true, "dx_mprotect": true,
	"dx_bpf": true, "dx_ptrace": true,
}

// darkExcludeCommsDefault is the node's own infra/self comms dropped from the
// node-scoped dark capture so the workload's activity stands out. Overridable at
// runtime via DC_SNOOP_EXCLUDE_COMMS (csv) — a process can be added without a
// recompile. Kept in sync with script.presets defaultExcludeComms.
var darkExcludeCommsDefault = []string{
	"pem", "kelvin", "containerd", "containerd-shim", "runc", "node-agent",
	"runc:[2:INIT]", "runc:[1:CHILD]",
	"vizier-query-broker", "vizier-metadata", "nats-server", "k3s-server",
	"k3s-agent", "systemd", "systemd-journal", "SystemLogFlush", "kubelet",
	"AsyncInsertQ", "BgSchPool", "Collector", "AsyncMetrics", "MergeMutate",
	"MergeTreeIndex", "CgrpMemUsgObsr", "coredns", "metadata", "storage",
	"operator", "iptables", "iptables-save", "iptables-restor", "ip6tables",
	"ConfigReloader", "clickhouse-oper", "Formatter", "(setup.sh)", "cmd",
	"vector-worker", "metrics-server", "local-path-prov", "portmap",
	"(udev-worker)", "systemd-resolve", "systemd-timesyn",
	// host/CNI/node daemons that flood dc_snoop with dcache churn but carry no
	// workload forensic value (observed leaking on a real k3s node, aeprod54).
	"systemd-udevd", "systemd-sysctl", "host-local", "bridge", "flannel",
	"loopback", "bandwidth", "dbus-daemon", "mount", "umount", "tailscaled",
	"grpc_health_pro", "kubevuln", "opm", "(spawn)", "kube-proxy",
}

// darkExcludeNamespacesDefault drops infra/system namespaces from the node-scoped
// dark capture (blank-namespace transient workload rows are KEPT — the attack's
// short-lived children resolve blank, so a namespace filter must never drop them).
// Overridable via DC_SNOOP_EXCLUDE_NAMESPACES (csv). Kept in sync with
// script/presets.go defaultExcludeNamespaces — the shipped cron path already
// filtered these; the dx-steered OrderExportAll path did not, so infra pods'
// process churn flooded every capture.
var darkExcludeNamespacesDefault = []string{
	"pl", "honey", "px-operator", "olm", "clickhouse", "socdemo", "socdemo-ch",
	"kube-system", "kube-public", "kube-node-lease", "local-path-storage",
}

// darkCommExclusion builds the infra-comm drop filter for a dark-vector table
// that has a comm column. Returns "" for comm-less tables (stack_trace).
func darkCommExclusion(table string) string {
	if !darkVectorHasComm[table] {
		return ""
	}
	comms := darkExcludeCommsDefault
	if v := strings.TrimSpace(os.Getenv("DC_SNOOP_EXCLUDE_COMMS")); v != "" {
		comms = nil
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				comms = append(comms, s)
			}
		}
	}
	var b strings.Builder
	for _, c := range comms {
		b.WriteString("df = df[df.comm != '" + escapePxL(c) + "']\n")
	}
	return b.String()
}

// darkNamespaceExclusion builds the infra-namespace drop filter for the node-scoped
// dark capture. Emitted AFTER PodEnrichPxL resolves df.namespace. Blank-namespace
// rows survive (each `!=` predicate is true for ”), so transient attack children
// are never dropped. Overridable via DC_SNOOP_EXCLUDE_NAMESPACES (csv).
func darkNamespaceExclusion() string {
	nss := darkExcludeNamespacesDefault
	if v := strings.TrimSpace(os.Getenv("DC_SNOOP_EXCLUDE_NAMESPACES")); v != "" {
		nss = nil
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				nss = append(nss, s)
			}
		}
	}
	var b strings.Builder
	for _, ns := range nss {
		b.WriteString("df = df[df.namespace != '" + escapePxL(ns) + "']\n")
	}
	return b.String()
}

// pxlEscaper turns raw bytes that could break out of a PxL single-quoted
// string into their Python-style escape sequences. The backslash MUST be
// mapped FIRST so its own substitution doesn't get double-escaped when
// processed alongside the rest.
//
// Why each entry: PxL is Python; a single-quoted literal closes on a bare
// ' and a raw newline (0x0A) terminates the statement, letting an
// adversary-controlled Target.Pod/Target.Namespace value inject a new
// PxL statement after the close. ', \r, \n, \t, and NUL are the
// byte-level shapes that can break the string boundary; everything
// else is opaque to the PxL parser inside a string literal.
var pxlEscaper = strings.NewReplacer(
	`\`, `\\`,
	`'`, `\'`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
	"\x00", `\0`,
)

func escapePxL(s string) string {
	return pxlEscaper.Replace(s)
}
