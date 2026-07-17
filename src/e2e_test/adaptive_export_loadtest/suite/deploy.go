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
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// EnsureE2EStack stands up the entire NON-Pixie e2e environment with ONE command,
// so the calibration runs against a reproducible stack instead of a hand-installed
// one. It deploys, in order:
//
//	1. the SOC stack   — ClickHouse -> kubescape -> vector   (soc repo, module soc-stack)
//	2. the sample apps — SBoBs -> java-poc workloads + pathogen (bob repo, module java-poc-apps)
//
// Each component is deployed from its golden-source repo's own Skaffold (one source
// of truth per component). Pixie itself (pl: vizier + adaptive_export) is overlaid
// separately by Pixie's native Skaffold and is NOT touched here.
//
// Gating / config (all via env, so the suite stays runnable on a pre-deployed rig):
//
//	AELOAD_DEPLOY=1   run the deploy (else no-op — assume the rig is already up)
//	AELOAD_CLEAN=1    first remove the non-Pixie components (keeps pl + dx), for a
//	                  from-scratch redeploy
//	AELOAD_SOC_DIR    local soc checkout (default: use the git-pinned ../skaffold.yaml)
//	AELOAD_BOB_DIR    local bob checkout (default: use the git-pinned reference)
//
// Requires `skaffold` and `kubectl` in PATH, with the current context pointed at
// the target k3s rig.
func EnsureE2EStack(t *testing.T) {
	t.Helper()
	if os.Getenv("AELOAD_DEPLOY") != "1" {
		t.Log("AELOAD_DEPLOY != 1 — skipping stack deploy (assuming a pre-deployed rig)")
		return
	}
	if _, err := exec.LookPath("skaffold"); err != nil {
		t.Fatalf("skaffold not found in PATH — required for AELOAD_DEPLOY=1: %v", err)
	}

	if os.Getenv("AELOAD_CLEAN") == "1" {
		cleanNonPixie(t)
	}

	// 1) SOC stack. Prefer a local checkout; else the git-pinned requires config.
	if soc := os.Getenv("AELOAD_SOC_DIR"); soc != "" {
		skaffoldDeploy(t, soc, "soc-stack")
	} else {
		skaffoldDeploy(t, ".", "soc-stack") // resolved via the e2e-nonpixie requires
	}
	// 2) sample apps (SBoBs then workloads).
	if bob := os.Getenv("AELOAD_BOB_DIR"); bob != "" {
		skaffoldDeploy(t, bob+"/example/java-poc", "java-poc-apps")
	} else {
		skaffoldDeploy(t, ".", "java-poc-apps")
	}

	// brief settle for the node-agent to bind the User SBoBs before the pods run.
	time.Sleep(8 * time.Second)
	t.Log("e2e non-Pixie stack deployed (soc stack -> bob sbobs+apps)")
}

// skaffoldDeploy runs `skaffold deploy -m <module> -p k3s` in dir, streaming output.
func skaffoldDeploy(t *testing.T, dir, module string) {
	t.Helper()
	cmd := exec.Command("skaffold", "deploy", "-m", module, "-p", "k3s")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("skaffold deploy -m %s (dir=%s) failed: %v\n%s", module, dir, err, out)
	}
	t.Logf("skaffold deploy -m %s: ok", module)
}

// cleanNonPixie removes the components this suite (re)deploys so a redeploy is
// from-scratch: the app + forensic namespaces, and the kubescape/vector helm
// releases in honey. It deliberately leaves `pl` (Pixie + adaptive_export) and the
// dx-daemon alone — those are owned elsewhere and are the overlay this suite runs on.
func cleanNonPixie(t *testing.T) {
	t.Helper()
	t.Log("AELOAD_CLEAN=1 — removing non-Pixie components (keeping pl + dx)")
	sh(t, "helm", "uninstall", "vector", "-n", "honey")
	sh(t, "helm", "uninstall", "kubescape", "-n", "honey")
	for _, ns := range []string{"java-poc", "pathogen-ns", "clickhouse"} {
		sh(t, "kubectl", "delete", "ns", ns, "--ignore-not-found", "--wait=false")
	}
	// wait for the namespaces to finish terminating before redeploy (server-side).
	for _, ns := range []string{"java-poc", "pathogen-ns", "clickhouse"} {
		_ = exec.Command("kubectl", "wait", "--for=delete", "ns/"+ns, "--timeout=120s").Run()
	}
}

// sh runs a command best-effort (clean steps should not fail the run if a
// component was already absent).
func sh(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not found") {
		t.Logf("(clean) %s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}
