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

package sink

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
)

// I1 — schema invariant.
//
// Parses the embedded schema.sql, walks every CREATE TABLE block,
// and asserts that any column named `content_type` is declared as
// `Int64` (case-sensitive; CH is). Catches a future PR that
// "improves" the column to String / Nullable(Int64) / etc. without
// updating the encoder side.
func TestContract_ContentTypeIsInt64InSchema(t *testing.T) {
	// Reach the canonical schema via the public DDL(table) API so this
	// test stays decoupled from the embed-internal var name.
	type col struct {
		table string
		typ   string
	}
	var found []col
	colRE := regexp.MustCompile(`(?m)^\s*content_type\s+([A-Za-z0-9_()]+)`)
	for _, table := range clickhouse.PixieTables() {
		ddl, err := clickhouse.DDL(table)
		if err != nil {
			t.Fatalf("DDL(%q): %v", table, err)
		}
		if m := colRE.FindStringSubmatch(ddl); m != nil {
			found = append(found, col{table: table, typ: strings.TrimRight(m[1], ",")})
		}
	}
	if len(found) == 0 {
		t.Fatalf("no content_type column found in PixieTables — did the column get renamed? Audit the encoder side too.")
	}
	for _, c := range found {
		if c.typ != "Int64" {
			t.Fatalf("schema drift: %s.content_type is %q, want Int64. CH input_format_skip_unknown_fields=1 will silent-drop encoder mismatches. Update encoder side together if intentional.", c.table, c.typ)
		}
	}
	t.Logf("invariant I1 holds across %d tables: %v", len(found), found)
}

// I2 — encoder invariant.
//
// Drives fastencode directly with content_type as int64 (the canonical
// shape Pixie's stirling http parser emits) and parses the emitted
// NDJSON to confirm the value is a JSON NUMBER, not a string. Also
// guards the int conversion path by feeding int / int32 / int64 / a
// json.Number and asserting each lands as a JSON number too.
func TestContract_FastEncodeContentTypeAsInt(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"int64", int64(2)},
		{"int32", int32(2)},
		{"int", 2},
		{"json.Number", json.Number("2")},
	}
	cols := minHTTPRowCols()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := canonicalHTTPRow()
			row["content_type"] = c.v
			var buf bytes.Buffer
			// stage the fast-path column cache the same way production
			// does via clickhouse.Columns — we don't reach into the
			// package-private cache; encodePixieRowsFast already does
			// the lookup itself.
			if err := encodePixieRowsFast(&buf, "http_events", []map[string]any{row}); err != nil {
				t.Fatalf("encodePixieRowsFast: %v", err)
			}
			line := strings.TrimSpace(buf.String())
			// Parse with json.Decoder + UseNumber so we see whether the
			// emitter wrote "content_type":2 (number) vs "2" (string).
			d := json.NewDecoder(strings.NewReader(line))
			d.UseNumber()
			var parsed map[string]any
			if err := d.Decode(&parsed); err != nil {
				t.Fatalf("decode emitted line: %v\nline=%s", err, line)
			}
			ct, ok := parsed["content_type"]
			if !ok {
				t.Fatalf("emitted line missing content_type; line=%s", line)
			}
			if _, isNum := ct.(json.Number); !isNum {
				t.Fatalf("content_type emitted as %T (%v), want JSON number — CH would silent-drop a non-number into an Int64 column. line=%s", ct, ct, line)
			}
			_ = cols
		})
	}
}

// I3 — silent-drop must be loud.
//
// A no-op CH that returns 200 OK + X-ClickHouse-Summary written_rows=0
// against a non-empty body. The sink must surface this as an error.
func TestContract_SilentDropDetected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("X-ClickHouse-Summary", `{"read_rows":"0","read_bytes":"0","written_rows":"0","written_bytes":"0"}`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = s.WritePixieRows(context.Background(), "http_events", []map[string]any{canonicalHTTPRow()})
	if err == nil {
		t.Fatalf("WritePixieRows returned nil on written_rows=0 reply — silent-drop detection is broken")
	}
	if !strings.Contains(err.Error(), "silent drop") {
		t.Fatalf("error %q does not mention 'silent drop' — runbook-grep will miss it", err.Error())
	}
}

