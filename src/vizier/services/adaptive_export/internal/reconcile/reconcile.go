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

// Package reconcile is the per-pull write-fidelity instrument for AE
// (gated by ADAPTIVE_RECONCILE). It is a LEAF package — it imports none
// of AE's other internal packages, so passthrough / controller / streaming
// can all depend on it and the sink can implement it with no import cycle.
//
// Each data-plane pull records ONE Row: how many rows AE READ back from
// Pixie for a (table, pod, window), and how many it WROTE to ClickHouse.
// Reconciliation then localizes any loss to a single hop:
//   - read  < px-direct PEM count  → query/window/filter miss   (hop R5)
//   - wrote < read                 → sink/batch drop             (hop R6)
//   - CH distinct > read           → re-pull duplication         (C8, quantified)
//
// The records land in forensic_db.ae_reconcile (see the CH-backed Recorder
// in the sink package). Best-effort: a failed reconcile write is logged,
// never fatal, and never blocks the data path.
package reconcile

import (
	"context"
	"time"
)

// Row is one per-pull reconciliation record.
type Row struct {
	TS         time.Time // when AE finished this pull
	Mode       string    // "filter" | "passthrough" | "streaming"
	Table      string    // pixie table, e.g. "conn_stats"
	Namespace  string    // target ns ("" for unfiltered passthrough/streaming)
	Pod        string    // target pod ("" for unfiltered)
	WinStart   time.Time // PxL slice lower bound (time_ >= WinStart)
	WinEnd     time.Time // PxL slice upper bound (time_ <  WinEnd)
	ReadCount  int64     // rows Pixie returned for this pull
	WroteCount int64     // rows AE sent to CH (0 on write failure / empty)
	WriteErr   string    // query or sink error, "" on success
	Hostname   string    // node name
}

// Recorder persists reconciliation Rows. Implementations MUST be
// best-effort and non-blocking-on-failure (the data path must never stall
// because reconciliation logging failed).
type Recorder interface {
	Record(ctx context.Context, r Row)
}

// Nop is the disabled-flag Recorder. It drops every Row.
type Nop struct{}

// Record implements Recorder.
func (Nop) Record(context.Context, Row) {}
