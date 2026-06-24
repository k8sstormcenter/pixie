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

package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/kubescape"
)

// Differential oracle for the trigger's incremental kubescape-events pump.
//
// The trigger is incremental: it polls CH for rows with
// `<normalised_event_time> >= watermark`, advances the in-memory watermark
// to the maximum of consumed rows' normalised event_time, and dedupes by
// fingerprint at the boundary so a row that crosses two polls (same
// event_time as the watermark) isn't emitted twice.
//
// Three independent moving parts: watermark advancement, boundary
// fingerprint dedup, and PollLimit-saturated draining (PR #67 fix). A
// classical mocks-and-assert test exercises each in isolation. This
// oracle pins them TOGETHER against the simplest possible reference:
// "consume everything in event_time order, dedupe by fingerprint,
// advance the cursor to max(event_time)." If the iterative trigger and
// the reference disagree on the set of emitted rows for ANY poll
// sequence, one of the three moving parts is wrong.
//
// The reference is intentionally PxL-free and stateless across the
// `allRows` corpus — the entire spec is six lines of Go in
// naiveTriggerReference below. Anything more complex would be testing
// the test, not the trigger.

// rowsForPoll is the per-poll subset of the corpus the mock returns,
// reflecting ClickHouse's behaviour: filter rows where
// normalizeEventTimeNanos(event_time) >= watermark, order by the same,
// then truncate to LIMIT (PollLimit). Deterministic given (corpus, wm).
func rowsForPoll(corpus []kubescape.Row, watermark uint64, limit int) []kubescape.Row {
	filtered := make([]kubescape.Row, 0, len(corpus))
	for _, r := range corpus {
		if normalizeEventTimeNanos(r.EventTime) >= watermark {
			filtered = append(filtered, r)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return normalizeEventTimeNanos(filtered[i].EventTime) <
			normalizeEventTimeNanos(filtered[j].EventTime)
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

// naiveTriggerReference is the spec: drain `corpus` in event_time order,
// dedupe by fingerprint, emit every row above `start` exactly once.
// Returns the emitted rows AND the final watermark — both are what the
// iterative trigger MUST converge to no matter how the polls slice up
// the corpus.
func naiveTriggerReference(corpus []kubescape.Row, start uint64) ([]kubescape.Row, uint64) {
	sorted := make([]kubescape.Row, len(corpus))
	copy(sorted, corpus)
	sort.SliceStable(sorted, func(i, j int) bool {
		return normalizeEventTimeNanos(sorted[i].EventTime) <
			normalizeEventTimeNanos(sorted[j].EventTime)
	})
	cursor := start
	seen := map[string]bool{}
	var out []kubescape.Row
	for _, r := range sorted {
		etn := normalizeEventTimeNanos(r.EventTime)
		if etn < cursor {
			continue
		}
		fp := rowFingerprint(r)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		out = append(out, r)
		if etn > cursor {
			cursor = etn
		}
	}
	return out, cursor
}

// mockKubescapeLogs serves the iterative trigger from a fixed corpus.
// The query SQL embeds the watermark + LIMIT; the handler extracts
// them via the same chNormEventTimeNanos shape the trigger emits and
// responds with the filtered/ordered/limited slice as JSONEachRow.
func mockKubescapeLogs(t *testing.T, corpus []kubescape.Row) *httptest.Server {
	t.Helper()
	// `... ) >= <watermark> ORDER BY ... LIMIT <N>`
	re := regexp.MustCompile(`\) >= (\d+) ORDER BY .* LIMIT (\d+)`)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		m := re.FindStringSubmatch(q)
		if m == nil {
			http.Error(w, "unexpected SQL: "+q, http.StatusBadRequest)
			return
		}
		watermark, _ := strconv.ParseUint(m[1], 10, 64)
		limit, _ := strconv.Atoi(m[2])
		rows := rowsForPoll(corpus, watermark, limit)
		for _, row := range rows {
			b, _ := json.Marshal(map[string]any{
				"RuleID":                row.RuleID,
				"RuntimeK8sDetails":     row.K8sDetails,
				"RuntimeProcessDetails": row.ProcessDetails,
				// the trigger parses event_time as a JSON number OR a
				// quoted string; CH returns the latter for UInt64. Match
				// CH's wire shape so we stay faithful to production.
				"event_time": strconv.FormatUint(row.EventTime, 10),
				"hostname":   row.Hostname,
			})
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n"))
		}
	}))
}

