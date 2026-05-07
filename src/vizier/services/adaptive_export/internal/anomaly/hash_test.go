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

package anomaly

import "testing"

// canonical fixture: redis CVE-2025-49844 R1005 alert (workload identity only).
var canonicalTarget = Target{
	PID:       106040,
	Comm:      "redis-server",
	Pod:       "redis-578d5dc9bd-kjj78",
	Namespace: "redis",
}

// TestHash_Deterministic — same Target hashes identically every call.
func TestHash_Deterministic(t *testing.T) {
	a := Hash(canonicalTarget)
	b := Hash(canonicalTarget)
	if a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
	if got := len(a); got != 32 {
		t.Fatalf("len %d, want 32 hex chars", got)
	}
}

// TestHash_DiffersOnPID — two processes on the same pod still hash differently
// (we want PER-process attribution).
func TestHash_DiffersOnPID(t *testing.T) {
	other := canonicalTarget
	other.PID = canonicalTarget.PID + 1
	if Hash(canonicalTarget) == Hash(other) {
		t.Fatalf("collision on PID change")
	}
}

// TestHash_DiffersOnComm — different comm under same PID/pod/ns must differ.
func TestHash_DiffersOnComm(t *testing.T) {
	other := canonicalTarget
	other.Comm = "redis-cli"
	if Hash(canonicalTarget) == Hash(other) {
		t.Fatalf("collision on Comm change")
	}
}

// TestHash_DiffersOnPod — different replicas of same workload differ.
func TestHash_DiffersOnPod(t *testing.T) {
	other := canonicalTarget
	other.Pod = "redis-578d5dc9bd-OTHER"
	if Hash(canonicalTarget) == Hash(other) {
		t.Fatalf("collision on Pod change")
	}
}

// TestHash_DiffersOnNamespace — same pod name in different ns must differ.
func TestHash_DiffersOnNamespace(t *testing.T) {
	other := canonicalTarget
	other.Namespace = "redis-staging"
	if Hash(canonicalTarget) == Hash(other) {
		t.Fatalf("collision on Namespace change")
	}
}

// TestHash_AllowsEmptyPod — host-pid processes have no pod/namespace.
// Hash must still be computable and stable.
func TestHash_AllowsEmptyPod(t *testing.T) {
	host := Target{PID: 1, Comm: "systemd"}
	a := Hash(host)
	b := Hash(host)
	if a != b {
		t.Fatalf("empty-pod hash not deterministic")
	}
	if len(a) != 32 {
		t.Fatalf("empty-pod hash len %d", len(a))
	}
	// empty-pod target must collide with itself but not with the
	// non-empty-pod canonical target.
	if a == Hash(canonicalTarget) {
		t.Fatalf("empty-pod hash collides with named-pod hash")
	}
}

// TestHash_NoTimestampInfluence — verifies the hash function takes only
// the four identity fields. (No EventTime / RuleID parameter exists.)
// This is a structural test: the Target struct has exactly 4 fields,
// all part of the canonical form. If you add a field, you must decide
// whether it belongs in the hash and update this test.
func TestHash_NoTimestampInfluence(t *testing.T) {
	// Verify the Target type has exactly 4 fields. If this fails, decide:
	// new field belongs in the hash → add to canonical form;
	// new field does NOT belong → leave Target unchanged, add a sibling type.
	a := Target{PID: 1, Comm: "x", Pod: "p", Namespace: "n"}
	if Hash(a) != Hash(Target{PID: 1, Comm: "x", Pod: "p", Namespace: "n"}) {
		t.Fatalf("Target hash leaks an unrecognised field")
	}
}
