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

import (
	"reflect"
	"testing"
)

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
	// Pin the shape so adding a new field (even at zero value) makes
	// this test fail loudly. CR feedback: an equality-of-two-equal-
	// constructions check would pass even when a new field is added,
	// so we also assert the type's field count.
	const wantFields = 4
	if got := reflect.TypeOf(Target{}).NumField(); got != wantFields {
		t.Fatalf("Target field count = %d, want %d; decide whether the new "+
			"field belongs in the canonical hash form (update Hash + this guard)",
			got, wantFields)
	}
	a := Target{PID: 1, Comm: "x", Pod: "p", Namespace: "n"}
	if Hash(a) != Hash(Target{PID: 1, Comm: "x", Pod: "p", Namespace: "n"}) {
		t.Fatalf("Target hash leaks an unrecognised field")
	}
}

// TestHash_NoDelimiterCollision — naive ":"-joined canonical forms
// collide when input values can contain ":" or be empty. The fix is a
// length-prefixed (or otherwise delimiter-safe) encoding before hashing.
// Without that fix, the two Targets below produce the same canonical
// string and therefore the same hash.
func TestHash_NoDelimiterCollision(t *testing.T) {
	a := Target{PID: 0, Comm: "", Pod: "a:b", Namespace: ""}
	b := Target{PID: 0, Comm: "", Pod: "a", Namespace: "b:"}
	if Hash(a) == Hash(b) {
		t.Fatalf("delimiter collision: %+v and %+v hash to the same value (%s)",
			a, b, Hash(a))
	}
	c := Target{PID: 0, Comm: "x:y", Pod: "", Namespace: ""}
	d := Target{PID: 0, Comm: "x", Pod: "y:", Namespace: ""}
	if Hash(c) == Hash(d) {
		t.Fatalf("delimiter collision: %+v and %+v hash to the same value (%s)",
			c, d, Hash(c))
	}
}
