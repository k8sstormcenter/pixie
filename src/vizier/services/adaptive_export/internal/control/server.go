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
	"encoding/json"
	"net/http"
	"time"

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

// Server is the control HTTP surface.
type Server struct {
	set    exporter
	runner queryRunner // may be nil; /query then returns 501
	mux    *http.ServeMux
}

// New builds the control server. runner may be nil for deployments that
// only need start/stop (no operator-side one-shot queries).
func New(set exporter, runner queryRunner) *Server {
	s := &Server{set: set, runner: runner, mux: http.NewServeMux()}
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/export/start", s.handleStart)
	s.mux.HandleFunc("/export/stop", s.handleStop)
	s.mux.HandleFunc("/query", s.handleQuery)
	return s
}

// Handler exposes the mux (for httptest + main.go wiring).
func (s *Server) Handler() http.Handler { return s.mux }

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

func decode(r *http.Request, v any) bool {
	defer r.Body.Close()
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
	if !decode(r, &req) || req.Pod == "" {
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
	if !decode(r, &req) || req.Pod == "" {
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
	if !decode(r, &req) || req.Pod == "" || req.Table == "" || req.QueryID == "" {
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
