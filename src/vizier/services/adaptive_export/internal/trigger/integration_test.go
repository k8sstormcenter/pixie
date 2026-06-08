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

//go:build integration
// +build integration

package trigger_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	chpkg "px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/trigger"
)

// Live integration test for the trigger's poll loop. Inserts a
// kubescape_logs row directly via HTTP, then asserts the trigger
// surfaces it as a kubescape.Event before the deadline.

func env(t *testing.T) (endpoint, user, pass string) {
	t.Helper()
	endpoint = os.Getenv("INTEGRATION_CH_ENDPOINT")
	if endpoint == "" {
		t.Skip("INTEGRATION_CH_ENDPOINT not set; skipping live ClickHouse test")
	}
	return endpoint, os.Getenv("INTEGRATION_CH_USER"), os.Getenv("INTEGRATION_CH_PASSWORD")
}

func ensureSchema(t *testing.T, endpoint, user, pass string) {
	t.Helper()
	a, err := chpkg.NewApplier(endpoint, user, pass)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.Apply(ctx); err != nil {
		t.Fatalf("Apply (precondition): %v", err)
	}
}

// insertKubescapeRow shoves one synthetic row into kubescape_logs via
// JSONEachRow on the HTTP interface — same shape Vector emits.
func insertKubescapeRow(t *testing.T, endpoint, user, pass, hostname, ruleID string, eventTime uint64) {
	t.Helper()
	body := fmt.Sprintf(
		`{"BaseRuntimeMetadata":"{\"alertName\":\"%s\"}","CloudMetadata":"","RuleID":"%s","RuntimeK8sDetails":"{\"podName\":\"redis-test\",\"podNamespace\":\"redis\"}","RuntimeProcessDetails":"{\"processTree\":{\"pid\":1234,\"comm\":\"redis-server\"}}","event":"","event_time":%d,"hostname":"%s"}`,
		ruleID, ruleID, eventTime, hostname,
	)
	q := url.Values{}
	q.Set("query", "INSERT INTO forensic_db.kubescape_logs FORMAT JSONEachRow")
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(endpoint, "/")+"/?"+q.Encode(),
		strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("seed insert HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(buf)))
	}
}

// TestTriggerSubscribe_Live: insert one row, expect one Event from the
// trigger's Subscribe channel within the deadline.
func TestTriggerSubscribe_Live(t *testing.T) {
	endpoint, user, pass := env(t)
	ensureSchema(t, endpoint, user, pass)

	hostname := fmt.Sprintf("aw-trig-%d", time.Now().UnixNano())
	now := time.Now()
	eventTime := uint64(now.UnixNano())

	// Use a watermark slightly before the synthetic event_time so the
	// first poll picks up exactly our row, regardless of unrelated rows
	// in the table from earlier runs.
	cfg := trigger.Config{
		Endpoint:         endpoint,
		Username:         user,
		Password:         pass,
		Hostname:         hostname,
		PollInterval:     200 * time.Millisecond,
		InitialWatermark: eventTime - 1,
	}
	trg, err := trigger.New(cfg)
	if err != nil {
		t.Fatalf("trigger.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ch, err := trg.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	insertKubescapeRow(t, endpoint, user, pass, hostname, "R1005", eventTime)

	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed before event arrived")
		}
		if ev.RuleID != "R1005" {
			t.Errorf("Event.RuleID = %q, want R1005", ev.RuleID)
		}
		if ev.Hostname != hostname {
			t.Errorf("Event.Hostname = %q, want %q", ev.Hostname, hostname)
		}
		if ev.EventTime != eventTime {
			t.Errorf("Event.EventTime = %d, want %d", ev.EventTime, eventTime)
		}
		if ev.Target.Pod != "redis-test" || ev.Target.Namespace != "redis" {
			t.Errorf("Event.Target = %+v, want pod=redis-test, ns=redis", ev.Target)
		}
	case <-ctx.Done():
		t.Fatalf("trigger did not surface the seeded row within 15s")
	}
}
