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

// Package sink writes operator-owned rows to ClickHouse over the HTTP
// interface (default port 8123). It has two write surfaces:
//
//  1. forensic_db.adaptive_attribution — one row per arriving kubescape
//     anomaly. ReplacingMergeTree(t_end) on the table side collapses
//     re-inserts with the same (hostname, anomaly_hash) primary key
//     into the row with the largest t_end.
//
//  2. forensic_db.<pixie_table> — operator-pushed pixie observation rows
//     (rev-1 fan-out path, gated on ADAPTIVE_PUSH_PIXIE_ROWS=true).
//     Used when Pixie's cloud-side retention plugin can't reach an
//     in-cluster CH endpoint; the operator queries pixie itself and
//     writes the result with WritePixieRows.
package sink

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
)

// pixieTableIdentRE accepts plain CH identifiers and dotted protobuf
// extensions like `http2_messages.beta`. Used to gate `table` strings
// before they're interpolated into the INSERT query.
var pixieTableIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// setFailLoudSettings pins ClickHouse's input-format settings on every
// AE INSERT so an upstream schema-drift surfaces as an HTTP 4xx with a
// real error body, not a silent written_rows=0 + 200 OK that AE's
// summaryWroteFewerThan only catches AFTER the data is lost (entlein/
// the rev-2 schema + the 2026-06-07 rig 6a25c85c regression that dropped
// http_events + dns_events at 0 written_rows while the writer reported
// 259 rows_sent and AE re-looped on the failure → 3.2-core CPU
// runaway).
//
// Pinned defaults differ from the historical CH 22+ tolerant defaults:
//
//	input_format_skip_unknown_fields=0    fail on a column AE writes
//	                                      that doesn't exist in CH.
//	input_format_null_as_default=0        fail on a NULL where the
//	                                      column is non-nullable.
//	input_format_allow_errors_num=0       reject the whole batch on
//	                                      the first parse error
//	                                      (rather than silently
//	                                      dropping bad rows up to the
//	                                      CH default's tolerance).
//	input_format_allow_errors_ratio=0     same, for the proportional
//	                                      knob.
//
// Loud failures are the contract: AE then either restarts on the
// fatal-path that wraps the write (controller / streaming both bubble
// the error up) OR retries — but never advances its watermark over
// data it didn't actually persist.
func setFailLoudSettings(q url.Values) {
	q.Set("input_format_skip_unknown_fields", "0")
	q.Set("input_format_null_as_default", "0")
	q.Set("input_format_allow_errors_num", "0")
	q.Set("input_format_allow_errors_ratio", "0")
}

// chIdentRE — strict CH identifier (no dots). Used to gate Database
// (and any future single-segment identifier) against SQL injection
// from env/config-driven values.
var chIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateTableIdentifier(t string) error {
	if !pixieTableIdentRE.MatchString(t) {
		return fmt.Errorf("sink: invalid table identifier %q", t)
	}
	return nil
}

// Config configures a ClickHouseHTTP sink.
type Config struct {
	Endpoint string        // e.g. http://clickhouse:8123
	Database string        // defaults to "forensic_db"
	Username string        // optional basic auth
	Password string        // optional basic auth
	Timeout  time.Duration // per-write HTTP timeout; 0 → 30s
}

// AttributionRow is one row of forensic_db.adaptive_attribution.
// All fields are required except LastRuleID.
type AttributionRow struct {
	AnomalyHash anomaly.AnomalyHash
	Namespace   string // may be empty
	Pod         string // may be empty
	Comm        string
	PID         uint64
	Hostname    string
	TStart      time.Time
	TEnd        time.Time
	LastSeen    time.Time
	LastRuleID  string
	NAnomalies  uint64
}

// ClickHouseHTTP is the production sink.
type ClickHouseHTTP struct {
	cfg    Config
	client *http.Client
}