// runTriggerAgainstCorpus drives the iterative trigger over `corpus`
// for up to `maxPolls * pollInterval` and returns the emitted events.
// Terminates as soon as the trigger emits len(naive) events OR maxPolls
// elapses (the second case is a real failure: the trigger missed rows
// the naive reference saw).
func runTriggerAgainstCorpus(
	t *testing.T,
	corpus []kubescape.Row,
	pollLimit int,
	expectedCount int,
) []kubescape.Event {
	t.Helper()
	srv := mockKubescapeLogs(t, corpus)
	defer srv.Close()

	tr, err := New(Config{
		Endpoint:     srv.URL,
		Hostname:     "node-1",
		PollInterval: 5 * time.Millisecond, // tight: many polls per second
		PollLimit:    pollLimit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := tr.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	var emitted []kubescape.Event
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for len(emitted) < expectedCount {
		select {
		case ev, ok := <-ch:
			if !ok {
				return emitted
			}
			emitted = append(emitted, ev)
		case <-timeout.C:
			t.Fatalf("trigger emitted %d events, want %d (missed rows under PollLimit=%d)",
				len(emitted), expectedCount, pollLimit)
		}
	}
	return emitted
}

// makeRow constructs a minimally-valid kubescape Row. event_time must
// already be in nanos for normalizeEventTimeNanos to be a no-op on it
// (>= 1e13 ⇒ already nanos), making the corpus's natural ordering match
// the SQL's ORDER BY chNormEventTimeNanos.
func makeRow(eventTimeNanos uint64, ruleID, pod string) kubescape.Row {
	k8sJSON, _ := json.Marshal(map[string]string{
		"podName":      pod,
		"podNamespace": "ns-" + pod,
	})
	procJSON, _ := json.Marshal(map[string]any{
		"processTree": map[string]any{
			"pid":  100 + eventTimeNanos%900,
			"comm": "proc-" + ruleID,
		},
	})
	return kubescape.Row{
		EventTime:      eventTimeNanos,
		RuleID:         ruleID,
		Hostname:       "node-1",
		K8sDetails:     string(k8sJSON),
		ProcessDetails: string(procJSON),
	}
}

// fingerprintSet collects rowFingerprint over a row slice. Set
// equality is what the oracle checks: trigger's emitted-event order
// is per-poll, but the UNION of polls must match the corpus minus
// dups, regardless of how the polls sliced it up.
func fingerprintSet(rows []kubescape.Row) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[rowFingerprint(r)] = true
	}
	return out
}

// eventFingerprintSet derives the same set via reconstructing each
// emitted Event's source Row. The (EventTime, RuleID) tuple is the
// natural key — multiple boundary rows share EventTime but their
// RuleIDs are unique in our test corpora (and unique-by-rule in the
// production kubescape feed, where two events at the same nanosecond
// have at minimum distinct rule IDs).
func eventFingerprintSet(rows []kubescape.Row, events []kubescape.Event) map[string]bool {
	type key struct {
		et   uint64
		rule string
	}
	idx := map[key]kubescape.Row{}
	for _, r := range rows {
		idx[key{r.EventTime, r.RuleID}] = r
	}
	out := make(map[string]bool, len(events))
	for _, e := range events {
		if r, ok := idx[key{e.EventTime, e.RuleID}]; ok {
			out[rowFingerprint(r)] = true
		}
	}
	return out
}

// TestOracle_TriggerEmitsNaiveSet_StaggeredCorpus drives 50 rows
// scattered across event_times with NO duplicates; PollLimit=10 forces
// ≥5 polls, exercising watermark advancement repeatedly. The trigger
// must emit exactly the 50 rows the naive reference computes.
func TestOracle_TriggerEmitsNaiveSet_StaggeredCorpus(t *testing.T) {
	const base = uint64(1_700_000_000_000_000_000)
	var corpus []kubescape.Row
	for i := uint64(0); i < 50; i++ {
		// Unique event_times, 1 ms apart.
		corpus = append(corpus, makeRow(base+i*1_000_000, fmt.Sprintf("R%03d", i), fmt.Sprintf("pod-%d", i)))
	}
	naive, _ := naiveTriggerReference(corpus, 0)
	got := runTriggerAgainstCorpus(t, corpus, 10, len(naive))

	want := fingerprintSet(naive)
	have := eventFingerprintSet(corpus, got)
	if len(want) != len(have) {
		t.Fatalf("set sizes differ: want=%d have=%d", len(want), len(have))
	}
	for fp := range want {
		if !have[fp] {
			t.Fatalf("trigger missed fingerprint %s (naive emitted, trigger didn't)", fp)
		}
	}
}

