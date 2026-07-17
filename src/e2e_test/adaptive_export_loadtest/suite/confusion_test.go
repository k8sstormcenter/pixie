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
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStackEvidence extracts the KPIs of EVERY workload in the deployed stack and
// scores dx's verdicts against ground truth as a confusion matrix. It is the
// apples-to-apples evidence report for a skaffold-deployed stack, and it turns
// false positives into a test failure.
//
// Ground truth (the java-poc chain): only `backend` is malignant, and only under
// the log4shell scenario. Everything else is benign. A verdict that rules a benign
// workload in — OR rules the backend in under the WRONG scenario (e.g. the
// argocd-malicious-render cross-fire) — is a false positive and fails the run.
//
// Live-gated: AELOAD_LIVE=1 (+ AELOAD_E2E=1 to assert, else report-only).
func TestStackEvidence(t *testing.T) {
	e := RequireLiveEnv(t)
	c := loadCalibConfig()
	// The matrix always REPORTS. It only fails the run under AELOAD_MATRIX=1 — a
	// dedicated gate, so a dx false positive (a dx-repo concern) doesn't turn an
	// AE change red by default.
	assert := isTrue("AELOAD_MATRIX")

	// ---- ground truth: workload -> the scenarios for which ruling it in is correct ----
	truth := map[string]map[string]bool{
		c.backend:    {"log4shell-rce-exfil": true, "listeriosis": true}, // the only malignant workload
		"frontend":   {},
		"observer":   {},
		"postgres":   {},
		"cleannoise": {},
	}
	malignant := map[string]bool{c.backend: true}

	pods := podsByWorkload(t, c.appNS, keys(truth))

	// ---- per-workload KPIs (from every pod, across the whole stack) ----
	t.Log("================ PER-WORKLOAD KPIs (java-poc) ================")
	t.Logf("%-12s %-28s %-6s %-6s %-6s %-8s", "workload", "pod", "R0001", "R0010", "attrib", "egress")
	for _, w := range keys(truth) {
		pod := pods[w]
		r1 := podAnomalies(t, e, c.appNS, w, "R0001")
		r10 := podAnomalies(t, e, c.appNS, w, "R0010")
		att := e.QueryInt(t, fmt.Sprintf(
			"SELECT count() FROM forensic_db.adaptive_attribution WHERE pod LIKE '%%%s%%'", w))
		egr := e.QueryInt(t, fmt.Sprintf(
			"SELECT countDistinct(remote_port) FROM forensic_db.conn_stats WHERE pod LIKE '%%%s%%' AND remote_port>0", w))
		t.Logf("%-12s %-28s %-6d %-6d %-6d %-8d", w, short(pod), r1, r10, att, egr)
	}

	// ---- dx verdicts per workload (latest ruled_in scenarios across ALL dx pods) ----
	ruleins := dxRuleinsByWorkload(t, c)
	t.Log("================ dx VERDICTS ================")
	for _, w := range keys(truth) {
		if s := ruleins[w]; len(s) > 0 {
			t.Logf("  %-12s ruled_in %v", w, s)
		} else {
			t.Logf("  %-12s ruled_out / none", w)
		}
	}

	// ---- confusion matrix over (workload, scenario) ----
	var tp, fp, fn int
	var fps, fns []string
	for w, scenarios := range ruleins {
		for sc := range scenarios {
			if truth[w][sc] {
				tp++
			} else {
				fp++
				fps = append(fps, fmt.Sprintf("%s ruled_in [%s]", w, sc))
			}
		}
	}
	for w := range malignant {
		hit := false
		for sc := range ruleins[w] {
			if truth[w][sc] {
				hit = true
			}
		}
		if !hit {
			fn++
			fns = append(fns, w)
		}
	}
	// TN: benign workloads with no rule-in at all.
	tn := 0
	for w := range truth {
		if !malignant[w] && len(ruleins[w]) == 0 {
			tn++
		}
	}

	sort.Strings(fps)
	sort.Strings(fns)
	t.Log("================ CONFUSION MATRIX ================")
	t.Logf("  TP=%d  FP=%d  FN=%d  TN=%d", tp, fp, fn, tn)
	prec := ratio(tp, tp+fp)
	rec := ratio(tp, tp+fn)
	t.Logf("  precision=%.2f  recall=%.2f", prec, rec)
	for _, f := range fps {
		t.Logf("  FALSE POSITIVE: %s", f)
	}
	for _, f := range fns {
		t.Logf("  FALSE NEGATIVE: %s (expected malignant, not ruled in)", f)
	}

	if !assert {
		t.Skip("AELOAD_E2E != 1 — evidence reported, matrix not asserted")
	}
	require.Zerof(t, fp, "dx false positives: %v", fps)
	require.Zerof(t, fn, "dx false negatives: %v", fns)
	require.Greater(t, tp, 0, "no true-positive rule-in — the disease was not detected")
}

// ---- helpers ----

var verdictRe = regexp.MustCompile(`verdict ([^/ ]+)/(\S+) .*?ruled_in \[([^\]]+)\]`)

// dxRuleinsByWorkload returns workload -> set of scenarios dx ruled in, across all
// dx-daemon pods (per-node DaemonSet), keyed by the pod's workload prefix.
func dxRuleinsByWorkload(t *testing.T, c calibConfig) map[string]map[string]bool {
	t.Helper()
	out, _ := exec.Command("kubectl", "-n", c.dxNS, "logs", "ds/"+c.dxDS, "--tail=8000", "--all-pods=true").CombinedOutput()
	res := map[string]map[string]bool{}
	for _, m := range verdictRe.FindAllStringSubmatch(string(out), -1) {
		ns, pod, scenario := m[1], m[2], m[3]
		if ns != c.appNS {
			continue
		}
		w := workloadOf(pod)
		if res[w] == nil {
			res[w] = map[string]bool{}
		}
		res[w][scenario] = true
	}
	return res
}

// workloadOf reduces a pod name (backend-59577db868-4wxxs) to its workload (backend).
func workloadOf(pod string) string {
	parts := strings.Split(pod, "-")
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return pod
}

func podsByWorkload(t *testing.T, ns string, workloads []string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, w := range workloads {
		out, _ := exec.Command("kubectl", "-n", ns, "get", "pod", "-l", "app="+w,
			"-o", "jsonpath={.items[0].metadata.name}").CombinedOutput()
		m[w] = strings.TrimSpace(string(out))
	}
	return m
}

func podAnomalies(t *testing.T, e Env, ns, workload, rule string) int {
	return e.QueryInt(t, fmt.Sprintf(
		"SELECT count() FROM forensic_db.kubescape_logs WHERE RuleID='%s' AND RuntimeK8sDetails LIKE '%%%s%%' AND RuntimeK8sDetails LIKE '%%%s%%'",
		rule, ns, workload))
}

func isTrue(env string) bool { return envOr(env, "") == "1" }
func keys(m map[string]map[string]bool) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
func short(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
