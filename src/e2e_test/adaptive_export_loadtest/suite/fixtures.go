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

package aeloadsuite

import (
	"fmt"
	"testing"
	"time"
)

// controlFixture is one deterministic control-plane experiment. It injects a
// curated kubescape_logs set and asserts the exact control surface AE derives
// from it (adaptive_attribution FINAL + uniqExact(anomaly_hash)). Because that
// surface is a pure function of the injected rows, the KPI is Reproducibility:
// every rep must yield the same (hashes, attrib), and it must equal want.
type controlFixture struct {
	name    string
	desc    string
	reps    int
	fanout  int      // distinct workloads (pods) per rep; default 1
	count   int      // rows per workload; default 1
	dtSec   int64    // seconds between successive rows; default 1
	rules   []string // kubescape RuleIDs injected per workload; default {"R0001"}
	same    bool     // reuse one event_time for all rows (boundary-dedup case)
	restart bool     // restart AE mid-rep, then re-measure (idempotency case)

	wantHashes int
	wantAttrib int
}

// controlFixtures — the deterministic reproducibility suite. Names describe the
// property under test; there are no CVE or incident-scenario tokens here because
// the control plane is pure kubescape-metadata bookkeeping, independent of any
// workload's behaviour.
var controlFixtures = []controlFixture{
	{
		name: "single-anomaly", reps: 100,
		desc:       "one anomaly row -> one workload identity, one attribution row",
		wantHashes: 1, wantAttrib: 1,
	},
	{
		name: "dedup-extend", reps: 100, count: 10, dtSec: 1,
		desc:       "10 rows, same workload, monotone event_time -> window extended, not multiplied",
		wantHashes: 1, wantAttrib: 1,
	},
	{
		name: "fan-out", reps: 20, fanout: 8,
		desc:       "8 distinct workloads -> 8 identities, 8 attribution rows",
		wantHashes: 8, wantAttrib: 8,
	},
	{
		name: "boundary-collision", reps: 100, rules: []string{"R0001", "R0010"}, same: true,
		desc:       "two rules at one event_time, same workload -> fingerprint dedup, one identity",
		wantHashes: 1, wantAttrib: 1,
	},
	{
		name: "watermark-idempotent-restart", reps: 10, restart: true,
		desc:       "attribution stays exactly 1 across an AE restart (no double-count)",
		wantHashes: 1, wantAttrib: 1,
	},
}

// measurement is one rep's observed control surface.
type measurement struct {
	hashes int
	attrib int
}

// runRep injects the fixture's rows for one rep and returns the measured
// control surface. podPrefix is globally unique per rep so the LIKE-scoped reads
// isolate reps even when their windows overlap.
func (f controlFixture) runRep(t *testing.T, e Env, node string, rep int) measurement {
	t.Helper()
	fanout := f.fanout
	if fanout == 0 {
		fanout = 1
	}
	count := f.count
	if count == 0 {
		count = 1
	}
	dt := f.dtSec
	if dt == 0 {
		dt = 1
	}
	rules := f.rules
	if len(rules) == 0 {
		rules = []string{"R0001"}
	}
	// event_time = real current second: the trigger watermark is a strict
	// high-water-mark, so now-based stamps keep it tracking wall-clock and
	// monotone across reps sharing this node (contract C3).
	base := time.Now().Unix()
	podPrefix := fmt.Sprintf("cp-%s-%03d", f.name, rep)

	for j := 0; j < fanout; j++ {
		pod := podPrefix
		if fanout > 1 {
			pod = fmt.Sprintf("%s-%d", podPrefix, j+1)
		}
		var rows []AnomalyRow
		for _, rule := range rules {
			for i := 0; i < count; i++ {
				et := base
				if !f.same {
					et = base + int64(i)*dt
				}
				rows = append(rows, AnomalyRow{
					Namespace: "aeload", Pod: pod, RuleID: rule,
					PID: 1234 + j, Comm: "java", EventTime: et, Hostname: node,
				})
			}
		}
		e.Inject(t, rows...)
	}

	// Let all rows (spanning (count-1)*dt seconds) be polled before measuring.
	if span := (count - 1) * int(dt); span > 0 {
		time.Sleep(time.Duration(span+2) * time.Second)
	}

	if f.restart {
		e.WaitAttrib(t, node, podPrefix, f.wantAttrib, 20)
		e.RestartAE(t)
	}

	attrib := e.WaitAttrib(t, node, podPrefix, f.wantAttrib, 25)
	hashes := e.UniqHashes(t, node, podPrefix)
	return measurement{hashes: hashes, attrib: attrib}
}
