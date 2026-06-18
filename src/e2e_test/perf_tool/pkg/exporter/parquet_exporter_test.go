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
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"px.dev/pixie/src/e2e_test/perf_tool/pkg/metrics"
)

func TestCollectTagKeys(t *testing.T) {
	rows := []bufferedRow{
		{Tags: map[string]string{"pod": "pod-1", "node_name": "node-1"}},
		{Tags: map[string]string{"pod": "pod-2", "instance": "inst-1"}},
		{Tags: map[string]string{}},
	}

	keys := collectTagKeys(rows)

	assert.Equal(t, []string{"instance", "node_name", "pod"}, keys)
}

func TestCollectTagKeys_Empty(t *testing.T) {
	rows := []bufferedRow{
		{Tags: map[string]string{}},
	}

	keys := collectTagKeys(rows)

	assert.Empty(t, keys)
}

func TestBuildResultSchema(t *testing.T) {
	tagKeys := []string{"node_name", "pod"}

	schema := buildResultSchema(tagKeys)

	fields := schema.Fields()
	fieldNames := make([]string, len(fields))
	for i, f := range fields {
		fieldNames[i] = f.Name()
	}
	sort.Strings(fieldNames)

	assert.Equal(t, []string{
		"experiment_id",
		"name",
		"tag_node_name",
		"tag_pod",
		"timestamp",
		"value",
	}, fieldNames)
}

func TestBuildResultRow_AllTagsPresent(t *testing.T) {
	ts := time.Date(2026, 4, 15, 10, 30, 0, 0, time.UTC)
	row := bufferedRow{
		ExperimentID: "test-id",
		Timestamp:    ts,
		Name:         "cpu_usage",
		Value:        42.5,
		Tags:         map[string]string{"pod": "pod-1", "node_name": "node-1"},
	}
	tagKeys := []string{"node_name", "pod"}

	parquetRow := buildResultRow(row, tagKeys)

	// Schema sorts fields alphabetically:
	// experiment_id, name, tag_node_name, tag_pod, timestamp, value
	assert.Equal(t, 6, len(parquetRow))

	// Verify column indices are sequential.
	for i, v := range parquetRow {
		assert.Equal(t, i, v.Column(), "column index mismatch at position %d", i)
	}
}

func TestBuildResultRow_MissingTag(t *testing.T) {
	ts := time.Date(2026, 4, 15, 10, 30, 0, 0, time.UTC)
	row := bufferedRow{
		ExperimentID: "test-id",
		Timestamp:    ts,
		Name:         "rss",
		Value:        1024.0,
		Tags:         map[string]string{"pod": "pod-1"},
	}
	tagKeys := []string{"node_name", "pod"}

	parquetRow := buildResultRow(row, tagKeys)

	assert.Equal(t, 6, len(parquetRow))

	// Find the tag_node_name column (should be null).
	// Alphabetical order: experiment_id(0), name(1), tag_node_name(2), tag_pod(3), timestamp(4), value(5)
	tagNodeNameVal := parquetRow[2]
	assert.True(t, tagNodeNameVal.IsNull(), "missing tag should produce a null value")
	assert.Equal(t, 0, tagNodeNameVal.DefinitionLevel(), "null optional field should have definitionLevel=0")

	// tag_pod should be present.
	tagPodVal := parquetRow[3]
	assert.False(t, tagPodVal.IsNull())
	assert.Equal(t, 1, tagPodVal.DefinitionLevel(), "present optional field should have definitionLevel=1")
}

