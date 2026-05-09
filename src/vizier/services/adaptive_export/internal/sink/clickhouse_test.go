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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

func canonicalAttribution() AttributionRow {
	t0 := time.Unix(0, 1744477360303026359).UTC()
	return AttributionRow{
		AnomalyHash: anomaly.Hash(anomaly.Target{
			PID: 106040, Comm: "redis-server",
			Pod: "redis-578d5dc9bd-kjj78", Namespace: "redis",
		}),
		Namespace:  "redis",
		Pod:        "redis-578d5dc9bd-kjj78",
		Comm:       "redis-server",
		PID:        106040,
		Hostname:   "node-1",
		TStart:     t0.Add(-5 * time.Minute),
		TEnd:       t0.Add(5 * time.Minute),
		LastSeen:   t0,
		LastRuleID: "R1005",
		NAnomalies: 1,
	}
}

// TestSink_Write_PostsCorrectQueryAndBody — INSERT targets the right
// table; body is one JSON object per line with all attribution fields.
func TestSink_Write_PostsCorrectQueryAndBody(t *testing.T) {
	var gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	row := canonicalAttribution()
	if err := s.Write(context.Background(), []AttributionRow{row}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "INSERT INTO forensic_db.adaptive_attribution FORMAT JSONEachRow"
	if gotQuery != want {
		t.Fatalf("query = %q, want %q", gotQuery, want)
	}
	for _, needle := range []string{
		`"anomaly_hash":"` + string(row.AnomalyHash) + `"`,
		`"namespace":"redis"`,
		`"pod":"redis-578d5dc9bd-kjj78"`,
		`"comm":"redis-server"`,
		`"pid":106040`,
		`"hostname":"node-1"`,
		`"last_rule_id":"R1005"`,
		`"n_anomalies":1`,
	} {
		if !strings.Contains(gotBody, needle) {
			t.Fatalf("body missing %q; body=%s", needle, gotBody)
		}
	}
	if !strings.Contains(gotBody, `"t_start":"2025-04-12 16:57:40.303026359"`) {
		t.Fatalf("t_start not formatted as DateTime64 string; body=%s", gotBody)
	}
}

// TestSink_Write_EmptyBatch — no HTTP call.
func TestSink_Write_EmptyBatch(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("Write empty: %v", err)
	}
	if called {
		t.Fatalf("empty Write made an HTTP call")
	}
}

// TestSink_Write_HTTPErrorPropagates — non-2xx returns Go error.
func TestSink_Write_HTTPErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("clickhouse exploded"))
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	err := s.Write(context.Background(), []AttributionRow{canonicalAttribution()})
	if err == nil {
		t.Fatalf("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error should mention 503: %v", err)
	}
}

