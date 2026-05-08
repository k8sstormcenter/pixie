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
	"fmt"
	"sync"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/errdefs"
	"px.dev/pixie/src/api/go/pxapi/types"
)

// Row is a flat per-pixie-row map[col]any. Compatible with sink's
// per-row JSONEachRow encoder.
type Row map[string]any

// Adapter executes PxL via pxapi and returns flat rows.
type Adapter struct {
	client    *pxapi.Client
	clusterID string
}

// New constructs an Adapter wired to the cluster's vizier.
func New(client *pxapi.Client, clusterID string) *Adapter {
	return &Adapter{client: client, clusterID: clusterID}
}

// Query executes pxl on the configured cluster and aggregates every
// emitted record from every table into one []Row.
func (a *Adapter) Query(ctx context.Context, pxl string) ([]Row, error) {
	vz, err := a.client.NewVizierClient(ctx, a.clusterID)
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