// TestOracle_PollLimitSaturation_AtCapacity is the regression guard for
// PR #67 (dfdc465a9): when EXACTLY PollLimit rows share the boundary
// event_time, every one of them must emit, and the cursor must clear
// the boundary for the next-event_time row that follows. Equivalent to
// the naive reference on this corpus shape.
//
// (The complementary OVERFLOW case — >PollLimit boundary rows — is the
// documented data-loss trade-off in PR #67's commit message: the
// trigger advances the watermark by 1ns to escape the infinite-stuck
// boundary, and the surplus rows beyond PollLimit at that nanosecond
// are intentionally not re-delivered. TestOracle_PollLimitOverflow
// below pins THAT behaviour so any future fix that recovers the lost
// rows must update both tests together.)
func TestOracle_PollLimitSaturation_AtCapacity(t *testing.T) {
	const base = uint64(1_700_000_000_000_000_000)
	const pollLimit = 5
	var corpus []kubescape.Row
	for i := uint64(0); i < pollLimit; i++ { // EXACTLY PollLimit at the boundary
		corpus = append(corpus, makeRow(base, fmt.Sprintf("R%03d", i), fmt.Sprintf("pod-%d", i)))
	}
	corpus = append(corpus, makeRow(base+1_000_000, "Rfollow", "pod-follow"))

	naive, _ := naiveTriggerReference(corpus, 0)
	if len(naive) != pollLimit+1 {
		t.Fatalf("naive should emit %d; got %d", pollLimit+1, len(naive))
	}
	got := runTriggerAgainstCorpus(t, corpus, pollLimit, len(naive))

	want := fingerprintSet(naive)
	have := eventFingerprintSet(corpus, got)
	for fp := range want {
		if !have[fp] {
			t.Fatalf("PollLimit-at-capacity lost fingerprint %s (PR #67 regression)", fp)
		}
	}
}

