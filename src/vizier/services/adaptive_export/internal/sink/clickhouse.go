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

// Package sink writes adaptive_attribution rows to ClickHouse over the
// HTTP interface (default port 8123). One row per arriving kubescape
// anomaly: ReplacingMergeTree(t_end) on the table side collapses
// re-inserts with the same (hostname, anomaly_hash) primary key into
// the row with the largest t_end.
//
// The sink does NOT write pixie observation rows — those are
// populated by Pixie's retention plugin from user-defined PxL scripts.
// The operator's only ClickHouse write surface is forensic_db.adaptive_attribution.
package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// pixieTableIdentRE accepts plain CH identifiers and dotted protobuf
// extensions like `http2_messages.beta`. Used to gate `table` strings
// before they're interpolated into the INSERT query.
var pixieTableIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

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
	if cfg.Database == "" {
		cfg.Database = "forensic_db"
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
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
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
	identifier := table
	if strings.Contains(table, ".") {
		identifier = "`" + table + "`"
	}
	q := url.Values{}
	q.Set("query", fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", s.cfg.Database, identifier))
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("sink: pixie HTTP %d (%s): %s", resp.StatusCode, table, strings.TrimSpace(string(body)))
	}
	return nil
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
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("sink: QueryActive HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseActiveRows(body)
}

// quoteCH wraps a string literal for safe ClickHouse SQL embedding.
func quoteCH(s string) string {
	// ClickHouse uses backslash escapes inside single-quoted literals.
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
	return "'" + r + "'"
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

// parseActiveRows ingests JSONEachRow output from QueryActive and
// converts unix-nano integer fields back into time.Time.
func parseActiveRows(body []byte) ([]AttributionRow, error) {
	type wireRow struct {
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
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	var out []AttributionRow
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var w wireRow
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, fmt.Errorf("sink: parse active row: %w (line=%q)", err, string(line))
		}
		ts, err1 := nsFromRaw(w.TStartNs)
		te, err2 := nsFromRaw(w.TEndNs)
		ls, err3 := nsFromRaw(w.LastSeenNs)
		pidI64, errPID := nsFromRaw(w.PID)
		nAn, errN := nsFromRaw(w.NAnomalies)
		if err1 != nil || err2 != nil || err3 != nil || errPID != nil || errN != nil {
			return nil, fmt.Errorf("sink: parse uint64 fields: t_start=%v t_end=%v last_seen=%v pid=%v n_anomalies=%v", err1, err2, err3, errPID, errN)
		}
		out = append(out, AttributionRow{
			AnomalyHash: anomaly.AnomalyHash(w.AnomalyHash),
			Namespace:   w.Namespace,
			Pod:         w.Pod,
			Comm:        w.Comm,
			PID:         uint64(pidI64),
			Hostname:    w.Hostname,
			TStart:      time.Unix(0, ts).UTC(),
			TEnd:        time.Unix(0, te).UTC(),
			LastSeen:    time.Unix(0, ls).UTC(),
			LastRuleID:  w.LastRuleID,
			NAnomalies:  uint64(nAn),
		})
	}
	return out, nil
}

func nsFromRaw(raw json.RawMessage) (int64, error) {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	var v int64
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}
