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

package trigger

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Watermark observability (#97 / F8 / AE-9). Registered on the DEFAULT
// prometheus registry via promauto — the same pattern the rest of pixie
// uses (e.g. query_broker's queryExec* summaries) — and served by the
// shared services/metrics /metrics handler wired up in cmd/main.go.
// Before these existed a watermark halt was completely invisible: writes
// stopped, no error, no signal (loadtest E8).
var (
	// metricWatermarkNS tracks the trigger's current cursor in
	// normalized unix NANOS, per (table, hostname). A flat gauge while
	// kubescape rows keep arriving is the F8 silent-halt signature —
	// alert on it.
	metricWatermarkNS = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ae_trigger_watermark_ns",
		Help: "Current trigger high-water-mark cursor in normalized unix nanoseconds, per (table, hostname).",
	}, []string{"table", "hostname"})

	// metricBelowWatermark counts rows processed with a normalized
	// event_time BELOW the poll-start watermark — i.e. out-of-order /
	// clock-skewed / restart-buried rows the legacy strict HWM silently
	// dropped and the bounded lookback now captures.
	metricBelowWatermark = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ae_trigger_below_watermark_total",
		Help: "Rows seen with event_time below the prior watermark that the bounded lookback captured (strict HWM would have dropped them).",
	})

	// metricEventTimeRejected counts poison clamps: rows whose
	// normalized event_time was implausibly far in the future
	// (> now + MaxSkew) and were therefore barred from advancing the
	// watermark.
	metricEventTimeRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ae_trigger_event_time_rejected_total",
		Help: "Rows whose normalized event_time exceeded now+max-skew and were rejected from advancing the watermark (poison clamp).",
	})
)
