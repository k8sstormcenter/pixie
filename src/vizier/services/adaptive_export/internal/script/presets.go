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
var defaultExcludeComms = []string{
	"k3s-server", "k3s-agent", "containerd", "containerd-shim",
	"runc", "runc:[2:INIT]", "runc:[1:CHILD]", "node-agent", "kelvin",
	"vizier-pem", "vizier-query-broker", "vizier-metadata",
	"systemd", "systemd-journal", "iptables", "ip6tables", "kubelet",
	"operator", "storage",
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

// dcSnoopExclusion builds the dc_snoop noise filter (namespace + comm drops) from
// DC_SNOOP_EXCLUDE_NAMESPACES / DC_SNOOP_EXCLUDE_COMMS, substituted into
// dc_snoop.pxl at #__DC_SNOOP_EXCLUSION__ so a process can be added without a
// recompile. Kept in sync with dx benchlive.writeSelfExclusion.
func dcSnoopExclusion() string {
	var b strings.Builder
	for _, ns := range csvEnv("DC_SNOOP_EXCLUDE_NAMESPACES", defaultExcludeNamespaces) {
		fmt.Fprintf(&b, "df = df[df.namespace != '%s']\n", ns)
	}
	for _, c := range csvEnv("DC_SNOOP_EXCLUDE_COMMS", defaultExcludeComms) {
		fmt.Fprintf(&b, "df = df[df.comm != '%s']\n", c)
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
	}
}

// DarkVectorPresets are the tracepoint/profiler export scripts the operator
// registers if-not-present. Names are operator-managed (reconciled on boot).
func DarkVectorPresets() []*ScriptDefinition {
	return []*ScriptDefinition{
		{Name: "ch-dc_snoop", Description: "dc_snoop (dentry cache: process+file, V1/V2) → ClickHouse", FrequencyS: 10, Script: strings.Replace(dcSnoopScript, "#__DC_SNOOP_EXCLUSION__", dcSnoopExclusion(), 1)},
		{Name: "ch-stack_trace", Description: "stack_traces.beta (continuous profiler, V9) → ClickHouse", FrequencyS: 10, Script: stackTraceScript},
		{Name: "ch-creds_change", Description: "commit_creds privilege-escalation to root (V7) → ClickHouse", FrequencyS: 10, Script: credsChangeScript},
	}
}
