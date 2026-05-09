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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// fixed reference time for deterministic relStart computation.
var (
	fixedNow   = time.Date(2026, 5, 9, 15, 23, 44, 0, time.UTC)
	fixedStart = fixedNow.Add(-5 * time.Minute) // ATTACK − 5 min
	fixedEnd   = fixedNow.Add(5 * time.Minute)  // ATTACK + 5 min
	target     = anomaly.Target{
		PID: 12345, Comm: "redis-server",
		Pod: "redis-6fbcfb97c-82qxv", Namespace: "redis",
	}
)

// TestQueryFor_UnknownTable — non-builtin tables wrap ErrUnknownTable.
func TestQueryFor_UnknownTable(t *testing.T) {
	_, err := QueryFor("nope_table", target, fixedStart, fixedEnd, fixedNow)
	if err == nil || !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("want ErrUnknownTable wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), `"nope_table"`) {
		t.Fatalf("error must echo the bad table name; got %v", err)
	}
}

// TestQueryFor_NamespacedPodFilter — px.upid_to_pod_name returns
// "<namespace>/<pod>" (verified in carnot's metadata_ops.h:387). The
// generated PxL must filter against the namespaced key when both
// fields are non-empty.
func TestQueryFor_NamespacedPodFilter(t *testing.T) {
	q, err := QueryFor("redis_events", target, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	wantPodFilter := `df = df[df.pod == 'redis/redis-6fbcfb97c-82qxv']`
	if !strings.Contains(q, wantPodFilter) {
		t.Fatalf("expected pod filter %q in:\n%s", wantPodFilter, q)
	}
	wantNS := `df = df[df.namespace == 'redis']`
	if !strings.Contains(q, wantNS) {
		t.Fatalf("expected namespace filter %q in:\n%s", wantNS, q)
	}
}

// TestQueryFor_NamespaceOnly — only namespace filter when Pod is empty.
func TestQueryFor_NamespaceOnly(t *testing.T) {
	tNoPod := anomaly.Target{Namespace: "redis"}
	q, err := QueryFor("redis_events", tNoPod, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if !strings.Contains(q, `df = df[df.namespace == 'redis']`) {
		t.Fatalf("expected namespace filter; got:\n%s", q)
	}
	if strings.Contains(q, "df = df[df.pod ==") {
		t.Fatalf("did not expect pod filter when Pod is empty; got:\n%s", q)
	}
}

// TestQueryFor_PodOnly — when Namespace is empty but Pod is set, fall
// back to a bare-pod filter (won't match in pixie since upid_to_pod_name
// always returns namespaced; documented as caller-shouldn't-do-this).
func TestQueryFor_PodOnly(t *testing.T) {
	tNoNS := anomaly.Target{Pod: "redis-foo"}
	q, err := QueryFor("redis_events", tNoNS, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if !strings.Contains(q, `df = df[df.pod == 'redis-foo']`) {
		t.Fatalf("expected bare pod filter; got:\n%s", q)
	}
	if strings.Contains(q, "df = df[df.namespace ==") {
		t.Fatalf("did not expect namespace filter; got:\n%s", q)
	}
}

// TestQueryFor_NoTargetFilters — empty Target → no namespace OR pod
// filter (caller-driven coarse query).
func TestQueryFor_NoTargetFilters(t *testing.T) {
	q, err := QueryFor("redis_events", anomaly.Target{}, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if strings.Contains(q, "df.namespace ==") || strings.Contains(q, "df.pod ==") {
		t.Fatalf("expected no namespace/pod filter for empty Target; got:\n%s", q)
	}
}

// TestQueryFor_TimeBoundsAreInclusiveLowerExclusiveUpper — sliceStart
// is `>=`; sliceEnd is `<`. Encoded as nanos.
func TestQueryFor_TimeBoundsAreInclusiveLowerExclusiveUpper(t *testing.T) {
	q, err := QueryFor("redis_events", target, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	// Derive the expected boundary calls from the fixtures so the
	// assertion stays valid if anyone retunes the reference window.
	wantLower := fmt.Sprintf("df = df[df.time_ >= px.int64_to_time(%d)]", fixedStart.UnixNano())
	wantUpper := fmt.Sprintf("df = df[df.time_ <  px.int64_to_time(%d)]", fixedEnd.UnixNano())
	if !strings.Contains(q, wantLower) {
		t.Fatalf("expected lower bound %q in:\n%s", wantLower, q)
	}
	if !strings.Contains(q, wantUpper) {
		t.Fatalf("expected upper bound %q in:\n%s", wantUpper, q)
	}
}

// TestQueryFor_RelativeStartTime — pad covers (now − sliceStart) plus
// 30 s. With ATTACK − 5min as sliceStart and now == ATTACK, pad is
// 5 min + 30 s = 330 s.
func TestQueryFor_RelativeStartTime(t *testing.T) {
	q, err := QueryFor("redis_events", target, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if !strings.Contains(q, "start_time='-330s'") {
		t.Fatalf("expected start_time='-330s' in:\n%s", q)
	}
}

// TestQueryFor_PadFloorOn30sWhenSliceStartIsFuture — caller-bug case;
// pad clamps to 30 s rather than emitting a positive (forward) start.
func TestQueryFor_PadFloorOn30sWhenSliceStartIsFuture(t *testing.T) {
	futureStart := fixedNow.Add(1 * time.Minute) // sliceStart > now
	q, err := QueryFor("redis_events", target, futureStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if !strings.Contains(q, "start_time='-30s'") {
		t.Fatalf("expected start_time='-30s' clamp in:\n%s", q)
	}
}

// TestQueryFor_EscapesSingleQuoteInTarget — apostrophes in pod /
// namespace get backslash-escaped so they don't break out of the
// PxL string literal.
func TestQueryFor_EscapesSingleQuoteInTarget(t *testing.T) {
	tWeird := anomaly.Target{Namespace: "ns'with'quotes", Pod: "p'od"}
	q, err := QueryFor("redis_events", tWeird, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if !strings.Contains(q, `df = df[df.namespace == 'ns\'with\'quotes']`) {
		t.Fatalf("expected escaped namespace; got:\n%s", q)
	}
	if !strings.Contains(q, `df = df[df.pod == 'ns\'with\'quotes/p\'od']`) {
		t.Fatalf("expected escaped namespaced pod key; got:\n%s", q)
	}
}

// TestQueryFor_EscapesBackslashInTarget — backslashes too. Asserts
// both namespace and the namespaced pod-key forms are escaped, so a
// `Pod` containing `\` can't terminate the PxL string literal.
func TestQueryFor_EscapesBackslashInTarget(t *testing.T) {
	tWeird := anomaly.Target{Namespace: `ns\back`, Pod: `p\od`}
	q, err := QueryFor("redis_events", tWeird, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if !strings.Contains(q, `df = df[df.namespace == 'ns\\back']`) {
		t.Fatalf("expected escaped namespace; got:\n%s", q)
	}
	if !strings.Contains(q, `df = df[df.pod == 'ns\\back/p\\od']`) {
		t.Fatalf("expected escaped namespaced pod key; got:\n%s", q)
	}
}

// TestQueryFor_EveryBuiltinTableEmits — smoke-test all known tables
// produce a syntactically-shaped PxL output (compile-not-tested).
// Fails fast if BuiltinTables ever becomes empty so the smoke loop
// can't silently no-op.
func TestQueryFor_EveryBuiltinTableEmits(t *testing.T) {
	tables := Names(BuiltinTables)
	if len(tables) == 0 {
		t.Fatal("BuiltinTables is empty — smoke loop would no-op")
	}
	for _, table := range tables {
		q, err := QueryFor(table, target, fixedStart, fixedEnd, fixedNow)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
		if !strings.HasPrefix(q, "import px\n") {
			t.Fatalf("table %s: expected import px header; got:\n%s", table, q)
		}
		if !strings.Contains(q, "px.display(df, '"+table+"')") {
			t.Fatalf("table %s: expected px.display call with table name; got:\n%s", table, q)
		}
	}
}

// TestEscapePxL_TableDriven — direct coverage of the escaper.
func TestEscapePxL_TableDriven(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"o'malley", `o\'malley`},
		{`back\slash`, `back\\slash`},
		{`mix'and\back`, `mix\'and\\back`},
		{"'; DROP TABLE alerts; --", `\'; DROP TABLE alerts; --`},
	}
	for _, c := range cases {
		if got := escapePxL(c.in); got != c.want {
			t.Errorf("escapePxL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
