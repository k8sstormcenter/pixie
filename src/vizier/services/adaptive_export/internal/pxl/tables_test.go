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

package pxl

import (
	"testing"
)

// TestBuiltinTables_Count — guard against accidental list churn.
// The set is the 13 socket_tracer tables in pixie's stirling layer
// (http_events, http2_messages.beta, dns_events, redis_events,
// mysql_events, pgsql_events, cql_events, mongodb_events,
// kafka_events.beta, amqp_events, mux_events, tls_events, conn_stats).
// Update this guard if the spec adds / removes a table.
// 13 socket_tracer tables + 8 dx dark-vector tracepoint tables (dx_execve,
// dx_vfs_events, dx_unlink, dx_dlookup, dx_mprotect, dx_creds, dx_bpf, dx_ptrace
// — entlein/dx#126). Update this guard if the spec adds / removes a table.
func TestBuiltinTables_Count(t *testing.T) {
	const want = 21
	if got := len(builtinTables); got != want {
		t.Fatalf("builtinTables = %d entries, want %d", got, want)
	}
}

// TestBuiltinTables_AllNamesUnique — no duplicates.
func TestBuiltinTables_AllNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, sp := range builtinTables {
		if seen[sp.Name] {
			t.Fatalf("duplicate table %q in builtinTables", sp.Name)
		}
		seen[sp.Name] = true
	}
}

// TestBuiltinTables_AllHaveProtocol — each entry is annotated, so audit
// queries like "which tables observe HTTP?" work without parsing the name.
func TestBuiltinTables_AllHaveProtocol(t *testing.T) {
	for _, sp := range builtinTables {
		if sp.Protocol == "" {
			t.Fatalf("BuiltinTable %q missing Protocol annotation", sp.Name)
		}
	}
}

// TestIsBuiltin — defensive guard for bare-string callers.
func TestIsBuiltin(t *testing.T) {
	if !IsBuiltin("redis_events") {
		t.Fatalf("redis_events should be a builtin")
	}
	if !IsBuiltin("http2_messages.beta") {
		t.Fatalf("dotted table http2_messages.beta should be a builtin")
	}
	if !IsBuiltin("conn_stats") {
		t.Fatalf("conn_stats was re-added; should be builtin")
	}
	if IsBuiltin("") {
		t.Fatalf("empty string should not be builtin")
	}
}

// TestDefaultRegistry — stub returns builtinTables.
func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()
	got := r.Tables()
	if len(got) != len(builtinTables) {
		t.Fatalf("DefaultRegistry().Tables() len %d, want %d", len(got), len(builtinTables))
	}
	for i, sp := range builtinTables {
		if got[i] != sp {
			t.Fatalf("DefaultRegistry().Tables()[%d] = %+v, want %+v", i, got[i], sp)
		}
	}
}

// TestNames — projection to []string preserves order.
func TestNames(t *testing.T) {
	names := Names(builtinTables)
	if len(names) != len(builtinTables) {
		t.Fatalf("Names len mismatch")
	}
	if names[0] != "http_events" {
		t.Fatalf("first name = %q, want http_events", names[0])
	}
}

// TestDefaultRegistry_Tables_IsCopy — defensive: callers cannot mutate
// the package-level table list by aliasing the slice returned from
// DefaultRegistry().Tables(). Append-to-zero-cap is the easy gotcha:
// if Tables() handed out the backing slice directly, an append-without-
// reallocation would clobber the next builtin.
func TestDefaultRegistry_Tables_IsCopy(t *testing.T) {
	got := DefaultRegistry().Tables()
	if len(got) == 0 {
		t.Fatalf("DefaultRegistry().Tables() is empty")
	}
	want0 := builtinTables[0].Name
	got[0].Name = "MUTATED"
	if builtinTables[0].Name != want0 {
		t.Fatalf("mutation through DefaultRegistry().Tables() leaked: builtinTables[0].Name=%q, want %q",
			builtinTables[0].Name, want0)
	}
}

// TestBuiltins_IsCopy — same guarantee for the Builtins() accessor.
func TestBuiltins_IsCopy(t *testing.T) {
	got := Builtins()
	if len(got) == 0 {
		t.Fatalf("Builtins() is empty")
	}
	want0 := builtinTables[0].Name
	got[0].Name = "MUTATED"
	if builtinTables[0].Name != want0 {
		t.Fatalf("mutation through Builtins() leaked: builtinTables[0].Name=%q, want %q",
			builtinTables[0].Name, want0)
	}
}
