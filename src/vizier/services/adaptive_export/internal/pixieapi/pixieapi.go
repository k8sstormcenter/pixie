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

// Package pixieapi adapts pxapi to a flat-row Pixie interface for the
// controller. Use when the operator (not the cloud's retention plugin)
// is the writer of pixie observation rows — necessary on deployments
// where the cloud can't reach an internal ClickHouse endpoint.
package pixieapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/errdefs"
	"px.dev/pixie/src/api/go/pxapi/types"
	jwtutils "px.dev/pixie/src/shared/services/utils"
)

// Row is a flat per-pixie-row map[col]any. Compatible with sink's
// per-row JSONEachRow encoder.
type Row map[string]any

// Adapter executes PxL via pxapi and returns flat rows.
type Adapter struct {
	client    *pxapi.Client
	clusterID string
	// directOpts, when non-nil, makes Query rebuild a pxapi.Client per
	// call with a freshly-minted service JWT in WithBearerAuth. Used
	// for direct-mode (in-cluster vizier-query-broker), where the cloud
	// passthrough proxy is bypassed entirely. JWTs are minted fresh
	// because GenerateJWTForService produces 10-minute claims and we
	// want each fan-out window to carry its own valid token.
	directOpts *DirectOptions
}

// DirectOptions configures direct-mode connection to vizier in-cluster.
// Use when the cloud's passthrough proxy can't authorize the operator's
// API key (e.g. self-hosted clouds where API keys are scoped per-cluster
// and a freshly-deployed cluster isn't yet linked to the key's owner).
type DirectOptions struct {
	// VizierAddr is the in-cluster gRPC endpoint, typically
	// "vizier-query-broker-svc.pl.svc.cluster.local:50300".
	VizierAddr string
	// SigningKey is the cluster's JWT signing key, mounted from
	// pl-cluster-secrets/jwt-signing-key.
	SigningKey string
	// ServiceID is the issuer-side service identifier (claim "sub").
	// Defaults to "adaptive_export" if empty.
	ServiceID string
}

// New constructs an Adapter wired to the cluster's vizier via cloud passthrough.
func New(client *pxapi.Client, clusterID string) *Adapter {
	return &Adapter{client: client, clusterID: clusterID}
}

// NewDirect constructs an Adapter that bypasses the pixie cloud and
// connects directly to the in-cluster vizier-query-broker. Each Query
// call rebuilds the gRPC client with a fresh service JWT.
func NewDirect(clusterID string, opts DirectOptions) *Adapter {
	if opts.ServiceID == "" {
		opts.ServiceID = "adaptive_export"
	}
	return &Adapter{clusterID: clusterID, directOpts: &opts}
}

// NewDirectFromEnv builds a direct-mode Adapter from the runtime env.
// Reads ADAPTIVE_VIZIER_DIRECT_ADDR for the broker addr and
// PL_JWT_SIGNING_KEY for the signing key (matching kelvin/metadata
// pod env conventions). Returns an error if either is missing.
//
// The caller MUST also set PX_DISABLE_TLS=1 in the operator pod —
// pxapi's WithDisableTLSVerification only sets InsecureSkipVerify when
// that env is "1" AND the addr contains "cluster.local"; without it,
// pxapi log.Fatal's at NewClient time. We accept skip-verify because
// query-broker's TLS uses a self-signed in-cluster CA we don't have a
// clean way to mount here.
func NewDirectFromEnv(clusterID string) (*Adapter, error) {
	addr := os.Getenv("ADAPTIVE_VIZIER_DIRECT_ADDR")
	if addr == "" {
		return nil, errors.New("pixieapi: ADAPTIVE_VIZIER_DIRECT_ADDR not set")
	}
	sk := os.Getenv("PL_JWT_SIGNING_KEY")
	if sk == "" {
		return nil, errors.New("pixieapi: PL_JWT_SIGNING_KEY not set (mount pl-cluster-secrets/jwt-signing-key)")
	}
	return NewDirect(clusterID, DirectOptions{VizierAddr: addr, SigningKey: sk}), nil
}

// Query executes pxl on the configured cluster and aggregates every
// emitted record from every table into one []Row.
func (a *Adapter) Query(ctx context.Context, pxl string) ([]Row, error) {
	client := a.client
	if a.directOpts != nil {
		// Direct mode: build fresh client + fresh service JWT for each
		// query. JWT is 10-min; fan-out is seconds, so this is safe.
		jwt, err := jwtutils.SignJWTClaims(
			jwtutils.GenerateJWTForService(a.directOpts.ServiceID, "vizier"),
			a.directOpts.SigningKey,
		)
		if err != nil {
			return nil, fmt.Errorf("pixieapi: sign JWT: %w", err)
		}
		c, err := pxapi.NewClient(ctx,
			pxapi.WithCloudAddr(a.directOpts.VizierAddr),
			pxapi.WithDisableTLSVerification(a.directOpts.VizierAddr),
			pxapi.WithBearerAuth(jwt),
		)
		if err != nil {
			return nil, fmt.Errorf("pixieapi: direct dial: %w", err)
		}
		client = c
	}
	vz, err := client.NewVizierClient(ctx, a.clusterID)
	if err != nil {
		return nil, fmt.Errorf("pixieapi: vizier dial: %w", err)
	}
	mux := newCollector()
	rs, err := vz.ExecuteScript(ctx, pxl, mux)
	if err != nil {
		return nil, fmt.Errorf("pixieapi: ExecuteScript: %w", err)
	}
	defer rs.Close()
	if err := rs.Stream(); err != nil {
		if errdefs.IsCompilationError(err) {
			return nil, fmt.Errorf("pixieapi: PxL compilation: %w", err)
		}
		return nil, fmt.Errorf("pixieapi: stream: %w", err)
	}
	return mux.rows(), nil
}

type collector struct {
	mu  sync.Mutex
	all []Row
}

func newCollector() *collector { return &collector{} }

func (c *collector) AcceptTable(_ context.Context, _ types.TableMetadata) (pxapi.TableRecordHandler, error) {
	return &tableHandler{out: c}, nil
}

func (c *collector) rows() []Row {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Row(nil), c.all...)
}

type tableHandler struct {
	out  *collector
	meta types.TableMetadata
}

func (h *tableHandler) HandleInit(_ context.Context, md types.TableMetadata) error {
	h.meta = md
	return nil
}

func (h *tableHandler) HandleRecord(_ context.Context, rec *types.Record) error {
	row := make(Row, len(h.meta.ColInfo))
	for _, col := range h.meta.ColInfo {
		datum := rec.GetDatum(col.Name)
		if datum == nil {
			continue
		}
		row[col.Name] = datumValue(datum)
	}
	h.out.mu.Lock()
	h.out.all = append(h.out.all, row)
	h.out.mu.Unlock()
	return nil
}

func (h *tableHandler) HandleDone(_ context.Context) error { return nil }

func datumValue(d types.Datum) any {
	switch v := d.(type) {
	case *types.BooleanValue:
		return v.Value()
	case *types.Int64Value:
		return v.Value()
	case *types.Float64Value:
		return v.Value()
	case *types.StringValue:
		return v.Value()
	case *types.Time64NSValue:
		return v.Value()
	case *types.UInt128Value:
		return v.Value()
	default:
		return d.String()
	}
}
