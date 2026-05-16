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

// Package pgsqlclient mirrors the HTTP seq-id loadgen pattern
// (see ../httpclient) for the Postgres wire protocol. Each request
// runs a parameterized SELECT whose first arg is a monotonic seq id;
// Pixie's pgsql_events records both the prepared statement and the
// parameter values, so DataLossCounter can detect gaps just as the
// HTTP variant does via the X-Px-Seq-Id header.
package pgsqlclient

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"

	"px.dev/pixie/src/e2e_test/util"
)

// PgsqlSeqClient drives N concurrent postgres connections at a rate-
// limited target queries/sec. Each query is `SELECT $1::int,
// $2::text` with the seq id passed as $1 — Pixie's pgsql parser
// captures the parameter list in `pgsql_events.req` so the seq id
// survives into the table for data-loss detection.
type PgsqlSeqClient struct {
	dsn         string
	startSeq    int
	numMessages int
	numConns    int
	padSize     int
	targetRPS   int

	rps        float64
	rpsLimiter *rate.Limiter
}

// New creates a new pgsql seq client.
func New(dsn string, startSeq, numMessages, numConns, padSize, targetRPS int) *PgsqlSeqClient {
	burst := targetRPS
	if burst < 1 {
		burst = 1
	}
	return &PgsqlSeqClient{
		dsn:         dsn,
		startSeq:    startSeq,
		numMessages: numMessages,
		numConns:    numConns,
		padSize:     padSize,
		targetRPS:   targetRPS,
		rpsLimiter:  rate.NewLimiter(rate.Limit(targetRPS), burst),
	}
}

// Run drives numMessages SELECTs through numConns workers.
func (c *PgsqlSeqClient) Run() error {
	var wg sync.WaitGroup
	jobs := make(chan int, c.numConns)
	results := make(chan error, c.numConns)

	for i := 0; i < c.numConns; i++ {
		wg.Add(1)
		go c.worker(&wg, jobs, results)
	}

	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		count := 0
		for r := range results {
			count++
			if r != nil {
				log.WithError(r).Error("pgsql op failed")
			}
			if count%10000 == 0 {
				log.WithField("count", count).Info("pgsql ops checkpoint")
			}
		}
	}()

	timeStart := time.Now()
	for i := c.startSeq; i <= c.startSeq+c.numMessages; i++ {
		jobs <- i
	}
	close(jobs)

	wg.Wait()
	close(results)
	readerWg.Wait()
	timeDelta := time.Since(timeStart)

	c.rps = float64(c.numMessages) / timeDelta.Seconds()
	return nil
}

// PrintStats logs the achieved ops/sec.
func (c *PgsqlSeqClient) PrintStats() error {
	log.WithField("rps", c.rps).WithField("protocol", "pgsql").Info("Done driving pgsql ops")
	return nil
}

func (c *PgsqlSeqClient) worker(wg *sync.WaitGroup, jobs <-chan int, results chan<- error) {
	defer wg.Done()
	db, err := openWithRetry(c.dsn, 30*time.Second)
	if err != nil {
		results <- fmt.Errorf("open: %w", err)
		return
	}
	defer db.Close()
	// Single-connection pool per worker so syscall traffic is a stable
	// 1 conn per worker (mirrors httpclient).
	db.SetMaxIdleConns(1)
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	pad := string(util.RandPrintable(c.padSize))

	const q = "SELECT $1::int AS seq_id, $2::text AS pad"
	ctx := context.Background()
	for seq := range jobs {
		if err := c.rpsLimiter.Wait(ctx); err != nil {
			results <- err
			continue
		}
		var gotSeq int
		var gotPad string
		row := db.QueryRowContext(ctx, q, seq, pad)
		if err := row.Scan(&gotSeq, &gotPad); err != nil {
			results <- fmt.Errorf("scan: %w", err)
			return
		}
		results <- nil
	}
}

func openWithRetry(dsn string, deadline time.Duration) (*sql.DB, error) {
	endBy := time.Now().Add(deadline)
	var lastErr error
	for {
		db, err := sql.Open("postgres", dsn)
		if err == nil {
			if pingErr := db.Ping(); pingErr == nil {
				return db, nil
			} else {
				lastErr = pingErr
				_ = db.Close()
			}
		} else {
			lastErr = err
		}
		if time.Now().After(endBy) {
			return nil, lastErr
		}
		time.Sleep(500 * time.Millisecond)
	}
}
