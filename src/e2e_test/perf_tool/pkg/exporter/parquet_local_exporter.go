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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/uuid"
	"github.com/parquet-go/parquet-go"
	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/e2e_test/perf_tool/pkg/metrics"
)

// ParquetLocalExporter writes the same parquet artifacts as
// ParquetGCSExporter, but to a directory on the local filesystem instead
// of a GCS bucket. The on-disk layout (`<dir>/<prefix>/YYYY/MM/DD/<expID>/...`)
// matches the GCS object layout exactly, so downstream BigQuery external
// tables, DuckDB readers, or DataStudio connectors can be re-pointed
// with just a base-URL swap.
//
// Use cases:
//   - Iterating on the perf_tool against a local k3s without paying for a
//     GCS bucket round-trip.
//   - CI on hosts without GCP credentials (the build VM in particular).
//   - Reproducing parquet output deterministically for diff'ing.
type ParquetLocalExporter struct {
	dir       string
	prefix    string
	batchSize int
}

// NewParquetLocalExporter constructs a local-fs parquet exporter.
// `dir` is created with mkdir -p semantics if it does not exist.
func NewParquetLocalExporter(dir, prefix string, batchSize int) (*ParquetLocalExporter, error) {
	if dir == "" {
		return nil, errors.New("parquet-local: --parquet_dir is required when using parquet-local backend")
	}
	if batchSize <= 0 {
		return nil, errors.New("parquet-local: batchSize must be > 0")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("parquet-local: mkdir %q: %w", dir, err)
	}
	return &ParquetLocalExporter{
		dir:       dir,
		prefix:    prefix,
		batchSize: batchSize,
	}, nil
}

// ExportResults consumes metrics from resultCh and writes them as
// batched parquet files under the experiment-specific directory.
func (e *ParquetLocalExporter) ExportResults(ctx context.Context, expID uuid.UUID, resultCh <-chan *metrics.ResultRow) error {
	now := time.Now()
	basePath := e.localPath(now, expID)
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return fmt.Errorf("parquet-local: mkdir %q: %w", basePath, err)
	}
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
			if err := e.flushBatch(basePath, seqNum, batch); err != nil {
				return err
			}
			seqNum++
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := e.flushBatch(basePath, seqNum, batch); err != nil {
			return err
		}
	}
	return nil
}

// ExportSpec writes the experiment spec as a parquet file alongside the
// results.
func (e *ParquetLocalExporter) ExportSpec(ctx context.Context, expID uuid.UUID, encodedSpec string, commitTopoOrder int) error {
	type specRow struct {
		ExperimentID    string `parquet:"experiment_id"`
		Spec            string `parquet:"spec"`
		CommitTopoOrder int64  `parquet:"commit_topo_order"`
	}

	now := time.Now()
	basePath := e.localPath(now, expID)
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return fmt.Errorf("parquet-local: mkdir %q: %w", basePath, err)
	}
	dst := filepath.Join(basePath, "spec.parquet")
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("parquet-local: create %q: %w", dst, err)
	}
	writer := parquet.NewGenericWriter[specRow](f)
	if _, err := writer.Write([]specRow{{
		ExperimentID:    expID.String(),
		Spec:            encodedSpec,
		CommitTopoOrder: int64(commitTopoOrder),
	}}); err != nil {
		f.Close()
		return fmt.Errorf("parquet-local: write spec parquet: %w", err)
	}
	if err := writer.Close(); err != nil {
		f.Close()
		return fmt.Errorf("parquet-local: close spec writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("parquet-local: close spec file: %w", err)
	}
	log.WithField("path", dst).Info("Wrote spec parquet")
	return nil
}

// Close releases resources. No-op for the local exporter.
func (e *ParquetLocalExporter) Close() error { return nil }

// localPath mirrors ParquetGCSExporter.gcsPath: <root>/<prefix>/YYYY/MM/DD/<expID>.
func (e *ParquetLocalExporter) localPath(t time.Time, expID uuid.UUID) string {
	datePath := t.Format("2006/01/02")
	if e.prefix != "" {
		return filepath.Join(e.dir, e.prefix, datePath, expID.String())
	}
	return filepath.Join(e.dir, datePath, expID.String())
}

func (e *ParquetLocalExporter) flushBatch(basePath string, seqNum int, rows []bufferedRow) error {
	tagKeys := collectTagKeys(rows)
	schema := buildResultSchema(tagKeys)

	dst := filepath.Join(basePath, fmt.Sprintf("results_%04d.parquet", seqNum))
	tmp, err := os.CreateTemp(basePath, fmt.Sprintf(".results_%04d.*.parquet", seqNum))
	if err != nil {
		return fmt.Errorf("parquet-local: create temp in %q: %w", basePath, err)
	}
	tmpPath := tmp.Name()

	writer := parquet.NewWriter(tmp, schema)
	cleanup := func(wrap string, cause error) error {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("parquet-local: %s: %w", wrap, cause)
	}
	for _, row := range rows {
		parquetRow := buildResultRow(row, tagKeys)
		if _, err := writer.WriteRows([]parquet.Row{parquetRow}); err != nil {
			return cleanup("write row", err)
		}
	}
	if err := writer.Close(); err != nil {
		return cleanup("close writer", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("parquet-local: close temp: %w", err)
	}
	// Atomic publish via rename — temp lives under basePath so we stay
	// on one filesystem.
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("parquet-local: rename %q -> %q: %w", tmpPath, dst, err)
	}
	log.WithField("path", dst).WithField("rows", len(rows)).Info("Wrote parquet batch")
	return nil
}

// Compile-time assertion that ParquetLocalExporter satisfies the
// Exporter interface.
var _ Exporter = (*ParquetLocalExporter)(nil)
