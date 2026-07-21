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
	b.WriteString("df = px.DataFrame(table='" + table + "', start_time='" + relStart + "')\n")
	b.WriteString("df = df[df.time_ >= px.int64_to_time(" + strconv.FormatInt(sliceStart.UnixNano(), 10) + ")]\n")
	b.WriteString("df = df[df.time_ <  px.int64_to_time(" + strconv.FormatInt(sliceEnd.UnixNano(), 10) + ")]\n")
	// Native tables: px.upid_to_pod_name returns "<namespace>/<pod>" (carnot:
	// metadata_ops.h UPIDToPodNameUDF::Exec → absl::Substitute("$0/$1", ns, name)),
	// not the bare pod name. Dark-vector tracepoint tables (pid-keyed) resolve pod
	// via a process_stats pid-merge instead and yield a BARE pod name (dx#126).
	b.WriteString(PodEnrichPxL(table))
	if t.Namespace != "" {
		b.WriteString("df = df[df.namespace == '" + escapePxL(t.Namespace) + "']\n")
	}
	if t.Pod != "" {
		if IsDarkVector(table) {
			// proc.ctx['pod'] yields the NAMESPACED pod name (ns/pod) on Pixie
			// v0.14.20+ (verified live, rig 6a5f6bc0: df.pod=='ns/pod' matches,
			// bare pod matches 0), NOT the bare name — so match the namespaced key
			// exactly as the native branch does.
			if t.Namespace != "" {
				b.WriteString("df = df[df.pod == '" + escapePxL(t.Namespace+"/"+t.Pod) + "']\n")
			} else {
				b.WriteString("df = df[df.pod == '" + escapePxL(t.Pod) + "']\n")
			}
		} else if t.Namespace != "" {
			// Both fields present — use exact equality on the namespaced key.
			b.WriteString("df = df[df.pod == '" + escapePxL(t.Namespace+"/"+t.Pod) + "']\n")
		} else {
			// Pod-only fallback: df.pod is "<ns>/<pod>", so a bare-pod
			// equality always misses. Regex-anchor "<any-ns>/<pod>" via
			// px.regex_match so the defensive path stays functional.
			b.WriteString("df = df[px.regex_match('^[^/]+/" + escapePxL(regexp.QuoteMeta(t.Pod)) + "$', df.pod)]\n")
		}
	}
	b.WriteString("px.display(df, '" + table + "')\n")
	return b.String(), nil
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
