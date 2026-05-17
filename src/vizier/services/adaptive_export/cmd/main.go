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

// Adaptive-export operator (push flow, design rev 2).
//
// Lifecycle (one pod per node, deployed as a DaemonSet):
//
//  1. boot:
//     - load config (env + k8s downward API for NODE_NAME)
//     - ensure ClickHouse retention plugin is enabled (idempotent;
//     retention scripts themselves are user-defined in the Pixie UI)
//     - rehydrate the in-memory active set from
//     forensic_db.adaptive_attribution FINAL WHERE hostname=<node>
//     - start the trigger + controller
//
//  2. steady state:
//     - trigger polls forensic_db.kubescape_logs WHERE hostname=<node>
//     - controller derives anomaly hash from each event and writes a
//     forensic_db.adaptive_attribution row (one INSERT per event;
//     ReplacingMergeTree(t_end) collapses re-inserts to the latest
//     end_time, extending the active window)
//
//  3. shutdown:
//     - on SIGINT/SIGTERM, cancel context, drain.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/activeset"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/config"
	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/controller"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/pixie"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/pixieapi"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/pxl"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/script"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/streaming"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/trigger"
)

const (
	// envCHHTTPEndpoint overrides the ClickHouse HTTP endpoint used by
	// both the trigger (poll kubescape_logs) and the sink (write
	// adaptive_attribution). Defaults to http://<config.ClickHouse.Host>:8123.
	envCHHTTPEndpoint = "FORENSIC_CH_HTTP_ENDPOINT"

	// envNodeName is the k8s downward API var the DaemonSet sets via
	// `valueFrom: fieldRef: spec.nodeName`. Falls back to os.Hostname().
	envNodeName = "NODE_NAME"

	// envWindowBeforeSec / envWindowAfterSec / envTriggerPollMS /
	// envPruneIntervalSec are programmatic overrides per the spec.
	envWindowBeforeSec  = "ADAPTIVE_WINDOW_BEFORE_SEC"
	envWindowAfterSec   = "ADAPTIVE_WINDOW_AFTER_SEC"
	envTriggerPollMS    = "ADAPTIVE_TRIGGER_POLL_MS"
	envPruneIntervalSec = "ADAPTIVE_PRUNE_INTERVAL_SEC"

	// envTriggerHTTPTimeoutSec — per-poll HTTP budget (default 30s).
	// The pre-watermark 5s default timed out every catch-up SELECT.
	envTriggerHTTPTimeoutSec = "ADAPTIVE_TRIGGER_HTTP_TIMEOUT_SEC"

	// envTriggerPollLimit — max rows fetched per poll (default 10000).
	// Bounds catch-up work after a restart so an N-hour backlog
	// drains in ceil(N/PollLimit) polls instead of one giant scan.
	envTriggerPollLimit = "ADAPTIVE_TRIGGER_POLL_LIMIT"

	// envWatermarkSaveSec — minimum interval between persistent
	// watermark INSERTs (default 5s). The in-memory watermark
	// advances every successful poll; flush is throttled.
	envWatermarkSaveSec = "ADAPTIVE_WATERMARK_SAVE_SEC"

	// envSkipApply lets a deployment opt out of in-process DDL when
	// the schema has been pre-applied by a separate Job (recommended
	// production split: high-priv Job for CREATE TABLE / ALTER, then
	// the operator runs with INSERT-only creds and skips Apply).
	// VerifyPixieSchema still runs and refuses to start on drift.
	envSkipApply = "ADAPTIVE_SKIP_APPLY"

	// envInstallPresets makes the operator boot install Pixie's preset
	// retention scripts on this cluster. One-shot, idempotent (script-name
	// match → skip). Defaults to false because the production design has
	// users author scripts in the Pixie UI.
	envInstallPresets = "INSTALL_PRESET_SCRIPTS"

	// === Throughput-protection knobs for the pushPixieRows fan-out.
	// All default to 0 (= legacy unbounded behavior preserved).
	envMaxParallelQueriesPerHash = "ADAPTIVE_MAX_PARALLEL_QUERIES_PER_HASH"
	envMaxInflightQueriesGlobal  = "ADAPTIVE_MAX_INFLIGHT_QUERIES_GLOBAL"
	envEmptyResultSkipAfterN     = "ADAPTIVE_EMPTY_RESULT_SKIP_AFTER_N"
	envEmptyResultSkipTTLSec     = "ADAPTIVE_EMPTY_RESULT_SKIP_TTL_SEC"

	// envPushPixieTables — when true, the operator queries vizier
	// directly via pxapi on each fresh anomaly and writes the resulting
	// rows to forensic_db.<table> (rev-1 path). Required when the
	// cloud's retention plugin can't reach the in-cluster CH (e.g.
	// AOCC pixie cloud + CH ClusterIP service).
	envPushPixieTables = "ADAPTIVE_PUSH_PIXIE_ROWS"

	// envAdaptiveWriteMode selects the protocol-table write path:
	//   "pull"      → rev-2: per-hash×per-table fan-out (default)
	//   "streaming" → rev-3: N TableScanners with shared whitelist
	//                 (see .local/adaptive-write-rev3-plan.md)
	envAdaptiveWriteMode = "ADAPTIVE_WRITE_MODE"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info("starting adaptive-export operator (push flow, rev 2)")
	cfg, err := config.GetConfig()
	if err != nil {
		log.WithError(err).Fatal("failed to load configuration")
	}

	hostname, err := resolveHostname()
	if err != nil {
		log.WithError(err).Fatal("failed to resolve node identity — set NODE_NAME via k8s downward API (spec.nodeName)")
	}
	log.WithField("hostname", hostname).Info("operator pod is node-local")

	chEndpoint := chHTTPEndpoint(cfg.ClickHouse().Host(), os.Getenv(envCHHTTPEndpoint))
	log.WithField("endpoint", chEndpoint).Info("clickhouse HTTP endpoint resolved")

	// 1. Apply operator-owned DDL FIRST, before Pixie's retention plugin
	//    has a chance to auto-create pixie tables with its minimal
	//    column set (no namespace / pod). The kubescape tables
	//    (alerts, kubescape_logs) are owned by the soc installer and
	//    are NOT touched here.
	applier, err := clickhouse.NewApplier(chEndpoint, cfg.ClickHouse().User(), cfg.ClickHouse().Password())
	if err != nil {
		log.WithError(err).Fatal("failed to construct schema applier")
	}
	if strings.EqualFold(os.Getenv(envSkipApply), "true") {
		log.Info("ADAPTIVE_SKIP_APPLY=true — schema apply skipped; expecting an out-of-band DDL Job to have created the tables")
	} else {
		if err := applier.Apply(ctx); err != nil {
			log.WithError(err).Fatal("schema apply failed; refusing to proceed with possibly drifted tables")
		}
		log.WithField("tables", clickhouse.OperatorOwnedTables).Info("operator-owned DDL applied")
	}

	// 2. Defensive guard against Pixie's retention plugin having
	//    auto-created any pixie table BEFORE our Apply ran (e.g. a
	//    pre-existing cluster install). Refuse to start if drift
	//    detected so the misconfig is loud, not silent.
	if err := applier.VerifyPixieSchema(ctx); err != nil {
		log.WithError(err).Fatal("pixie table schema drift detected — pre-existing tables are missing operator-required columns; drop and re-create OR ALTER TABLE ADD COLUMN before retrying")
	}
	log.Info("pixie table schemas verified — namespace + pod columns present on all 12 tables")

	// 3. Ensure the Pixie ClickHouse retention plugin is enabled. The
	//    retention scripts themselves are defined by the user via the
	//    Pixie UI — we don't manage them.
	pluginClient, err := pixie.NewClient(ctx, cfg.Pixie().APIKey(), cfg.Pixie().Host())
	if err != nil {
		log.WithError(err).Fatal("failed to create pixie plugin client")
	}
	chDSN := cfg.ClickHouse().DSN()
	exportURL, err := pluginClient.EnsureClickHousePluginEnabled(chDSN)
	if err != nil {
		// non-fatal — the operator's own write path doesn't depend on
		// the plugin; analyst joins against pixie-table rows do, but a
		// missing plugin is a deployment misconfiguration the user
		// surfaces via UI.
		log.WithError(err).Warn("could not ensure ClickHouse plugin is enabled — pixie tables will not be populated until you turn it on in the Pixie UI")
	} else {
		log.WithField("export_url", exportURL).Info("clickhouse retention plugin is enabled")
	}

	// 3b. (optional) install Pixie's preset retention scripts so the
	//     pixie observation tables actually receive rows. Without this,
	//     the plugin is enabled but does nothing.
	if strings.EqualFold(os.Getenv(envInstallPresets), "true") {
		installed, err := installPresetScripts(pluginClient, cfg.Pixie().ClusterID(), cfg.Worker().ClusterName())
		if err != nil {
			log.WithError(err).Warn("INSTALL_PRESET_SCRIPTS=true but install failed — pixie tables will stay empty")
		} else {
			log.WithField("installed", installed).Info("preset retention scripts installed on cluster")
		}
	}

	// 4. Build trigger + sink + controller.
	pollInterval := durEnv(envTriggerPollMS, 250*time.Millisecond, time.Millisecond)
	httpTimeout := durEnv(envTriggerHTTPTimeoutSec, 30*time.Second, time.Second)
	saveInterval := durEnv(envWatermarkSaveSec, 5*time.Second, time.Second)
	pollLimit := intEnv(envTriggerPollLimit, 10000)
	// Persistent watermark store keeps the trigger's kubescape_logs
	// cursor in forensic_db.trigger_watermark, so a restart on a busy
	// node doesn't replay the full table from event_time=0 (which
	// timed out every single HTTP read and pinned the watermark at 0
	// forever — the failure mode that produced "AE silent for 10h
	// after OOM-restart" in the field).
	wmStore, err := trigger.NewClickHouseWatermarkStore(
		chEndpoint, cfg.ClickHouse().Database(),
		cfg.ClickHouse().User(), cfg.ClickHouse().Password(),
		httpTimeout)
	if err != nil {
		log.WithError(err).Fatal("failed to create persistent watermark store")
	}
	trg, err := trigger.New(trigger.Config{
		Endpoint:              chEndpoint,
		Database:              cfg.ClickHouse().Database(),
		Table:                 cfg.ClickHouse().Table(),
		Username:              cfg.ClickHouse().User(),
		Password:              cfg.ClickHouse().Password(),
		Hostname:              hostname,
		PollInterval:          pollInterval,
		Watermark:             wmStore,
		WatermarkSaveInterval: saveInterval,
		PollLimit:             pollLimit,
		HTTPTimeout:           httpTimeout,
	})
	if err != nil {
		log.WithError(err).Fatal("failed to create trigger")
	}

	snk, err := sink.New(sink.Config{
		Endpoint: chEndpoint,
		Database: cfg.ClickHouse().Database(),
		Username: cfg.ClickHouse().User(),
		Password: cfg.ClickHouse().Password(),
	})
	if err != nil {
		log.WithError(err).Fatal("failed to create sink")
	}

	// Mode selection:
	//   "streaming" → rev-3: leave PushPixieTables EMPTY (so the
	//                 controller skips fan-out) and stand up the
	//                 streaming.Supervisor instead.
	//   else        → rev-2: per-hash×per-table fan-out (legacy).
	streamingMode := strings.EqualFold(os.Getenv(envAdaptiveWriteMode), "streaming")
	pushPixieRequested := strings.EqualFold(os.Getenv(envPushPixieTables), "true")
	if streamingMode && pushPixieRequested {
		log.Info("ADAPTIVE_WRITE_MODE=streaming overrides ADAPTIVE_PUSH_PIXIE_ROWS — fan-out disabled, streaming.Supervisor will own protocol-table writes")
	}

	// Shared ActiveSet (used only by streaming mode; harmless in pull mode).
	activeSet := activeset.New()
	// AttributionNotifier — non-blocking shim so the controller's
	// synchronous OnAttribution / OnPrune callbacks don't pin
	// controller.handle on slow ActiveSet writes. Tests in
	// streaming/notifier_test.go cover the buffer-overflow + drop
	// semantics. The Run goroutine is started below in streaming mode.
	attrNotifier := streaming.NewAttributionNotifier(activeSet, streaming.NotifierConfig{
		BufferSize: intEnvOrZero("ADAPTIVE_STREAM_NOTIFIER_BUFFER"),
	})

	ctlCfg := controller.Config{
		Hostname:                  hostname,
		Before:                    durEnv(envWindowBeforeSec, 5*time.Minute, time.Second),
		After:                     durEnv(envWindowAfterSec, 5*time.Minute, time.Second),
		MaxParallelQueriesPerHash: intEnvOrZero(envMaxParallelQueriesPerHash),
		MaxInflightQueriesGlobal:  intEnvOrZero(envMaxInflightQueriesGlobal),
		EmptyResultSkipAfterN:     intEnvOrZero(envEmptyResultSkipAfterN),
		EmptyResultSkipTTL:        durEnvOrZero(envEmptyResultSkipTTLSec, time.Second),
	}
	if streamingMode {
		// Route through the non-blocking notifier — handle() returns
		// in <1µs even if ActiveSet writers are slow. Host-pid pods
		// (empty Pod) are filtered inside the notifier.
		ctlCfg.OnAttribution = attrNotifier.SubmitFromController
		ctlCfg.OnPrune = attrNotifier.RemoveFromController
	}
	if !streamingMode && pushPixieRequested {
		// PxL's px.DataFrame(table=…) rejects dotted table names even
		// though px.GetSchemas() lists them. Drop them from the push
		// list; the cloud-side retention plugin would have to handle
		// those if the user wants them.
		var tables []string
		for _, t := range pxl.Names(pxl.BuiltinTables) {
			if strings.Contains(t, ".") {
				log.WithField("table", t).Info("skipping dotted-name table from push list — PxL DataFrame rejects it")
				continue
			}
			tables = append(tables, t)
		}
		ctlCfg.PushPixieTables = tables
		log.WithField("tables", ctlCfg.PushPixieTables).
			Info("ADAPTIVE_PUSH_PIXIE_ROWS=true — operator will query pixie + write rows directly on each anomaly")
	}
	ctl := controller.New(trg, snk, ctlCfg, nil)

	// Build the pixie adapter ONCE — shared by both rev-2's
	// pushPixieRows path and the rev-3 streaming.Supervisor.
	var pixieAdapterInst *pixieapi.Adapter
	if len(ctlCfg.PushPixieTables) > 0 || streamingMode {
		var adapter *pixieapi.Adapter
		if direct := os.Getenv("ADAPTIVE_VIZIER_DIRECT_ADDR"); direct != "" {
			// Direct mode — bypass the cloud's passthrough proxy and
			// connect to the in-cluster vizier-query-broker. Use this
			// on self-hosted clouds where pxapi.WithAPIKey isn't
			// authorized for the cluster (e.g. a freshly-deployed
			// vizier whose ID isn't yet linked to the API key's owner).
			a, err := pixieapi.NewDirectFromEnv(cfg.Pixie().ClusterID())
			if err != nil {
				log.WithError(err).Fatal("ADAPTIVE_VIZIER_DIRECT_ADDR set but direct-mode adapter init failed")
			}
			log.WithField("addr", direct).Info("pixieapi: direct mode (bypassing cloud proxy)")
			adapter = a
		} else {
			pxClient, err := pxapi.NewClient(ctx,
				pxapi.WithAPIKey(cfg.Pixie().APIKey()),
				pxapi.WithCloudAddr(cfg.Pixie().Host()))
			if err != nil {
				log.WithError(err).Fatal("failed to create pxapi client")
			}
			adapter = pixieapi.New(pxClient, cfg.Pixie().ClusterID())
		}
		pixieAdapterInst = adapter
		if len(ctlCfg.PushPixieTables) > 0 {
			ctl = ctl.WithPixieQuerier(&pixieAdapter{a: adapter})
		}
	}

	// 5. Rehydrate active state across crashes.
	if err := ctl.Rehydrate(ctx); err != nil {
		log.WithError(err).Warn("could not rehydrate active set; starting cold")
	} else {
		log.WithField("active", ctl.Active()).Info("active set rehydrated")
	}

	// 6. Periodic prune of in-memory expired entries + main controller loop.
	//    Both goroutines are tracked in a WaitGroup so SIGTERM cleanly waits
	//    for in-flight HTTP calls (trigger 5s timeout, sink 30s timeout)
	//    instead of being cut off by an arbitrary 500ms sleep.
	pruneInterval := durEnv(envPruneIntervalSec, 30*time.Second, time.Second)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(pruneInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if removed := ctl.PruneExpired(); removed > 0 {
					log.WithField("removed", removed).Debug("pruned expired active entries")
				}
			}
		}
	}()

	// 7. Run the controller.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ctl.Run(ctx); err != nil && err != context.Canceled {
			log.WithError(err).Error("controller exited with error")
		}
	}()

	// 7b. Streaming mode (rev-3): start the per-table scanners +
	//     batched writers. Replaces the per-hash×per-table fan-out.
	if streamingMode {
		// Start the AttributionNotifier consumer so SubmitFromController
		// calls actually get delivered to ActiveSet.
		wg.Add(1)
		go func() {
			defer wg.Done()
			attrNotifier.Run(ctx)
		}()

		// Seed the ActiveSet from the rehydrated controller so existing
		// alive attribution rows resume streaming immediately on boot.
		// Without this seeding, only fresh kubescape events would
		// repopulate the set — losing N minutes of coverage per restart.
		seedActiveSetFromRehydrate(ctl, activeSet)

		streamTables := make([]string, 0, len(pxl.BuiltinTables))
		for _, t := range pxl.Names(pxl.BuiltinTables) {
			if strings.Contains(t, ".") {
				continue // PxL DataFrame rejects dotted names
			}
			streamTables = append(streamTables, t)
		}
		updater := streaming.NewUpdater(activeSet, streaming.UpdaterConfig{
			Debounce:         durEnvOrZero("ADAPTIVE_STREAM_DEBOUNCE_SEC", time.Second),
			MaxWhitelistSize: intEnvOrZero("ADAPTIVE_STREAM_MAX_WHITELIST"),
		})
		supervisor := streaming.NewSupervisor(
			updater,
			&pixieAdapter{a: pixieAdapterInst},
			snk,
			streamTables,
			streaming.ScannerConfig{
				QueryWindow:     durEnvOrZero("ADAPTIVE_STREAM_WINDOW_SEC", time.Second),
				RefreshInterval: durEnvOrZero("ADAPTIVE_STREAM_REFRESH_SEC", time.Second),
			},
			streaming.WriterConfig{
				BatchRows:  intEnvOrZero("ADAPTIVE_STREAM_BATCH_ROWS"),
				BatchEvery: durEnvOrZero("ADAPTIVE_STREAM_BATCH_EVERY_SEC", time.Second),
			},
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			supervisor.Run(ctx)
		}()
		log.WithField("tables", streamTables).Info("rev-3 streaming supervisor started")
	}

	log.WithFields(log.Fields{
		"hostname":       hostname,
		"poll_interval":  pollInterval,
		"prune_interval": pruneInterval,
		"window_before":  ctlCfg.Before,
		"window_after":   ctlCfg.After,
	}).Info("operator running")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Info("shutdown signal received; waiting for goroutines to drain")
	cancel()
	// Bound the wait so a hung HTTP call can't keep the process up forever.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		log.Info("clean shutdown")
	case <-time.After(35 * time.Second):
		log.Warn("shutdown deadline reached with goroutines still running; exiting")
	}
}

