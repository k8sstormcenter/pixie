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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	if _, err := url.Parse(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("trigger: invalid Endpoint %q: %w", cfg.Endpoint, err)
	}
	if cfg.Database == "" {
		cfg.Database = "forensic_db"
	}
	if cfg.Table == "" {
		cfg.Table = "kubescape_logs"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	return &ClickHouseHTTP{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// Subscribe starts the background poll loop. The returned channel
// produces kubescape.Event values until ctx is cancelled, then closes.
func (t *ClickHouseHTTP) Subscribe(ctx context.Context) (<-chan kubescape.Event, error) {
	out := make(chan kubescape.Event, 64)
	go t.run(ctx, out)
	return out, nil
}

func (t *ClickHouseHTTP) run(ctx context.Context, out chan<- kubescape.Event) {
	defer close(out)
	watermark := t.cfg.InitialWatermark
	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	pollOnce := func() {
		rows, maxSeen, err := t.fetchSince(ctx, watermark)
		if err != nil {
			log.WithError(err).Warn("trigger: poll failed")
			return
		}
		for _, row := range rows {
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
		}
		if maxSeen > watermark {
			watermark = maxSeen
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

func (t *ClickHouseHTTP) fetchSince(ctx context.Context, watermark uint64) ([]kubescape.Row, uint64, error) {
	q := url.Values{}
	q.Set("query", fmt.Sprintf(
		"SELECT RuleID, RuntimeK8sDetails, RuntimeProcessDetails, event_time, hostname "+
			"FROM %s.%s "+
			"WHERE hostname = %s AND event_time > %d "+
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return parseJSONEachRow(body)
}

func parseJSONEachRow(body []byte) ([]kubescape.Row, uint64, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, 0, nil
	}
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
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rr rawRow
		if err := json.Unmarshal(line, &rr); err != nil {
			return nil, 0, fmt.Errorf("trigger: parse row: %w (line=%q)", err, string(line))
		}
		ev, err := parseUint64Loose(rr.EventTime)
		if err != nil {
			return nil, 0, fmt.Errorf("trigger: parse event_time: %w", err)
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

func quoteCH(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
	return "'" + r + "'"
}
