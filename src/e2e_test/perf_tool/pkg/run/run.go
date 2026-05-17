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

package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gofrs/uuid"
	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/types"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"px.dev/pixie/src/e2e_test/perf_tool/experimentpb"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/cluster"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/deploy"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/exporter"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/metrics"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/pixie"
)

// Runner is responsible for running experiments using the ClusterProvider to get a cluster for the experiment.
type Runner struct {
	c                     cluster.Provider
	pxCtx                 *pixie.Context
	exporter              exporter.Exporter
	containerRegistryRepo string
	skaffoldStderrFile    string
	// KeepOnFailure, when true, skips teardown (stop vizier/workloads/recorders
	// and cluster cleanup) if the experiment errors, so the cluster state can
	// be inspected after the fact. Successful runs still tear down normally.
	keepOnFailure bool

	clusterCtx          *cluster.Context
	clusterCleanup      func()
	vizier              deploy.Workload
	metricsResultCh     chan *metrics.ResultRow
	metricsBySelector   map[string][]metrics.Recorder
	workloadsBySelector map[string][]deploy.Workload

	// wg is for goroutines that are unrelated to the main execution of the experiment.
	wg sync.WaitGroup
	// eg is for goroutines that should fail the experiment if they return an error.
	eg *errgroup.Group
}

// NewRunner creates a new Runner for the given contexts.
// skaffoldStderrFile, when non-empty, is the path to which skaffold's stderr is appended
// during deploy steps. Pass "" to keep skaffold's stderr going only to the perf_tool
// process's stderr.
func NewRunner(c cluster.Provider, pxCtx *pixie.Context, exp exporter.Exporter, containerRegistryRepo, skaffoldStderrFile string) *Runner {
	return &Runner{
		c:                     c,
		pxCtx:                 pxCtx,
		exporter:              exp,
		containerRegistryRepo: containerRegistryRepo,
		skaffoldStderrFile:    skaffoldStderrFile,
	}
}

// SetKeepOnFailure toggles whether teardown is skipped on experiment failure.
func (r *Runner) SetKeepOnFailure(v bool) {
	r.keepOnFailure = v
}

// RunExperiment runs an experiment according to the given ExperimentSpec.
func (r *Runner) RunExperiment(ctx context.Context, expID uuid.UUID, spec *experimentpb.ExperimentSpec) error {
	commitTopoOrder, err := getTopoOrder()
	if err != nil {
		return err
	}

	if err := r.getCluster(ctx, spec.ClusterSpec); err != nil {
		return err
	}

	var runErr error
	defer func() {
		if r.keepOnFailure && runErr != nil {
			log.WithError(runErr).Warn("Experiment failed; --keep_on_failure is set, leaving cluster state intact. " +
				"Inspect with kubectl; you are responsible for manual cleanup (e.g. `px delete`, delete workload namespaces).")
			return
		}
		r.clusterCleanup()
		r.clusterCtx.Close()
	}()

	if err := r.prepareWorkloads(ctx, spec); err != nil {
		runErr = err
		return err
	}

	r.metricsBySelector = make(map[string][]metrics.Recorder)
	r.metricsResultCh = make(chan *metrics.ResultRow)
	metricsChCloseOnce := sync.Once{}
	// Ensure the exporter goroutine drains and BQ flushes even on early
	// return / errgroup error — close the channel, then Wait on the WG.
	defer func() {
		metricsChCloseOnce.Do(func() { close(r.metricsResultCh) })
		r.wg.Wait()
	}()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := r.exporter.ExportResults(ctx, expID, r.metricsResultCh); err != nil {
			log.WithError(err).Error("Failed to export results")
		}
	}()

	var egCtx context.Context
	r.eg, egCtx = errgroup.WithContext(ctx)
	// errgroup.WithContext causes the egCtx to be cancelled when the first goroutine in the group errors.
	// We pass the group down. So, for example, if there's an error in one of the metric recorders' goroutines
	// it will cause context cancellation for the whole experiment,
	// allowing us to exit as soon as the error happens instead of waiting for the experiment to finish.
	r.eg.Go(func() error {
		return r.runActions(egCtx, spec)
	})

	if err := r.eg.Wait(); err != nil {
		runErr = err
		return err
	}

	// The experiment succeeded so we write the spec to the exporter.
	encodedSpec, err := (&jsonpb.Marshaler{}).MarshalToString(spec)
	if err != nil {
		runErr = err
		return err
	}
	if err := r.exporter.ExportSpec(ctx, expID, encodedSpec, commitTopoOrder); err != nil {
		runErr = err
		return err
	}

	// Flush metrics: deferred close+wait above handles this path too.
	return nil
}