// I3.b — sibling guard: when CH reports written_rows >= rows_sent the
// sink must NOT error. Pins the parse so a future refactor doesn't
// over-trigger and false-positive every successful write.
func TestContract_SilentDropNotTriggeredOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("X-ClickHouse-Summary", `{"written_rows":"1"}`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	if err := s.WritePixieRows(context.Background(), "http_events", []map[string]any{canonicalHTTPRow()}); err != nil {
		t.Fatalf("WritePixieRows errored on success summary: %v", err)
	}
}

// I3.c — header absence is tolerated (older CH versions / proxies
// strip it). Documents the policy decision so a future "tighten the
// gate" PR doesn't break clusters running CH 22.x.
func TestContract_SilentDropToleratesMissingSummaryHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		// no X-ClickHouse-Summary header
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	if err := s.WritePixieRows(context.Background(), "http_events", []map[string]any{canonicalHTTPRow()}); err != nil {
		t.Fatalf("WritePixieRows errored on missing summary header: %v (policy is tolerate-missing)", err)
	}
}

// I4 — round-trip an http_events row through WritePixieRows against a
// recording httptest CH; assert the on-wire body has content_type as
// a number, the INSERT targets the right table, and the body is
// one NDJSON object per row.
func TestContract_HTTPEventsRoundTrip(t *testing.T) {
	var gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		// echo back a successful summary so the silent-drop guard is satisfied
		w.Header().Set("X-ClickHouse-Summary", `{"written_rows":"1"}`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, _ := New(Config{Endpoint: srv.URL})
	if err := s.WritePixieRows(context.Background(), "http_events", []map[string]any{canonicalHTTPRow()}); err != nil {
		t.Fatalf("WritePixieRows: %v", err)
	}
	if !strings.Contains(gotQuery, "INSERT INTO ") {
		t.Fatalf("query missing INSERT INTO; got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "http_events") {
		t.Fatalf("query doesn't target http_events; got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "FORMAT JSONEachRow") {
		t.Fatalf("query missing FORMAT JSONEachRow; got %q", gotQuery)
	}
	if !strings.Contains(gotBody, `"content_type":2`) {
		t.Fatalf("body has content_type as non-number (CH would silent-drop). body=%s", gotBody)
	}
	// One NDJSON line per row → exactly one newline-trailing object here.
	sc := bufio.NewScanner(strings.NewReader(strings.TrimRight(gotBody, "\n")))
	n := 0
	for sc.Scan() {
		n++
	}
	if n != 1 {
		t.Fatalf("body has %d NDJSON lines, want 1; body=%s", n, gotBody)
	}
}

// canonicalHTTPRow returns a row whose shape matches what
// fastencode would see from a pxapi http_events read. Any new
// schema column added must be appended here too — the test will
// fail with a clear "schema added X column; canonical row needs
// it" message if a missing column hits errFastEncodeUnsupported.
func canonicalHTTPRow() map[string]any {
	return map[string]any{
		"time_":          int64(1_717_200_000_000_000_000),
		"upid":           "00000000-0000-0000-0000-000000000001",
		"namespace":      "redis",
		"pod":            "redis-578d5dc9bd-kjj78",
		"remote_addr":    "10.0.0.1",
		"remote_port":    int64(443),
		"local_addr":     "10.0.0.2",
		"local_port":     int64(48000),
		"trace_role":     int64(1),
		"encrypted":      int64(0),
		"major_version":  int64(1),
		"minor_version":  int64(1),
		"content_type":   int64(2), // JSON — the schema-honest int
		"req_headers":    `{"User-Agent":"curl/8"}`,
		"req_method":     "GET",
		"req_path":       "/x",
		"req_body":       "",
		"req_body_size":  int64(0),
		"resp_headers":   `{"Content-Type":"application/json"}`,
		"resp_status":    int64(200),
		"resp_message":   "OK",
		"resp_body":      "{}",
		"resp_body_size": int64(2),
		"latency":        int64(123_456),
		"hostname":       "node-1",
	}
}

// minHTTPRowCols is the small fixed column list any "is it int?"
// micro-check uses; kept aligned with the canonical row above so
// schema additions surface as a missing-column in the canonical row,
// not a flaky test.
func minHTTPRowCols() []string {
	return []string{"content_type", "remote_port", "local_port", "trace_role", "encrypted", "major_version", "minor_version", "resp_status", "latency", "req_body_size", "resp_body_size"}
}