// chHTTPEndpoint resolves the ClickHouse HTTP endpoint. Explicit env
// override wins; otherwise build "http://<host>:8123" from config.
func chHTTPEndpoint(host, override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	if host == "" {
		host = "localhost"
	}
	return "http://" + host + ":8123"
}

// resolveHostname picks the node identity for node-local scoping.
// REQUIRES NODE_NAME (set via k8s downward API spec.nodeName). The
// previous os.Hostname() fallback returned the POD hostname, not the
// node — making the operator silently miss its node's rows.
func resolveHostname() (string, error) {
	if v := strings.TrimSpace(os.Getenv(envNodeName)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s env var is required (set via k8s downward API: valueFrom.fieldRef.fieldPath=spec.nodeName)", envNodeName)
}

// durEnv reads a positive-integer-valued duration env var. unit
// defines the unit (time.Second, time.Millisecond). Returns dflt on
// missing / unparseable / non-positive values — non-positive would
// either panic time.NewTicker or invert the attribution window, so
// we fall back to the default and log loudly.
func durEnv(key string, dflt, unit time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return dflt
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"key": key, "value": v}).
			Warn("invalid duration env; using default")
		return dflt
	}
	if n <= 0 {
		log.WithFields(log.Fields{"key": key, "value": v}).
			Warn("non-positive duration env; using default")
		return dflt
	}
	return time.Duration(n) * unit
}

