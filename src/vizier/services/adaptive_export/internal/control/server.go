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

// graphWriter persists dx evidence-graph edges (newline-delimited JSON,
// JSONEachRow) to forensic_db.dx_attack_graph. nil → /dx/attack_graph 501s.
type graphWriter interface {
	WriteAttackGraph(ctx context.Context, jsonEachRow []byte) error
}

// Server is the control HTTP surface.
type Server struct {
	set    exporter
	runner queryRunner // may be nil; /query then returns 501
	graph  graphWriter // may be nil; /dx/attack_graph then returns 501
	mux    *http.ServeMux
	verify func(bearer string) error // nil → auth disabled; set via SetAuth
}

// New builds the control server. runner may be nil for deployments that
// only need start/stop (no operator-side one-shot queries).
func New(set exporter, runner queryRunner) *Server {
	s := &Server{set: set, runner: runner, mux: http.NewServeMux()}
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/export/start", s.handleStart)
	s.mux.HandleFunc("/export/stop", s.handleStop)
	s.mux.HandleFunc("/query", s.handleQuery)
	s.mux.HandleFunc("/dx/attack_graph", s.handleDXAttackGraph)
	return s
}

// SetGraphWriter wires the dx_attack_graph sink.
func (s *Server) SetGraphWriter(g graphWriter) { s.graph = g }

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

// handleDXAttackGraph ingests a JSON array of dx evidence-graph edges and writes
// them to forensic_db.dx_attack_graph (as JSONEachRow).
func (s *Server) handleDXAttackGraph(w http.ResponseWriter, r *http.Request) {
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
	if err := s.graph.WriteAttackGraph(r.Context(), buf.Bytes()); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ── wire types ────────────────────────────────────────────────────────
type targetReq struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Comm      string `json:"comm"`
}

type startReq struct {
	targetReq
	TEnd int64 `json:"t_end"` // unix seconds
}

type queryReq struct {
	targetReq
	Table   string   `json:"table"`
	Window  [2]int64 `json:"window"` // [start,end] unix seconds
	QueryID string   `json:"query_id"`
}

func (t targetReq) key() activeset.Key {
	return activeset.Key{Namespace: t.Namespace, Pod: t.Pod}
}

func (t targetReq) target() anomaly.Target {
	return anomaly.Target{Comm: t.Comm, Pod: t.Pod, Namespace: t.Namespace}
}

// maxControlBodyBytes caps a single control-surface request body. The
// largest legitimate payload we accept is /dx/attack_graph which is a
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
	s.set.Upsert(req.key(), time.Unix(req.TEnd, 0))
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
	err := s.runner.OrderQuery(req.target(), req.Table,
		time.Unix(req.Window[0], 0), time.Unix(req.Window[1], 0), req.QueryID)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
