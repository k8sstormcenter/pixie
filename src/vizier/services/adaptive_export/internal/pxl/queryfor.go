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
	"strconv"
	"strings"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// ErrUnknownTable is returned by QueryFor for a table not in BuiltinTables.
var ErrUnknownTable = errors.New("pxl: unknown pixie table")

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
	b.WriteString("import px\n")
	b.WriteString("df = px.DataFrame(table='" + table + "', start_time='" + relStart + "')\n")
	b.WriteString("df = df[df.time_ >= px.int64_to_time(" + strconv.FormatInt(sliceStart.UnixNano(), 10) + ")]\n")
	b.WriteString("df = df[df.time_ <  px.int64_to_time(" + strconv.FormatInt(sliceEnd.UnixNano(), 10) + ")]\n")
	b.WriteString("df.namespace = px.upid_to_namespace(df.upid)\n")
	// px.upid_to_pod_name returns "<namespace>/<pod>" (carnot:
	// metadata_ops.h UPIDToPodNameUDF::Exec → absl::Substitute("$0/$1", ns, name)),
	// not the bare pod name. Filtering against bare t.Pod would always
	// miss; build the namespaced key when we have both fields.
	b.WriteString("df.pod = px.upid_to_pod_name(df.upid)\n")
	if t.Namespace != "" {
		b.WriteString("df = df[df.namespace == '" + escapePxL(t.Namespace) + "']\n")
	}
	if t.Pod != "" {
		podKey := t.Pod
		if t.Namespace != "" {
			podKey = t.Namespace + "/" + t.Pod
		}
		b.WriteString("df = df[df.pod == '" + escapePxL(podKey) + "']\n")
	}
	b.WriteString("px.display(df, '" + table + "')\n")
	return b.String(), nil
}

var pxlEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func escapePxL(s string) string {
	return pxlEscaper.Replace(s)
}
