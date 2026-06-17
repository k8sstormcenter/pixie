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

// TestCompilePassthrough_MatchesQueryFor is the behaviour-preservation
// proof: rendering a precompiled template for a window must produce the
// EXACT bytes QueryFor emits for an empty Target over that same window.
// If this holds, the compiled firehose path is a pure structural change —
// it cannot capture differently than the legacy path it replaces.
func TestCompilePassthrough_MatchesQueryFor(t *testing.T) {
	window := 3 * time.Minute
	// Fixed instant so UnixNano bounds are deterministic.
	now := time.Unix(1778339984, 0).UTC()
	sliceStart := now.Add(-window)
	sliceEnd := now

	legacy, err := QueryFor("http_events", anomaly.Target{}, sliceStart, sliceEnd, now)
	if err != nil {
		t.Fatalf("QueryFor: %v", err)
	}
	tmpl, err := CompilePassthrough("http_events", window)
	if err != nil {
		t.Fatalf("CompilePassthrough: %v", err)
	}
	got := Render(tmpl, sliceStart, sliceEnd)
	if got != legacy {
		t.Fatalf("rendered template != QueryFor\n--- compiled ---\n%s\n--- legacy ---\n%s", got, legacy)
	}
}

// TestCompilePassthrough_Shape pins the essential tokens so an accidental
// edit to the template (dropped time bound, lost upid resolution) fails
// loudly even without the byte-equality oracle above.
func TestCompilePassthrough_Shape(t *testing.T) {
	tmpl, err := CompilePassthrough("dns_events", 60*time.Second)
	if err != nil {
		t.Fatalf("CompilePassthrough: %v", err)
	}
	for _, want := range []string{
		"px.DataFrame(table='dns_events', start_time='-90s')", // window 60s + 30s pad
		"df.time_ >= px.int64_to_time(%d)",
		"df.time_ <  px.int64_to_time(%d)",
		"px.upid_to_namespace(df.upid)",
		"px.upid_to_pod_name(df.upid)",
		"px.display(df, 'dns_events')",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template missing %q:\n%s", want, tmpl)
		}
	}
	// Exactly two %d verbs (the two time bounds) — nothing else parameterized.
	if n := strings.Count(tmpl, "%d"); n != 2 {
		t.Errorf("template has %d %%d verbs, want 2:\n%s", n, tmpl)
	}
}

// TestCompilePassthrough_UnknownTable rejects non-builtin tables, matching
// QueryFor's contract.
func TestCompilePassthrough_UnknownTable(t *testing.T) {
	_, err := CompilePassthrough("not_a_table", time.Second)
	if !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("err=%v want ErrUnknownTable", err)
	}
}
