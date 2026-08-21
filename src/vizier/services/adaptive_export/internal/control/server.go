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

// Package control is the external control surface. It lets the controller
// (the diagnostician) steer this AE (the hands): start/stop exporting a
// target, and order a specific (table, window) query. AE's existing
// kubescape-trigger → controller → activeSet flow is untouched; this is an
// additional, env-gated driver of the same activeSet. Off unless
// CONTROL_ADDR is set.
//
// The handlers depend on narrow interfaces (exporter, queryRunner) — not on
// the concrete Controller — so the package is unit-testable with fakes and so
// the blast radius on AE is a single wiring line in main.go.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	jwtutils "px.dev/pixie/src/shared/services/utils"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/anomaly"
)

// exporter is the slice of *activeset.ActiveSet this package needs: the controller
// decides membership, AE's streaming/controller acts on the deltas.
type exporter interface {
	Upsert(k activeset.Key, tEnd time.Time)
	Remove(k activeset.Key)
}

// queryRunner executes one controller-ordered (table, target, window) query and
// writes the result through AE's normal sink. The query_id is carried so
// exported rows can be flagged provisional→confirmed/benign_retire (audit).
type queryRunner interface {
	OrderQuery(target anomaly.Target, table string, start, end time.Time, queryID string) error
}

// exportAller is the optional "steer-all" capability behind /export/start: given
// a target, capture the COMPLETE evidence set (every configured pixie table) for
// its pod. The controller implements it. Optional (type-asserted) so start/stop-
// only deployments and test mocks that only implement queryRunner still compile.
type exportAller interface {
	OrderExportAll(target anomaly.Target, start, end time.Time)
}

// controlExportLookback is how far back /export/start reaches when a client
// (dx) steers a full capture — it sends only t_end, so AE captures
// [t_end-lookback, t_end]. 600s mirrors dx's ±300s referral window so the
// anomaly is comfortably inside the pulled slice.
const controlExportLookback = 600 * time.Second

// A /query window narrower than this is widened to controlExportLookback (a
// point window keyed on one finding's timestamp matches no pixie rows).
const minControlQueryWindow = 5 * time.Second

// The control API carries timestamps in the pipeline's ONE unit: unix
// NANOSECONDS — the same unit as forensic_db.*.event_time and dx's referral
// windows. Read them with time.Unix(0, ns). (This spot previously did
// time.Unix(ns, 0), reading nanos AS seconds → a year-56-billion window that
// overlaps no data, so every dx-steered export — all dark tables included —
// silently returned zero rows.)

// graphWriter persists dx evidence-graph edges (newline-delimited JSON,
// JSONEachRow) to forensic_db.dx_evidence_graph. nil → /dx/evidence_graph 501s.
type graphWriter interface {
	WriteEvidenceGraph(ctx context.Context, jsonEachRow []byte) error
}

// manifestWriter persists one dx §9 completeness manifest per verdict
// (JSONEachRow) to forensic_db.dx_evidence_manifest. nil → /dx/evidence_manifest 501s.
type manifestWriter interface {
	WriteEvidenceManifest(ctx context.Context, jsonEachRow []byte) error
}

// rowsWriter persists dx-handed pixie base rows (loop 1: conn_stats with a
// pre-stamped unique_id) into forensic_db.<table> through the SAME sink the
// controller capture path uses (sink.ClickHouseHTTP.WritePixieRows).
// nil → /dx/rows 501s.
type rowsWriter interface {
	WritePixieRows(ctx context.Context, table string, rows []map[string]any) error
}

// Server is the control HTTP surface.
type Server struct {
	set      exporter
	runner   queryRunner    // may be nil; /query then returns 501
	graph    graphWriter    // may be nil; /dx/evidence_graph then returns 501
	manifest manifestWriter // may be nil; /dx/evidence_manifest then returns 501
	rows     rowsWriter     // may be nil; /dx/rows then returns 501
	mux      *http.ServeMux
	verify   func(bearer string) error // nil → auth disabled; set via SetAuth
}

// New builds the control server. runner may be nil for deployments that
// only need start/stop (no operator-side one-shot queries).
func New(set exporter, runner queryRunner) *Server {
	s := &Server{set: set, runner: runner, mux: http.NewServeMux()}
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/export/start", s.handleStart)
	s.mux.HandleFunc("/export/stop", s.handleStop)
	s.mux.HandleFunc("/query", s.handleQuery)
	s.mux.HandleFunc("/dx/evidence_graph", s.handleDXEvidenceGraph)
	s.mux.HandleFunc("/dx/evidence_manifest", s.handleDXEvidenceManifest)
	s.mux.HandleFunc("/dx/rows", s.handleDXRows)
	return s
}

