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

// Package aeloadsuite is the live adaptive-export (AE) load-test suite.
//
// It replaces the former shell harness with a table-driven Go test framework:
// each experiment is a fixture (§fixtures.go), each measurement is a named KPI
// asserted with testify/require (§kpi.go), and one runner drives them against a
// deployed AE image on a real rig (§suite_test.go).
//
// The only AE input under test is the kubescape_logs trigger stream: real
// kubescape is NOT deployed. Fixtures inject curated rows over the ClickHouse
// HTTP interface (Vector-shaped, exact event_time control) and read back the
// deterministic forensic_db surface. Kubernetes/kubescape wire tokens (RuleID
// R00xx, column names) are external contracts and stay literal.
package aeloadsuite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Env is the resolved live-rig configuration. Populated from AELOAD_* env vars
// by RequireLiveEnv, which skips the suite when AELOAD_LIVE != "1" so a plain
// `go test ./...` is a no-op in CI.
type Env struct {
	CHURL    string // ClickHouse HTTP endpoint, e.g. http://127.0.0.1:8123
	CHUser   string // read-side user (empty = default user)
	CHPass   string
	CHWUser  string // ingest (write) user
	CHWPass  string
	AENS     string // AE namespace (default pl)
	AEDaemon string // AE DaemonSet name (default adaptive-export)
	Node     string // the node whose hostname AE polls; resolved if empty

	http *http.Client
}