// intEnv reads a positive-integer-valued env var. Returns dflt on
// missing / unparseable / non-positive. Same shape as durEnv but
// without the unit multiplier — for counts (e.g. row limits).
func intEnv(key string, dflt int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"key": key, "value": v}).
			Warn("invalid int env; using default")
		return dflt
	}
	if n <= 0 {
		log.WithFields(log.Fields{"key": key, "value": v}).
			Warn("non-positive int env; using default")
		return dflt
	}
	return n
}

// intEnvOrZero is like intEnv but treats unset / empty / non-positive
// as 0 (= "feature disabled"). Used for opt-in throttle knobs where 0
// preserves legacy behavior and a positive integer enables the throttle.
func intEnvOrZero(key string) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		log.WithFields(log.Fields{"key": key, "value": v}).
			Warn("invalid int env; treating as 0 (disabled)")
		return 0
	}
	return n
}

// durEnvOrZero is the duration-typed counterpart. unit lets the caller
// express the env value in seconds / milliseconds without per-knob
// parsing logic. 0 → returned as 0 (= feature disabled).
func durEnvOrZero(key string, unit time.Duration) time.Duration {
	n := intEnvOrZero(key)
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * unit
}

// seedActiveSetFromRehydrate reads the operator's rehydrated
// attribution rows back from CH and Upserts them into the streaming
// ActiveSet. Without this, a restart in streaming mode leaves the
// scanners with an empty whitelist until the next kubescape event
// arrives — N minutes of coverage gap per restart.
func seedActiveSetFromRehydrate(ctl *controller.Controller, set *activeset.ActiveSet) {
	// The controller's Rehydrate already populated its in-memory
	// active map from CH. We re-issue QueryActive here to mirror
	// those rows into the ActiveSet — keeping the streaming layer
	// fully decoupled from controller internals.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := ctl.SnapshotActive(ctx)
	if err != nil {
		log.WithError(err).Warn("seed: SnapshotActive failed; streaming starts cold")
		return
	}
	for _, r := range rows {
		if r.Pod == "" {
			continue
		}
		set.Upsert(activeset.Key{Namespace: r.Namespace, Pod: r.Pod}, r.TEnd)
	}
	log.WithField("seeded", set.Size()).Info("streaming.ActiveSet seeded from rehydrated rows")
}

