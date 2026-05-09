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

// Package trigger watches forensic_db.kubescape_logs for new rows and
// pushes parsed kubescape.Event values onto a channel. Polls the
// ClickHouse HTTP interface (default 250ms cadence). Operator runs as
// a DaemonSet — each instance polls only its OWN node's rows via
// `WHERE hostname = '<this-node>'`.
package trigger

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/kubescape"
)

// Config configures the trigger. PollInterval defaults to 250ms.
// Hostname is REQUIRED — it scopes every poll to a single node.
type Config struct {
	Endpoint         string
	Database         string
	Table            string
	Username         string
	Password         string
	Hostname         string
	PollInterval     time.Duration
	InitialWatermark uint64
}

// ClickHouseHTTP polls forensic_db.<table> over the ClickHouse HTTP
// interface, scoped to a single node.
type ClickHouseHTTP struct {
	cfg    Config
	client *http.Client
}

// New validates Config and returns a ready trigger.
func New(cfg Config) (*ClickHouseHTTP, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("trigger: empty Endpoint")
	}
	if cfg.Hostname == "" {
		return nil, fmt.Errorf("trigger: empty Hostname (operator must run node-local)")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("trigger: invalid Endpoint %q: %w", cfg.Endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("trigger: Endpoint %q must use http or https scheme", cfg.Endpoint)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("trigger: Endpoint %q has empty host", cfg.Endpoint)
	}
	if cfg.Database == "" {
		cfg.Database = "forensic_db"
	}
	if cfg.Table == "" {
		cfg.Table = "kubescape_logs"
	}
	// Validate Database / Table as plain ClickHouse identifiers
	// (alphanumeric + underscore, not starting with a digit) so the
	// SELECT in fetchSince cannot be subverted by an attacker-controlled
	// Config. Hostname is value-quoted via quoteCH; identifiers cannot
	// be parameterised, hence validation here.
	if !validIdentifier(cfg.Database) {
		return nil, fmt.Errorf("trigger: invalid Database identifier %q (must match [A-Za-z_][A-Za-z0-9_]*)", cfg.Database)
	}
	if !validIdentifier(cfg.Table) {
		return nil, fmt.Errorf("trigger: invalid Table identifier %q (must match [A-Za-z_][A-Za-z0-9_]*)", cfg.Table)
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	return &ClickHouseHTTP{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// identifierRE accepts plain ClickHouse identifiers — letters, digits,
// underscores; not starting with a digit. Dotted identifiers (e.g.
// "http2_messages.beta") are deliberately rejected here because the
// trigger only ever queries the kubescape ingest table, not a pixie
// observation table.
var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validIdentifier(s string) bool { return identifierRE.MatchString(s) }

// Subscribe starts the background poll loop. The returned channel
// produces kubescape.Event values until ctx is cancelled, then closes.
func (t *ClickHouseHTTP) Subscribe(ctx context.Context) (<-chan kubescape.Event, error) {
	out := make(chan kubescape.Event, 64)
	go t.run(ctx, out)
	return out, nil
}

func (t *ClickHouseHTTP) run(ctx context.Context, out chan<- kubescape.Event) {
	defer close(out)
	// Watermark uses event_time as the cursor PLUS a set of row
	// fingerprints already pushed at that exact event_time. This
	// closes the race where two kubescape rows share the same
	// event_time but the second arrives after our previous poll: the
	// query is `event_time >= watermark` (inclusive) and we skip rows
	// whose fingerprint we have already seen at the boundary.
	watermark := t.cfg.InitialWatermark
	seenAtBoundary := map[string]bool{}
	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	pollOnce := func() {
		rows, maxSeen, err := t.fetchSince(ctx, watermark)
		if err != nil {
			log.WithError(err).Warn("trigger: poll failed")
			return
		}
		nextSeen := map[string]bool{}
		for _, row := range rows {
			fp := rowFingerprint(row)
			if row.EventTime == watermark && seenAtBoundary[fp] {
				continue // already pushed in a prior poll at this exact boundary
			}
			ev, err := kubescape.Extract(row)
			if err != nil {
				log.WithError(err).Debug("trigger: skip incomplete row")
				continue
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
			if row.EventTime == maxSeen {
				nextSeen[fp] = true
			}
		}
		if maxSeen > watermark {
			watermark = maxSeen
			seenAtBoundary = nextSeen
		} else if maxSeen == watermark {
			// no progress this tick — preserve boundary set, optionally extend
			for fp := range nextSeen {
				seenAtBoundary[fp] = true
			}
		}
	}

	pollOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollOnce()
		}
	}
}

// rowFingerprint hashes the row's content so we can dedupe at the
// watermark boundary without trusting kubescape to give us a unique row id.
func rowFingerprint(r kubescape.Row) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s",
		r.EventTime, r.RuleID, r.Hostname, r.K8sDetails, r.ProcessDetails)
	return hex.EncodeToString(h.Sum(nil))
}

