// Copyright 2018- The Pixie Authors.
// SPDX-License-Identifier: Apache-2.0

package script

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
)

var defaultExcludeNamespaces = []string{
	"pl", "honey", "px-operator", "olm", "clickhouse",
	"kube-system", "kube-public", "kube-node-lease", "local-path-storage",
}

func csvEnv(key string, def []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	out := []string{}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// dcSnoopExclusion builds the dc_snoop CHILD-namespace noise filter from
// DC_SNOOP_EXCLUDE_NAMESPACES, substituted into dc_snoop.pxl at
// # __DC_SNOOP_EXCLUSION__ so an infra namespace can be added without a recompile.
// The hardcoded comm blocklist was DELETED (pure-ancestry cleanup): own-stack pods
// are dropped here by their resolved namespace, and their transient blank-namespace
// children by the parent-ancestry filter (dcSnoopParentExclusion). Host/kernel
// processes (blank namespace, blank/kernel parent) are left to dx's process forest.
func dcSnoopExclusion() string {
	var b strings.Builder
	for _, ns := range csvEnv("DC_SNOOP_EXCLUDE_NAMESPACES", defaultExcludeNamespaces) {
		fmt.Fprintf(&b, "df = df[df.namespace != '%s']\n", ns)
	}
	return strings.TrimRight(b.String(), "\n")
}

// dcSnoopParentExclusion builds the ppid-ancestry filter (drop events whose PARENT
// resolves to an own-stack namespace) from the SAME namespace list as the self
// filter, substituted into dc_snoop.pxl at # __DC_SNOOP_PARENT_EXCLUSION__. Rooted
// on the parent's namespace (via the ppid->process_stats join), not comm, so it
// catches transient children exec'd by infra pods without touching workload/attack
// children of shared (blank-namespace) parents like containerd-shim/runc.
func dcSnoopParentExclusion() string {
	var b strings.Builder
	for _, ns := range csvEnv("DC_SNOOP_EXCLUDE_NAMESPACES", defaultExcludeNamespaces) {
		fmt.Fprintf(&b, "df = df[df.parent_namespace != '%s']\n", ns)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Dark-vector + profiler retention/export scripts, embedded so the operator can
// register them (if not already present) at boot via CreateRetentionScript.
// Each keeps its tracepoint permanently upserted ("876000h" ≈ 100y, effectively
// permanent — no reliance on the 24h re-run) and exports its table to ClickHouse
// via the OTel plugin (px.export + px.otel.ClickHouseRows). stack_trace needs no
// tracepoint — it exports the native continuous profiler (stack_traces.beta).

//go:embed presets/dc_snoop.pxl
var dcSnoopScript string

//go:embed presets/stack_trace.pxl
var stackTraceScript string

//go:embed presets/creds_change.pxl
var credsChangeScript string

//go:embed presets/dc_snoop_deploy.pxl
var dcSnoopDeployScript string

//go:embed presets/creds_change_deploy.pxl
var credsChangeDeployScript string

//go:embed presets/process_tree.pxl
var processTreeScript string

//go:embed presets/proc_exec_deploy.pxl
var procExecDeployScript string

// TracepointDef is a bpftrace tracepoint the AE deploys itself at boot. The
// retention/cron export path cannot deploy tracepoints (its pxtrace mutation is
// dropped), so the AE owns deployment via a mutation ExecuteScript (pixieapi).
// Script is an `import pxtrace` + UpsertTracepoint program (idempotent upsert,
// permanent TTL); Table is the tracepoint output table its export preset reads.
type TracepointDef struct {
	Name   string
	Table  string
	Script string
}

// DesiredTracepoints is the source of truth for the bpftraces the AE deploys at
// boot. stack_traces.beta (V9) is the native continuous profiler and needs no
// tracepoint, so it is absent here (its export preset works with no deploy).
// Extend this list as new bpftraces (V6 mprotect, V8 bpf/ptrace, …) are added.
func DesiredTracepoints() []TracepointDef {
	return []TracepointDef{
		{Name: "dc_snoop", Table: "dc_snoop", Script: dcSnoopDeployScript},
		{Name: "creds_change", Table: "creds_change", Script: credsChangeDeployScript},
		{Name: "proc_exec", Table: "proc_exec", Script: procExecDeployScript},
	}
}

// DarkVectorPresets are the tracepoint/profiler export scripts the operator
// registers if-not-present. Names are operator-managed (reconciled on boot).
func DarkVectorPresets() []*ScriptDefinition {
	return []*ScriptDefinition{
		{Name: "ch-dc_snoop", Description: "dc_snoop (dentry cache: process+file, V1/V2) → ClickHouse", FrequencyS: 10, Script: strings.Replace(strings.Replace(dcSnoopScript, "# __DC_SNOOP_PARENT_EXCLUSION__", dcSnoopParentExclusion(), 1), "# __DC_SNOOP_EXCLUSION__", dcSnoopExclusion(), 1)},
		{Name: "ch-stack_trace", Description: "stack_traces.beta (continuous profiler, V9) → ClickHouse", FrequencyS: 10, Script: stackTraceScript},
		{Name: "ch-creds_change", Description: "commit_creds privilege-escalation to root (V7) → ClickHouse", FrequencyS: 10, Script: credsChangeScript},
		{Name: "ch-process_tree", Description: "process_tree (exec-edge forest for ancestry classification) → ClickHouse", FrequencyS: 10, Script: processTreeScript},
	}
}
