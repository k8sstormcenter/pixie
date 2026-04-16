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
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gofrs/uuid"
	"github.com/parquet-go/parquet-go"
	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/e2e_test/perf_tool/pkg/metrics"
)

type bufferedRow struct {
	ExperimentID string
	Timestamp    time.Time
	Name         string
	Value        float64
	Tags         map[string]string
}

// uploadFunc is the signature for uploading a local file to a remote path.
type uploadFunc func(ctx context.Context, objectPath string, localPath string) error

// ParquetGCSExporter exports experiment results as parquet files to GCS.
type ParquetGCSExporter struct {
	bucket    string
	prefix    string
	batchSize int
	gcsClient *storage.Client
	upload    uploadFunc
}

// NewParquetGCSExporter creates a new Parquet+GCS exporter.
func NewParquetGCSExporter(ctx context.Context, bucket, prefix string, batchSize int) (*ParquetGCSExporter, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}
	e := &ParquetGCSExporter{
		bucket:    bucket,
		prefix:    prefix,
		batchSize: batchSize,
		gcsClient: client,
	}
	e.upload = e.uploadToGCS
	return e, nil
}

// ExportResults consumes metrics from resultCh and writes them as batched parquet files to GCS.
func (e *ParquetGCSExporter) ExportResults(ctx context.Context, expID uuid.UUID, resultCh <-chan *metrics.ResultRow) error {
	now := time.Now()
	basePath := e.gcsPath(now, expID)
	seqNum := 0
	batch := make([]bufferedRow, 0, e.batchSize)

	for row := range resultCh {
		batch = append(batch, bufferedRow{
			ExperimentID: expID.String(),
			Timestamp:    row.Timestamp,
			Name:         row.Name,
			Value:        row.Value,
			Tags:         row.Tags,
		})
		if len(batch) >= e.batchSize {
			if err := e.flushBatch(ctx, basePath, seqNum, batch); err != nil {
				return err
			}
			seqNum++
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := e.flushBatch(ctx, basePath, seqNum, batch); err != nil {
			return err
		}
	}
	return nil
}

// ExportSpec writes the experiment spec as a parquet file to GCS.
func (e *ParquetGCSExporter) ExportSpec(ctx context.Context, expID uuid.UUID, encodedSpec string, commitTopoOrder int) error {
	type specRow struct {
		ExperimentID    string `parquet:"experiment_id"`
		Spec            string `parquet:"spec"`
		CommitTopoOrder int64  `parquet:"commit_topo_order"`
	}

	tmpFile, err := os.CreateTemp("", "spec-*.parquet")
	if err != nil {
		return fmt.Errorf("failed to create temp file for spec parquet: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	writer := parquet.NewGenericWriter[specRow](tmpFile)
	_, err = writer.Write([]specRow{{
		ExperimentID:    expID.String(),
		Spec:            encodedSpec,
		CommitTopoOrder: int64(commitTopoOrder),
	}})
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write spec parquet: %w", err)
	}
	if err := writer.Close(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to close spec parquet writer: %w", err)
	}
	tmpFile.Close()

	now := time.Now()
	gcsPath := fmt.Sprintf("%s/spec.parquet", e.gcsPath(now, expID))
	return e.upload(ctx, gcsPath, tmpPath)
}

// Close releases resources held by the exporter.
func (e *ParquetGCSExporter) Close() error {
	return e.gcsClient.Close()
}

func (e *ParquetGCSExporter) gcsPath(t time.Time, expID uuid.UUID) string {
	datePath := t.Format("2006/01/02")
	if e.prefix != "" {
		return fmt.Sprintf("%s/%s/%s", e.prefix, datePath, expID.String())
	}
	return fmt.Sprintf("%s/%s", datePath, expID.String())
}

func (e *ParquetGCSExporter) flushBatch(ctx context.Context, basePath string, seqNum int, rows []bufferedRow) error {
	tagKeys := collectTagKeys(rows)
	schema := buildResultSchema(tagKeys)

	tmpFile, err := os.CreateTemp("", "results-*.parquet")
	if err != nil {
		return fmt.Errorf("failed to create temp file for parquet: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	writer := parquet.NewWriter(tmpFile, schema)

	for _, row := range rows {
		parquetRow := buildResultRow(row, tagKeys)
		if _, err := writer.WriteRows([]parquet.Row{parquetRow}); err != nil {
			tmpFile.Close()
			return fmt.Errorf("failed to write parquet row: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to close parquet writer: %w", err)
	}
	tmpFile.Close()

	gcsPath := fmt.Sprintf("%s/results_%04d.parquet", basePath, seqNum)
	log.WithField("gcs_path", gcsPath).WithField("rows", len(rows)).Info("Uploading parquet batch")
	return e.upload(ctx, gcsPath, tmpPath)
}

func (e *ParquetGCSExporter) uploadToGCS(ctx context.Context, objectPath string, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open temp file for upload: %w", err)
	}
	defer f.Close()

	obj := e.gcsClient.Bucket(e.bucket).Object(objectPath)
	wc := obj.NewWriter(ctx)
	if _, err := io.Copy(wc, f); err != nil {
		wc.Close()
		return fmt.Errorf("failed to upload to GCS: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to finalize GCS upload: %w", err)
	}
	return nil
}

// collectTagKeys returns a sorted list of unique tag keys across all rows.
func collectTagKeys(rows []bufferedRow) []string {
	keySet := make(map[string]struct{})
	for _, row := range rows {
		for k := range row.Tags {
			keySet[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildResultSchema creates a parquet schema with fixed columns plus dynamic tag columns.
func buildResultSchema(tagKeys []string) *parquet.Schema {
	group := parquet.Group{
		"experiment_id": parquet.String(),
		"timestamp":     parquet.Timestamp(parquet.Millisecond),
		"name":          parquet.String(),
		"value":         parquet.Leaf(parquet.DoubleType),
	}
	for _, key := range tagKeys {
		group["tag_"+key] = parquet.Optional(parquet.String())
	}
	return parquet.NewSchema("result", group)
}

// buildResultRow constructs a parquet.Row from a bufferedRow with the given tag key ordering.
// Column ordering matches the schema's sorted field order (alphabetical by field name).
func buildResultRow(row bufferedRow, tagKeys []string) parquet.Row {
	// parquet.Group sorts fields alphabetically. We must produce values in that order.
	// Build named values, sort them, then assign column indices.

	type colEntry struct {
		name     string
		val      parquet.Value
		optional bool
	}

	entries := []colEntry{
		{"experiment_id", parquet.ValueOf(row.ExperimentID), false},
		{"name", parquet.ValueOf(row.Name), false},
		{"timestamp", parquet.ValueOf(row.Timestamp), false},
		{"value", parquet.ValueOf(row.Value), false},
	}

	for _, key := range tagKeys {
		colName := "tag_" + key
		if v, ok := row.Tags[key]; ok {
			entries = append(entries, colEntry{colName, parquet.ValueOf(v), true})
		} else {
			// Null value for missing optional tag.
			entries = append(entries, colEntry{colName, parquet.Value{}, true})
		}
	}

	// Sort by column name to match schema field order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	parquetRow := make(parquet.Row, len(entries))
	for i, e := range entries {
		if e.optional {
			if e.val.IsNull() {
				// Null optional: definitionLevel=0
				parquetRow[i] = parquet.Value{}.Level(0, 0, i)
			} else {
				// Present optional: definitionLevel=1
				parquetRow[i] = e.val.Level(0, 1, i)
			}
		} else {
			// Required: definitionLevel=0
			parquetRow[i] = e.val.Level(0, 0, i)
		}
	}
	return parquetRow
}
