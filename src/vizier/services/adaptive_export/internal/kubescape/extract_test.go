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

package kubescape

import (
	"errors"
	"testing"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

const canonicalK8sDetails = `{"clusterName":"bobexample","containerName":"redis","namespace":"redis","podName":"redis-578d5dc9bd-kjj78","podNamespace":"redis","workloadName":"redis","workloadKind":"Deployment"}`

const canonicalProcessDetails = `{"processTree":{"pid":106040,"cmdline":"redis-server 0.0.0.0:6379","comm":"redis-server","ppid":105965,"uid":999}}`

func canonicalRow() Row {
	return Row{
		EventTime:      1744477360303026359,
		RuleID:         "R1005",
		Hostname:       "node-1",
		K8sDetails:     canonicalK8sDetails,
		ProcessDetails: canonicalProcessDetails,
	}
}

// TestExtract_FromCanonicalRow — pulls all four target fields plus
// EventTime + RuleID + Hostname from a real-shape kubescape row.
func TestExtract_FromCanonicalRow(t *testing.T) {
	ev, err := Extract(canonicalRow())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if ev.Target.PID != 106040 {
		t.Fatalf("PID = %d", ev.Target.PID)
	}
	if ev.Target.Comm != "redis-server" {
		t.Fatalf("Comm = %q", ev.Target.Comm)
	}
	if ev.Target.Pod != "redis-578d5dc9bd-kjj78" {
		t.Fatalf("Pod = %q", ev.Target.Pod)
	}
	if ev.Target.Namespace != "redis" {
		t.Fatalf("Namespace = %q", ev.Target.Namespace)
	}
	if ev.EventTime != 1744477360303026359 {
		t.Fatalf("EventTime = %d", ev.EventTime)
	}
	if ev.RuleID != "R1005" || ev.Hostname != "node-1" {
		t.Fatalf("RuleID/Hostname wrong: %+v", ev)
	}
}

// TestExtract_AllowsEmptyPodNamespace — host-pid processes (no pod)
// must still produce a valid Event.
func TestExtract_AllowsEmptyPodNamespace(t *testing.T) {
	row := canonicalRow()
	row.K8sDetails = "" // host-pid: no k8s context
	ev, err := Extract(row)
	if err != nil {
		t.Fatalf("Extract empty-k8s row: %v", err)
	}
	if ev.Target.Pod != "" || ev.Target.Namespace != "" {
		t.Fatalf("expected empty Pod/Namespace, got %+v", ev.Target)
	}
	if ev.Target.PID != 106040 || ev.Target.Comm != "redis-server" {
		t.Fatalf("PID/Comm lost: %+v", ev.Target)
	}
	// And the hash should still compute deterministically.
	if h := anomaly.Hash(ev.Target); len(h) != 32 {
		t.Fatalf("hash on empty-k8s target invalid: %q", h)
	}
}

// TestExtract_StableUnderJSONReorder — re-ordering JSON keys yields
// identical Target / Event.
func TestExtract_StableUnderJSONReorder(t *testing.T) {
	r := canonicalRow()
	r.K8sDetails = `{"workloadKind":"Deployment","podNamespace":"redis","podName":"redis-578d5dc9bd-kjj78","clusterName":"bobexample"}`
	r.ProcessDetails = `{"processTree":{"comm":"redis-server","ppid":1,"pid":106040,"cmdline":"redis-server","uid":0}}`
	a, errA := Extract(canonicalRow())
	b, errB := Extract(r)
	if errA != nil || errB != nil {
		t.Fatalf("Extract errors: a=%v b=%v", errA, errB)
	}
	if a.Target != b.Target {
		t.Fatalf("Target differs under JSON reorder: %+v vs %+v", a.Target, b.Target)
	}
	if anomaly.Hash(a.Target) != anomaly.Hash(b.Target) {
		t.Fatalf("Hash differs under JSON reorder")
	}
}

// TestExtract_RequiresProcessTreeComm — empty / missing comm errors.
func TestExtract_RequiresProcessTreeComm(t *testing.T) {
	for _, p := range []string{"", `{"processTree":}`, `{}`, `{"processTree":{"pid":1}}`, `{"processTree":{"comm":"","pid":1}}`} {
		row := canonicalRow()
		row.ProcessDetails = p
		_, err := Extract(row)
		if !errors.Is(err, ErrIncompleteEvent) {
			t.Fatalf("proc=%q → %v, want ErrIncompleteEvent", p, err)
		}
	}
}

// TestExtract_RequiresProcessTreePID — pid is required for hash uniqueness.
func TestExtract_RequiresProcessTreePID(t *testing.T) {
	row := canonicalRow()
	row.ProcessDetails = `{"processTree":{"comm":"redis-server","pid":0}}`
	_, err := Extract(row)
	if !errors.Is(err, ErrIncompleteEvent) {
		t.Fatalf("got %v, want ErrIncompleteEvent for pid=0", err)
	}
}

// TestExtract_RequiresEventTimeAndRuleID — both required.
func TestExtract_RequiresEventTimeAndRuleID(t *testing.T) {
	r := canonicalRow()
	r.EventTime = 0
	if _, err := Extract(r); !errors.Is(err, ErrIncompleteEvent) {
		t.Fatalf("EventTime=0 not rejected: %v", err)
	}
	r = canonicalRow()
	r.RuleID = ""
	if _, err := Extract(r); !errors.Is(err, ErrIncompleteEvent) {
		t.Fatalf("RuleID='' not rejected: %v", err)
	}
}
