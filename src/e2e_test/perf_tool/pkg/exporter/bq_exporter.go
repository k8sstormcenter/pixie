/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package exporter

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/e2e_test/perf_tool/pkg/metrics"
	"px.dev/pixie/src/shared/bq"
)

// ResultRow represents a single datapoint for a single metric, to be stored in bigquery.
// Each result row is associated to a particular run of an experiment (via ExperimentID).
type ResultRow struct {
	// ExperimentID is a string representation of a UUID.
	ExperimentID string    `bigquery:"experiment_id"`
	Timestamp    time.Time `bigquery:"timestamp"`
	Name         string    `bigquery:"name"`
	Value        float64   `bigquery:"value"`
	// JSON encoded map[string]string of tags.
	Tags string `bigquery:"tags"`
}

// SpecRow stores an experiments ExperimentSpec in bigquery, encoded as JSON.
// SpecRows are only written to bigquery on experiment success, so all results from failed attempts can be ignored by joining on the ExperimentID
type SpecRow struct {
	// ExperimentID is a string representation of a experiment UUID.
	ExperimentID string `bigquery:"experiment_id"`
	// Spec is a json encoded `experimentpb.ExperimentSpec`
	Spec string `bigquery:"spec"`
	// CommitTopoOrder is the number of commits since the beginning of history for the commit this experiment was run on.
	// This is used to order experiments in datastudio views.
	CommitTopoOrder int `bigquery:"commit_topo_order"`
}

// MetricsRowToResultRow converts a `metrics.ResultRow` into a `ResultRow`.
func MetricsRowToResultRow(expID uuid.UUID, row *metrics.ResultRow) (*ResultRow, error) {
	encodedTags, err := json.Marshal(row.Tags)
	if err != nil {
		return nil, err
	}
	return &ResultRow{
		ExperimentID: expID.String(),
		Timestamp:    row.Timestamp,
		Name:         row.Name,
		Value:        row.Value,
		Tags:         string(encodedTags),
	}, nil
}

// BQExporter exports experiment results and specs to BigQuery.
type BQExporter struct {
	resultTable *bq.Table
	specTable   *bq.Table
}

// NewBQExporter creates a new BigQuery exporter.
func NewBQExporter(resultTable, specTable *bq.Table) *BQExporter {
	return &BQExporter{
		resultTable: resultTable,
		specTable:   specTable,
	}
}

// ExportResults consumes metrics from resultCh and inserts them into BigQuery in batches.
// Returns when resultCh closes; waits for the inserter goroutine to drain
// the final batch before returning, so callers can rely on "ExportResults
// returned without error" meaning every row was actually pushed to BQ.
func (e *BQExporter) ExportResults(ctx context.Context, expID uuid.UUID, resultCh <-chan *metrics.ResultRow) error {
	bqCh := make(chan interface{})

	inserter := &bq.BatchInserter{
		Table:       e.resultTable,
		BatchSize:   512,
		PushTimeout: 2 * time.Minute,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		inserter.Run(bqCh)
	}()

	for {
		select {
		case <-ctx.Done():
			close(bqCh)
			<-done
			return ctx.Err()
		case row, ok := <-resultCh:
			if !ok {
				// resultCh closed → close bqCh so the inserter drains its
				// last batch, then wait for inserter goroutine exit before
				// returning so the caller doesn't observe an in-flight push.
				close(bqCh)
				<-done
				return nil
			}
			bqRow, err := MetricsRowToResultRow(expID, row)
			if err != nil {
				log.WithError(err).Error("Failed to convert result row")
				continue
			}
			bqCh <- bqRow
		}
	}
}

// ExportSpec writes the experiment spec to BigQuery on experiment success.
func (e *BQExporter) ExportSpec(ctx context.Context, expID uuid.UUID, encodedSpec string, commitTopoOrder int) error {
	specRow := &SpecRow{
		ExperimentID:    expID.String(),
		Spec:            encodedSpec,
		CommitTopoOrder: commitTopoOrder,
	}

	inserter := e.specTable.Inserter()
	inserter.SkipInvalidRows = false

	putCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return inserter.Put(putCtx, specRow)
}

// Close is a no-op for the BigQuery exporter.
func (e *BQExporter) Close() error {
	return nil
}
