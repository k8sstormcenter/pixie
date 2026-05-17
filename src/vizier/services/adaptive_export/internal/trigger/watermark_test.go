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

package trigger

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStore is an in-memory WatermarkStore for testing trigger
// integration without needing a live ClickHouse.
type fakeStore struct {
	mu         sync.Mutex
	saves      []uint64
	loadResult uint64
	loadOK     bool
	loadErr    error
	saveErr    error
}

func (f *fakeStore) Load(ctx context.Context, hostname, table string) (uint64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loadResult, f.loadOK, f.loadErr
}

func (f *fakeStore) Save(ctx context.Context, hostname, table string, wm uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, wm)
	return nil
}

func (f *fakeStore) savedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saves)
}

func (f *fakeStore) lastSaved() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saves) == 0 {
		return 0
	}
	return f.saves[len(f.saves)-1]
}

// TestTrigger_LoadsPersistentWatermarkOnBoot — the very first SELECT
// the trigger issues must filter event_time by the persisted watermark,
// not by InitialWatermark or 0.
func TestTrigger_LoadsPersistentWatermarkOnBoot(t *testing.T) {
	queries := make(chan string, 256)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query().Get("query")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	store := &fakeStore{loadResult: 1744000000000000000, loadOK: true}
	tr, err := New(Config{
		Endpoint:     srv.URL,
		Hostname:     "node-1",
		PollInterval: 30 * time.Millisecond,
		Watermark:    store,
		// InitialWatermark deliberately set to a SMALLER value than
		// the store's — the store's value must win.
		InitialWatermark: 0,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = tr.Subscribe(ctx)
	select {
	case q := <-queries:
		if !strings.Contains(q, "event_time >= 1744000000000000000") {
			t.Fatalf("first query did not use persisted watermark; got %q", q)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for first poll")
	}
}

// TestTrigger_FallsBackToInitialWatermarkWhenStoreEmpty — fresh cluster:
// the persistent table has no row for this host yet, trigger uses
// the configured InitialWatermark instead.
func TestTrigger_FallsBackToInitialWatermarkWhenStoreEmpty(t *testing.T) {
	queries := make(chan string, 256)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query().Get("query")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	store := &fakeStore{loadOK: false} // no row present
	tr, _ := New(Config{
		Endpoint: srv.URL, Hostname: "node-1",
		PollInterval:     30 * time.Millisecond,
		Watermark:        store,
		InitialWatermark: 42,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = tr.Subscribe(ctx)
	select {
	case q := <-queries:
		if !strings.Contains(q, "event_time >= 42") {
			t.Fatalf("first query did not use InitialWatermark fallback; got %q", q)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for first poll")
	}
}

// TestTrigger_FallsBackOnStoreLoadError — store unreachable on boot
// must not block the trigger from starting; it falls back to
// InitialWatermark and continues.
func TestTrigger_FallsBackOnStoreLoadError(t *testing.T) {
	queries := make(chan string, 256)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query().Get("query")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	store := &fakeStore{loadErr: fmt.Errorf("clickhouse unreachable")}
	tr, _ := New(Config{
		Endpoint: srv.URL, Hostname: "node-1",
		PollInterval:     30 * time.Millisecond,
		Watermark:        store,
		InitialWatermark: 7,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = tr.Subscribe(ctx)
	select {
	case q := <-queries:
		if !strings.Contains(q, "event_time >= 7") {
			t.Fatalf("error path did not fall back to InitialWatermark; got %q", q)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for first poll")
	}
}

// TestTrigger_ThrottledWatermarkSave — successful advances are
// flushed at WatermarkSaveInterval cadence, not on every poll. The
// fake store should see far fewer saves than there were polls.
func TestTrigger_ThrottledWatermarkSave(t *testing.T) {
	const row1 = `{"RuleID":"R1","RuntimeK8sDetails":"{\"podName\":\"p\",\"podNamespace\":\"ns\"}","RuntimeProcessDetails":"{\"processTree\":{\"pid\":1,\"comm\":\"c\"}}","event_time":"1000000000000000001","hostname":"node-1"}`
	const row2 = `{"RuleID":"R1","RuntimeK8sDetails":"{\"podName\":\"p\",\"podNamespace\":\"ns\"}","RuntimeProcessDetails":"{\"processTree\":{\"pid\":1,\"comm\":\"c\"}}","event_time":"1000000000000000002","hostname":"node-1"}`
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if n%2 == 1 {
			_, _ = w.Write([]byte(row1 + "\n"))
		} else {
			_, _ = w.Write([]byte(row2 + "\n"))
		}
	}))
	defer srv.Close()

	store := &fakeStore{loadOK: false}
	tr, _ := New(Config{
		Endpoint: srv.URL, Hostname: "node-1",
		PollInterval:          10 * time.Millisecond,
		Watermark:             store,
		WatermarkSaveInterval: 100 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)
	go func() {
		for range ch {
		}
	}()

	time.Sleep(250 * time.Millisecond) // ≥ 25 polls, ~2-3 save intervals
	saves := store.savedCount()
	pollCalls := int(atomic.LoadInt64(&calls))
	if pollCalls < 10 {
		t.Fatalf("expected many polls in 250ms; got %d", pollCalls)
	}
	if saves >= pollCalls {
		t.Fatalf("saves not throttled: %d saves vs %d polls", saves, pollCalls)
	}
	if saves == 0 {
		t.Fatalf("no watermark saves at all in 250ms with active rows")
	}
}

// TestTrigger_LimitsRowsPerPoll — every query carries LIMIT N so
// catch-up after a stale watermark doesn't translate into one giant
// scan that times out.
func TestTrigger_LimitsRowsPerPoll(t *testing.T) {
	queries := make(chan string, 256)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query().Get("query")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	tr, _ := New(Config{
		Endpoint: srv.URL, Hostname: "node-1",
		PollInterval: 30 * time.Millisecond,
		PollLimit:    250,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = tr.Subscribe(ctx)
	select {
	case q := <-queries:
		if !strings.Contains(q, "LIMIT 250") {
			t.Fatalf("query missing LIMIT clause: %q", q)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for first poll")
	}
}

// TestTrigger_PartialBodyReadStillAdvances — server emits one
// well-formed line then closes the connection mid-second-line. The
// trigger must still emit the first event AND advance its watermark
// so the next poll picks up from there, instead of looping forever
// on the same start watermark.
func TestTrigger_PartialBodyReadStillAdvances(t *testing.T) {
	const goodLine = `{"RuleID":"R1","RuntimeK8sDetails":"{\"podName\":\"p\",\"podNamespace\":\"ns\"}","RuntimeProcessDetails":"{\"processTree\":{\"pid\":1,\"comm\":\"c\"}}","event_time":"5000","hostname":"node-1"}`
	queries := make(chan string, 256)
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query().Get("query")
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			// Take over the raw conn so we can write a valid HTTP response
			// then close the connection mid-stream — emulating the
			// production failure mode where CH starts streaming, the
			// HTTP timeout fires, and the body read returns mid-line.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatalf("ResponseWriter does not support Hijack")
			}
			conn, bufrw, err := hj.Hijack()
			if err != nil {
				t.Fatalf("Hijack: %v", err)
			}
			_, _ = io.WriteString(bufrw, "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
			_, _ = io.WriteString(bufrw, goodLine+"\n")
			_, _ = io.WriteString(bufrw, "{\"RuleID\":\"R2\",\"Runtime")
			_ = bufrw.Flush()
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	tr, _ := New(Config{
		Endpoint: srv.URL, Hostname: "node-1",
		PollInterval: 30 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := tr.Subscribe(ctx)

	select {
	case ev := <-ch:
		if ev.Target.PID != 1 {
			t.Fatalf("first event PID = %d, want 1", ev.Target.PID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for first event from partial body")
	}

	// First poll's query went to ch; drain it then wait for the second
	// poll and assert the watermark advanced past 0.
	<-queries
	select {
	case q := <-queries:
		if !strings.Contains(q, "event_time >= 5000") {
			t.Fatalf("watermark did not advance on partial read; second query: %q", q)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for second poll")
	}
}
