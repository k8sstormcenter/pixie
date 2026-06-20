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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/chhttp"
)

// WatermarkStore persists the trigger's per-(hostname,table) cursor
// across operator restarts. Without persistence, every restart on a
// busy node replays kubescape_logs from event_time=0 — multi-GiB
// single-shot SELECTs that the trigger's HTTP client times out on,
// pinning the watermark at 0 forever.
//
// Load returns (watermark, true, nil) when a row exists, or
// (0, false, nil) when no row exists yet (fresh cluster). An error
// returned from Load or Save is logged + non-fatal: the trigger falls
// back to whatever cold-start strategy the caller chose.
type WatermarkStore interface {
	Load(ctx context.Context, hostname, table string) (uint64, bool, error)
	Save(ctx context.Context, hostname, table string, watermark uint64) error
}

// ClickHouseWatermarkStore is the production WatermarkStore — reads
// and writes forensic_db.trigger_watermark over the same HTTP endpoint
// as the rest of the operator. Schema is owned by the clickhouse
// package's Apply (CREATE TABLE IF NOT EXISTS at boot).
type ClickHouseWatermarkStore struct {
	database string
	c        *chhttp.Client
}

// NewClickHouseWatermarkStore validates the endpoint and returns a
// ready store. timeout=0 → chhttp default (watermark IO is tiny, but
// we share the operator's overall conservative network-call budget).
func NewClickHouseWatermarkStore(endpoint, database, user, pass string, timeout time.Duration) (*ClickHouseWatermarkStore, error) {
	if database == "" {
		database = "forensic_db"
	}
	if !validIdentifier(database) {
		return nil, fmt.Errorf("watermark: invalid database identifier %q", database)
	}
	c, err := chhttp.New(endpoint, user, pass, timeout)
	if err != nil {
		return nil, fmt.Errorf("watermark: %w", err)
	}
	return &ClickHouseWatermarkStore{database: database, c: c}, nil
}

// Load returns the most-recent persisted watermark for (hostname, table).
// Uses FINAL — the table is ReplacingMergeTree, and per-(hostname,table)
// cardinality is one, so the cost is negligible. (false, nil, nil) means
// no row exists for the key yet — the trigger's caller chooses cold-start.
func (s *ClickHouseWatermarkStore) Load(ctx context.Context, hostname, table string) (uint64, bool, error) {
	body, err := s.c.Query(ctx, fmt.Sprintf(
		"SELECT watermark FROM %s.trigger_watermark FINAL "+
			"WHERE hostname = %s AND table_name = %s LIMIT 1 FORMAT JSONEachRow",
		s.database, quoteCH(hostname), quoteCH(table)))
	if err != nil {
		return 0, false, fmt.Errorf("watermark load: %w", err)
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return 0, false, nil
	}
	// JSONEachRow returns watermark as a JSON number; UInt64 values
	// above 2^53 lose precision through float64, so we accept either
	// number or string and parse strictly as uint64.
	var raw struct {
		Watermark json.RawMessage `json:"watermark"`
	}
	if err := json.Unmarshal(bytes.Split(body, []byte{'\n'})[0], &raw); err != nil {
		return 0, false, fmt.Errorf("watermark load: parse response: %w", err)
	}
	wm, err := parseUint64Loose(raw.Watermark)
	if err != nil {
		return 0, false, fmt.Errorf("watermark load: %w", err)
	}
	return wm, true, nil
}

// Save inserts a new row. ReplacingMergeTree(updated_at) merges later;
// reads via FINAL always return the freshest. Write is fire-and-merge
// — no UPDATE semantics, no contention with concurrent INSERTs from
// other operator instances (each pins its own hostname).
func (s *ClickHouseWatermarkStore) Save(ctx context.Context, hostname, table string, watermark uint64) error {
	row, err := json.Marshal(struct {
		Hostname  string `json:"hostname"`
		TableName string `json:"table_name"`
		Watermark uint64 `json:"watermark"`
		UpdatedAt string `json:"updated_at"`
	}{
		Hostname:  hostname,
		TableName: table,
		Watermark: watermark,
		UpdatedAt: time.Now().UTC().Format("2006-01-02 15:04:05.000000000"),
	})
	if err != nil {
		return err
	}
	if _, err := s.c.Insert(ctx,
		fmt.Sprintf("INSERT INTO %s.trigger_watermark FORMAT JSONEachRow", s.database),
		row, chhttp.InsertOptions{}); err != nil {
		return fmt.Errorf("watermark save: %w", err)
	}
	return nil
}
