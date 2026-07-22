// Copyright 2018- The Pixie Authors.
// SPDX-License-Identifier: Apache-2.0

package script

import _ "embed"

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
		{Name: "ch-dc_snoop", Description: "dc_snoop (dentry cache: process+file, V1/V2) → ClickHouse", FrequencyS: 10, Script: dcSnoopScript},
		{Name: "ch-stack_trace", Description: "stack_traces.beta (continuous profiler, V9) → ClickHouse", FrequencyS: 10, Script: stackTraceScript},
		{Name: "ch-creds_change", Description: "commit_creds privilege-escalation to root (V7) → ClickHouse", FrequencyS: 10, Script: credsChangeScript},
	}
}
