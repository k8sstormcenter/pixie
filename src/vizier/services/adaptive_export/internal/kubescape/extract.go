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
//
// This package is the only place in the operator that knows the JSON
// shape of RuntimeK8sDetails / RuntimeProcessDetails. Once an Event
// has been extracted, no further code needs to care that the source
// was Kubescape.
package kubescape

import (
	"encoding/json"
	"errors"
	"fmt"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// ErrIncompleteEvent is returned by Extract when one of the required
// fields (event_time, rule id, comm, pid) is missing or unparseable.
// Pod and Namespace are NOT required — host-pid processes legitimately
// run with empty pod / namespace.
var ErrIncompleteEvent = errors.New("kubescape: incomplete event")

// Row is the operator-facing shape of one forensic_db.kubescape_logs row.
// JSON-encoded fields stay as strings — the operator parses them itself
// to keep the ClickHouse driver layer simple.
type Row struct {
	EventTime      uint64 // schema: event_time UInt64 (unix nanos)
	RuleID         string
	Hostname       string
	K8sDetails     string // schema: RuntimeK8sDetails String (JSON)
	ProcessDetails string // schema: RuntimeProcessDetails String (JSON)
}

// Event is one parsed kubescape anomaly: workload identity + the bits
// we need for time-window math and ClickHouse persistence.
type Event struct {
	Target    anomaly.Target
	EventTime uint64 // unix nanoseconds — propagated end-to-end
	RuleID    string // diagnostic only
	Hostname  string // node-local key
}

// k8sDetails captures only pod / namespace; ignore the rest so JSON
// evolution upstream doesn't break us.
type k8sDetails struct {
	PodName      string `json:"podName"`
	PodNamespace string `json:"podNamespace"`
}

type processDetails struct {
	ProcessTree struct {
		PID  uint64 `json:"pid"`
		Comm string `json:"comm"`
	} `json:"processTree"`
}

// Extract parses a Row into an Event. Required fields are EventTime,
// RuleID, processTree.pid, processTree.comm. Pod and Namespace MAY be
// empty (host-pid processes outside any pod). Pure: no I/O, no clock.
func Extract(r Row) (Event, error) {
	if r.RuleID == "" {
		return Event{}, fmt.Errorf("%w: RuleID empty", ErrIncompleteEvent)
	}
	if r.EventTime == 0 {
		return Event{}, fmt.Errorf("%w: EventTime zero", ErrIncompleteEvent)
	}
	// K8sDetails is OPTIONAL at parse time — host-pid events legitimately
	// have no pod/namespace. We only error on malformed JSON.
	var k8s k8sDetails
	if r.K8sDetails != "" {
		if err := json.Unmarshal([]byte(r.K8sDetails), &k8s); err != nil {
			return Event{}, fmt.Errorf("%w: parse RuntimeK8sDetails: %v", ErrIncompleteEvent, err)
		}
	}
	var proc processDetails
	if err := json.Unmarshal([]byte(r.ProcessDetails), &proc); err != nil {
		return Event{}, fmt.Errorf("%w: parse RuntimeProcessDetails: %v", ErrIncompleteEvent, err)
	}
	if proc.ProcessTree.Comm == "" {
		return Event{}, fmt.Errorf("%w: processTree.comm empty", ErrIncompleteEvent)
	}
	if proc.ProcessTree.PID == 0 {
		return Event{}, fmt.Errorf("%w: processTree.pid zero", ErrIncompleteEvent)
	}
	return Event{
		Target: anomaly.Target{
			PID:       proc.ProcessTree.PID,
			Comm:      proc.ProcessTree.Comm,
			Pod:       k8s.PodName,
			Namespace: k8s.PodNamespace,
		},
		EventTime: r.EventTime,
		RuleID:    r.RuleID,
		Hostname:  r.Hostname,
	}, nil
}