// SetGraphWriter wires the dx_evidence_graph sink.
func (s *Server) SetGraphWriter(g graphWriter) { s.graph = g }

// SetManifestWriter wires the dx_evidence_manifest sink.
func (s *Server) SetManifestWriter(m manifestWriter) { s.manifest = m }

// SetRowsWriter wires the /dx/rows base-row sink (loop 1).
func (s *Server) SetRowsWriter(rw rowsWriter) { s.rows = rw }

// SetAuth turns on bearer-JWT auth for the control surface, verified with the
// SAME shared lib + signing key the vizier broker/PEM use (px.dev/pixie/src/
// shared/services/utils). dx already mints a service JWT (GenerateJWTForService,
// PL_JWT_SIGNING_KEY) for its broker/PEM queries — it attaches the same token
// here. No new secret/crypto. /healthz stays open for k8s probes.
// (CodeRabbit: protect control endpoints with auth — server.go.)
func (s *Server) SetAuth(signingKey, audience string) {
	s.verify = func(bearer string) error {
		_, err := jwtutils.ParseToken(bearer, signingKey, audience)
		return err
	}
}

// Handler exposes the mux (for httptest + main.go wiring), wrapped in the auth
// middleware when SetAuth was called.
func (s *Server) Handler() http.Handler {
	if s.verify == nil {
		return s.mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" { // probes stay unauthenticated
			const p = "Bearer "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, p) || s.verify(strings.TrimPrefix(h, p)) != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		s.mux.ServeHTTP(w, r)
	})
}

// handleDXEvidenceGraph ingests a JSON array of dx evidence-graph edges and writes
// them to forensic_db.dx_evidence_graph (as JSONEachRow).
func (s *Server) handleDXEvidenceGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.graph == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	var edges []json.RawMessage
	if !decode(w, r, &edges) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(edges) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var buf bytes.Buffer
	for _, e := range edges {
		buf.Write(e)
		buf.WriteByte('\n')
	}
	if err := s.graph.WriteEvidenceGraph(r.Context(), buf.Bytes()); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// dxRowsAllowedTables guards /dx/rows against arbitrary-table writes: only the
// bridged tables dx hands base rows for (each carries a pre-stamped unique_id and
// a dx_ord__ view) are accepted. Mirrors evidencegraph.UIDColsByTable on the dx side.
var dxRowsAllowedTables = map[string]bool{
	"conn_stats":   true,
	"redis_events": true,
	"http_events":  true,
	"dns_events":   true,
	"pgsql_events": true,
	"mysql_events": true,
	"dc_snoop":     true,
	"stack_trace":  true,
}

// dxRowsReq is the /dx/rows wire body: dx-handed base rows for one table.
type dxRowsReq struct {
	Table string           `json:"table"`
	Rows  []map[string]any `json:"rows"`
}