func TestFlushBatch_WritesValidParquet(t *testing.T) {
	tmpDir := t.TempDir()
	var uploadedPath string

	e := &ParquetGCSExporter{
		batchSize: 100,
		upload: func(ctx context.Context, objectPath string, localPath string) error {
			// Copy the parquet file to our temp dir before it gets cleaned up.
			dest := filepath.Join(tmpDir, filepath.Base(objectPath))
			src, err := os.Open(localPath)
			if err != nil {
				return err
			}
			defer src.Close()
			dst, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer dst.Close()
			if _, err := io.Copy(dst, src); err != nil {
				return err
			}
			uploadedPath = dest
			return nil
		},
	}

	ts := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	rows := []bufferedRow{
		{
			ExperimentID: "exp-1",
			Timestamp:    ts,
			Name:         "cpu_usage",
			Value:        0.85,
			Tags:         map[string]string{"pod": "kelvin-abc", "node_name": "node-1"},
		},
		{
			ExperimentID: "exp-1",
			Timestamp:    ts.Add(30 * time.Second),
			Name:         "rss",
			Value:        1048576,
			Tags:         map[string]string{"pod": "kelvin-abc"},
		},
	}

	err := e.flushBatch(context.Background(), "test/path", 0, rows)
	require.NoError(t, err)
	require.NotEmpty(t, uploadedPath)

	// Read back the parquet file and verify contents.
	f, err := os.Open(uploadedPath)
	require.NoError(t, err)
	defer f.Close()

	stat, err := f.Stat()
	require.NoError(t, err)

	pf, err := parquet.OpenFile(f, stat.Size())
	require.NoError(t, err)

	schema := pf.Schema()
	assert.Equal(t, int64(2), pf.NumRows())

	// Verify schema has expected columns.
	fields := schema.Fields()
	fieldNames := make([]string, len(fields))
	for i, f := range fields {
		fieldNames[i] = f.Name()
	}
	sort.Strings(fieldNames)
	assert.Equal(t, []string{
		"experiment_id",
		"name",
		"tag_node_name",
		"tag_pod",
		"timestamp",
		"value",
	}, fieldNames)

	// Re-open the file for the reader (the File consumed the initial handle).
	f2, err := os.Open(uploadedPath)
	require.NoError(t, err)
	defer f2.Close()

	reader := parquet.NewReader(f2)
	defer reader.Close()

	parquetRows := make([]parquet.Row, 2)
	n, err := reader.ReadRows(parquetRows)
	// ReadRows returns io.EOF when it reaches the end, even if it read rows.
	if err != nil && !errors.Is(err, io.EOF) {
		require.NoError(t, err)
	}
	assert.Equal(t, 2, n)

	// First row should have all tags present.
	// Second row should have tag_node_name as null.
	// Column order (alphabetical): experiment_id(0), name(1), tag_node_name(2), tag_pod(3), timestamp(4), value(5)
	row0NodeName := parquetRows[0][2]
	assert.False(t, row0NodeName.IsNull(), "first row tag_node_name should be present")

	row1NodeName := parquetRows[1][2]
	assert.True(t, row1NodeName.IsNull(), "second row tag_node_name should be null")
}

func TestExportResults_SingleBatch(t *testing.T) {
	tmpDir := t.TempDir()
	uploadedFiles := make(map[string]string)

	expID := uuid.Must(uuid.NewV4())
	e := &ParquetGCSExporter{
		prefix:    "perf-results",
		batchSize: 100,
		upload: func(ctx context.Context, objectPath string, localPath string) error {
			dest := filepath.Join(tmpDir, strings.ReplaceAll(objectPath, "/", "_"))
			src, err := os.Open(localPath)
			if err != nil {
				return err
			}
			defer src.Close()
			dst, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer dst.Close()
			if _, err := io.Copy(dst, src); err != nil {
				return err
			}
			uploadedFiles[objectPath] = dest
			return nil
		},
	}

	resultCh := make(chan *metrics.ResultRow, 3)
	ts := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)
	resultCh <- &metrics.ResultRow{
		Timestamp: ts,
		Name:      "cpu_seconds_counter",
		Value:     100.5,
		Tags:      map[string]string{"pod": "server-abc"},
	}
	resultCh <- &metrics.ResultRow{
		Timestamp: ts.Add(30 * time.Second),
		Name:      "rss",
		Value:     2097152,
		Tags:      map[string]string{"pod": "server-abc", "node_name": "node-0"},
	}
	resultCh <- &metrics.ResultRow{
		Timestamp: ts.Add(60 * time.Second),
		Name:      "vsize",
		Value:     4194304,
		Tags:      map[string]string{"pod": "server-abc", "node_name": "node-0"},
	}
	close(resultCh)

	err := e.ExportResults(context.Background(), expID, resultCh)
	require.NoError(t, err)

	// Should have produced exactly one batch file.
	assert.Equal(t, 1, len(uploadedFiles), "expected exactly one parquet file")

	// Verify the GCS path includes the date and experiment ID.
	for objectPath := range uploadedFiles {
		assert.Contains(t, objectPath, expID.String())
		assert.Contains(t, objectPath, "perf-results/")
		assert.Contains(t, objectPath, "results_0000.parquet")
	}

	// Read the parquet file and verify row count.
	for _, localPath := range uploadedFiles {
		f, err := os.Open(localPath)
		require.NoError(t, err)
		defer f.Close()

		stat, err := f.Stat()
		require.NoError(t, err)

		pf, err := parquet.OpenFile(f, stat.Size())
		require.NoError(t, err)
		assert.Equal(t, int64(3), pf.NumRows())

		// Verify schema has tag columns from the union of all rows.
		fields := pf.Schema().Fields()
		fieldNames := make([]string, len(fields))
		for i, f := range fields {
			fieldNames[i] = f.Name()
		}
		sort.Strings(fieldNames)
		assert.Equal(t, []string{
			"experiment_id",
			"name",
			"tag_node_name",
			"tag_pod",
			"timestamp",
			"value",
		}, fieldNames)
	}
}