func (r *Runner) runActions(ctx context.Context, spec *experimentpb.ExperimentSpec) (retErr error) {
	canceledErr := backoff.Permanent(context.Canceled)
	// Collect start-action cleanups explicitly so we can skip them when
	// --keep_on_failure is set and the experiment errors.
	var cleanups []func()
	defer func() {
		failed := retErr != nil || ctx.Err() != nil
		if r.keepOnFailure && failed {
			log.Warn("Skipping per-action teardown due to --keep_on_failure")
			return
		}
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}()
	for _, a := range spec.RunSpec.Actions {
		log.Tracef("started action %s", experimentpb.ActionType_name[int32(a.Type)])
		if canceled := r.sendActionTimestamp(ctx, a, "begin"); canceled {
			return canceledErr
		}
		switch a.Type {
		case experimentpb.START_VIZIER:
			cleanup, err := r.startVizier(ctx, spec)
			if err != nil {
				return err
			}
			cleanups = append(cleanups, cleanup)
		case experimentpb.START_WORKLOADS:
			cleanup, err := r.startWorkloads(ctx, spec, a.Name)
			if err != nil {
				return err
			}
			cleanups = append(cleanups, cleanup)
		case experimentpb.START_METRIC_RECORDERS:
			cleanup, err := r.startMetricRecorders(ctx, spec, a.Name)
			if err != nil {
				return err
			}
			cleanups = append(cleanups, cleanup)
		case experimentpb.STOP_VIZIER:
			if err := r.stopVizier(); err != nil {
				return err
			}
		case experimentpb.STOP_WORKLOADS:
			if err := r.stopWorkloads(a.Name); err != nil {
				return err
			}
		case experimentpb.STOP_METRIC_RECORDERS:
			if err := r.stopMetricRecorders(a.Name); err != nil {
				return err
			}
		case experimentpb.RUN, experimentpb.BURNIN:
			dur, err := types.DurationFromProto(a.Duration)
			if err != nil {
				return err
			}
			log.WithField("duration", dur).
				WithField("action_name", a.Name).
				Infof("Waiting for %s action", experimentpb.ActionType_name[int32(a.Type)])
			if canceled := sleep(ctx, dur); canceled {
				return canceledErr
			}
		}
		log.Tracef("finished action %s", experimentpb.ActionType_name[int32(a.Type)])
		if canceled := r.sendActionTimestamp(ctx, a, "end"); canceled {
			return canceledErr
		}
	}
	return nil
}

func (r *Runner) startVizier(ctx context.Context, spec *experimentpb.ExperimentSpec) (func(), error) {
	log.Info("Deploying Vizier")
	noCleanup := func() {}
	if err := r.vizier.Start(r.clusterCtx); err != nil {
		return noCleanup, fmt.Errorf("failed to deploy vizier: %w", err)
	}

	log.Info("Waiting for Vizier HealthCheck")
	if err := r.vizier.WaitForHealthCheck(ctx, r.clusterCtx, spec.ClusterSpec); err != nil {
		_ = r.stopVizier()
		return noCleanup, err
	}
	return func() { _ = r.vizier.Close() }, nil
}

func (r *Runner) startMetricRecorders(ctx context.Context, spec *experimentpb.ExperimentSpec, selector string) (func(), error) {
	log.WithField("selector", selector).Infof("Starting metric recorders")
	noCleanup := func() {}
	for _, ms := range spec.MetricSpecs {
		if ms.ActionSelector != selector {
			continue
		}

		recorder, err := metrics.NewMetricsRecorder(r.pxCtx, r.clusterCtx, ms, r.eg, r.metricsResultCh)
		if err != nil {
			_ = r.stopMetricRecorders(selector)
			return noCleanup, fmt.Errorf("failed to create metrics recorder: %w", err)
		}
		r.metricsBySelector[selector] = append(r.metricsBySelector[selector], recorder)
		if err := recorder.Start(ctx); err != nil {
			_ = r.stopMetricRecorders(selector)
			return noCleanup, fmt.Errorf("failed to start metrics recorder: %s", err)
		}
	}
	cleanup := func() { _ = r.stopMetricRecorders(selector) }
	return cleanup, nil
}

