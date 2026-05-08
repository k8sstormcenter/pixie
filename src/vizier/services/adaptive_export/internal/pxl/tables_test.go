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
// The set is the 12 socket_tracer tables in pixie's stirling layer
// (http_events, http2_messages.beta, dns_events, redis_events,
// mysql_events, pgsql_events, cql_events, mongodb_events,
// kafka_events.beta, amqp_events, mux_events, tls_events). Update
// this guard if the spec adds / removes a table.
func TestBuiltinTables_Count(t *testing.T) {
	const want = 12
	if got := len(BuiltinTables); got != want {
		t.Fatalf("BuiltinTables = %d entries, want %d", got, want)
	}
}

// TestBuiltinTables_AllNamesUnique — no duplicates.
func TestBuiltinTables_AllNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, sp := range BuiltinTables {
		if seen[sp.Name] {
			t.Fatalf("duplicate table %q in BuiltinTables", sp.Name)
		}
		seen[sp.Name] = true
	}
}

// TestBuiltinTables_AllHaveProtocol — each entry is annotated, so audit
// queries like "which tables observe HTTP?" work without parsing the name.
func TestBuiltinTables_AllHaveProtocol(t *testing.T) {
	for _, sp := range BuiltinTables {
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
	if IsBuiltin("conn_stats") {
		t.Fatalf("conn_stats is no longer in scope; should NOT be builtin")
	}
	if IsBuiltin("") {
		t.Fatalf("empty string should not be builtin")
	}
}

// TestDefaultRegistry — stub returns BuiltinTables.
func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()
	got := r.Tables()
	if len(got) != len(BuiltinTables) {
		t.Fatalf("DefaultRegistry().Tables() len %d, want %d", len(got), len(BuiltinTables))
	}
	for i, sp := range BuiltinTables {
		if got[i] != sp {
			t.Fatalf("DefaultRegistry().Tables()[%d] = %+v, want %+v", i, got[i], sp)
		}
	}
}

// TestNames — projection to []string preserves order.
func TestNames(t *testing.T) {
	names := Names(BuiltinTables)
	if len(names) != len(BuiltinTables) {
		t.Fatalf("Names len mismatch")
	}
	if names[0] != "http_events" {
		t.Fatalf("first name = %q, want http_events", names[0])
	}
}
