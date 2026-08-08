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
	// Bound the source scan on both sides; the df.time_ < sliceEnd filter trims the exact upper bound.
	dfArgs := "table='" + pixieSourceFor(table) + "', start_time='" + relStart + "'"
	if relEnd := relEndBound(now, sliceEnd); relEnd != "" {
		dfArgs += ", end_time='" + relEnd + "'"
	}
	b.WriteString("df = px.DataFrame(" + dfArgs + ")\n")
	b.WriteString("df = df[df.time_ >= px.int64_to_time(" + strconv.FormatInt(sliceStart.UnixNano(), 10) + ")]\n")
	b.WriteString("df = df[df.time_ <  px.int64_to_time(" + strconv.FormatInt(sliceEnd.UnixNano(), 10) + ")]\n")
	// px.upid_to_pod_name yields "<namespace>/<pod>"; dark-vector tables resolve pod via a pid-merge (bare name).
	if table == "stack_trace" {
		// Native profiler (stack_traces.beta): resolve pod/ns/container from ctx, stamp event_time.
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
		// Node-scoped (transient attack pids resolve blank ns, so no pod filter).
		// Noise is cut structurally, NOT by a hardcoded comm blocklist (deleted):
		//   1. child-namespace exclusion drops resolved own-stack processes (vizier,
		//      pem, node-agent, … resolve to pl/honey/clickhouse via their pod);
		//   2. parent-ancestry exclusion (ppid -> parent namespace) drops the
		//      transient children of own-stack pods that resolve blank themselves.
		// Host-level runtime + kernel threads (containerd-shim/systemd/k3s/kworker)
		// have a blank namespace AND a blank/kernel-thread parent, so 1-level ancestry
		// cannot resolve them — they enter the always-on retention look-back and are
		// relevance-filtered by dx's multi-level process forest, not here.
		b.WriteString(PodEnrichPxL(table))
		b.WriteString(darkNamespaceExclusion())
		b.WriteString(darkParentAncestryExclusion(table))
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

// relEndBound returns a relative end_time ("-<n>s"), or "" when sliceEnd is at/after now.
func relEndBound(now, sliceEnd time.Time) string {
	gap := now.Sub(sliceEnd)
	if gap < time.Second {
		return "" // at/after now → default end_time (scan to now)
	}
	return "-" + strconv.FormatInt(int64(gap/time.Second), 10) + "s"
}

// pixieSourceFor maps a CH table to the pixie table it's read from (stack_trace ← stack_traces.beta).
func pixieSourceFor(table string) string {
	if table == "stack_trace" {
		return "stack_traces.beta"
	}
	return table
}

// Dark-vector tables that carry a ppid column (so the parent-ancestry exclusion
// applies). Only dc_snoop captures ppid today; creds_change and the dx_* bpftrace
// tables do not, so they get the child-namespace exclusion only.
var darkVectorHasPpid = map[string]bool{
	"dc_snoop": true,
}

// Infra namespaces dropped from the node-scoped dark capture — applied BOTH to the
// row's own namespace (resolved own-stack processes) and, for tables with ppid, to
// the parent's namespace (ancestry). Blank-namespace rows survive (transient attack
// children; host/kernel processes). Override via DC_SNOOP_EXCLUDE_NAMESPACES.
var darkExcludeNamespacesDefault = []string{
	"pl", "honey", "px-operator", "olm", "clickhouse", "socdemo", "socdemo-ch",
	"kube-system", "kube-public", "kube-node-lease", "local-path-storage",
}

// darkExcludeNamespaces resolves the effective infra-namespace list (env override
// or default), shared by the child-namespace and parent-ancestry exclusions so the
// two can never drift.
func darkExcludeNamespaces() []string {
	if v := strings.TrimSpace(os.Getenv("DC_SNOOP_EXCLUDE_NAMESPACES")); v != "" {
		var nss []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				nss = append(nss, s)
			}
		}
		return nss
	}
	return darkExcludeNamespacesDefault
}

func darkNamespaceExclusion() string {
	var b strings.Builder
	for _, ns := range darkExcludeNamespaces() {
		b.WriteString("df = df[df.namespace != '" + escapePxL(ns) + "']\n")
	}
	return b.String()
}

// darkParentAncestryExclusion resolves each row's PARENT namespace via a
// process_stats join on ppid and drops rows whose parent lives in an own-stack
// namespace — even when the row's own pod is blank (a transient process exec'd by
// an infra pod). This is the ppid-based ancestry cut that REPLACES the deleted
// hardcoded comm blocklist: pod/namespace-rooted, not comm-matched. parent_namespace
// is dropped before display so the sink projection is unchanged. Emits nothing for
// tables without a ppid column. Mirrors the retention path (dc_snoop.pxl).
func darkParentAncestryExclusion(table string) string {
	if !darkVectorHasPpid[table] {
		return ""
	}
	var b strings.Builder
	b.WriteString("par = px.DataFrame(table='process_stats', start_time='" + darkProcStatsWindow + "')\n")
	b.WriteString("par.parent_namespace = par.ctx['namespace']\n")
	b.WriteString("par.ppid = px.upid_to_pid(par.upid)\n")
	b.WriteString("par = par.groupby(['parent_namespace', 'ppid']).agg()\n")
	b.WriteString("df = df.merge(par, how='left', left_on=['ppid'], right_on=['ppid'], suffixes=['', '_par'])\n")
	for _, ns := range darkExcludeNamespaces() {
		b.WriteString("df = df[df.parent_namespace != '" + escapePxL(ns) + "']\n")
	}
	b.WriteString("df = df.drop(['parent_namespace'])\n")
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
