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

// Package chhttp is the one HTTP client every AE-internal package uses to
// talk to ClickHouse's HTTP interface (port 8123 by default). Previously
// the same client was reimplemented three times (clickhouse.Applier,
// sink.ClickHouseHTTP, trigger.ClickHouseWatermarkStore) with subtly
// different endpoint validation, timeout defaults and error-extraction
// logic; this package collapses that to a single implementation.
package chhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout is applied when New is called with timeout==0. Matches
// the budget the original three clients each chose independently.
const DefaultTimeout = 30 * time.Second

// Client is a minimal HTTP CH client. Safe for concurrent use.
type Client struct {
	endpoint string
	user     string
	pass     string
	hc       *http.Client
	// streamHC is a parallel client with NO Timeout — Go's
	// http.Client.Timeout covers body reads, so reusing hc for
	// QueryStream would silently truncate a multi-MB active-set
	// rehydrate at DefaultTimeout. Stream callers must bound their
	// own ctx deadline (CodeRabbit r-#68/chhttp.go).
	streamHC *http.Client
}

// New validates the endpoint and returns a ready client. timeout<=0 →
// DefaultTimeout. endpoint must be an absolute http(s) URL with no query
// string or fragment (we append ?query=… ourselves); trailing slashes
// are stripped so concatenations don't produce //.
func New(endpoint, user, pass string, timeout time.Duration) (*Client, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("chhttp: empty endpoint")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("chhttp: invalid endpoint %q: %w", endpoint, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("chhttp: endpoint must be an absolute http(s) URL: %q", endpoint)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("chhttp: endpoint must not include query parameters or a fragment: %q", endpoint)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		user:     user,
		pass:     pass,
		hc:       &http.Client{Timeout: timeout},
		streamHC: &http.Client{}, // no Timeout — see streamHC docstring above
	}, nil
}

// Endpoint returns the (validated, trimmed) base URL — useful for log
// fields where the caller wants to identify which CH the client targets.
func (c *Client) Endpoint() string { return c.endpoint }

// Exec POSTs sql as the request body (DDL / DML without source data). Returns
// the response body bytes. Use for CREATE DATABASE, CREATE TABLE, etc.
func (c *Client) Exec(ctx context.Context, sql string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, c.endpoint+"/", strings.NewReader(sql), "")
}

// Query GETs sql via ?query= so it shows up greppable in CH's query log.
// Use for SELECT — the body is whatever FORMAT was requested. Buffers
// the entire response in memory; for large result sets prefer
// QueryStream.
func (c *Client) Query(ctx context.Context, sql string) ([]byte, error) {
	q := url.Values{}
	q.Set("query", sql)
	return c.do(ctx, http.MethodGet, c.endpoint+"/?"+q.Encode(), nil, "")
}

// QueryStream GETs sql like Query, but returns the response body as an
// io.ReadCloser the caller drains incrementally. Use for SELECTs whose
// result set is unbounded (e.g. an active-set rehydrate that may be
// multi-MB). Caller MUST Close the returned body, even on error, and
// MUST bound the request via ctx.Deadline — the underlying transport
// here has NO http.Client.Timeout because that timeout would cover
// body reads and silently truncate a long stream
// (CodeRabbit r-#68/chhttp.go).
func (c *Client) QueryStream(ctx context.Context, sql string) (io.ReadCloser, error) {
	q := url.Values{}
	q.Set("query", sql)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.streamHC.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp.Body, nil
}

// InsertOptions tunes one Insert call.
type InsertOptions struct {
	// ContentType sets the HTTP Content-Type. Defaults to
	// "application/x-ndjson" when empty (matches FORMAT JSONEachRow).
	ContentType string
	// FailLoud, when true, attaches the CH settings that turn silent
	// drops into errors (input_format_skip_unknown_fields=0 etc.) —
	// see setFailLoudSettings.
	FailLoud bool
	// Settings carries additional CH settings as URL params on the
	// query string. Keys are passed through unchanged.
	Settings url.Values
}

// InsertResult is what Insert returns on success.
type InsertResult struct {
	// Summary is the X-ClickHouse-Summary response header verbatim (may
	// be empty — older CH or middlebox stripping). Callers parse for
	// silent-drop detection.
	Summary string
	// BodyBytes is the count of bytes in the request body (not the
	// response). Convenient for logging the wire size at the call site.
	BodyBytes int
}

// Insert posts the body for an INSERT … FORMAT X statement (sql contains
// the statement; body contains the data in the named format). The
// per-call options carry content-type + the fail-loud setting.
func (c *Client) Insert(ctx context.Context, sql string, body []byte, opts InsertOptions) (InsertResult, error) {
	q := url.Values{}
	q.Set("query", sql)
	for k, vs := range opts.Settings {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if opts.FailLoud {
		setFailLoudSettings(q)
	}
	ct := opts.ContentType
	if ct == "" {
		ct = "application/x-ndjson"
	}
	out, resp, err := c.doRaw(ctx, http.MethodPost, c.endpoint+"/?"+q.Encode(), bytes.NewReader(body), ct)
	if err != nil {
		return InsertResult{}, err
	}
	_ = out // discarded: INSERT bodies are empty
	return InsertResult{
		Summary:   resp.Header.Get("X-ClickHouse-Summary"),
		BodyBytes: len(body),
	}, nil
}

// do is the simple variant used by Exec/Query — it discards the response
// headers and only surfaces the body bytes.
func (c *Client) do(ctx context.Context, method, urlStr string, body io.Reader, contentType string) ([]byte, error) {
	out, _, err := c.doRaw(ctx, method, urlStr, body, contentType)
	return out, err
}

// doRaw builds + sends one request, returning the body and the response
// (so Insert can read the X-ClickHouse-Summary header). Non-2xx becomes a
// formatted Go error.
func (c *Client) doRaw(ctx context.Context, method, urlStr string, body io.Reader, contentType string) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, resp, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp, err
	}
	return out, resp, nil
}

// setFailLoudSettings pins ClickHouse's input-format settings on every
// INSERT so an upstream schema-drift surfaces as an HTTP 4xx with a real
// error body, not a silent written_rows=0 + 200 OK that downstream
// silent-drop detection only catches after the data is lost.
//
//	input_format_skip_unknown_fields=0    fail on a column we write that
//	                                      doesn't exist in CH.
//	input_format_null_as_default=0        fail on a NULL where the
//	                                      column is non-nullable.
//	input_format_allow_errors_num=0       reject the whole batch on
//	                                      the first parse error.
//	input_format_allow_errors_ratio=0     same, for the proportional
//	                                      knob.
func setFailLoudSettings(q url.Values) {
	q.Set("input_format_skip_unknown_fields", "0")
	q.Set("input_format_null_as_default", "0")
	q.Set("input_format_allow_errors_num", "0")
	q.Set("input_format_allow_errors_ratio", "0")
}
