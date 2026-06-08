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

package streaming

import (
	"context"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// SinkWriter is the abstraction over sink.WritePixieRows. Defining
// it here avoids a sink package import cycle and lets tests inject
// fakes.
type SinkWriter interface {
	WritePixieRows(ctx context.Context, table string, rows []map[string]any) error
}

// BatchWriter buffers per-table pixie rows and flushes them as one
// CH INSERT either when the buffer hits BatchRows OR when BatchEvery
// elapses since the last successful flush, whichever comes first.
// One goroutine per BatchWriter.
//
// Why batching: rev-2's per-hash fan-out produced ~10 small INSERTs
// per pass per pod. CH handles small INSERTs poorly (each spawns a
// merge; merge throughput is the bottleneck on heavily-active
// tables). One larger INSERT per N seconds dramatically reduces
// merge pressure.
type BatchWriter struct {
	table      string
	sink       SinkWriter
	in         chan []map[string]any
	batchRows  int
	batchEvery time.Duration
	bufferCap  int

	// Counters exposed via Stats — read-only after Run starts.
	written atomic.Int64
	dropped atomic.Int64
	flushes atomic.Int64
	errors  atomic.Int64
}

// WriterConfig tunes a BatchWriter. Zero → defaults.
type WriterConfig struct {
	BatchRows  int           // flush when buffered ≥ this many rows. default 10000.
	BatchEvery time.Duration // flush when this much time has elapsed. default 5 s.
	BufferCap  int           // input chan capacity (rows-of-batches). default 64.
}

func (c WriterConfig) defaulted() WriterConfig {
	if c.BatchRows <= 0 {
		c.BatchRows = 10000
	}
	if c.BatchEvery <= 0 {
		c.BatchEvery = 5 * time.Second
	}
	if c.BufferCap <= 0 {
		c.BufferCap = 64
	}
	return c
}

// NewBatchWriter constructs but does not start the writer.
func NewBatchWriter(table string, sink SinkWriter, cfg WriterConfig) *BatchWriter {
	cfg = cfg.defaulted()
	return &BatchWriter{
		table:      table,
		sink:       sink,
		in:         make(chan []map[string]any, cfg.BufferCap),
		batchRows:  cfg.BatchRows,
		batchEvery: cfg.BatchEvery,
		bufferCap:  cfg.BufferCap,
	}
}

// Submit hands rows to the writer. Non-blocking — if the input chan
// is full, the rows are DROPPED (oldest semantics handled at the
// table-scanner level; per-call drop here is the simpler contract).
// Returns true if accepted, false if dropped. Caller can log on drop.
func (w *BatchWriter) Submit(rows []map[string]any) bool {
	if len(rows) == 0 {
		return true
	}
	select {
	case w.in <- rows:
		return true
	default:
		w.dropped.Add(int64(len(rows)))
		return false
	}
}

// Run owns the BatchWriter goroutine. Returns when ctx is cancelled,
// after attempting a best-effort final flush.
func (w *BatchWriter) Run(ctx context.Context) {
	var buf []map[string]any
	ticker := time.NewTicker(w.batchEvery)
	defer ticker.Stop()

	flush := func(reason string) {
		if len(buf) == 0 {
			return
		}
		// Bound the CH write so a stalled CH HTTP doesn't pin us.
		fctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := w.sink.WritePixieRows(fctx, w.table, buf)
		cancel()
		if err != nil {
			w.errors.Add(1)
			log.WithError(err).WithFields(log.Fields{
				"table":  w.table,
				"rows":   len(buf),
				"reason": reason,
			}).Warn("streaming.BatchWriter: flush failed")
		} else {
			w.written.Add(int64(len(buf)))
			w.flushes.Add(1)
			log.WithFields(log.Fields{
				"table":  w.table,
				"rows":   len(buf),
				"reason": reason,
			}).Info("streaming.BatchWriter: flushed batch")
		}
		buf = buf[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush("shutdown")
			return

		case rows := <-w.in:
			buf = append(buf, rows...)
			if len(buf) >= w.batchRows {
				flush("size")
				// Reset ticker so we don't get a redundant flush 100ms later
				ticker.Reset(w.batchEvery)
			}

		case <-ticker.C:
			flush("timer")
		}
	}
}

// Stats snapshots the four counters.
type Stats struct {
	Written int64
	Dropped int64
	Flushes int64
	Errors  int64
}

// Stats returns a Stats snapshot (atomic loads).
func (w *BatchWriter) Stats() Stats {
	return Stats{
		Written: w.written.Load(),
		Dropped: w.dropped.Load(),
		Flushes: w.flushes.Load(),
		Errors:  w.errors.Load(),
	}
}
