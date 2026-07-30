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

package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwtutils "px.dev/pixie/src/shared/services/utils"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// fakeExporter records Upsert/Remove calls (the controller → activeSet contract).
type fakeExporter struct {
	upserts []activeset.Key
	removes []activeset.Key
	lastEnd time.Time
}

func (f *fakeExporter) Upsert(k activeset.Key, tEnd time.Time) {
	f.upserts = append(f.upserts, k)
	f.lastEnd = tEnd
}
func (f *fakeExporter) Remove(k activeset.Key) { f.removes = append(f.removes, k) }

// fakeRunner records OrderQuery calls; err controls the failure path.
type fakeRunner struct {
	calls []string // "table|ns/pod|queryID"
	err   error
}

func (f *fakeRunner) OrderQuery(t anomaly.Target, table string, start, end time.Time, qid string) error {
	f.calls = append(f.calls, table+"|"+t.Namespace+"/"+t.Pod+"|"+qid)
	return f.err
}

// fakeExportAller implements BOTH queryRunner and exportAller — the controller's
// real shape. It sends the OrderExportAll target on a channel so the test can
// assert /export/start drove the steer-all full-evidence capture.
type fakeExportAller struct {
	fakeRunner
	exported chan anomaly.Target
}

func (f *fakeExportAller) OrderExportAll(t anomaly.Target, start, end time.Time) {
	f.exported <- t
}

// TestStartExportDrivesSteerAll pins the steer-all contract: a POST /export/start
// (what dx sends default-on per referral) triggers OrderExportAll for the pod —
// i.e. dx steers AE to grab the complete evidence set, no per-table decision.
func TestStartExportDrivesSteerAll(t *testing.T) {
	rn := &fakeExportAller{exported: make(chan anomaly.Target, 1)}
	srv := New(&fakeExporter{}, rn)
	r := do(t, srv, http.MethodPost, "/export/start",
		`{"namespace":"redis","pod":"redis-1","t_end":1785000000}`)
	if r.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", r.StatusCode)
	}
	select {
	case got := <-rn.exported:
		if got.Pod != "redis-1" || got.Namespace != "redis" {
			t.Fatalf("steer-all target wrong: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OrderExportAll not called by /export/start within 2s")
	}
}

func do(t *testing.T, srv *Server, method, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w.Result()
}

// TestControlAuth: with SetAuth on, every endpoint except /healthz requires a
// valid bearer JWT minted by the shared lib (the same one dx uses); missing/bad
// tokens get 401. (CodeRabbit: protect control endpoints with auth.)
func TestControlAuth(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef" // HS256 test key
	srv := New(&fakeExporter{}, nil)
	srv.SetAuth(key, "vizier")
	h := srv.Handler()

	good, err := jwtutils.SignJWTClaims(jwtutils.GenerateJWTForService("dx", "vizier"), key)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	call := func(path, auth string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"pod":"p","t_end":1}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Result().StatusCode
	}
	if got := call("/export/start", ""); got != http.StatusUnauthorized {
		t.Fatalf("no bearer: want 401, got %d", got)
	}
	if got := call("/export/start", "Bearer not-a-jwt"); got != http.StatusUnauthorized {
		t.Fatalf("bad bearer: want 401, got %d", got)
	}
	if got := call("/export/start", "Bearer "+good); got == http.StatusUnauthorized {
		t.Fatalf("valid bearer wrongly rejected (401)")
	}
	reqH := httptest.NewRequest(http.MethodGet, "/healthz", nil) // probes stay open
	wH := httptest.NewRecorder()
	h.ServeHTTP(wH, reqH)
	if wH.Result().StatusCode == http.StatusUnauthorized {
		t.Fatal("/healthz must not require auth")
	}
}