// pixieAdapter wraps pixieapi.Adapter so its return type matches the
// controller's PixieQuerier interface (which uses []map[string]any
// rather than the pixieapi-internal Row alias).
type pixieAdapter struct{ a *pixieapi.Adapter }

func (p *pixieAdapter) Query(ctx context.Context, src string) ([]map[string]any, error) {
	rows, err := p.a.Query(ctx, src)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = map[string]any(r)
	}
	return out, nil
}

// installPresetScripts purges any stale ClickHouse-plugin retention
// scripts on the cluster, then installs the operator's built-in PxL
// scripts targeting the 12 socket_tracer tables we DDL'd. Cloud-side
// "presets" are deliberately ignored: in this fork they target legacy
// tables (conn_stats, stack_traces, dc_snoop) that aren't in the
// rev-2 schema, so installing them would just silently fail to write.
func installPresetScripts(client *pixie.Client, clusterID, clusterName string) (int, error) {
	current, err := client.GetClusterScripts(clusterID, clusterName)
	if err != nil {
		return 0, fmt.Errorf("get cluster scripts: %w", err)
	}
	currentNames := make([]string, 0, len(current))
	for _, s := range current {
		currentNames = append(currentNames, s.Name)
	}
	log.WithFields(log.Fields{
		"already_on_cluster":   len(current),
		"cluster_script_names": currentNames,
	}).Info("preset script install — purging managed + installing built-ins")

	// Purge ONLY scripts we recognise as operator-managed or as legacy
	// presets we know are broken in the rev-2 schema. User-authored
	// retention scripts are left alone.
	for _, s := range current {
		if !isOperatorManagedScript(s.Name) {
			log.WithField("script", s.Name).
				Debug("preset install — leaving user-authored script alone")
			continue
		}
		if err := client.DeleteDataRetentionScript(s.ScriptId); err != nil {
			log.WithError(err).WithField("script", s.Name).Warn("failed to delete stale script")
			continue
		}
		log.WithField("script", s.Name).Info("purged stale retention script")
	}

	// Install built-ins.
	presets := builtinPresetScripts()
	installed := 0
	for _, p := range presets {
		if err := client.AddDataRetentionScript(clusterID, p.Name, p.Description, p.FrequencyS, p.Script); err != nil {
			log.WithError(err).WithField("script", p.Name).Warn("failed to install built-in script")
			continue
		}
		installed++
		log.WithField("script", p.Name).Info("installed retention script")
	}
	return installed, nil
}

