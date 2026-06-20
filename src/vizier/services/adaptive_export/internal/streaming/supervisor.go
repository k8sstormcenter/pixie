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
	"sync"

	log "github.com/sirupsen/logrus"
)

// Supervisor owns the lifecycle of N TableScanner + N BatchWriter
// pairs (one pair per pixie table) plus the shared FilterUpdater.
// Single entry point from main.go.
//
// Goroutine inventory at steady state:
//
//	1 FilterUpdater
//	N TableScanners      (1 per pixie table)
//	N BatchWriters       (1 per pixie table)
//	──────────────────
//	1 + 2N total
//
// For N=10 (current PushPixieTables count): 21 goroutines, constant
// regardless of active hash count.
type Supervisor struct {
	updater  *FilterUpdater
	scanners []*TableScanner
	writers  []*BatchWriter
	tables   []string

	wg sync.WaitGroup
}

// NewSupervisor wires up scanners + writers for the given table list.
// One scanner + one writer per table. Each scanner gets its own
// channel from the updater.
func NewSupervisor(
	updater *FilterUpdater,
	querier Querier,
	sink SinkWriter,
	tables []string,
	scannerCfg ScannerConfig,
	writerCfg WriterConfig,
) *Supervisor {
	s := &Supervisor{
		updater: updater,
		tables:  tables,
	}
	for _, t := range tables {
		w := NewBatchWriter(t, sink, writerCfg)
		c := scannerCfg
		c.Table = t
		sc := NewScanner(c, querier, w, updater.Subscribe())
		s.scanners = append(s.scanners, sc)
		s.writers = append(s.writers, w)
	}
	return s
}

// Run starts FilterUpdater + every scanner + every writer.
// Blocks until ctx is cancelled, at which point all goroutines
// drain and Run returns.
func (s *Supervisor) Run(ctx context.Context) {
	log.WithFields(log.Fields{
		"tables":     len(s.tables),
		"goroutines": 1 + 2*len(s.tables),
	}).Info("streaming.Supervisor: starting rev-3 push flow")

	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.updater.Run(ctx) }()

	for i := range s.scanners {
		sc := s.scanners[i]
		w := s.writers[i]
		s.wg.Add(2)
		go func() { defer s.wg.Done(); w.Run(ctx) }()
		go func() { defer s.wg.Done(); sc.Run(ctx) }()
	}
	s.wg.Wait()
}
