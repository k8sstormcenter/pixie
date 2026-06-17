/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/cenkalti/backoff/v4"
	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/types"
	"px.dev/pixie/src/api/proto/vizierpb"
	"px.dev/pixie/src/e2e_test/perf_tool/experimentpb"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/cluster"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/pixie"
)

type pxlHealthCheck struct {
	pxCtx *pixie.Context
	spec  *experimentpb.PxLHealthCheck

	script string

	scriptSuccess bool
}

var _ HealthCheck = &pxlHealthCheck{}

// NewPxLHealthCheck returns a new HealthCheck based on a 'success' column of a PxL script being true.
func NewPxLHealthCheck(pxCtx *pixie.Context, spec *experimentpb.PxLHealthCheck) HealthCheck {
	return &pxlHealthCheck{
		pxCtx: pxCtx,
		spec:  spec,
	}
}

// Name returns a printable name for this healthcheck.
func (hc *pxlHealthCheck) Name() string {
	return "PxL Healthcheck"
}

const (
	// A freshly deployed Vizier can report healthy and then flap back to
	// unhealthy for a couple minutes after it first responds. On Cilium with
	// kube-proxy-replacement, the BPF socket-LB maps for the new pods' ClusterIP
	// backends take ~40s to program -- until then the cloud-connector's
	// connect() to the query-broker returns EPERM ("operation not permitted")
	// and the cluster is marked unhealthy. The cloud-connector's NATS bridge
	// also recycles every 60s. A single passing healthcheck is therefore not
	// enough: require a run of consecutive successes so we don't start metric
	// recorders (or workloads) during a flap and then fail with "cluster is not
	// in a healthy state".
	healthcheckStableCount   = 5
	healthcheckStableSpacing = 15 * time.Second
	healthcheckMaxWait       = 10 * time.Minute
)

// Wait waits for the PxL healthcheck script to succeed for healthcheckStableCount
// consecutive attempts (with the 'success' column true), so the cluster is
// confirmed stably healthy rather than momentarily healthy during a flap.
func (hc *pxlHealthCheck) Wait(ctx context.Context, clusterCtx *cluster.Context, clusterSpec *experimentpb.ClusterSpec) error {
	if err := hc.prepareScript(clusterSpec); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, healthcheckMaxWait)
	defer cancel()

	consecutive := 0
	var lastErr error
	for {
		hc.scriptSuccess = false
		if err := hc.runHealthCheck(ctx); err != nil {
			// A permanent error (e.g. a misconfigured success column) is not
			// going to resolve by retrying -- fail fast.
			var permErr *backoff.PermanentError
			if errors.As(err, &permErr) {
				return permErr.Err
			}
			lastErr = err
			if consecutive > 0 {
				log.WithError(err).Tracef("healthcheck flapped after %d/%d consecutive successes; restarting stability window", consecutive, healthcheckStableCount)
			}
			consecutive = 0
		} else {
			consecutive++
			log.Tracef("pxl healthcheck passed (%d/%d consecutive)", consecutive, healthcheckStableCount)
			if consecutive >= healthcheckStableCount {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for stable healthcheck (%d/%d consecutive): %w", consecutive, healthcheckStableCount, lastErr)
			}
			return ctx.Err()
		case <-time.After(healthcheckStableSpacing):
		}
	}
}

func (hc *pxlHealthCheck) runHealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	vz, err := hc.pxCtx.NewVizierClient()
	if err != nil {
		log.WithError(err).Trace("failed to create vizier client")
		return err
	}
	resultSet, err := vz.ExecuteScript(ctx, hc.script, hc)
	if err != nil {
		return err
	}
	if err := resultSet.Stream(); err != nil {
		return err
	}
	if !hc.scriptSuccess {
		return errors.New("healthcheck script executed successfully, but returned false (unhealthy)")
	}
	return nil
}

func (hc *pxlHealthCheck) prepareScript(clusterSpec *experimentpb.ClusterSpec) error {
	t, err := template.New("").Parse(hc.spec.Script)
	if err != nil {
		return err
	}
	buf := &strings.Builder{}
	if err := t.Execute(buf, clusterSpec); err != nil {
		return err
	}
	hc.script = buf.String()
	return nil
}

// AcceptTable implements pxapi.TableMuxer.
func (hc *pxlHealthCheck) AcceptTable(context.Context, types.TableMetadata) (pxapi.TableRecordHandler, error) {
	return hc, nil
}

// HandleInit implements pxapi.TableRecordHandler.
func (hc *pxlHealthCheck) HandleInit(context.Context, types.TableMetadata) error {
	return nil
}

// HandleRecord implements pxapi.TableRecordHandler.
func (hc *pxlHealthCheck) HandleRecord(ctx context.Context, r *types.Record) error {
	d := r.GetDatum(hc.spec.SuccessColumn)
	if d == nil || d.Type() != vizierpb.BOOLEAN {
		return backoff.Permanent(fmt.Errorf("success_column: '%s' is not a boolean column in the output", hc.spec.SuccessColumn))
	}
	success := d.(*types.BooleanValue).Value()
	hc.scriptSuccess = success
	return nil
}

// HandleDone implements pxapi.TableRecordHandler.
func (hc *pxlHealthCheck) HandleDone(context.Context) error {
	return nil
}
