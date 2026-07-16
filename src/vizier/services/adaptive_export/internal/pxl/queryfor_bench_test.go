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
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// pxl.QueryFor sits on the controller fan-out path: ONE QueryFor call
// per (anomaly_hash, table) tuple per pass. With 11 PushPixieTables and
// N active anomaly windows, the per-pass cost is 11×N QueryFor calls
// (plus 11×N broker queries that the QueryFor strings parameterise).
//
// At sustained 100 active anomalies → 1100 QueryFor/sec. Allocation
// behaviour of fmt.Sprintf-style string builders is what the bench
// quantifies — informs whether sync.Pool'd strings.Builder would pay
// off if QueryFor turns up in CPU profiles.

func BenchmarkQueryFor_http_events(b *testing.B) {
	t := anomaly.Target{
		PID:       12345,
		Comm:      "java",
		Pod:       "backend-vulnerable-779cd9d765-mxr8t",
		Namespace: "svc-poc",
	}
	now := time.Now()
	start := now.Add(-30 * time.Second)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = QueryFor("http_events", t, start, now, now)
	}
}

// BenchmarkQueryFor_AllTables varies the table across all 13 BuiltinTables
// to ensure we're not missing a slow-path on a specific table.
func BenchmarkQueryFor_AllTables(b *testing.B) {
	t := anomaly.Target{
		PID:       12345,
		Comm:      "java",
		Pod:       "backend-vulnerable-779cd9d765-mxr8t",
		Namespace: "svc-poc",
	}
	now := time.Now()
	start := now.Add(-30 * time.Second)
	tables := Names(Builtins())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = QueryFor(tables[i%len(tables)], t, start, now, now)
	}
}
