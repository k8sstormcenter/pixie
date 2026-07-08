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

package kubescape_test

import (
	"testing"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/kubescape"
)

func obsIndex() kubescape.PIDIndex {
	return kubescape.BuildPIDIndex([]kubescape.Row{{
		K8sDetails: `{"podName":"observer-x","podNamespace":"java-poc"}`,
		ProcessDetails: `{"processTree":{"pid":25740,"comm":"sh",` +
			`"childrenMap":{"getent␟882811":{"pid":882811,"ppid":25740,"comm":"getent"}}}}`,
	}})
}

func TestEnrichRows_ReattributesEmptyPodViaPID(t *testing.T) {
	rows := []map[string]any{
		{"pid": int64(882811), "pod": ""},               // DNS pixie left blank -> observer
		{"pid": int64(4242), "pod": ""},                 // host DNS -> unresolved -> dropped
		{"pid": int64(0), "pod": "java-poc/observer-x"}, // already attributed -> kept
	}
	got := kubescape.EnrichRows(rows, obsIndex(), "java-poc", "observer-x")
	if len(got) != 2 {
		t.Fatalf("want 2 rows (reattributed + already-attributed), got %d: %v", len(got), got)
	}
	for _, r := range got {
		if r["pod"] != "java-poc/observer-x" {
			t.Errorf("row not attributed to target: %v", r)
		}
	}
}

func TestEnrichRows_HandlesNumericShapes(t *testing.T) {
	for _, pidVal := range []any{int64(882811), float64(882811), uint64(882811), "882811"} {
		rows := []map[string]any{{"pid": pidVal, "pod": ""}}
		got := kubescape.EnrichRows(rows, obsIndex(), "java-poc", "observer-x")
		if len(got) != 1 || got[0]["pod"] != "java-poc/observer-x" {
			t.Errorf("pid shape %T (%v) not resolved: %v", pidVal, pidVal, got)
		}
	}
}
