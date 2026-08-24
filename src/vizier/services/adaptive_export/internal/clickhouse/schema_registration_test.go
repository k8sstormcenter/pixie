/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package clickhouse

import (
	"regexp"
	"testing"
)

// Drift guard between schema.sql and the two registration lists.
//
// schema.sql is the source of truth for DDL, but nothing is created unless the
// object is also listed: Apply iterates OperatorOwnedTables, and DDL/Columns
// resolve through KnownTables. An object that exists in schema.sql and in
// neither list is inert — and inert *silently*: no error at boot, no log line,
// just a view that is never created and panels that return nothing. That has
// happened twice (dx_base__dc_snoop, then dx_ord__{cql,mongodb,creds_change}),
// both caught on a rig rather than in CI.
//
// The reverse drift is cheaper to hit but also worth pinning: a name listed
// with no DDL behind it fails only when DDL() is called for it.

// createStmt matches the object name in `CREATE TABLE|VIEW IF NOT EXISTS
// forensic_db.<name>`, with or without the backticks used for dotted names
// (e.g. `http2_messages.beta`).
var createStmt = regexp.MustCompile("(?i)CREATE\\s+(?:TABLE|VIEW)\\s+IF\\s+NOT\\s+EXISTS\\s+forensic_db\\.`?([A-Za-z0-9_.]+)`?")

// socOwnedTables are declared in schema.sql so the operator can verify and read
// them, but are created by the soc/clickhouse-lab installer — AE must never
// issue their CREATE TABLE. See TestOperatorOwnedTables_DoesNotIncludeKubescape.
var socOwnedTables = map[string]bool{
	"alerts":         true,
	"kubescape_logs": true,
}

func schemaObjects(t *testing.T) map[string]bool {
	t.Helper()
	objs := map[string]bool{}
	for _, m := range createStmt.FindAllStringSubmatch(canonicalSchema, -1) {
		objs[m[1]] = true
	}
	if len(objs) == 0 {
		t.Fatal("parsed no CREATE statements out of schema.sql — the regex or the file shape changed")
	}
	return objs
}

func TestEverySchemaObjectIsRegistered(t *testing.T) {
	known := map[string]bool{}
	for _, n := range KnownTables {
		known[n] = true
	}
	owned := map[string]bool{}
	for _, n := range OperatorOwnedTables {
		owned[n] = true
	}

	for name := range schemaObjects(t) {
		if !known[name] {
			t.Errorf("%q is in schema.sql but missing from KnownTables — DDL(%q) will not resolve it", name, name)
		}
		if socOwnedTables[name] {
			if owned[name] {
				t.Errorf("%q is soc-owned; AE must not create it", name)
			}
			continue
		}
		if !owned[name] {
			t.Errorf("%q is in schema.sql but missing from OperatorOwnedTables — Apply will never create it, "+
				"and nothing will report that at boot", name)
		}
	}
}

func TestEveryRegisteredNameHasDDL(t *testing.T) {
	objs := schemaObjects(t)
	for _, name := range KnownTables {
		if !objs[name] {
			t.Errorf("%q is in KnownTables but has no CREATE statement in schema.sql", name)
		}
	}
	for _, name := range OperatorOwnedTables {
		if !objs[name] {
			t.Errorf("%q is in OperatorOwnedTables but has no CREATE statement in schema.sql", name)
		}
	}
}