// isOperatorManagedScript decides whether a cluster-side retention
// script is safe to delete during INSTALL_PRESET_SCRIPTS. The criteria:
//
//  1. Anything with the "ch-" prefix matches the operator's own
//     builtinPresetScripts naming (ch-<table>) — managed.
//  2. The legacy AOCC presets we explicitly want to retire because
//     their target tables don't exist in the rev-2 schema:
//     "conn_stats export", "dc snoop export", "stack_traces export".
//
// Any other script is assumed user-authored and left alone.
func isOperatorManagedScript(name string) bool {
	if strings.HasPrefix(name, "ch-") {
		return true
	}
	switch name {
	case "conn_stats export", "dc snoop export", "stack_traces export":
		return true
	}
	return false
}

// builtinPresetScripts returns a minimum set of PxL scripts mirroring
// the canonical Pixie preset shape — one bulk-write script per
// socket_tracer table. Each adds namespace + pod columns and emits to
// the matching CH table via px.display(name='<table>') which the
// retention plugin maps to forensic_db.<table>.
//
// Schedule: 10s. Window: -15s (overlap so we don't lose rows during
// schedule jitter).
func builtinPresetScripts() []*script.ScriptDefinition {
	// Drop dotted-name tables (http2_messages.beta, kafka_events.beta):
	// `px.DataFrame(table='…')` rejects them at PxL compile time, so a
	// preset for them would be permanently broken. The cloud-side
	// retention plugin would have to handle those if needed.
	tables := []string{
		"http_events", "dns_events", "redis_events", "mysql_events",
		"pgsql_events", "cql_events", "mongodb_events", "amqp_events",
		"mux_events", "tls_events",
	}
	out := make([]*script.ScriptDefinition, 0, len(tables))
	for _, t := range tables {
		body := "import px\n" +
			"df = px.DataFrame(table='" + t + "', start_time='-15s')\n" +
			"df.namespace = px.upid_to_namespace(df.upid)\n" +
			"df.pod = px.upid_to_pod_name(df.upid)\n" +
			"px.display(df, '" + t + "')\n"
		out = append(out, &script.ScriptDefinition{
			Name:        "ch-" + t,
			Description: "adaptive_export builtin preset for " + t,
			FrequencyS:  10,
			Script:      body,
			IsPreset:    false,
		})
	}
	return out
}
