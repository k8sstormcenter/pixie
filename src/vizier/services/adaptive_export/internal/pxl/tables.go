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

// Package pxl carries the strongly-typed list of pixie observation
// tables the adaptive-write feature targets, plus a stub Registry
// extension point for the future-PR work that lets users plug in their
// own tables alongside their UI-defined retention scripts.
//
// Importantly: the operator does NOT execute PxL itself in the current
// design. Pixie's retention plugin runs the user-defined PxL scripts
// and populates ClickHouse. This package is only used to:
//   - enumerate the pixie tables the operator is aware of
//   - keep a stable, named, audit-friendly set (no dynamic discovery)
//   - declare the future Registry extension surface
package pxl

// TableSpec is the strongly-typed identity of one pixie socket_tracer
// table the operator knows about. Bare-string identifiers are
// deliberately avoided in callers — TableSpec carries the table name
// today and is the natural place to attach future fields (column
// projections, retention TTLs, semantic tags) without breaking the API.
type TableSpec struct {
	// Name is the ClickHouse / Pixie table name. Dotted names
	// (e.g. "http2_messages.beta") are stored verbatim; backtick
	// quoting is the responsibility of SQL emitters.
	Name string

	// Protocol is the wire protocol the table observes. Documentary;
	// helps an operator audit "which tables are about HTTP".
	Protocol string
}

// builtinTables enumerates the 13 pixie socket_tracer tables the
// adaptive-write feature is shipped with. The order is stable and
// matches the project's published documentation. Do NOT loop over
// dynamic discovery to populate this — strong static definition is
// the requirement. Unexported so the slice cannot be mutated by
// external callers; use [Builtins] or [DefaultRegistry] for read
// access (both return defensive copies).
//
// conn_stats was previously out-of-scope (rev-1) but is re-added for
// the rev-2 schema — the rev-2 ClickHouse schema now carries it and the
// retention-script preset emits it alongside the protocol-events
// tables. Unlike the protocol tables it carries counters, not
// per-message rows; ClickHouse MERGEs snapshot rows over the order
// key (no aggregating engine — each retention-script pull is its own
// snapshot row).
var builtinTables = []TableSpec{
	{Name: "http_events", Protocol: "HTTP/1.x"},
	{Name: "http2_messages.beta", Protocol: "HTTP/2 + gRPC"},
	{Name: "dns_events", Protocol: "DNS"},
	{Name: "redis_events", Protocol: "Redis (RESP)"},
	{Name: "mysql_events", Protocol: "MySQL"},
	{Name: "pgsql_events", Protocol: "PostgreSQL"},
	{Name: "cql_events", Protocol: "Cassandra / CQL"},
	{Name: "mongodb_events", Protocol: "MongoDB"},
	{Name: "kafka_events.beta", Protocol: "Kafka"},
	{Name: "amqp_events", Protocol: "AMQP / RabbitMQ"},
	{Name: "mux_events", Protocol: "Mux (Twitter Finagle)"},
	{Name: "tls_events", Protocol: "TLS handshake"},
	{Name: "conn_stats", Protocol: "Connection-level statistics"},
}

// Registry is the extension surface for users to register their own
// tables alongside the built-ins. STUB — not wired into the controller
// or main.go in this PR. The intended future shape is:
//
//	ctlCfg.Registry = pxl.Compose(pxl.DefaultRegistry(), userRegistry)
//
// where Compose merges built-ins with user additions, and the
// controller iterates Registry.Tables() instead of builtinTables.
//
// Today the controller and main.go consume BuiltinTables directly.
// The future PR will plumb a Registry through controller.Config and
// rewrite the consumers.
type Registry interface {
	Tables() []TableSpec
}

// DefaultRegistry returns a Registry over the built-in tables.
// Future-PR callers compose this with user-supplied registries.
func DefaultRegistry() Registry { return defaultRegistry{} }

type defaultRegistry struct{}

// Tables returns a defensive copy so callers cannot mutate the
// package-level table list at runtime.
func (defaultRegistry) Tables() []TableSpec {
	return append([]TableSpec(nil), builtinTables...)
}

// Builtins returns a defensive copy of the built-in table list.
// Prefer this over a (now removed) exported slice so the global
// registry cannot be aliased and mutated by callers.
func Builtins() []TableSpec {
	return append([]TableSpec(nil), builtinTables...)
}

// Names projects a []TableSpec to a []string for legacy callers that
// take bare names. Useful at API boundaries that haven't been
// strong-typed yet (controller.Config.Tables is one).
func Names(specs []TableSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	return out
}

// IsBuiltin reports whether the given name is one of the built-in
// tables. Bare-string callers can use this as a defensive guard.
func IsBuiltin(name string) bool {
	for _, t := range builtinTables {
		if t.Name == name {
			return true
		}
	}
	return false
}