func (r *Runner) startWorkloads(ctx context.Context, spec *experimentpb.ExperimentSpec, selector string) (func(), error) {
	log.WithField("selector", selector).Info("Deploying workloads")
	noCleanup := func() {}
	for _, w := range r.workloadsBySelector[selector] {
		log.WithField("workload", w.Name()).Trace("deploying workload")
		if err := w.Start(r.clusterCtx); err != nil {
			_ = r.stopWorkloads(selector)
			return noCleanup, fmt.Errorf("failed to start workload deployment: %w", err)
		}
	}

	// Wait for workload healthchecks.
	eg := errgroup.Group{}
	for _, w := range r.workloadsBySelector[selector] {
		workload := w
		eg.Go(func() error {
			log.WithField("workload", workload.Name()).Trace("Waiting for workload healthcheck")
			if err := workload.WaitForHealthCheck(ctx, r.clusterCtx, spec.ClusterSpec); err != nil {
				return err
			}
			log.WithField("workload", workload.Name()).Trace("HealthCheck passed")
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		_ = r.stopWorkloads(selector)
		return noCleanup, err
	}
	cleanup := func() { _ = r.stopWorkloads(selector) }
	return cleanup, nil
}

func (r *Runner) stopVizier() error {
	log.Info("Stopping Vizier")
	return r.vizier.Close()
}

func (r *Runner) stopMetricRecorders(selector string) error {
	log.WithField("selector", selector).Info("Stopping metric recorders")
	mrs, ok := r.metricsBySelector[selector]
	if !ok {
		return nil
	}
	for _, mr := range mrs {
		mr.Close()
	}
	return nil
}

func (r *Runner) stopWorkloads(selector string) error {
	log.WithField("selector", selector).Info("Stopping workloads")
	ws, ok := r.workloadsBySelector[selector]
	if !ok {
		return nil
	}
	var errs []error
	for _, w := range ws {
		errs = append(errs, w.Close())
	}
	return errors.Join(errs...)
}

func (r *Runner) sendActionTimestamp(ctx context.Context, action *experimentpb.ActionSpec, prefix string) bool {
	actionName := strings.ToLower(experimentpb.ActionType_name[int32(action.Type)])
	name := fmt.Sprintf("%s_%s:%s", prefix, actionName, action.Name)
	row := &metrics.ResultRow{
		Timestamp: time.Now(),
		Name:      name,
		Value:     0.0,
	}
	select {
	case <-ctx.Done():
		return true
	case r.metricsResultCh <- row:
		return false
	}
}

func sleep(ctx context.Context, dur time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(dur):
		return false
	}
}

func (r *Runner) getCluster(ctx context.Context, spec *experimentpb.ClusterSpec) error {
	log.Info("Getting cluster")
	clusterCtx, cleanup, err := r.c.GetCluster(ctx, spec)
	if err != nil {
		return err
	}
	r.clusterCtx = clusterCtx
	r.clusterCleanup = cleanup
	return nil
}

func (r *Runner) prepareWorkloads(ctx context.Context, spec *experimentpb.ExperimentSpec) error {
	vizier, err := deploy.NewWorkload(r.pxCtx, r.containerRegistryRepo, r.skaffoldStderrFile, spec.VizierSpec)
	if err != nil {
		return err
	}
	r.vizier = vizier
	log.Trace("Preparing Vizier deployment")
	if err := r.vizier.Prepare(); err != nil {
		return err
	}
	r.workloadsBySelector = make(map[string][]deploy.Workload)
	for _, s := range spec.WorkloadSpecs {
		w, err := deploy.NewWorkload(r.pxCtx, r.containerRegistryRepo, r.skaffoldStderrFile, s)
		if err != nil {
			return err
		}
		log.Tracef("Preparing %s deployment", s.Name)
		if err := w.Prepare(); err != nil {
			return err
		}
		r.workloadsBySelector[s.ActionSelector] = append(r.workloadsBySelector[s.ActionSelector], w)
	}
	return nil
}

func getTopoOrder() (int, error) {
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.Trim(stdout.String(), " \n"))
}