func (t *ClickHouseHTTP) fetchSince(ctx context.Context, watermark uint64) ([]kubescape.Row, uint64, error) {
	q := url.Values{}
	q.Set("query", fmt.Sprintf(
		"SELECT RuleID, RuntimeK8sDetails, RuntimeProcessDetails, event_time, hostname "+
			"FROM %s.%s "+
			"WHERE hostname = %s AND event_time >= %d "+
			"ORDER BY event_time FORMAT JSONEachRow",
		t.cfg.Database, t.cfg.Table, quoteCH(t.cfg.Hostname), watermark))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		t.cfg.Endpoint+"/?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	if t.cfg.Username != "" {
		req.SetBasicAuth(t.cfg.Username, t.cfg.Password)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseJSONEachRow(resp.Body)
}

// parseJSONEachRow streams JSONEachRow output line-by-line from r.
// Streaming (vs io.ReadAll into a []byte) bounds memory at one row
// regardless of how large the ClickHouse result set is.
//
// Malformed rows are LOGGED + SKIPPED, never fatal: a single bad line
// must not block watermark advancement and re-pin the bad row on every
// subsequent poll. Only an unrecoverable scanner error (e.g. line
// exceeds the 16 MiB buffer) fails the call.
func parseJSONEachRow(r io.Reader) ([]kubescape.Row, uint64, error) {
	type rawRow struct {
		RuleID                string          `json:"RuleID"`
		RuntimeK8sDetails     string          `json:"RuntimeK8sDetails"`
		RuntimeProcessDetails string          `json:"RuntimeProcessDetails"`
		EventTime             json.RawMessage `json:"event_time"`
		Hostname              string          `json:"hostname"`
	}
	var (
		rows    []kubescape.Row
		maxSeen uint64
	)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rr rawRow
		if err := json.Unmarshal(line, &rr); err != nil {
			log.WithError(err).Debug("trigger: skip malformed JSON row")
			continue
		}
		ev, err := parseUint64Loose(rr.EventTime)
		if err != nil {
			log.WithError(err).Debug("trigger: skip row with bad event_time")
			continue
		}
		rows = append(rows, kubescape.Row{
			EventTime:      ev,
			RuleID:         rr.RuleID,
			Hostname:       rr.Hostname,
			K8sDetails:     rr.RuntimeK8sDetails,
			ProcessDetails: rr.RuntimeProcessDetails,
		})
		if ev > maxSeen {
			maxSeen = ev
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return rows, maxSeen, nil
}

func parseUint64Loose(raw json.RawMessage) (uint64, error) {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	return strconv.ParseUint(s, 10, 64)
}

// chLiteralEscaper — hoisted to a package-level var so we don't allocate
// a Replacer per call (quoteCH is hot in rowFingerprint).
var chLiteralEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func quoteCH(s string) string {
	return "'" + chLiteralEscaper.Replace(s) + "'"
}
