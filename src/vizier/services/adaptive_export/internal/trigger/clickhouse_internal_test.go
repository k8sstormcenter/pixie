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
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNormalizeEventTimeNanos pins the unit normalization at the current epoch
// (the magnitude heuristic is exact for present-day timestamps). This is the
// core of the F8 fix: seconds, millis and nanos all map to the SAME nanos scale,
// so a mixed-unit row cannot drive the watermark past real seconds rows.
func TestNormalizeEventTimeNanos(t *testing.T) {
	const sec = uint64(1781590000)            // ~now in seconds
	const milli = uint64(1781590000_000)      // same instant in millis
	const nano = uint64(1781590000_000000000) // same instant in nanos
	cases := []struct {
		in, want uint64
	}{
		{sec, nano},
		{milli, nano},
		{nano, nano},
		{0, 0},
	}
	for _, c := range cases {
		if got := normalizeEventTimeNanos(c.in); got != c.want {
			t.Errorf("normalizeEventTimeNanos(%d) = %d, want %d", c.in, got, c.want)
		}
	}
	// All three units for the SAME instant must collapse to one value, so the
	// HWM cursor is unit-agnostic.
	if normalizeEventTimeNanos(sec) != normalizeEventTimeNanos(nano) ||
		normalizeEventTimeNanos(milli) != normalizeEventTimeNanos(nano) {
		t.Fatalf("same-instant s/ms/ns did not normalize equal: s=%d ms=%d ns=%d",
			normalizeEventTimeNanos(sec), normalizeEventTimeNanos(milli), normalizeEventTimeNanos(nano))
	}
}

// TestFetchSinceFiltersOnNormalizedEventTime asserts the trigger SELECT gates on
// the NORMALIZED event_time (server-side), not the raw column — the fix that
// stops a larger-unit row from poisoning the watermark (F8). It captures the
// query the trigger sends to ClickHouse.
func TestFetchSinceFiltersOnNormalizedEventTime(t *testing.T) {
	var (
		mu       sync.Mutex
		gotQuery string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotQuery = r.URL.Query().Get("query")
		mu.Unlock()
		w.WriteHeader(200) // empty body = 0 rows, valid JSONEachRow
	}))
	defer srv.Close()

	trg, err := New(Config{Endpoint: srv.URL, Hostname: "node-1", PollInterval: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const wmNanos = uint64(1781590000_000000000)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := trg.fetchSince(ctx, wmNanos); err != nil {
		t.Fatalf("fetchSince: %v", err)
	}

	mu.Lock()
	q := gotQuery
	mu.Unlock()

	if !strings.Contains(q, chNormEventTimeNanos) {
		t.Errorf("query does not normalize event_time; want %q in:\n%s", chNormEventTimeNanos, q)
	}
	// The >= bound must compare the normalized expression against the nanos
	// watermark, not the raw column.
	wantPred := chNormEventTimeNanos + " >= " + strconv.FormatUint(wmNanos, 10)
	if !strings.Contains(q, wantPred) {
		t.Errorf("query filter is not normalized-vs-nanos-watermark; want %q in:\n%s", wantPred, q)
	}
	if strings.Contains(q, "event_time >= ") {
		t.Errorf("query still uses RAW event_time filter (poison-prone):\n%s", q)
	}
}
