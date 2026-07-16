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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/kubescape"
)

// rowFingerprint is the deduper for boundary rows at each poll. It
// runs ONCE PER kubescape row pulled from ClickHouse by the trigger
// (clickhouse.go:272-273). With PollLimit=10000 and a 250ms ticker, a
// trigger that's catching up from a stale watermark can process 40k
// rows/sec PURELY in the fingerprint loop — every one of which:
//
//  1. Allocates a fresh sha256 hasher (sha256.New).
//  2. Runs fmt.Fprintf with %d/%s verbs into the hasher (uses reflect).
//  3. Hex-encodes the 32-byte digest into a 64-char string.
//
// The bench numbers below quantify that. If the per-row cost is
// significant, the trigger backlog drain itself is a CPU consumer
// independent of any downstream work.

func benchKubescapeRow(i int) kubescape.Row {
	// K8sDetails / ProcessDetails are JSON blobs in production —
	// kubescape emits them at ~500 bytes typical, ~2KB upper.
	const k8sDetails = `{"podNamespace":"svc-poc","podName":"backend-vulnerable-779cd9d765-mxr8t","containerName":"backend","workloadName":"backend-vulnerable","workloadKind":"Deployment","image":"ghcr.io/k8sstormcenter/chain-backend-vuln:latest","clusterName":"soc-demo-pg","nodeName":"node-1"}`
	const procDetails = `{"comm":"java","pid":1234,"ppid":1,"path":"/usr/lib/jvm/java-11/bin/java","argv":["java","-cp","/app/app-vuln-1.0.jar","com.example.App"],"user":"appuser","cwd":"/app","spawn_time":"2026-06-07T18:00:00Z"}`
	return kubescape.Row{
		EventTime:      uint64(1_700_000_000_000_000_000 + i),
		RuleID:         "R1100",
		Hostname:       "pixie-worker-node",
		K8sDetails:     k8sDetails,
		ProcessDetails: procDetails,
	}
}

func BenchmarkRowFingerprint(b *testing.B) {
	row := benchKubescapeRow(0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rowFingerprint(row)
	}
}

// BenchmarkRowFingerprint_Unique varies event_time per call so the
// hasher gets unique input bytes (matches real boundary-row behaviour
// where each row has its own event_time).
func BenchmarkRowFingerprint_Unique(b *testing.B) {
	rows := make([]kubescape.Row, 1024)
	for i := range rows {
		rows[i] = benchKubescapeRow(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rowFingerprint(rows[i%len(rows)])
	}
}

// BenchmarkRowFingerprint_LargePoll simulates one trigger poll
// draining PollLimit=10000 rows — the boundary-dedup pass after a
// stale-watermark catchup. The trigger does this ONCE per
// PollInterval (250ms default) when there's a backlog; under a
// 100ms-jitter ticker drift this can run 4-10× per second.
func BenchmarkRowFingerprint_LargePoll(b *testing.B) {
	const batch = 10_000
	rows := make([]kubescape.Row, batch)
	for i := range rows {
		rows[i] = benchKubescapeRow(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := range rows {
			_ = rowFingerprint(rows[i])
		}
	}
}

// BenchmarkRowFingerprintSimple_LargePoll uses an alternative
// allocation-free fingerprint (sha256-of-concatenated-strings via a
// builder + direct Write). Lets us compare the current Fprintf-based
// implementation's reflect-driven cost against a hand-rolled version
// — informs whether replacing the fmt.Fprintf is a worthwhile
// micro-optimisation if the standard bench shows the trigger
// fingerprint as a CPU hotspot.
func BenchmarkRowFingerprintSimple_LargePoll(b *testing.B) {
	const batch = 10_000
	rows := make([]kubescape.Row, batch)
	for i := range rows {
		rows[i] = benchKubescapeRow(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := range rows {
			_ = fingerprintNoFmt(rows[i])
		}
	}
}

// fingerprintNoFmt is the Fprintf-free reference. Same output guarantee
// is NOT asserted here — this is a perf-comparison anchor only. If the
// numbers diverge by >2× from rowFingerprint, the fmt.Fprintf path is
// a real cost.
func fingerprintNoFmt(r kubescape.Row) string {
	h := sha256.New()
	var b strings.Builder
	b.Grow(64 + len(r.RuleID) + len(r.Hostname) + len(r.K8sDetails) + len(r.ProcessDetails))
	_, _ = fmt.Fprintf(&b, "%d", r.EventTime)
	b.WriteByte(0)
	b.WriteString(r.RuleID)
	b.WriteByte(0)
	b.WriteString(r.Hostname)
	b.WriteByte(0)
	b.WriteString(r.K8sDetails)
	b.WriteByte(0)
	b.WriteString(r.ProcessDetails)
	h.Write([]byte(b.String()))
	return hex.EncodeToString(h.Sum(nil))
}