// handleDXRows ingests dx-handed base rows (loop 1: conn_stats carrying a
// pre-stamped content-hash unique_id, a hex String) and writes them to
// forensic_db.<table> via the same sink path the controller capture uses.
// decodeNumber (UseNumber) keeps large integer columns as json.Number so the
// fast encoder emits exact decimal text; the shared decode() would cast them to
// float64, and the sink's appendFloat renders large values in scientific
// notation, which ClickHouse rejects for Int64/UInt64 columns (whole batch 502).
func (s *Server) handleDXRows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.rows == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	var req dxRowsReq
	if !decodeNumber(w, r, &req) || !dxRowsAllowedTables[req.Table] {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(req.Rows) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := s.rows.WritePixieRows(r.Context(), req.Table, req.Rows); err != nil {
		log.WithField("table", req.Table).WithField("rows", len(req.Rows)).WithError(err).Error("dx/rows: WritePixieRows failed")
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// dxManifest mirrors the wire shape of dx's manifest.Manifest (internal/manifest).
// Scalars map to typed forensic_db.dx_evidence_manifest columns; the nested
// collections are held as raw JSON and persisted as JSON text in String columns
// so the JSONEachRow insert is ClickHouse-version independent.
type dxManifest struct {
	InvestigationID string          `json:"investigation_id"`
	EventTime       int64           `json:"event_time"`
	Hostname        string          `json:"hostname"`
	Condition       string          `json:"condition"`
	Verdict         string          `json:"verdict"`
	Confidence      float64         `json:"confidence"`
	Posterior       float64         `json:"posterior"`
	CatalogVersion  string          `json:"catalog_version"`
	CaseWindow      json.RawMessage `json:"case_window"`
	Findings        json.RawMessage `json:"findings"`
	Orders          json.RawMessage `json:"orders"`
	Seeds           json.RawMessage `json:"seeds"`
	Chain           json.RawMessage `json:"chain"`
	EvidenceHash    string          `json:"evidence_hash"`
}

// handleDXEvidenceManifest ingests ONE dx completeness manifest (per verdict)
// and writes it to forensic_db.dx_evidence_manifest as a single JSONEachRow
// row, with the nested collections rendered as JSON text.
func (s *Server) handleDXEvidenceManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.manifest == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	var m dxManifest
	if !decode(w, r, &m) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	row := map[string]any{
		"investigation_id": m.InvestigationID,
		"event_time":       m.EventTime,
		"hostname":         m.Hostname,
		"condition":        m.Condition,
		"verdict":          m.Verdict,
		"confidence":       m.Confidence,
		"posterior":        m.Posterior,
		"catalog_version":  m.CatalogVersion,
		"case_window":      jsonText(m.CaseWindow),
		"findings":         jsonText(m.Findings),
		"orders":           jsonText(m.Orders),
		"seeds":            jsonText(m.Seeds),
		"chain":            jsonText(m.Chain),
		"evidence_hash":    m.EvidenceHash,
	}
	line, err := json.Marshal(row)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := s.manifest.WriteEvidenceManifest(r.Context(), append(line, '\n')); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// jsonText renders a nested JSON value as compact text for a String column;
// nil/absent/null → "" so the column holds an empty string rather than "null".
func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}

// ── wire types ────────────────────────────────────────────────────────
type targetReq struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Comm      string `json:"comm"`
}

type startReq struct {
	targetReq
	TEnd int64 `json:"t_end"` // unix NANOSECONDS (pipeline-wide unit)
}

type queryReq struct {
	targetReq
	Table   string   `json:"table"`
	Window  [2]int64 `json:"window"` // [start,end] unix NANOSECONDS (pipeline-wide unit)
	QueryID string   `json:"query_id"`
}

func (t targetReq) key() activeset.Key {
	return activeset.Key{Namespace: t.Namespace, Pod: t.Pod}
}

func (t targetReq) target() anomaly.Target {
	return anomaly.Target{Comm: t.Comm, Pod: t.Pod, Namespace: t.Namespace}
}

// maxControlBodyBytes caps a single control-surface request body. The
// largest legitimate payload we accept is /dx/evidence_graph which is a
// JSON array of pre-marshalled JSONEachRow lines — measured live the
// hottest dx rule-in pass fits in ~256 KiB. 4 MiB is well above that
// and below the per-pod memory headroom an oversized POST could
// exhaust on the operator (CodeRabbit r-#68/control/server.go).
const maxControlBodyBytes = 4 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBodyBytes)
	return json.NewDecoder(r.Body).Decode(v) == nil
}

func decodeNumber(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	return dec.Decode(v) == nil
}

// ── handlers ──────────────────────────────────────────────────────────
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req startReq
	if !decode(w, r, &req) || req.Pod == "" || req.TEnd <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.set.Upsert(req.key(), time.Unix(0, req.TEnd).UTC())
	// Steer-all: when a querier is wired (pull mode), a StartExport is dx telling
	// AE "grab the complete evidence set for this pod." The activeSet.Upsert above
	// only feeds streaming mode, so in pull mode drive the full-table capture here.
	// Async: the fan-out is slow (one query per table); dx must get its 202 back
	// immediately and not block on the export.
	if ea, ok := s.runner.(exportAller); ok {
		hi := time.Unix(0, req.TEnd).UTC()
		lo := hi.Add(-controlExportLookback)
		go ea.OrderExportAll(req.target(), lo, hi)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req targetReq
	if !decode(w, r, &req) || req.Pod == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.set.Remove(req.key())
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	var req queryReq
	if !decode(w, r, &req) || req.Pod == "" || req.Table == "" || req.QueryID == "" ||
		req.Window[0] <= 0 || req.Window[1] <= 0 || req.Window[0] >= req.Window[1] {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	hi := time.Unix(0, req.Window[1]).UTC()
	lo := time.Unix(0, req.Window[0]).UTC()
	if hi.Sub(lo) < minControlQueryWindow {
		lo = hi.Add(-controlExportLookback) // widen a point window
	}
	err := s.runner.OrderQuery(req.target(), req.Table, lo, hi, req.QueryID)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