// RequireLiveEnv loads the rig config or skips the test. The suite is live-only:
// it drives a deployed AE image, so it runs only when explicitly enabled.
func RequireLiveEnv(t *testing.T) Env {
	t.Helper()
	if os.Getenv("AELOAD_LIVE") != "1" {
		t.Skip("AELOAD_LIVE != 1 — live AE suite skipped (set AELOAD_LIVE=1 + AELOAD_CH_URL + KUBECONFIG)")
	}
	e := Env{
		CHURL:    envOr("AELOAD_CH_URL", "http://127.0.0.1:8123"),
		CHUser:   os.Getenv("AELOAD_CH_USER"),
		CHPass:   os.Getenv("AELOAD_CH_PASS"),
		CHWUser:  envOr("AELOAD_CH_WUSER", "ingest_writer"),
		CHWPass:  envOr("AELOAD_CH_WPASS", "changeme-ingest"),
		AENS:     envOr("AELOAD_AE_NS", "pl"),
		AEDaemon: envOr("AELOAD_AE_DS", "adaptive-export"),
		Node:     os.Getenv("AELOAD_NODE"),
		http:     &http.Client{Timeout: 30 * time.Second},
	}
	if e.Node == "" {
		e.Node = e.FirstNode(t)
	}
	if e.Node == "" {
		t.Fatal("could not resolve a node name (set AELOAD_NODE)")
	}
	return e
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---- ClickHouse over HTTP (no driver; mirrors the curl path the scripts used) ----

// chReq posts a query with the given credentials and returns the trimmed body.
func (e Env) chReq(user, pass, sql string, body []byte) (string, error) {
	url := strings.TrimRight(e.CHURL, "/") + "/"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if body == nil {
		req.Body = nil
		req, _ = http.NewRequest(http.MethodPost, url, strings.NewReader(sql))
	} else {
		q := req.URL.Query()
		q.Set("query", sql)
		req.URL.RawQuery = q.Encode()
		req.Header.Set("Content-Type", "application/x-ndjson")
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("clickhouse HTTP %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}
	return strings.TrimSpace(buf.String()), nil
}

// Query runs a read query with the read-side credentials.
func (e Env) Query(t *testing.T, sql string) string {
	t.Helper()
	out, err := e.chReq(e.CHUser, e.CHPass, sql, nil)
	if err != nil {
		t.Fatalf("clickhouse query failed: %v\nsql: %s", err, sql)
	}
	return out
}

// QueryInt runs a scalar read query and parses it as an int (0 if empty).
func (e Env) QueryInt(t *testing.T, sql string) int {
	t.Helper()
	s := strings.TrimSpace(e.Query(t, sql))
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(digitsOnly(s))
	if err != nil {
		t.Fatalf("clickhouse scalar %q not an int: %v (sql: %s)", s, err, sql)
	}
	return n
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "0"
	}
	return b.String()
}

// ---- kubescape_logs injection (Vector-shaped rows; port of inject.sh) ----

// AnomalyRow is one kubescape_logs trigger row. event_time is unix SECONDS —
// the unit the SOC Vector kubescape sink emits and the unit the forensic_db DDL
// TTL/PARTITION assume (contract C1). RuleID values (R0001, R0010, …) are
// kubescape external wire and stay literal. anomaly_hash is computed by AE, not
// here, so per-rep isolation comes from a unique Pod (+ unique Hostname).
type AnomalyRow struct {
	Namespace string
	Pod       string
	RuleID    string
	PID       int
	Comm      string
	EventTime int64
	Hostname  string
}

// Inject writes the rows into forensic_db.kubescape_logs via JSONEachRow. The
// JSON-string columns (RuntimeK8sDetails/RuntimeProcessDetails/BaseRuntimeMetadata)
// are marshaled with encoding/json so escaping is correct by construction.
func (e Env) Inject(t *testing.T, rows ...AnomalyRow) {
	t.Helper()
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, r := range rows {
		k8s, _ := json.Marshal(map[string]any{"podName": r.Pod, "podNamespace": r.Namespace})
		proc, _ := json.Marshal(map[string]any{"processTree": map[string]any{"pid": r.PID, "comm": r.Comm}})
		base, _ := json.Marshal(map[string]any{"alertName": r.RuleID})
		if err := enc.Encode(map[string]any{
			"BaseRuntimeMetadata":   string(base),
			"CloudMetadata":         "",
			"RuleID":                r.RuleID,
			"RuntimeK8sDetails":     string(k8s),
			"RuntimeProcessDetails": string(proc),
			"event":                 "",
			"event_time":            r.EventTime,
			"hostname":              r.Hostname,
		}); err != nil {
			t.Fatalf("encode anomaly row: %v", err)
		}
	}
	sql := "INSERT INTO forensic_db.kubescape_logs FORMAT JSONEachRow"
	if _, err := e.chReq(e.CHWUser, e.CHWPass, sql, body.Bytes()); err != nil {
		t.Fatalf("inject kubescape_logs failed: %v", err)
	}
}

// ---- deterministic control-surface reads (port of lib.sh count helpers) ----

// AttribCount returns adaptive_attribution FINAL rows for a rep, matched by the
// rep's globally-unique pod substring. adaptive_attribution stores the BARE pod
// name, so the LIKE is safe.
func (e Env) AttribCount(t *testing.T, node, podLike string) int {
	return e.QueryInt(t, fmt.Sprintf(
		"SELECT count() FROM (SELECT 1 FROM forensic_db.adaptive_attribution FINAL WHERE hostname='%s' AND pod LIKE '%%%s%%')",
		node, podLike))
}

// UniqHashes returns the distinct anomaly_hash count for a rep.
func (e Env) UniqHashes(t *testing.T, node, podLike string) int {
	return e.QueryInt(t, fmt.Sprintf(
		"SELECT uniqExact(anomaly_hash) FROM forensic_db.adaptive_attribution WHERE hostname='%s' AND pod LIKE '%%%s%%'",
		node, podLike))
}

// Watermark returns the persisted trigger watermark for a node (monotone across
// reps sharing the node; persistence is throttled ~5s, so it is checked for
// monotonicity, never as a hard exact gate).
func (e Env) Watermark(t *testing.T, node string) int64 {
	s := e.Query(t, fmt.Sprintf(
		"SELECT watermark FROM forensic_db.trigger_watermark FINAL WHERE hostname='%s' AND table_name='kubescape_logs'", node))
	n, _ := strconv.ParseInt(digitsOnly(s), 10, 64)
	return n
}

// TableRows returns protocol-table rows for a rep (pod stored as "<ns>/<pod>").
func (e Env) TableRows(t *testing.T, table, podLike string) int {
	return e.QueryInt(t, fmt.Sprintf(
		"SELECT count() FROM forensic_db.`%s` WHERE pod LIKE '%%%s%%'", table, podLike))
}

// WaitAttrib polls adaptive_attribution until it reaches want (AE's 250ms poll +
// write can lag a few seconds). Returns the final observed count.
func (e Env) WaitAttrib(t *testing.T, node, podLike string, want, timeoutSec int) int {
	t.Helper()
	var n int
	for i := 0; i < timeoutSec; i++ {
		n = e.AttribCount(t, node, podLike)
		if n >= want {
			return n
		}
		time.Sleep(time.Second)
	}
	return n
}

// ---- kubectl helpers (port of lib.sh + ae_config.sh) ----

func (e Env) kube(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// FirstNode returns the first node name (fixture hostname for control fixtures).
func (e Env) FirstNode(t *testing.T) string {
	out, err := exec.Command("kubectl", "get", "nodes",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}").CombinedOutput()
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return ln
		}
	}
	return ""
}

// RestartAE rolls the AE DaemonSet and waits for it to be ready (E6 restart).
func (e Env) RestartAE(t *testing.T) {
	t.Helper()
	e.kube(t, "-n", e.AENS, "rollout", "restart", "ds/"+e.AEDaemon)
	e.kube(t, "-n", e.AENS, "rollout", "status", "ds/"+e.AEDaemon, "--timeout=180s")
	time.Sleep(8 * time.Second)
}

// Warmup absorbs the AE trigger cold-start on a node so rep 1 is steady-state:
// the first poll after AE boots only establishes the watermark baseline.
func (e Env) Warmup(t *testing.T, node string) {
	t.Helper()
	e.Inject(t, AnomalyRow{
		Namespace: "aeload", Pod: fmt.Sprintf("warmup-%d", time.Now().UnixNano()),
		RuleID: "R0001", PID: 999, Comm: "warmup", EventTime: time.Now().Unix(), Hostname: node,
	})
	time.Sleep(6 * time.Second)
}