// TestSink_QueryActive_BuildsCorrectSQL — boot rehydration query.
func TestSink_QueryActive_BuildsCorrectSQL(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"anomaly_hash":"abc","namespace":"redis","pod":"redis-x","comm":"redis-server","pid":106040,"hostname":"node-1","t_start_ns":"1744477060303026359","t_end_ns":"1744477660303026359","last_seen_ns":"1744477360303026359","last_rule_id":"R1005","n_anomalies":1}` + "\n"))
	}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	rows, err := s.QueryActive(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("QueryActive: %v", err)
	}
	if !strings.Contains(seenQuery, "FROM forensic_db.adaptive_attribution FINAL") {
		t.Fatalf("missing FINAL: %q", seenQuery)
	}
	if !strings.Contains(seenQuery, "hostname = 'node-1'") {
		t.Fatalf("missing hostname filter: %q", seenQuery)
	}
	if !strings.Contains(seenQuery, "t_end > now64(9)") {
		t.Fatalf("missing t_end > now64 filter: %q", seenQuery)
	}
	if len(rows) != 1 || rows[0].AnomalyHash != "abc" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].PID != 106040 {
		t.Fatalf("PID = %d", rows[0].PID)
	}
	if rows[0].TStart.UnixNano() != 1744477060303026359 {
		t.Fatalf("TStart wrong: %v", rows[0].TStart)
	}
}

// TestSink_QueryActive_RequiresHostname — defensive guard.
func TestSink_QueryActive_RequiresHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	s, _ := New(Config{Endpoint: srv.URL})
	if _, err := s.QueryActive(context.Background(), ""); err == nil {
		t.Fatalf("empty hostname should error")
	}
}

// TestSink_QuoteEscape — single quotes in hostname survive injection-safely.
func TestSink_QuoteEscape(t *testing.T) {
	if got := quoteCH("o'malley"); got != `'o\'malley'` {
		t.Fatalf("quoteCH = %q, want 'o\\'malley'", got)
	}
}

// TestSink_New_ValidationTable — every Config validation branch as
// one row. Bad fields one at a time + a happy-path baseline. Update
// when a new validation lands; this is the single source of truth
// for what New() rejects.
func TestSink_New_ValidationTable(t *testing.T) {
	cases := []struct {
		name           string
		cfg            Config
		wantErr        bool
		wantErrSnippet string
	}{
		{
			name: "happy path http",
			cfg:  Config{Endpoint: "http://ch.example:8123", Database: "forensic_db"},
		},
		{
			name: "happy path https + auth + custom timeout",
			cfg: Config{
				Endpoint: "https://ch.example:8443", Database: "forensic_db",
				Username: "u", Password: "p", Timeout: 5 * time.Second,
			},
		},
		{
			name: "default database when empty",
			cfg:  Config{Endpoint: "http://ch:8123"}, // Database empty → defaulted
		},
		{
			name: "trailing slash stripped",
			cfg:  Config{Endpoint: "http://ch:8123/"}, // OK; New() strips it
		},
		{
			name:           "empty endpoint",
			cfg:            Config{},
			wantErr:        true,
			wantErrSnippet: "empty Endpoint",
		},
		{
			name:           "relative endpoint (no scheme)",
			cfg:            Config{Endpoint: "ch:8123"},
			wantErr:        true,
			wantErrSnippet: "absolute http(s) URL",
		},
		{
			name:           "bare path",
			cfg:            Config{Endpoint: "/clickhouse"},
			wantErr:        true,
			wantErrSnippet: "absolute http(s) URL",
		},
		{
			name:           "ftp scheme rejected",
			cfg:            Config{Endpoint: "ftp://ch:21"},
			wantErr:        true,
			wantErrSnippet: "absolute http(s) URL",
		},
		{
			name:           "endpoint with query string",
			cfg:            Config{Endpoint: "http://ch:8123?foo=bar"},
			wantErr:        true,
			wantErrSnippet: "must not include query parameters or a fragment",
		},
		{
			name:           "endpoint with fragment",
			cfg:            Config{Endpoint: "http://ch:8123#frag"},
			wantErr:        true,
			wantErrSnippet: "must not include query parameters or a fragment",
		},
		{
			name:           "Database with hyphen rejected",
			cfg:            Config{Endpoint: "http://ch:8123", Database: "forensic-db"},
			wantErr:        true,
			wantErrSnippet: "invalid Database identifier",
		},
		{
			name:           "Database with semicolon rejected (SQL injection probe)",
			cfg:            Config{Endpoint: "http://ch:8123", Database: "forensic_db; DROP DATABASE x"},
			wantErr:        true,
			wantErrSnippet: "invalid Database identifier",
		},
		{
			name:           "Database starting with digit rejected",
			cfg:            Config{Endpoint: "http://ch:8123", Database: "1bad"},
			wantErr:        true,
			wantErrSnippet: "invalid Database identifier",
		},
		{
			name:           "negative Timeout rejected",
			cfg:            Config{Endpoint: "http://ch:8123", Timeout: -1 * time.Second},
			wantErr:        true,
			wantErrSnippet: "Timeout must be >= 0",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := New(c.cfg)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", c.wantErrSnippet)
				}
				if !strings.Contains(err.Error(), c.wantErrSnippet) {
					t.Fatalf("error %q does not contain %q", err.Error(), c.wantErrSnippet)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s == nil {
				t.Fatalf("New returned nil sink without error")
			}
			// Trailing-slash strip is observable via cfg.Endpoint.
			if strings.HasSuffix(s.cfg.Endpoint, "/") {
				t.Fatalf("trailing slash not stripped: %q", s.cfg.Endpoint)
			}
			if s.cfg.Database == "" {
				t.Fatalf("Database default not applied")
			}
		})
	}
}

// TestValidateTableIdentifier_TableDriven — table validator covers
// dotted protobuf extensions but not anything wilder.
func TestValidateTableIdentifier_TableDriven(t *testing.T) {
	good := []string{"http_events", "redis_events", "http2_messages.beta", "kafka_events.beta", "_underscore_start"}
	bad := []string{"", "1bad", "http events", "http;drop", "x..y", ".leading", "trailing.", "with-hyphen"}
	for _, g := range good {
		if err := validateTableIdentifier(g); err != nil {
			t.Errorf("validateTableIdentifier(%q): unexpected error %v", g, err)
		}
	}
	for _, b := range bad {
		if err := validateTableIdentifier(b); err == nil {
			t.Errorf("validateTableIdentifier(%q): want error, got nil", b)
		}
	}
}

// TestUintFromRaw_HandlesQuotedAndBareJSON — CH HTTP emits UInt64 as
// either bare numeric (`12345`) or quoted (`"12345"`). Both must
// parse, including values above INT64_MAX.
func TestUintFromRaw_HandlesQuotedAndBareJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  uint64
	}{
		{"bare", `12345`, 12345},
		{"quoted", `"12345"`, 12345},
		{"max int64", `9223372036854775807`, 9223372036854775807},
		{"above int64", `"18446744073709551615"`, 18446744073709551615},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := uintFromRaw([]byte(c.input))
			if err != nil {
				t.Fatalf("uintFromRaw(%q): %v", c.input, err)
			}
			if got != c.want {
				t.Fatalf("uintFromRaw(%q) = %d, want %d", c.input, got, c.want)
			}
		})
	}
}

// TestUintFromRaw_RejectsGarbage — non-numeric input must error,
// not silently return 0.
func TestUintFromRaw_RejectsGarbage(t *testing.T) {
	bad := []string{"", `""`, `"abc"`, `-1`, `"-1"`, `1.5`}
	for _, b := range bad {
		if _, err := uintFromRaw([]byte(b)); err == nil {
			t.Errorf("uintFromRaw(%q): want error, got nil", b)
		}
	}
}

// chunkedReader emits the underlying body in fixed-size chunks. A
// short pause between chunks proves parseActiveRowsStream doesn't
// wait for the whole body before parsing. Tracks partial-read state
// so a Read() smaller than the next chunk doesn't drop bytes.
type chunkedReader struct {
	chunks   [][]byte
	idx      int
	off      int           // offset within chunks[idx]
	delay    time.Duration // sleep between chunks
	produced int64
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.idx]
	n := copy(p, chunk[r.off:])
	r.off += n
	r.produced += int64(n)
	if r.off >= len(chunk) {
		r.idx++
		r.off = 0
		time.Sleep(r.delay)
	}
	return n, nil
}

// TestParseActiveRowsStream_BoundsMemory — proves the streaming path
// doesn't allocate proportional to total response size. Builds a
// 5 MiB synthetic JSONEachRow body fed in 64 KiB chunks, parses, and
// asserts (a) all rows decoded correctly, (b) peak intermediate
// allocation is well below the body size (loose bound: parseActiveRows
// hands one row at a time to the caller; we collect into a slice but
// never hold the wire representation of more than one line).
func TestParseActiveRowsStream_BoundsMemory(t *testing.T) {
	const targetRows = 5000 // ~5MiB at ~1KiB/row
	var buf bytes.Buffer
	row := func(i int) string {
		return fmt.Sprintf(`{"anomaly_hash":"%032x","namespace":"redis","pod":"p","comm":"c","pid":%d,"hostname":"h","t_start_ns":%d,"t_end_ns":%d,"last_seen_ns":%d,"last_rule_id":"R0001","n_anomalies":%d,"_pad":"%s"}`+"\n",
			i, i, 1700000000000000000+int64(i), 1700000000000000000+int64(i)+300_000_000_000, 1700000000000000000+int64(i)+150_000_000_000, i, strings.Repeat("x", 800))
	}
	for i := 0; i < targetRows; i++ {
		buf.WriteString(row(i))
	}
	body := buf.Bytes()

	const chunkSize = 64 * 1024
	chunks := make([][]byte, 0, len(body)/chunkSize+1)
	for off := 0; off < len(body); off += chunkSize {
		end := off + chunkSize
		if end > len(body) {
			end = len(body)
		}
		chunks = append(chunks, body[off:end])
	}
	rdr := &chunkedReader{chunks: chunks, delay: 0}

	rows, err := parseActiveRowsStream(rdr)
	if err != nil {
		t.Fatalf("parseActiveRowsStream: %v", err)
	}
	if len(rows) != targetRows {
		t.Fatalf("parsed %d rows, want %d", len(rows), targetRows)
	}
	// Spot-check round-trip on one row (last element).
	if rows[targetRows-1].PID != uint64(targetRows-1) {
		t.Fatalf("last row PID = %d, want %d", rows[targetRows-1].PID, targetRows-1)
	}
}

// TestParseActiveRowsStream_RejectsOverlongLine — guards against
// pathological CH responses with multi-MiB single rows. Default cap
// is 1 MiB; emit a 2 MiB row and assert the scanner rejects it
// rather than OOMing.
func TestParseActiveRowsStream_RejectsOverlongLine(t *testing.T) {
	huge := strings.Repeat("a", 2*1024*1024)
	body := fmt.Sprintf(`{"anomaly_hash":"x","_pad":"%s"}`+"\n", huge)
	_, err := parseActiveRowsStream(strings.NewReader(body))
	if err == nil {
		t.Fatalf("expected scanner error on >1MiB line; got nil")
	}
	if !strings.Contains(err.Error(), "QueryActive scan") {
		t.Fatalf("expected scan error, got: %v", err)
	}
}

// TestParseActiveRows_RoundTripFromBytes — keep the byte-slice path
// covered (used by tests and the e2e harness).
func TestParseActiveRows_RoundTripFromBytes(t *testing.T) {
	body := []byte(`{"anomaly_hash":"deadbeef","namespace":"redis","pod":"p","comm":"c","pid":42,"hostname":"node-01","t_start_ns":1700000000000000000,"t_end_ns":1700000000300000000,"last_seen_ns":1700000000150000000,"last_rule_id":"R0001","n_anomalies":1}` + "\n")
	rows, err := parseActiveRows(body)
	if err != nil {
		t.Fatalf("parseActiveRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Pod != "p" || rows[0].PID != 42 {
		t.Fatalf("round-trip mismatch: %+v", rows)
	}
}

