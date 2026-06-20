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

package chhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNew_RejectsBadEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, ep string
	}{
		{"empty", ""},
		{"no-scheme", "localhost:8123"},
		{"unsupported-scheme", "ftp://localhost:8123"},
		{"has-query", "http://localhost:8123/?foo=bar"},
		{"has-fragment", "http://localhost:8123/#bar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.ep, "", "", 0); err == nil {
				t.Fatalf("New(%q) = nil err, want error", tc.ep)
			}
		})
	}
}

func TestNew_DefaultsTimeout(t *testing.T) {
	c, err := New("http://localhost:8123", "", "", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.hc.Timeout != DefaultTimeout {
		t.Fatalf("timeout = %v, want %v", c.hc.Timeout, DefaultTimeout)
	}
}

func TestNew_StripsTrailingSlashFromEndpoint(t *testing.T) {
	c, err := New("http://localhost:8123/", "", "", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Endpoint() != "http://localhost:8123" {
		t.Fatalf("endpoint = %q, want trimmed", c.Endpoint())
	}
}

func TestExec_PostsSQLAsBody(t *testing.T) {
	var gotBody string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "", "", time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Exec(context.Background(), "CREATE DATABASE x"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotBody != "CREATE DATABASE x" {
		t.Fatalf("body = %q, want %q", gotBody, "CREATE DATABASE x")
	}
}

func TestQuery_PutsSQLInURLParam(t *testing.T) {
	var gotMethod, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.Query().Get("query")
		w.Write([]byte(`{"hits":1}` + "\n"))
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "", "", time.Second)
	body, err := c.Query(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want GET", gotMethod)
	}
	if gotQuery != "SELECT 1" {
		t.Fatalf("query = %q, want %q", gotQuery, "SELECT 1")
	}
	if !strings.Contains(string(body), "hits") {
		t.Fatalf("body = %q", body)
	}
}

func TestInsert_SetsContentTypeAndFailLoud(t *testing.T) {
	var gotCT, gotQ string
	var gotSettings = map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotQ = r.URL.Query().Get("query")
		for _, k := range []string{"input_format_skip_unknown_fields", "input_format_null_as_default", "input_format_allow_errors_num", "input_format_allow_errors_ratio"} {
			gotSettings[k] = r.URL.Query().Get(k)
		}
		w.Header().Set("X-ClickHouse-Summary", `{"written_rows":"3"}`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "", "", time.Second)
	res, err := c.Insert(context.Background(),
		"INSERT INTO t FORMAT JSONEachRow", []byte("{}\n"),
		InsertOptions{FailLoud: true})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if gotCT != "application/x-ndjson" {
		t.Fatalf("content-type = %q", gotCT)
	}
	if gotQ != "INSERT INTO t FORMAT JSONEachRow" {
		t.Fatalf("query = %q", gotQ)
	}
	if gotSettings["input_format_skip_unknown_fields"] != "0" {
		t.Fatalf("fail-loud not applied: %v", gotSettings)
	}
	if res.Summary != `{"written_rows":"3"}` {
		t.Fatalf("summary = %q", res.Summary)
	}
	if res.BodyBytes != 3 {
		t.Fatalf("body bytes = %d", res.BodyBytes)
	}
}

func TestExec_PropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("syntax error near 'GROOT'"))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "", "", time.Second)
	_, err := c.Exec(context.Background(), "GROOT")
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("err = %v", err)
	}
}

func TestExec_SendsBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, hadAuth = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "default", "s3cret", time.Second)
	if _, err := c.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !hadAuth || gotUser != "default" || gotPass != "s3cret" {
		t.Fatalf("basic auth: had=%v user=%q pass=%q", hadAuth, gotUser, gotPass)
	}
}
