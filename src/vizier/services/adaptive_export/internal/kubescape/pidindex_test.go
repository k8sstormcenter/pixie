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

// Real-shape kubescape RuntimeProcessDetails: a parent (tini) with a child
// (getent — the exfil DNS resolver) in childrenMap. Both pids must resolve to
// the owning pod, which is what recovers DNS attribution pixie drops.
func TestBuildPIDIndex_ResolvesChildAndParent(t *testing.T) {
	rows := []kubescape.Row{{
		K8sDetails: `{"podName":"observer-6fbb545fdd-r4dt8","podNamespace":"java-poc"}`,
		ProcessDetails: `{"processTree":{"pid":25740,"ppid":25061,"comm":"tini",` +
			`"childrenMap":{"getent␟882811":{"pid":882811,"ppid":25740,"comm":"getent"}}}}`,
	}}
	idx := kubescape.BuildPIDIndex(rows)

	if got := idx.Resolve(882811); got != "java-poc/observer-6fbb545fdd-r4dt8" {
		t.Errorf("child getent pid 882811: got %q, want java-poc/observer-6fbb545fdd-r4dt8", got)
	}
	if got := idx.Resolve(25740); got != "java-poc/observer-6fbb545fdd-r4dt8" {
		t.Errorf("parent pid 25740: got %q, want java-poc/observer-6fbb545fdd-r4dt8", got)
	}
	if got := idx.Resolve(999999); got != "" {
		t.Errorf("unseen pid: got %q, want empty", got)
	}
}

// Host-pid events (no pod) must contribute nothing, not a "/" key.
func TestBuildPIDIndex_SkipsHostPidRows(t *testing.T) {
	rows := []kubescape.Row{{
		K8sDetails:     `{"podName":"","podNamespace":""}`,
		ProcessDetails: `{"processTree":{"pid":4242,"comm":"kubelet"}}`,
	}}
	if got := kubescape.BuildPIDIndex(rows).Resolve(4242); got != "" {
		t.Errorf("host pid must not resolve, got %q", got)
	}
}
