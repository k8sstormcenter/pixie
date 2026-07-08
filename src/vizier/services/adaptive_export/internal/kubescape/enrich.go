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

// Package kubescape parses the Kubescape-shaped fields of a
// forensic_db.kubescape_logs row into the source-agnostic types used
// downstream:
//   - anomaly.Target — workload identity (used to compute the hash)
//   - Event          — Target plus event-specific fields (event_time,
//     rule id, hostname) needed for window math + persistence

package kubescape

import (
	"encoding/json"
	"strconv"
	"strings"
)

// asUint64 coerces the many numeric shapes a pixie/ClickHouse row column can
// carry (the querier returns map[string]any) into a PID. Unknown shapes -> 0.
func asUint64(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case int64:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case int:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case float64:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case json.Number:
		if p, err := n.Int64(); err == nil && p >= 0 {
			return uint64(p)
		}
	case string:
		if p, err := strconv.ParseUint(n, 10, 64); err == nil {
			return p
		}
	}
	return 0
}

// EnrichRows reattributes pixie rows that pixie left with an empty "pod" by
// looking up their "pid" in the process-tree index, then keeps only rows that
// belong to the target ("<namespace>/<pod>"). Rows pixie already attributed are
// kept unchanged. Rows still unresolved after the index (genuine host DNS,
// pods kubescape does not profile) are NOT evidence for this anomaly's pod and
// are dropped — preserving the operator's active-set discipline.
//
// When targetPod is empty the target filter is skipped (attribute-only mode).
// The input slice is filtered in place; the returned slice aliases it.
func EnrichRows(rows []map[string]any, idx PIDIndex, targetNS, targetPod string) []map[string]any {
	want := targetNS + "/" + targetPod
	out := rows[:0]
	for _, row := range rows {
		pod, _ := row["pod"].(string)
		if pod == "" {
			if pid := asUint64(row["pid"]); pid != 0 {
				if k := idx.Resolve(pid); k != "" {
					pod = k
					row["pod"] = k
					if i := strings.IndexByte(k, '/'); i >= 0 {
						row["namespace"] = k[:i]
					}
				}
			}
		}
		if targetPod == "" || pod == want {
			out = append(out, row)
		}
	}
	return out
}