func TestStartExportUpserts(t *testing.T) {
	ex := &fakeExporter{}
	srv := New(ex, nil)
	// t_end is unix NANOSECONDS (the pipeline-wide unit) — 1717200600s expressed in ns.
	resp := do(t, srv, http.MethodPost, "/export/start",
		`{"namespace":"svc-poc","pod":"chain-backend-abc","comm":"sh","t_end":1717200600000000000}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if len(ex.upserts) != 1 || ex.upserts[0].Pod != "chain-backend-abc" ||
		ex.upserts[0].Namespace != "svc-poc" {
		t.Fatalf("upsert = %+v, want one for svc-poc/chain-backend-abc", ex.upserts)
	}
	if !ex.lastEnd.Equal(time.Unix(0, 1717200600000000000)) {
		t.Fatalf("tEnd = %v, want %v", ex.lastEnd, time.Unix(0, 1717200600000000000).UTC())
	}
}

func TestStopExportRemoves(t *testing.T) {
	ex := &fakeExporter{}
	srv := New(ex, nil)
	resp := do(t, srv, http.MethodPost, "/export/stop",
		`{"namespace":"svc-poc","pod":"chain-backend-abc"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if len(ex.removes) != 1 || ex.removes[0].Pod != "chain-backend-abc" {
		t.Fatalf("remove = %+v, want one for chain-backend-abc", ex.removes)
	}
}

func TestOrderQueryRunsAndCarriesID(t *testing.T) {
	ex := &fakeExporter{}
	rn := &fakeRunner{}
	srv := New(ex, rn)
	resp := do(t, srv, http.MethodPost, "/query",
		`{"namespace":"svc-poc","pod":"p","comm":"sh","table":"conn_stats","window":[100,200],"query_id":"svc-poc:p:conn_stats:100-200"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if len(rn.calls) != 1 || rn.calls[0] != "conn_stats|svc-poc/p|svc-poc:p:conn_stats:100-200" {
		t.Fatalf("calls = %v", rn.calls)
	}
}

func TestQueryWithoutRunnerIs501(t *testing.T) {
	srv := New(&fakeExporter{}, nil) // no runner wired
	resp := do(t, srv, http.MethodPost, "/query",
		`{"namespace":"n","pod":"p","table":"conn_stats","window":[1,2],"query_id":"x"}`)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestBadInputRejected(t *testing.T) {
	srv := New(&fakeExporter{}, &fakeRunner{})
	// missing pod
	if r := do(t, srv, http.MethodPost, "/export/start", `{"namespace":"n"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("start no-pod = %d, want 400", r.StatusCode)
	}
	// malformed json
	if r := do(t, srv, http.MethodPost, "/export/stop", `{not json`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("stop bad-json = %d, want 400", r.StatusCode)
	}
	// query missing table
	if r := do(t, srv, http.MethodPost, "/query", `{"pod":"p","query_id":"x","window":[1,2]}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("query no-table = %d, want 400", r.StatusCode)
	}
	// /export/start with t_end <= 0 — pins the new contract (CodeRabbit
	// r-#68/control/server_test.go). Without this assertion a regression
	// that drops the `req.TEnd <= 0` gate would Upsert with a
	// time.Unix(0,0) tEnd, immediately-expired.
	if r := do(t, srv, http.MethodPost, "/export/start",
		`{"pod":"p","namespace":"n","t_end":0}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("start t_end=0 = %d, want 400", r.StatusCode)
	}
	if r := do(t, srv, http.MethodPost, "/export/start",
		`{"pod":"p","namespace":"n","t_end":-1}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("start t_end=-1 = %d, want 400", r.StatusCode)
	}
	// /query with inverted or zero window — same idea.
	if r := do(t, srv, http.MethodPost, "/query",
		`{"pod":"p","table":"http_events","query_id":"x","window":[10,5]}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("query inverted-window = %d, want 400", r.StatusCode)
	}
	if r := do(t, srv, http.MethodPost, "/query",
		`{"pod":"p","table":"http_events","query_id":"x","window":[5,5]}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("query zero-window = %d, want 400", r.StatusCode)
	}
}

func TestWrongMethodRejected(t *testing.T) {
	srv := New(&fakeExporter{}, &fakeRunner{})
	if r := do(t, srv, http.MethodGet, "/export/start", ``); r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET start = %d, want 405", r.StatusCode)
	}
}

func TestRunnerErrorIsBadGateway(t *testing.T) {
	rn := &fakeRunner{err: errFake}
	srv := New(&fakeExporter{}, rn)
	r := do(t, srv, http.MethodPost, "/query",
		`{"namespace":"n","pod":"p","table":"conn_stats","window":[1,2],"query_id":"x"}`)
	if r.StatusCode != http.StatusBadGateway {
		t.Fatalf("runner-error = %d, want 502", r.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv := New(&fakeExporter{}, nil)
	if r := do(t, srv, http.MethodGet, "/healthz", ``); r.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", r.StatusCode)
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "boom" }

var errFake = fakeErr{}

type fakeManifest struct {
	got string
	err error
}

func (f *fakeManifest) WriteEvidenceManifest(_ context.Context, jsonEachRow []byte) error {
	f.got = string(jsonEachRow)
	return f.err
}

// TestEvidenceManifest: /dx/evidence_manifest is 501 without a writer; with one it
// persists ONE JSONEachRow row per verdict, scalars as typed columns and the nested
// collections (findings/case_window/...) rendered as JSON *text* so the insert is
// ClickHouse-version independent.
func TestEvidenceManifest(t *testing.T) {
	srv := New(&fakeExporter{}, nil)
	if r := do(t, srv, http.MethodPost, "/dx/evidence_manifest", `{"investigation_id":"i1"}`); r.StatusCode != http.StatusNotImplemented {
		t.Fatalf("no writer: got %d, want 501", r.StatusCode)
	}

	fm := &fakeManifest{}
	srv.SetManifestWriter(fm)
	body := `{"investigation_id":"i1","event_time":1730000000000000000,"hostname":"n1",` +
		`"verdict":"ruled_in","confidence":0.9,"case_window":[1.0,2.0],` +
		`"findings":[{"vector":"process"}],"evidence_hash":"h"}`
	if r := do(t, srv, http.MethodPost, "/dx/evidence_manifest", body); r.StatusCode != http.StatusAccepted {
		t.Fatalf("got %d, want 202", r.StatusCode)
	}
	if !strings.HasSuffix(fm.got, "\n") {
		t.Fatalf("missing JSONEachRow newline terminator: %q", fm.got)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(fm.got)), &row); err != nil {
		t.Fatalf("row not valid JSON: %v (%s)", err, fm.got)
	}
	if row["investigation_id"] != "i1" || row["hostname"] != "n1" || row["verdict"] != "ruled_in" {
		t.Fatalf("scalar columns wrong: %#v", row)
	}
	// event_time is a large nanos int; it must round-trip as an integer, not a float in sci notation.
	if !strings.Contains(fm.got, `"event_time":1730000000000000000`) {
		t.Fatalf("event_time not an integer literal: %s", fm.got)
	}
	// nested collections persisted as JSON text strings, not raw arrays.
	if row["findings"] != `[{"vector":"process"}]` {
		t.Fatalf("findings should be JSON text, got %#v", row["findings"])
	}
	if row["case_window"] != `[1.0,2.0]` {
		t.Fatalf("case_window should be JSON text, got %#v", row["case_window"])
	}

	// writer failure surfaces as 502.
	fm.err = errFake
	if r := do(t, srv, http.MethodPost, "/dx/evidence_manifest", body); r.StatusCode != http.StatusBadGateway {
		t.Fatalf("writer error: got %d, want 502", r.StatusCode)
	}
}
