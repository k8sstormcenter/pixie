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

package clickhouse

import (
	"reflect"
	"strings"
	"testing"
)

// http_events is the shape AE writes most often (and the bench shape).
// Pin the exact ordered column list so a schema.sql edit that drops or
// reorders a column trips this test loudly.
func TestColumns_http_events_ExactList(t *testing.T) {
	got, err := Columns("http_events")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	want := []string{
		"time_", "upid", "namespace", "pod",
		"remote_addr", "remote_port", "local_addr", "local_port",
		"trace_role", "encrypted", "major_version", "minor_version",
		"content_type", "req_headers", "req_method", "req_path",
		"req_body", "req_body_size", "resp_headers", "resp_status",
		"resp_message", "resp_body", "resp_body_size", "latency",
		"hostname", "event_time",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Columns(http_events) mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// conn_stats is the column shape pinned by entlein/dx#5; if anyone
// drops or renames a column the bench-encoder fast-path would silently
// emit the wrong JSON, so this guard is mandatory.
func TestColumns_conn_stats_ExactList(t *testing.T) {
	got, err := Columns("conn_stats")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	want := []string{
		"time_", "upid", "namespace", "pod",
		"remote_addr", "remote_port", "trace_role", "addr_family",
		"protocol", "ssl", "conn_open", "conn_close", "conn_active",
		"bytes_sent", "bytes_recv", "hostname", "event_time",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Columns(conn_stats) mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// Every table in PixieTables() must successfully parse, and each must
// include the operator-mandated namespace + pod columns plus the
// retention-plugin-mandated hostname + event_time columns.
func TestColumns_AllPixieTables_HaveOperatorColumns(t *testing.T) {
	for _, table := range PixieTables() {
		cols, err := Columns(table)
		if err != nil {
			t.Errorf("Columns(%q): %v", table, err)
			continue
		}
		for _, required := range []string{"namespace", "pod", "hostname", "event_time"} {
			found := false
			for _, c := range cols {
				if c == required {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Columns(%q) missing required column %q (cols=%v)", table, required, cols)
			}
		}
	}
}

// Backtick-quoted (dotted) tables also resolve.
func TestColumns_DottedTables(t *testing.T) {
	for _, table := range []string{"http2_messages.beta", "kafka_events.beta"} {
		got, err := Columns(table)
		if err != nil {
			t.Errorf("Columns(%q): %v", table, err)
			continue
		}
		if len(got) == 0 {
			t.Errorf("Columns(%q): empty", table)
		}
	}
}

// Unknown tables return ErrUnknownTable so callers (sink) can fall
// back to the encoding/json slow path safely.
func TestColumns_UnknownTable_ErrUnknownTable(t *testing.T) {
	_, err := Columns("not_a_real_table")
	if err == nil || !strings.Contains(err.Error(), "unknown table") {
		t.Fatalf("expected ErrUnknownTable for unknown table, got %v", err)
	}
}

// Repeated lookups for the same table return the same content. (The
// underlying parser may or may not cache — the sink's fast-path
// encoder caches the column slice itself once per table; what we test
// here is that the public Columns() answer is stable.)
func TestColumns_Repeated_StableResult(t *testing.T) {
	a, err := Columns("dns_events")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Columns("dns_events")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Columns(dns_events) drift across calls: a=%v b=%v", a, b)
	}
}