func TestExportResults_MultipleBatches(t *testing.T) {
	tmpDir := t.TempDir()
	uploadedFiles := make(map[string]string)

	expID := uuid.Must(uuid.NewV4())
	e := &ParquetGCSExporter{
		batchSize: 2, // Small batch size to force multiple files.
		upload: func(ctx context.Context, objectPath string, localPath string) error {
			dest := filepath.Join(tmpDir, strings.ReplaceAll(objectPath, "/", "_"))
			src, err := os.Open(localPath)
			if err != nil {
				return err
			}
			defer src.Close()
			dst, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer dst.Close()
			if _, err := io.Copy(dst, src); err != nil {
				return err
			}
			uploadedFiles[objectPath] = dest
			return nil
		},
	}

	resultCh := make(chan *metrics.ResultRow, 5)
	ts := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		resultCh <- &metrics.ResultRow{
			Timestamp: ts.Add(time.Duration(i) * 30 * time.Second),
			Name:      "cpu_usage",
			Value:     float64(i) * 0.1,
			Tags:      map[string]string{"pod": "test-pod"},
		}
	}
	close(resultCh)

	err := e.ExportResults(context.Background(), expID, resultCh)
	require.NoError(t, err)

	// 5 rows with batch size 2 should produce 3 files: [2, 2, 1].
	assert.Equal(t, 3, len(uploadedFiles), "expected 3 parquet files for 5 rows with batch size 2")

	// Verify file naming.
	hasFile0, hasFile1, hasFile2 := false, false, false
	for objectPath := range uploadedFiles {
		if strings.Contains(objectPath, "results_0000.parquet") {
			hasFile0 = true
		}
		if strings.Contains(objectPath, "results_0001.parquet") {
			hasFile1 = true
		}
		if strings.Contains(objectPath, "results_0002.parquet") {
			hasFile2 = true
		}
	}
	assert.True(t, hasFile0, "missing results_0000.parquet")
	assert.True(t, hasFile1, "missing results_0001.parquet")
	assert.True(t, hasFile2, "missing results_0002.parquet")

	// Verify total row count across all files.
	totalRows := int64(0)
	for _, localPath := range uploadedFiles {
		f, err := os.Open(localPath)
		require.NoError(t, err)
		defer f.Close()
		stat, err := f.Stat()
		require.NoError(t, err)
		pf, err := parquet.OpenFile(f, stat.Size())
		require.NoError(t, err)
		totalRows += pf.NumRows()
	}
	assert.Equal(t, int64(5), totalRows)
}

func TestExportResults_EmptyChannel(t *testing.T) {
	uploadCalled := false
	e := &ParquetGCSExporter{
		batchSize: 100,
		upload: func(ctx context.Context, objectPath string, localPath string) error {
			uploadCalled = true
			return nil
		},
	}

	resultCh := make(chan *metrics.ResultRow)
	close(resultCh)

	expID := uuid.Must(uuid.NewV4())
	err := e.ExportResults(context.Background(), expID, resultCh)
	require.NoError(t, err)
	assert.False(t, uploadCalled, "no files should be uploaded for empty channel")
}

// --- Benchmarks ---

// makeBenchRows generates n buffered rows with the specified number of tag keys.
func makeBenchRows(n int, numTags int) []bufferedRow {
	ts := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	rows := make([]bufferedRow, n)
	for i := range rows {
		tags := make(map[string]string, numTags)
		for j := 0; j < numTags; j++ {
			tags[fmt.Sprintf("tag_key_%d", j)] = fmt.Sprintf("value_%d_%d", i, j)
		}
		rows[i] = bufferedRow{
			ExperimentID: "bench-exp-id",
			Timestamp:    ts.Add(time.Duration(i) * 30 * time.Second),
			Name:         "cpu_usage",
			Value:        float64(i) * 0.01,
			Tags:         tags,
		}
	}
	return rows
}

func BenchmarkBuildResultRow(b *testing.B) {
	for _, numTags := range []int{2, 5, 10} {
		b.Run(fmt.Sprintf("tags=%d", numTags), func(b *testing.B) {
			rows := makeBenchRows(1, numTags)
			tagKeys := collectTagKeys(rows)
			row := rows[0]
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buildResultRow(row, tagKeys)
			}
		})
	}
}

func BenchmarkCollectTagKeys(b *testing.B) {
	for _, numRows := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", numRows), func(b *testing.B) {
			rows := makeBenchRows(numRows, 3)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				collectTagKeys(rows)
			}
		})
	}
}

func BenchmarkFlushBatch(b *testing.B) {
	for _, numRows := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", numRows), func(b *testing.B) {
			rows := makeBenchRows(numRows, 3)
			e := &ParquetGCSExporter{
				batchSize: numRows,
				upload: func(ctx context.Context, objectPath string, localPath string) error {
					// No-op upload: measures only in-memory conversion + parquet write to disk.
					return nil
				},
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := e.flushBatch(context.Background(), "bench/path", 0, rows); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