// New validates Config + returns a ready-to-use sink.
func New(cfg Config) (*ClickHouseHTTP, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("sink: empty Endpoint")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("sink: invalid Endpoint %q: %w", cfg.Endpoint, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("sink: Endpoint must be an absolute http(s) URL: %q", cfg.Endpoint)
	}
	// We append "/?query=…" downstream via string concatenation; if
	// the configured Endpoint already carries a query or fragment, the
	// concatenated URL is malformed (a second '?' becomes path data,
	// fragments swallow trailing characters). Forbid both up-front.
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("sink: Endpoint must not include query parameters or a fragment: %q", cfg.Endpoint)
	}
	// Strip a trailing "/" from the path so downstream concatenation
	// (Endpoint + "/?query=…") doesn't produce a "//?query=…" — some
	// proxies / ingress controllers reject double-slashes.
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	if cfg.Database == "" {
		cfg.Database = "forensic_db"
	}
	// Database is interpolated directly into INSERT/SELECT statements
	// (used in WriteAttribution, WritePixieRows, QueryActive). Block
	// injection via env/config-supplied values.
	if !chIdentRE.MatchString(cfg.Database) {
		return nil, fmt.Errorf("sink: invalid Database identifier %q (must match [A-Za-z_][A-Za-z0-9_]*)", cfg.Database)
	}
	// http.Client.Timeout enforces only when >0; a negative value
	// would silently disable the deadline. Reject explicitly so the
	// "0 → 30s default" branch below is the only zero-handling path.
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("sink: Timeout must be >= 0 (got %s)", cfg.Timeout)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &ClickHouseHTTP{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// WritePixieRows POSTs a batch of arbitrary rows (one map per CH row,
// keyed by column name) into forensic_db.<table> via FORMAT JSONEachRow.
// Used by the operator's per-anomaly fan-out path that queries pixie
// directly and pushes the resulting rows into CH (bypasses the cloud's
// retention plugin, which can't reach an in-cluster CH endpoint).
func (s *ClickHouseHTTP) WritePixieRows(ctx context.Context, table string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	if err := validateTableIdentifier(table); err != nil {
		return err
	}
	// Pooled buffer (option 1) — controller fan-out + streaming flush
	// call this on a tight cadence, so reusing the backing array across
	// calls cuts the per-call B/op cost by ~70 % once the pool stabilises
	// (the bench BenchmarkEncodePixieRowsFast_Pooled tracks the steady
	// state). buf.Reset() preserves the cap on Put so the next caller
	// gets a warm allocation.
	buf := encodeBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		// Avoid hoarding pathologically large buffers. The pixie batch
		// upper bound is ~MaxBatchRows * ~900 B/row ≈ 1 MB; anything
		// over 2 MB came from a one-off oversize batch and shouldn't
		// stay in the pool eating heap.
		if buf.Cap() > 2*1024*1024 {
			return
		}
		encodeBufPool.Put(buf)
	}()
	// Fast path: known table → walk rows in schema column order, no
	// reflect, no map-key sort. The fast encoder's CPU + alloc profile
	// is ~3 % of the encoding/json path (AE benchmark suite); it's the
	// hot path for every controller fan-out + streaming flush.
	// errFastEncodeUnsupported falls back so an unexpected value type
	// can't silently drop a row. ErrUnknownTable falls back so a new
	// pixie table not yet in schema.sql still works (just slower).
	if err := encodePixieRowsFast(buf, table, rows); err != nil {
		if !errors.Is(err, errFastEncodeUnsupported) && !errors.Is(err, clickhouse.ErrUnknownTable) {
			return fmt.Errorf("sink: fast encode %s: %w", table, err)
		}
		buf.Reset()
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		for _, r := range rows {
			obj := make(map[string]any, len(r))
			for k, v := range r {
				obj[k] = normalisePixieValue(v)
			}
			if err := enc.Encode(obj); err != nil {
				return fmt.Errorf("sink: encode pixie row for %s: %w", table, err)
			}
		}
	}
	identifier := table
	if strings.Contains(table, ".") {
		identifier = "`" + table + "`"
	}
	q := url.Values{}
	q.Set("query", fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", s.cfg.Database, identifier))
	setFailLoudSettings(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Endpoint+"/?"+q.Encode(), bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.cfg.Username != "" {
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sink: pixie POST %s: %w", table, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Echo CH's error body so we can see WHY it rejected. Truncated
		// to 1KiB to bound log spam from large reject lists.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("sink: pixie HTTP %d (%s): %s",
			resp.StatusCode, table, strings.TrimSpace(string(body)))
	}
	// DEBUG: ALWAYS log what CH says it wrote — temporary while we
	// chase the pgsql_events silent-drop mystery. Includes a snippet
	// of the first row so we can compare what was sent vs what CH
	// reported.
	summary := resp.Header.Get("X-ClickHouse-Summary")
	var firstRowKeys []string
	if len(rows) > 0 {
		for k := range rows[0] {
			firstRowKeys = append(firstRowKeys, k)
		}
	}
	log.WithFields(log.Fields{
		"table":          table,
		"rows_sent":      len(rows),
		"body_bytes":     buf.Len(),
		"ch_summary":     summary,
		"first_row_keys": strings.Join(firstRowKeys, ","),
	}).Info("sink: pixie write completed")
	// Detect the silent-drop class: CH returns 2xx but
	// X-ClickHouse-Summary.written_rows < len(rows). Observed live on
	// 2026-05-23T20:58Z (redis_events: rows_sent=1658, written_rows=0)
	// — the operator reported success and the analyst saw the gap days
	// later. Header absence is tolerated (older CH versions / proxies
	// strip it); only an EXPLICIT zero-of-non-zero counts.
	if writeMismatch := summaryWroteFewerThan(summary, len(rows)); writeMismatch != nil {
		return fmt.Errorf("sink: pixie write to %s reported %d rows_sent but CH summary written_rows=%d (silent drop): %s",
			table, len(rows), writeMismatch.writtenRows, summary)
	}
	return nil
}

// summaryDelta carries the parsed write counters from CH's
// X-ClickHouse-Summary response header.
type summaryDelta struct {
	writtenRows int64
}

// summaryWroteFewerThan returns non-nil when the X-ClickHouse-Summary
// header is present, parseable, and reports written_rows < rowsSent.
// Returns nil when the header is missing, unparseable, or the count
// matches/exceeds rowsSent — those are not data-loss signals.
func summaryWroteFewerThan(summary string, rowsSent int) *summaryDelta {
	if summary == "" {
		return nil
	}
	var parsed struct {
		WrittenRows json.Number `json:"written_rows"`
	}
	if err := json.Unmarshal([]byte(summary), &parsed); err != nil {
		return nil
	}
	if parsed.WrittenRows == "" {
		return nil
	}
	wrote, err := parsed.WrittenRows.Int64()
	if err != nil {
		return nil
	}
	if wrote >= int64(rowsSent) {
		return nil
	}
	return &summaryDelta{writtenRows: wrote}
}

// normalisePixieValue coerces pxapi-emitted Go values into JSON-friendly
// shapes ClickHouse parses cleanly. time.Time → "YYYY-MM-DD HH:MM:SS.NNN…"
// (CH's DateTime64 input format); []byte → string; everything else → as-is.
func normalisePixieValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format("2006-01-02 15:04:05.000000000")
	case []byte:
		return string(x)
	default:
		return v
	}
}

