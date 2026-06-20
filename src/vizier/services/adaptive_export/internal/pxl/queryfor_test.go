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
// back to a regex match on `*/<pod>` since px.upid_to_pod_name always
// returns "<namespace>/<pod>" — a bare-pod equality filter would always
// miss. The defensive path stays usable instead of being silently broken.
func TestQueryFor_PodOnly(t *testing.T) {
	tNoNS := anomaly.Target{Pod: "redis-foo"}
	q, err := QueryFor("redis_events", tNoNS, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	// Must NOT emit the bare-pod equality (CR: that's a known-miss filter).
	if strings.Contains(q, `df = df[df.pod == 'redis-foo']`) {
		t.Fatalf("regression: emitted bare-pod equality that always misses:\n%s", q)
	}
	// Must emit a working filter that matches "<any-ns>/redis-foo".
	want := `df = df[px.regex_match('^[^/]+/redis-foo$', df.pod)]`
	if !strings.Contains(q, want) {
		t.Fatalf("expected regex-anchored pod filter\nwant: %s\ngot:\n%s", want, q)
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
	wantLower := `df = df[df.time_ >= px.int64_to_time(1778339924000000000)]` // 15:18:44 UTC ns
	wantUpper := `df = df[df.time_ <  px.int64_to_time(1778340524000000000)]` // 15:28:44 UTC ns
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
func TestQueryFor_EveryBuiltinTableEmits(t *testing.T) {
	for _, table := range Names(builtinTables) {
		q, err := QueryFor(table, target, fixedStart, fixedEnd, fixedNow)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
		if !strings.HasPrefix(q, "#px:set max_output_rows_per_table=1000000\nimport px\n") {
			t.Fatalf("table %s: expected #px:set cap header then import px; got:\n%s", table, q)
		}
		if !strings.Contains(q, "px.display(df, '"+table+"')") {
			t.Fatalf("table %s: expected px.display call with table name; got:\n%s", table, q)
		}
	}
}

// TestEscapePxL_TableDriven — direct coverage of the escaper. Every byte
// that could break out of a single-quoted PxL string literal must come
// back as a non-breaking escape sequence.
func TestEscapePxL_TableDriven(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"o'malley", `o\'malley`},
		{`back\slash`, `back\\slash`},
		{`mix'and\back`, `mix\'and\\back`},
		{"'; DROP TABLE alerts; --", `\'; DROP TABLE alerts; --`},
		// Byte-level string-breaking attempts: a raw \n would terminate
		// the PxL statement and inject a new one on the next line. The
		// escaper turns these into Python-style escape sequences that
		// PxL renders as inert backslash-letter pairs inside the string.
		{"line1\nline2", `line1\nline2`},
		{"line1\r\nline2", `line1\r\nline2`},
		{"col1\tcol2", `col1\tcol2`},
		{"trailing\x00", `trailing\0`},
		// The full injection probe targeting Target.Pod/Target.Namespace:
		// close the literal, inject a new statement, comment out the
		// trailing fragment. The escaper neutralises the close + newline;
		// the trailing # stays as a literal '#' inside the string.
		{"redis-pod', exec('rm -rf /'), '\n#", `redis-pod\', exec(\'rm -rf /\'), \'\n#`},
	}
	for _, c := range cases {
		if got := escapePxL(c.in); got != c.want {
			t.Errorf("escapePxL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestQueryFor_RejectsInjectionInTargetFields drives QueryFor with
// adversarial Pod/Namespace values and asserts the resulting PxL has
// EXACTLY the line count of a clean call — proving an injected newline
// can't add a statement, and the embedded literal stays single-quoted.
//
// PxL line breakdown for a fully-populated Target (cf. QueryFor):
//
//	#px:set ...                               1
//	import px                                 1
//	df = px.DataFrame(...)                    1
//	df = df[df.time_ >= ...]                  1
//	df = df[df.time_ <  ...]                  1
//	df.namespace = px.upid_to_namespace(...)  1
//	df.pod = px.upid_to_pod_name(...)         1
//	df = df[df.namespace == '...']            1
//	df = df[df.pod == '...']                  1
//	px.display(df, '...')                     1
//	(trailing newline → empty 11th split)     1
//
// Total: 10 statements + trailing empty == strings.Split == 11 entries.
func TestQueryFor_RejectsInjectionInTargetFields(t *testing.T) {
	const wantLines = 11

	cases := []struct {
		name   string
		target anomaly.Target
	}{
		{
			name:   "newline-in-pod",
			target: anomaly.Target{Pod: "p\n', exec('rm -rf /'), '", Namespace: "ns"},
		},
		{
			name:   "newline-in-namespace",
			target: anomaly.Target{Pod: "p", Namespace: "ns\n', exec('rm -rf /'), '"},
		},
		{
			name:   "single-quote-only",
			target: anomaly.Target{Pod: "p'); display('owned", Namespace: "ns"},
		},
		{
			name:   "carriage-return",
			target: anomaly.Target{Pod: "p\rexec('owned')", Namespace: "ns"},
		},
		{
			name:   "backslash-escape-of-escape",
			target: anomaly.Target{Pod: `p\', exec('owned'), \'`, Namespace: "ns"},
		},
		{
			name:   "null-byte",
			target: anomaly.Target{Pod: "p\x00bonus", Namespace: "ns"},
		},
		{
			name:   "tab-bytes",
			target: anomaly.Target{Pod: "p\texec('owned')", Namespace: "ns"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, err := QueryFor("http_events", c.target, fixedStart, fixedEnd, fixedNow)
			if err != nil {
				t.Fatalf("QueryFor: %v", err)
			}
			if got := strings.Count(q, "\n") + 1; got != wantLines {
				t.Fatalf("got %d lines, want %d (injection succeeded?)\n%s", got, wantLines, q)
			}
			// The exact statement count: each line must start with
			// either #px:, import, df, or px.display — anything else is
			// a smuggled call.
			for i, line := range strings.Split(q, "\n") {
				if line == "" {
					continue
				}
				if !(strings.HasPrefix(line, "#px:") ||
					strings.HasPrefix(line, "import ") ||
					strings.HasPrefix(line, "df") ||
					strings.HasPrefix(line, "px.display")) {
					t.Fatalf("line %d looks injected: %q\nfull script:\n%s", i, line, q)
				}
			}
		})
	}
}

// TestQueryFor_PodOnlyRegexEscapesQuoteMetaInjection — the bare-pod
// fallback uses regexp.QuoteMeta + escapePxL; verify a pod name carrying
// regex meta chars + a single quote both survive without breaking out
// of the px.regex_match literal.
func TestQueryFor_PodOnlyRegexEscapesQuoteMetaInjection(t *testing.T) {
	tgt := anomaly.Target{Pod: "p.*'; exec('owned')"}
	q, err := QueryFor("http_events", tgt, fixedStart, fixedEnd, fixedNow)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	if strings.Contains(q, "exec(") || strings.Count(q, "\n") > 9 {
		t.Fatalf("pod-only path injection succeeded:\n%s", q)
	}
}
