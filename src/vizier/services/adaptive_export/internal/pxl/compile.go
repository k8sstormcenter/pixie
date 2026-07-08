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
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CompilePassthrough returns a precompiled PxL TEMPLATE for a firehose
// (empty-Target) pull of `table` over a fixed rolling `window`. The result
// is identical to QueryFor with an empty anomaly.Target EXCEPT the two
// precise time_ bounds are left as `%d` verbs (lower, upper — both
// UnixNano), to be rendered per tick with Render / fmt.Sprintf.
//
// Why a template instead of calling QueryFor every tick:
//   - QueryFor takes `now` and derives the relative `start_time=` bound from
//     `now - sliceStart`. For passthrough that delta is ALWAYS `window`, so
//     the relative bound is constant across ticks and can be baked in once.
//   - The script body (DataFrame, upid_to_namespace/pod, display) never
//     changes, so it is compiled once at loop construction rather than
//     re-resolved on every refresh.
//
// Only the two post-filter bounds vary per tick, so the rendered string is
// byte-identical to what QueryFor would have produced for the same window —
// the precompiled path is a pure performance/structure change, not a
// behavioural one. upid→namespace/pod resolution stays in PxL (unchanged).
func CompilePassthrough(table string, window time.Duration) (string, error) {
	if !IsBuiltin(table) {
		return "", fmt.Errorf("%w: %q", ErrUnknownTable, table)
	}
	// Mirror QueryFor's pad: covers the full window plus a 30s safety
	// margin, clamped to a 30s floor.
	pad := window + 30*time.Second
	if pad < 30*time.Second {
		pad = 30 * time.Second
	}
	relStart := "-" + strconv.FormatInt(int64(pad/time.Second), 10) + "s"

	// Builtin table names never contain '%', so embedding them around the
	// two `%d` verbs is Sprintf-safe.
	var b strings.Builder
	b.WriteString(pxSetMaxRows)
	b.WriteString("import px\n")
	b.WriteString("df = px.DataFrame(table='" + table + "', start_time='" + relStart + "')\n")
	b.WriteString("df = df[df.time_ >= px.int64_to_time(%d)]\n")
	b.WriteString("df = df[df.time_ <  px.int64_to_time(%d)]\n")
	b.WriteString("df.namespace = px.upid_to_namespace(df.upid)\n")
	b.WriteString("df.pod = px.upid_to_pod_name(df.upid)\n")
	// Carry the host PID so downstream enrichment can reattribute rows from
	// short-lived processes (DNS resolvers) via kubescape's process tree —
	// keeps the firehose column shape identical to QueryFor. See kubescape.PIDIndex.
	b.WriteString("df.pid = px.upid_to_pid(df.upid)\n")
	b.WriteString("px.display(df, '" + table + "')\n")
	return b.String(), nil
}

// Render fills a CompilePassthrough template with the precise [sliceStart,
// sliceEnd) bounds for one tick.
func Render(tmpl string, sliceStart, sliceEnd time.Time) string {
	return fmt.Sprintf(tmpl, sliceStart.UnixNano(), sliceEnd.UnixNano())
}
