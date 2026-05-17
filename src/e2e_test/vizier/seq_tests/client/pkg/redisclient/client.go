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

// Package redisclient mirrors the HTTP seq-id loadgen pattern
// (see ../httpclient) for the redis wire protocol. Each request
// is a SET command whose key embeds a monotonic sequence id, so
// Pixie's redis_events.cmd_args contains the seq_id and the
// DataLossCounter PxL output can detect drops in the same way
// HTTPDataLossMetric does for http_events.
package redisclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"

	"px.dev/pixie/src/e2e_test/util"
)

// RedisSeqClient drives N concurrent redis connections at a rate-limited
// target ops/sec, each emitting `SET seq:<id> <pad>` commands. The seq id
// is encoded in the key so Pixie's redis parser captures it in cmd_args.
type RedisSeqClient struct {
	addr        string
	startSeq    int
	numMessages int
	numConns    int
	valSize     int
	targetRPS   int

	rps        float64
	rpsLimiter *rate.Limiter
}

// New creates a new redis seq client.
func New(addr string, startSeq, numMessages, numConns, valSize, targetRPS int) *RedisSeqClient {
	burst := targetRPS
	if burst < 1 {
		burst = 1
	}
	return &RedisSeqClient{
		addr:        addr,
		startSeq:    startSeq,
		numMessages: numMessages,
		numConns:    numConns,
		valSize:     valSize,
		targetRPS:   targetRPS,
		rpsLimiter:  rate.NewLimiter(rate.Limit(targetRPS), burst),
	}
}

// Run drives numMessages SET commands through numConns workers.
func (c *RedisSeqClient) Run() error {
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
				log.WithError(r).Error("redis op failed")
			}
			if count%10000 == 0 {
				log.WithField("count", count).Info("redis ops checkpoint")
			}
		}
	}()

	timeStart := time.Now()
	// Inclusive upper bound (`<=`) dispatched numMessages+1 messages,
	// throwing off rps math and the per-conn budget tracking by 1.
	for i := c.startSeq; i < c.startSeq+c.numMessages; i++ {
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
func (c *RedisSeqClient) PrintStats() error {
	log.WithField("rps", c.rps).WithField("protocol", "redis").Info("Done driving redis ops")
	return nil
}

func (c *RedisSeqClient) worker(wg *sync.WaitGroup, jobs <-chan int, results chan<- error) {
	defer wg.Done()
	conn, err := dialWithRetry(c.addr, 30*time.Second)
	if err != nil {
		results <- fmt.Errorf("dial: %w", err)
		return
	}
	defer conn.Close()
	rd := bufio.NewReader(conn)
	pad := string(util.RandPrintable(c.valSize))

	for seq := range jobs {
		if err := c.rpsLimiter.Wait(context.Background()); err != nil {
			results <- err
			continue
		}
		// SET seq:<id> <pad>  EX 60
		key := "seq:" + strconv.Itoa(seq)
		cmd := encodeArray([]string{"SET", key, pad, "EX", "60"})
		if _, err := conn.Write(cmd); err != nil {
			results <- fmt.Errorf("write: %w", err)
			return
		}
		// We expect "+OK\r\n" for SET.
		if err := readSimpleString(rd); err != nil {
			results <- fmt.Errorf("read: %w", err)
			return
		}
		results <- nil
	}
}

// encodeArray serializes a list of bulk strings as a RESP array.
//
//	*<N>\r\n
//	$<len(arg0)>\r\n<arg0>\r\n
//	...
func encodeArray(args []string) []byte {
	buf := make([]byte, 0, 32+sum(args))
	buf = append(buf, '*')
	buf = strconv.AppendInt(buf, int64(len(args)), 10)
	buf = append(buf, '\r', '\n')
	for _, a := range args {
		buf = append(buf, '$')
		buf = strconv.AppendInt(buf, int64(len(a)), 10)
		buf = append(buf, '\r', '\n')
		buf = append(buf, a...)
		buf = append(buf, '\r', '\n')
	}
	return buf
}

func sum(args []string) int {
	n := 0
	for _, a := range args {
		n += len(a) + 16
	}
	return n
}

// readSimpleString reads one RESP reply. We accept "+..." (simple string)
// or "-..." (error). Anything else is unexpected for SET.
func readSimpleString(rd *bufio.Reader) error {
	prefix, err := rd.ReadByte()
	if err != nil {
		return err
	}
	line, err := rd.ReadString('\n')
	if err != nil {
		return err
	}
	switch prefix {
	case '+':
		return nil
	case '-':
		return fmt.Errorf("redis error: %s", line)
	default:
		// Drain any payload (bulk string etc.) so the connection stays
		// usable. SET should not produce these but be defensive.
		_, _ = io.ReadAll(io.LimitReader(rd, 0))
		return fmt.Errorf("unexpected reply prefix: %q line=%s", prefix, line)
	}
}

func dialWithRetry(addr string, deadline time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	endBy := time.Now().Add(deadline)
	var lastErr error
	for {
		conn, err := d.Dial("tcp", addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if time.Now().After(endBy) {
			return nil, lastErr
		}
		time.Sleep(500 * time.Millisecond)
	}
}
