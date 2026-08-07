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
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// credsSentinel is a distinctive real-uid used only by this calibration, so the
// resulting creds_change row is unambiguous (no collision with a system daemon
// that happens to change credentials to root).
const credsSentinel = 12345

// TestCredsChangeCalibration is the end-to-end calibration for the creds_change
// dark-vector tracepoint (V7 credential vector). It guarantees, on every run,
// the two properties the trace exists to provide:
//
//	a) the TRACE WORKS — the AE-deployed commit_creds bpftrace captures a real
//	   privilege escalation (a process whose REAL uid transitions >0 -> 0), and
//	b) ATTRIBUTION reaches ClickHouse — the event flows Pixie -> AE retention
//	   export -> forensic_db.creds_change carrying its pid + comm identity (the
//	   filterable base dx projects as V7 IPC/credential evidence).
//
// The escalation is fired deterministically with a stock image and no custom
// binary: a root container drops its REAL uid to the sentinel while KEEPING
// effective uid 0 (so it stays privileged), then setuid(0) pulls the real uid
// back to 0 — exactly the commit_creds(new_uid==0 && old_uid>0) the tracepoint
// filters for. Reading the effective/saved trick wrong is the classic pitfall:
// setuid(0) from a non-privileged euid changes only euid, leaving the real uid
// untouched (no match), so we must retain euid=0 across the drop.
//
// Live + e2e gated: AELOAD_LIVE=1 AELOAD_E2E=1. Requires kubectl + a deployed AE
// with INSTALL_PRESET_SCRIPTS=true (so the AE has deployed the creds_change
// tracepoint at boot and registered its export preset).
func TestCredsChangeCalibration(t *testing.T) {
	e := RequireLiveEnv(t)
	if os.Getenv("AELOAD_E2E") != "1" {
		t.Skip("AELOAD_E2E != 1 — creds_change calibration (fires a real privilege escalation) skipped")
	}
	requireRunning(t, e, e.AENS, e.AEDaemon)

	const ns, job = "creds-calib", "creds-calib"
	// Everything on/after this instant is "post-fire". -2s absorbs minor clock
	// skew between the test host and the ClickHouse/PEM nodes.
	fireStart := time.Now().Add(-2 * time.Second).UnixNano()

	base := e.QueryInt(t, fmt.Sprintf(
		"SELECT count() FROM forensic_db.creds_change WHERE old_uid=%d AND new_uid=0", credsSentinel))
	t.Logf("baseline creds_change(old_uid=%d,new_uid=0) = %d", credsSentinel, base)

	// --- fire the escalation on the AE's node ---
	kubeTry("create", "namespace", ns)
	t.Cleanup(func() { kubeTry("delete", "namespace", ns, "--wait=false") })
	kubeApplyStdin(t, credsCalibJob(ns, job, e.Node, credsSentinel))
	waitCredsJobRan(t, ns)

	// --- assert (a) trace works + (b) attribution in CH ---
	// Export is the retention-plugin cron (10s) + native-sink lag; poll to 3m.
	var got credsRow
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if got = e.queryCredsRow(t, credsSentinel, fireStart); got.count > 0 {
			break
		}
		time.Sleep(5 * time.Second)
	}
	require.Positivef(t, got.count,
		"no creds_change row (old_uid=%d,new_uid=0) reached forensic_db after firing the escalation — "+
			"the commit_creds tracepoint is not deployed/capturing OR the AE export is not flowing to ClickHouse",
		credsSentinel)
	t.Logf("(a) TRACE OK: creds_change captured count=%d pid=%d comm=%q old_uid=%d new_uid=0",
		got.count, got.pid, got.comm, credsSentinel)

	// (b) attribution base: the pid + comm identity dx filters/projects on.
	require.Positivef(t, got.pid, "creds_change row carries no pid — attribution incomplete")
	require.NotEmptyf(t, got.comm, "creds_change row carries no comm — attribution incomplete")
	t.Logf("(b) ATTRIBUTION OK: pid=%d comm=%q reached ClickHouse", got.pid, got.comm)

	// (c) pod/namespace attribution via the process_stats pid-merge in the export
	// preset. The escalation pod sleeps so process_stats captures its pid. This is
	// a best-effort left join, so assert-or-log: when attribution lands we verify
	// it is the calibration's own namespace (correctness); a merge miss is logged,
	// not a hard flake. Harden to require once proven stable on a live rig.
	if got.pod != "" || got.namespace != "" {
		t.Logf("(c) METADATA ATTRIBUTION OK: namespace=%q pod=%q container=%q node=%q reached ClickHouse",
			got.namespace, got.pod, got.container, got.hostname)
		require.Containsf(t, got.namespace, ns,
			"creds_change namespace=%q did not resolve to the calibration namespace %q", got.namespace, ns)
	} else {
		t.Logf("NOTE: creds_change pod/namespace empty — process_stats pid-merge did not attribute pid=%d "+
			"(best-effort join; check the enrichment / process_stats coverage)", got.pid)
	}
}