// Write upserts a batch of AttributionRows. Implementation: HTTP POST
// `INSERT INTO forensic_db.adaptive_attribution FORMAT JSONEachRow`
// with one JSON object per row. Empty batch is a no-op.
func (s *ClickHouseHTTP) Write(ctx context.Context, rows []AttributionRow) error {
	if len(rows) == 0 {
		return nil
	}
	body, err := encodeJSONEachRow(rows)
	if err != nil {
		return fmt.Errorf("sink: encode %d attribution rows: %w", len(rows), err)
	}
	q := url.Values{}
	q.Set("query", fmt.Sprintf(
		"INSERT INTO %s.adaptive_attribution FORMAT JSONEachRow", s.cfg.Database))
	setFailLoudSettings(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.Endpoint+"/?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sink: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.cfg.Username != "" {
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sink: POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("sink: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// QueryActive fetches all attribution rows on this hostname whose t_end
// is still in the future. Used by the operator at boot to rehydrate
// the in-memory active set after a pod crash. Returns rows ordered
// by anomaly_hash so the caller's set is deterministic.
func (s *ClickHouseHTTP) QueryActive(ctx context.Context, hostname string) ([]AttributionRow, error) {
	if hostname == "" {
		return nil, fmt.Errorf("sink: QueryActive requires hostname")
	}
	q := url.Values{}
	// `FINAL` collapses ReplacingMergeTree to the row with the largest
	// t_end (because the engine's version column is t_end).
	// We escape hostname inside the SQL via simple ClickHouse-style
	// quoting (single quote, no backslash escapes).
	sql := fmt.Sprintf(
		"SELECT anomaly_hash, namespace, pod, comm, pid, hostname, "+
			"toUnixTimestamp64Nano(t_start) AS t_start_ns, "+
			"toUnixTimestamp64Nano(t_end) AS t_end_ns, "+
			"toUnixTimestamp64Nano(last_seen) AS last_seen_ns, "+
			"last_rule_id, n_anomalies "+
			"FROM %s.adaptive_attribution FINAL "+
			"WHERE hostname = %s AND t_end > now64(9) "+
			"ORDER BY anomaly_hash FORMAT JSONEachRow",
		s.cfg.Database, quoteCH(hostname))
	q.Set("query", sql)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.cfg.Endpoint+"/?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if s.cfg.Username != "" {
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sink: QueryActive GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Drain (don't echo) — body may carry attribution rows.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("sink: QueryActive HTTP %d", resp.StatusCode)
	}
	// Stream the response line-by-line so the per-call buffer is
	// bounded by max_line_length, not by the total active-set size.
	return parseActiveRowsStream(resp.Body)
}

// chLiteralEscaper escapes a string for ClickHouse single-quoted literals.
// Hoisted to a package-level var so we don't allocate a Replacer per call
// — quoteCH runs in the per-row write path.
var chLiteralEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

// quoteCH wraps a string literal for safe ClickHouse SQL embedding.
func quoteCH(s string) string {
	return "'" + chLiteralEscaper.Replace(s) + "'"
}

func encodeJSONEachRow(rows []AttributionRow) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		obj := map[string]any{
			"anomaly_hash": string(r.AnomalyHash),
			"namespace":    r.Namespace,
			"pod":          r.Pod,
			"comm":         r.Comm,
			"pid":          r.PID,
			"hostname":     r.Hostname,
			"t_start":      r.TStart.UTC().Format("2006-01-02 15:04:05.000000000"),
			"t_end":        r.TEnd.UTC().Format("2006-01-02 15:04:05.000000000"),
			"last_seen":    r.LastSeen.UTC().Format("2006-01-02 15:04:05.000000000"),
			"last_rule_id": r.LastRuleID,
			"n_anomalies":  r.NAnomalies,
		}
		if err := enc.Encode(obj); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// activeWireRow mirrors the JSONEachRow shape emitted by QueryActive.
// json.RawMessage on UInt64 fields lets us tolerate CH's two wire
// formats (`12345` and `"12345"`).
type activeWireRow struct {
	AnomalyHash string          `json:"anomaly_hash"`
	Namespace   string          `json:"namespace"`
	Pod         string          `json:"pod"`
	Comm        string          `json:"comm"`
	PID         json.RawMessage `json:"pid"`
	Hostname    string          `json:"hostname"`
	TStartNs    json.RawMessage `json:"t_start_ns"`
	TEndNs      json.RawMessage `json:"t_end_ns"`
	LastSeenNs  json.RawMessage `json:"last_seen_ns"`
	LastRuleID  string          `json:"last_rule_id"`
	NAnomalies  json.RawMessage `json:"n_anomalies"`
}

// parseActiveRowsStream ingests JSONEachRow output from QueryActive
// directly from a reader so the per-call buffer is bounded by
// `max_active_row_bytes` (per row) rather than by the entire active
// set. Mirrors trigger.parseJSONEachRow's streaming posture.
func parseActiveRowsStream(r io.Reader) ([]AttributionRow, error) {
	const maxActiveRowBytes = 1 << 20 // 1 MiB per JSONEachRow line
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxActiveRowBytes)
	var out []AttributionRow
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		row, err := parseActiveRowLine(line)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sink: QueryActive scan: %w", err)
	}
	return out, nil
}

// parseActiveRowLine decodes a single JSONEachRow line into one
// AttributionRow. Used by parseActiveRowsStream and accessible to
// tests via parseActiveRows.
func parseActiveRowLine(line []byte) (AttributionRow, error) {
	var w activeWireRow
	if err := json.Unmarshal(line, &w); err != nil {
		// Don't echo the raw line — it can carry CH row payloads
		// that propagate to logs / surfaced errors. Length only.
		return AttributionRow{}, fmt.Errorf("sink: parse active row (%d bytes): %w", len(line), err)
	}
	ts, err1 := nsFromRaw(w.TStartNs)
	te, err2 := nsFromRaw(w.TEndNs)
	ls, err3 := nsFromRaw(w.LastSeenNs)
	pid, errPID := uintFromRaw(w.PID)
	nAn, errN := uintFromRaw(w.NAnomalies)
	if err1 != nil || err2 != nil || err3 != nil || errPID != nil || errN != nil {
		return AttributionRow{}, fmt.Errorf("sink: parse uint64 fields: t_start=%v t_end=%v last_seen=%v pid=%v n_anomalies=%v", err1, err2, err3, errPID, errN)
	}
	return AttributionRow{
		AnomalyHash: anomaly.AnomalyHash(w.AnomalyHash),
		Namespace:   w.Namespace,
		Pod:         w.Pod,
		Comm:        w.Comm,
		PID:         pid,
		Hostname:    w.Hostname,
		TStart:      time.Unix(0, ts).UTC(),
		TEnd:        time.Unix(0, te).UTC(),
		LastSeen:    time.Unix(0, ls).UTC(),
		LastRuleID:  w.LastRuleID,
		NAnomalies:  nAn,
	}, nil
}

// parseActiveRows is the byte-slice convenience wrapper around
// parseActiveRowsStream — kept for tests and e2e fixtures that have
// already buffered the full response.
func parseActiveRows(body []byte) ([]AttributionRow, error) {
	return parseActiveRowsStream(bytes.NewReader(body))
}

// nsFromRaw parses a CH UInt64-as-JSON value (CH may emit either
// `12345` or `"12345"`) into an int64. Used for time_ columns.
func nsFromRaw(raw json.RawMessage) (int64, error) {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err
}

// uintFromRaw is the uint64 equivalent — covers values above INT64_MAX
// for fields like PID and NAnomalies that are documented uint64 in CH.
func uintFromRaw(raw json.RawMessage) (uint64, error) {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	return strconv.ParseUint(s, 10, 64)
}
