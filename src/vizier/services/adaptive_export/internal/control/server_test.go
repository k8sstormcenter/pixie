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
	resp := do(t, srv, http.MethodPost, "/export/start",
		`{"namespace":"log4j-poc","pod":"chain-backend-abc","comm":"sh","t_end":1717200600}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if len(ex.upserts) != 1 || ex.upserts[0].Pod != "chain-backend-abc" ||
		ex.upserts[0].Namespace != "log4j-poc" {
		t.Fatalf("upsert = %+v, want one for log4j-poc/chain-backend-abc", ex.upserts)
	}
	if ex.lastEnd != time.Unix(1717200600, 0) {
		t.Fatalf("tEnd = %v, want 1717200600", ex.lastEnd)
	}
}

func TestStopExportRemoves(t *testing.T) {
	ex := &fakeExporter{}
	srv := New(ex, nil)
	resp := do(t, srv, http.MethodPost, "/export/stop",
		`{"namespace":"log4j-poc","pod":"chain-backend-abc"}`)
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
		`{"namespace":"log4j-poc","pod":"p","comm":"sh","table":"conn_stats","window":[100,200],"query_id":"log4j-poc:p:conn_stats:100-200"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if len(rn.calls) != 1 || rn.calls[0] != "conn_stats|log4j-poc/p|log4j-poc:p:conn_stats:100-200" {
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