// credsRow is the single freshest sentinel escalation row read back from CH.
type credsRow struct {
	count     int
	pid       int
	comm      string
	namespace string
	pod       string
	container string
	hostname  string
}

// queryCredsRow reads the creds_change row(s) for the sentinel escalation fired
// on/after sinceNanos. count>0 proves the trace + export worked; pid/comm/pod
// carry the attribution.
func (e Env) queryCredsRow(t *testing.T, oldUID int, sinceNanos int64) credsRow {
	where := fmt.Sprintf(
		"old_uid=%d AND new_uid=0 AND toUnixTimestamp64Nano(event_time) >= %d", oldUID, sinceNanos)
	r := credsRow{count: e.QueryInt(t, "SELECT count() FROM forensic_db.creds_change WHERE "+where)}
	if r.count == 0 {
		return r
	}
	// anyIf(x, x!='') prefers an attributed row if any export landed pod/namespace,
	// so a later enriched write wins over an earlier bare one for the same event.
	r.pid = e.QueryInt(t, "SELECT any(pid) FROM forensic_db.creds_change WHERE "+where)
	r.comm = strings.TrimSpace(e.Query(t, "SELECT any(comm) FROM forensic_db.creds_change WHERE "+where))
	r.namespace = strings.TrimSpace(e.Query(t, "SELECT anyIf(namespace, namespace!='') FROM forensic_db.creds_change WHERE "+where))
	r.pod = strings.TrimSpace(e.Query(t, "SELECT anyIf(pod, pod!='') FROM forensic_db.creds_change WHERE "+where))
	r.container = strings.TrimSpace(e.Query(t, "SELECT anyIf(container, container!='') FROM forensic_db.creds_change WHERE "+where))
	r.hostname = strings.TrimSpace(e.Query(t, "SELECT anyIf(hostname, hostname!='') FROM forensic_db.creds_change WHERE "+where))
	return r
}

// credsCalibJob renders a one-shot Job that fires exactly one
// commit_creds(new_uid==0 && old_uid>0). setresuid(sentinel,0,0) drops the REAL
// uid to the sentinel while keeping effective uid 0 (privileged); setuid(0) then
// pulls the real uid back to 0 — the escalation the tracepoint filters for.
// python is present in python:3-slim; no custom image or setuid binary needed.
func credsCalibJob(ns, name, node string, oldUID int) string {
	// After firing the escalation the process sleeps ~20s so it is alive long
	// enough for Pixie's process_stats to capture its pid — the dc_snoop/
	// creds_change export presets resolve pod/namespace by merging process_stats
	// on pid, and a sub-second process would never be sampled (empty attribution).
	py := fmt.Sprintf(
		"import os,time; os.setresuid(%d,0,0); os.setuid(0); print('credcalib escalated', os.getresuid()); time.sleep(20)",
		oldUID)
	nodeLine := ""
	if node != "" {
		nodeLine = "\n      nodeName: " + node
	}
	return fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
spec:
  backoffLimit: 1
  ttlSecondsAfterFinished: 120
  template:
    metadata:
      labels: { app: creds-calib }
    spec:
      restartPolicy: Never%s
      containers:
      - name: escalate
        image: python:3-slim
        command: ["python3","-c","%s"]
        securityContext:
          runAsUser: 0
          allowPrivilegeEscalation: true
          capabilities:
            add: ["SETUID","SETGID"]
`, name, ns, nodeLine, py)
}

// kubeApplyStdin applies a manifest piped over stdin (kubectl apply -f -).
func kubeApplyStdin(t *testing.T, manifest string) {
	t.Helper()
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "kubectl apply creds-calib job:\n%s\n%s", manifest, string(out))
}

// waitCredsJobRan blocks until the escalation pod reached a terminal phase. The
// commit_creds event fires the instant setuid(0) runs, so either Succeeded or
// Failed means the trace has already had its chance to capture.
func waitCredsJobRan(t *testing.T, ns string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("kubectl", "-n", ns, "get", "pods", "-l", "app=creds-calib",
			"-o", "jsonpath={.items[*].status.phase}").CombinedOutput()
		phase := strings.TrimSpace(string(out))
		if strings.Contains(phase, "Succeeded") || strings.Contains(phase, "Failed") {
			t.Logf("creds-calib pod phase: %s", phase)
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Log("creds-calib pod did not reach a terminal phase in 90s — polling ClickHouse anyway")
}
