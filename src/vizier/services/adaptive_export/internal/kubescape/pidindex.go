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

import "encoding/json"

// PIDIndex maps a host PID to its owning "<namespace>/<pod>", reconstructed
// from the process trees kubescape records in forensic_db.kubescape_logs.
//
// Why this exists: Pixie drops pod attribution for short-lived processes —
// most importantly UDP DNS resolvers (getent/nslookup/the libc resolver),
// which are forked from a long-running container process, do one lookup, and
// exit. By the time upid_to_pod_name runs at query time the PID (and its
// cgroup) is gone, so dns_events land with an empty pod and get filtered out
// of the steered set. Kubescape, however, captured that PID — and its parent
// chain — at exec time, tagged with the owning pod. Joining pixie's ephemeral
// dns_events.pid against this index recovers the attribution with no pixie
// change. See k8sstormcenter/pixie#80.
type PIDIndex map[uint64]string

// ptNode mirrors one node of kubescape's RuntimeProcessDetails.processTree.
// childrenMap is keyed by "<comm>␟<pid>"; we only need the pids, so the
// map values recurse into the same shape.
type ptNode struct {
	PID         uint64            `json:"pid"`
	PPID        uint64            `json:"ppid"`
	ChildrenMap map[string]ptNode `json:"childrenMap"`
}

// walk records this node's pid (and its parent's, so an unseen child still
// resolves through the tree) then recurses into every child.
func (n *ptNode) walk(idx PIDIndex, podKey string) {
	if n.PID != 0 {
		idx[n.PID] = podKey
	}
	if n.PPID != 0 {
		// The parent is the long-running process that owns the pod; index it
		// too so a child pid we never see directly can be reached via ppid.
		if _, ok := idx[n.PPID]; !ok {
			idx[n.PPID] = podKey
		}
	}
	for _, c := range n.ChildrenMap {
		c := c
		c.walk(idx, podKey)
	}
}

// BuildPIDIndex folds a batch of kubescape rows into a pid -> "<ns>/<pod>"
// index. Rows without a pod (host-pid events) contribute nothing. Pure: no
// I/O, no clock, safe to call on every refresh with the recent kubescape set.
func BuildPIDIndex(rows []Row) PIDIndex {
	idx := PIDIndex{}
	for _, r := range rows {
		var k k8sDetails
		if err := json.Unmarshal([]byte(r.K8sDetails), &k); err != nil || k.PodName == "" {
			continue
		}
		podKey := k.PodNamespace + "/" + k.PodName
		var pd struct {
			ProcessTree ptNode `json:"processTree"`
		}
		if err := json.Unmarshal([]byte(r.ProcessDetails), &pd); err != nil {
			continue
		}
		pd.ProcessTree.walk(idx, podKey)
	}
	return idx
}

// Resolve returns "<namespace>/<pod>" for pid, or "" if kubescape never saw
// it (a genuine host process, or a pod kubescape does not profile).
func (idx PIDIndex) Resolve(pid uint64) string { return idx[pid] }