// TestOracle_PollLimitOverflow_DocumentsLossBound asserts the
// documented trade-off in PR #67 (dfdc465a9): when >PollLimit rows
// share the boundary event_time, the trigger emits the FIRST PollLimit
// of them, then the 1ns escape advances the cursor past the rest. The
// surplus rows are lost — by design — to avoid an infinite stuck
// boundary.
//
// Why this is an explicit test, not a TODO comment: if a future PR
// "fixes" the overflow loss (e.g. by adding a secondary ORDER BY key),
// this test will fail loudly, which is the right signal — both the
// fix AND this assertion need to update together. Without this guard,
// the loss can regress silently in either direction.
func TestOracle_PollLimitOverflow_DocumentsLossBound(t *testing.T) {
	const base = uint64(1_700_000_000_000_000_000)
	const pollLimit = 5
	const overflow = 25
	var corpus []kubescape.Row
	for i := uint64(0); i < overflow; i++ { // 5× PollLimit at one event_time
		corpus = append(corpus, makeRow(base, fmt.Sprintf("R%03d", i), fmt.Sprintf("pod-%d", i)))
	}
	corpus = append(corpus, makeRow(base+1_000_000, "Rfollow", "pod-follow"))

	srv := mockKubescapeLogs(t, corpus)
	defer srv.Close()
	tr, err := New(Config{
		Endpoint: srv.URL, Hostname: "node-1",
		PollInterval: 5 * time.Millisecond, PollLimit: pollLimit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	// Wait for the trigger to settle: pollLimit emissions at the
	// boundary, then the escape, then Rfollow. Total = pollLimit+1.
	// Anything more would mean the surplus came through (an upgrade),
	// anything less would mean the escape ate Rfollow too (a regression).
	collected := []kubescape.Event{}
	deadline := time.After(2 * time.Second)
	for len(collected) < pollLimit+1 {
		select {
		case ev := <-ch:
			collected = append(collected, ev)
		case <-deadline:
			break
		}
	}
	// Extra-drain pass to catch any late surplus emissions.
	timeout := time.NewTimer(200 * time.Millisecond)
	defer timeout.Stop()
DRAIN:
	for {
		select {
		case ev := <-ch:
			collected = append(collected, ev)
		case <-timeout.C:
			break DRAIN
		}
	}

	if len(collected) != pollLimit+1 {
		t.Fatalf("emitted %d events; want exactly %d (PollLimit at boundary + Rfollow). "+
			"More ⇒ overflow recovery landed (good — update this test). "+
			"Less ⇒ Rfollow lost (regression in the 1ns escape).",
			len(collected), pollLimit+1)
	}
	// Of the pollLimit boundary emissions, all should be DISTINCT.
	seen := map[uint64]map[string]bool{} // event_time → ruleID seen
	for _, e := range collected {
		if seen[e.EventTime] == nil {
			seen[e.EventTime] = map[string]bool{}
		}
		if seen[e.EventTime][e.RuleID] {
			t.Fatalf("duplicate emission RuleID=%s at event_time=%d", e.RuleID, e.EventTime)
		}
		seen[e.EventTime][e.RuleID] = true
	}
}

// TestOracle_BoundaryDedup_NoDuplicates probes the cross-poll dedup
// machinery: identical rows are returned in two consecutive polls
// (mock holds state, sees the second poll's watermark equals the first
// poll's max event_time, and re-returns the boundary row). The trigger's
// seenAtBoundary map must filter the duplicate.
func TestOracle_BoundaryDedup_NoDuplicates(t *testing.T) {
	const base = uint64(1_700_000_000_000_000_000)
	corpus := []kubescape.Row{
		makeRow(base, "R001", "pod-a"),
		makeRow(base, "R002", "pod-b"), // same event_time
		makeRow(base+1_000_000, "R003", "pod-c"),
	}
	// mock with rate-limited delivery: returns first 2 on first poll,
	// then on the second poll (watermark = base) returns ALL 3 again,
	// then exhausts. The trigger should emit each row exactly once.
	srv := stutteringMock(t, corpus, base)
	defer srv.Close()

	tr, err := New(Config{
		Endpoint:     srv.URL,
		Hostname:     "node-1",
		PollInterval: 5 * time.Millisecond,
		PollLimit:    10,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	emitted := map[string]int{}
	deadline := time.After(2 * time.Second)
	for len(emitted) < 3 {
		select {
		case ev := <-ch:
			// Reconstruct the row by event_time + RuleID to count
			// per-fingerprint occurrences.
			emitted[ev.RuleID]++
		case <-deadline:
			t.Fatalf("emitted=%v, want 3 unique events", emitted)
		}
	}
	for rule, n := range emitted {
		if n != 1 {
			t.Fatalf("rule %s emitted %d times, want 1 (boundary-dedup regression)", rule, n)
		}
	}
}

// stutteringMock returns the first 2 rows on the first poll and ALL 3
// on every subsequent poll — simulating CH returning a duplicate at
// the watermark boundary (e.g., because a new row landed with the same
// event_time after our previous poll's cursor advanced past it).
func stutteringMock(t *testing.T, corpus []kubescape.Row, _ uint64) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	callCount := 0
	re := regexp.MustCompile(`\) >= (\d+) ORDER BY .* LIMIT (\d+)`)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		thisCall := callCount
		mu.Unlock()
		q := r.URL.Query().Get("query")
		m := re.FindStringSubmatch(q)
		if m == nil {
			http.Error(w, "unexpected SQL", http.StatusBadRequest)
			return
		}
		watermark, _ := strconv.ParseUint(m[1], 10, 64)
		var send []kubescape.Row
		if thisCall == 1 {
			// Only the first 2 (same event_time as the boundary row).
			send = corpus[:2]
		} else {
			send = rowsForPoll(corpus, watermark, 10)
		}
		for _, row := range send {
			b, _ := json.Marshal(map[string]any{
				"RuleID":                row.RuleID,
				"RuntimeK8sDetails":     row.K8sDetails,
				"RuntimeProcessDetails": row.ProcessDetails,
				"event_time":            strconv.FormatUint(row.EventTime, 10),
				"hostname":              row.Hostname,
			})
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n"))
		}
	}))
}
