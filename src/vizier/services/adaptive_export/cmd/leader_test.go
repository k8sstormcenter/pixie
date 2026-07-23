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

package main

import "testing"

// TestLeaderNodeIsDeterministic pins the cluster-setup leader election: the
// lexicographically smallest node name, computed identically by every AE pod, so
// exactly one pod registers the cluster-scoped retention scripts + tracepoints.
// This is the guard against the DaemonSet duplicate-registration bug (N pods → N
// duplicate cron scripts → N-times-duplicated dark-table exports).
func TestLeaderNodeIsDeterministic(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		want  string
	}{
		{"two nodes — smallest wins", []string{"node-01", "cplane-01"}, "cplane-01"},
		{"order independent", []string{"cplane-01", "node-01"}, "cplane-01"},
		{"skips empty node names", []string{"node-b", "", "node-a"}, "node-a"},
		{"single pod is its own leader", []string{"only-node"}, "only-node"},
		{"no scheduled pods", []string{}, ""},
		{"all empty", []string{"", ""}, ""},
		{"duplicates collapse", []string{"n2", "n1", "n1", "n2"}, "n1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := leaderNode(c.nodes); got != c.want {
				t.Errorf("leaderNode(%v) = %q, want %q", c.nodes, got, c.want)
			}
		})
	}
}

// TestLeaderNodeElectsExactlyOne — across every pod's view of the SAME node set,
// exactly one node is the leader (the invariant that prevents duplicate setup).
func TestLeaderNodeElectsExactlyOne(t *testing.T) {
	nodes := []string{"node-03", "node-01", "node-02"}
	leader := leaderNode(nodes)
	winners := 0
	for _, myNode := range nodes {
		if myNode == leader {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one leader among %v, got %d (leader=%q)", nodes, winners, leader)
	}
}
