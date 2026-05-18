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

package clickhouse_test

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
)

// Live integration tests for the operator's schema-apply path. Driven
// against a real ClickHouse reachable at INTEGRATION_CH_ENDPOINT.
// Skipped if the env var is unset, so `go test` (without -tags
// integration) is unaffected.

func envEndpoint(t *testing.T) string {
	t.Helper()
	e := os.Getenv("INTEGRATION_CH_ENDPOINT")
	if e == "" {
		t.Skip("INTEGRATION_CH_ENDPOINT not set; skipping live ClickHouse test")
	}
	return e
}

func envCreds() (string, string) {
	return os.Getenv("INTEGRATION_CH_USER"), os.Getenv("INTEGRATION_CH_PASSWORD")
}

func httpExists(t *testing.T, endpoint, user, pass, table string) string {
	t.Helper()
	ident := table
	if strings.Contains(table, ".") {
		ident = "`" + table + "`"
	}
	q := url.Values{}
	q.Set("query", fmt.Sprintf("EXISTS forensic_db.%s", ident))
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("build EXISTS req for %s: %v", table, err)
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("EXISTS %s: %v", table, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		t.Fatalf("EXISTS %s: HTTP %d: %s", table, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return strings.TrimSpace(string(body))
}

// TestApply_Live runs the operator's Apply() against a live ClickHouse
// and asserts every OperatorOwnedTables entry is materialised. This is
// the regression guard for the "tables never appear in clickhouse"
// class of bug — a green run here proves the embedded schema.sql is
// reachable, the DDL extractor produces valid statements, and the HTTP
// transport posts them successfully.
func TestApply_Live(t *testing.T) {
	endpoint := envEndpoint(t)
	user, pass := envCreds()

	a, err := chpkg.NewApplier(endpoint, user, pass)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Every operator-owned table must EXIST.
	for _, table := range chpkg.OperatorOwnedTables {
		got := httpExists(t, endpoint, user, pass, table)
		if got != "1" {
			t.Errorf("table forensic_db.%s: EXISTS=%q, want 1", table, got)
		}
	}
}

// TestApply_Idempotent runs Apply() twice and asserts the second pass
// is a no-op (CREATE TABLE IF NOT EXISTS semantics on every statement).
func TestApply_Idempotent(t *testing.T) {
	endpoint := envEndpoint(t)
	user, pass := envCreds()
	a, err := chpkg.NewApplier(endpoint, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	// Separate contexts per Apply — sharing one 60s budget across both
	// calls makes Apply #2 occasionally fail with context.DeadlineExceeded
	// when the live cluster is slow, masking the idempotency property.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel1()
	if err := a.Apply(ctx1); err != nil {
		t.Fatalf("Apply #1: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()
	if err := a.Apply(ctx2); err != nil {
		t.Fatalf("Apply #2 (should be idempotent): %v", err)
	}
}

// TestVerifyPixieSchema_Live runs the post-Apply guard against the
// live cluster. Required pixie columns (namespace, pod, hostname, time_)
// must be present on every pixie observation table.
func TestVerifyPixieSchema_Live(t *testing.T) {
	endpoint := envEndpoint(t)
	user, pass := envCreds()

	a, err := chpkg.NewApplier(endpoint, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Apply first so the test is order-independent w.r.t. TestApply_Live.
	if err := a.Apply(ctx); err != nil {
		t.Fatalf("Apply (precondition): %v", err)
	}
	if err := a.VerifyPixieSchema(ctx); err != nil {
		t.Fatalf("VerifyPixieSchema: %v", err)
	}
}
