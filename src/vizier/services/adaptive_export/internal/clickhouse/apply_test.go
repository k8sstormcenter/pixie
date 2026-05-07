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

package clickhouse

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestApply_ExecutesEveryOperatorOwnedTable — Apply POSTs one DDL per
// table in OperatorOwnedTables, in order. None of the kubescape tables
// (alerts, kubescape_logs) are touched — those belong to the soc installer.
func TestApply_ExecutesEveryOperatorOwnedTable(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	a, err := NewApplier(srv.URL, "", "")
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	if err := a.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bodies) != len(OperatorOwnedTables) {
		t.Fatalf("Apply made %d calls, want %d", len(bodies), len(OperatorOwnedTables))
	}
	// Spot-check that the FIRST call is for the first OperatorOwnedTables entry,
	// and that the LAST call is for adaptive_attribution.
	if !strings.Contains(bodies[0], "forensic_db."+OperatorOwnedTables[0]) {
		t.Fatalf("first DDL not for %s; got: %s", OperatorOwnedTables[0], bodies[0])
	}
	if !strings.Contains(bodies[len(bodies)-1], "forensic_db.adaptive_attribution") {
		t.Fatalf("last DDL not for adaptive_attribution; got: %s", bodies[len(bodies)-1])
	}
	// And ensure no kubescape DDL leaked through.
	for _, b := range bodies {
		if strings.Contains(b, "forensic_db.alerts") || strings.Contains(b, "forensic_db.kubescape_logs") {
			t.Fatalf("operator's Apply must not create kubescape tables; got:\n%s", b)
		}
	}
}

// TestApply_FailsFastOnHTTPError — if any CREATE returns non-2xx,
// Apply returns immediately without attempting later tables.
func TestApply_FailsFastOnHTTPError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("ddl exploded"))
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	a, _ := NewApplier(srv.URL, "", "")
	err := a.Apply(context.Background())
	if err == nil {
		t.Fatalf("expected error from Apply on HTTP 500")
	}
	if calls != 1 {
		t.Fatalf("Apply continued past first failure; calls = %d", calls)
	}
}

// TestVerifyPixieSchema_DetectsMissingColumns — defensive guard:
// if a pixie table lacks namespace or pod (because Pixie's plugin
// auto-created it before our Apply), VerifyPixieSchema returns
// SchemaDriftError naming the table and the missing columns.
func TestVerifyPixieSchema_DetectsMissingColumns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		// First pixie table → respond with FULL column list (well-formed).
		// Subsequent pixie tables → respond with a column list missing namespace + pod
		// (simulating Pixie's auto-DDL having created them earlier).
		if strings.Contains(q, "table='http_events'") {
			_, _ = w.Write([]byte(`{"name":"time_"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"upid"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"namespace"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"pod"}` + "\n"))
			_, _ = w.Write([]byte(`{"name":"hostname"}` + "\n"))
			return
		}
		// pretend dns_events was auto-created by Pixie without our columns.
		_, _ = w.Write([]byte(`{"name":"time_"}` + "\n"))
		_, _ = w.Write([]byte(`{"name":"upid"}` + "\n"))
		_, _ = w.Write([]byte(`{"name":"hostname"}` + "\n"))
	}))
	defer srv.Close()
	a, _ := NewApplier(srv.URL, "", "")
	err := a.VerifyPixieSchema(context.Background())
	if err == nil {
		t.Fatalf("expected SchemaDriftError; got nil")
	}
	var drift *SchemaDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("err type = %T, want *SchemaDriftError", err)
	}
	if drift.Table != "http2_messages.beta" {
		// pixie tables iterated in PixieTables() order; first one missing should
		// be http2_messages.beta (the second entry).
		t.Fatalf("first drift = %q, want http2_messages.beta", drift.Table)
	}
	if !contains(drift.Missing, "namespace") || !contains(drift.Missing, "pod") {
		t.Fatalf("Missing should include namespace + pod; got %v", drift.Missing)
	}
}

// TestVerifyPixieSchema_AllPresent — happy path: all expected columns
// present on every pixie table.
func TestVerifyPixieSchema_AllPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"time_"}` + "\n"))
		_, _ = w.Write([]byte(`{"name":"upid"}` + "\n"))
		_, _ = w.Write([]byte(`{"name":"namespace"}` + "\n"))
		_, _ = w.Write([]byte(`{"name":"pod"}` + "\n"))
		_, _ = w.Write([]byte(`{"name":"hostname"}` + "\n"))
	}))
	defer srv.Close()
	a, _ := NewApplier(srv.URL, "", "")
	if err := a.VerifyPixieSchema(context.Background()); err != nil {
		t.Fatalf("VerifyPixieSchema: %v", err)
	}
}

// TestNewApplier_RejectsBadEndpoint — defensive contract.
func TestNewApplier_RejectsBadEndpoint(t *testing.T) {
	if _, err := NewApplier("", "", ""); err == nil {
		t.Fatalf("empty endpoint not rejected")
	}
	if _, err := NewApplier("http://%zz", "", ""); err == nil {
		t.Fatalf("malformed endpoint not rejected")
	}
}

// TestOperatorOwnedTables_DoesNotIncludeKubescape — structural guard:
// the operator never owns kubescape tables.
func TestOperatorOwnedTables_DoesNotIncludeKubescape(t *testing.T) {
	for _, x := range []string{"alerts", "kubescape_logs"} {
		if contains(OperatorOwnedTables, x) {
			t.Fatalf("%q must not be in OperatorOwnedTables (it belongs to the soc installer)", x)
		}
	}
}

// TestOperatorOwnedTables_LastIsAdaptiveAttribution — ordering guard.
func TestOperatorOwnedTables_LastIsAdaptiveAttribution(t *testing.T) {
	last := OperatorOwnedTables[len(OperatorOwnedTables)-1]
	if last != "adaptive_attribution" {
		t.Fatalf("last entry = %q, want adaptive_attribution", last)
	}
}
