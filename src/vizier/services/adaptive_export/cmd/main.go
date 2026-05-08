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

	"px.dev/pixie/src/vizier/services/adaptive_export/internal/clickhouse"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/config"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/controller"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/pixie"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/script"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/sink"
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

	// 2. Build trigger + sink + controller.
	pollInterval := durEnv(envTriggerPollMS, 250*time.Millisecond, time.Millisecond)
	trg, err := trigger.New(trigger.Config{
		Endpoint:     chEndpoint,
		Database:     cfg.ClickHouse().Database(),
		Table:        cfg.ClickHouse().Table(),
		Username:     cfg.ClickHouse().User(),
		Password:     cfg.ClickHouse().Password(),
		Hostname:     hostname,
		PollInterval: pollInterval,
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

	ctlCfg := controller.Config{
		Hostname: hostname,
		Before:   durEnv(envWindowBeforeSec, 5*time.Minute, time.Second),
		After:    durEnv(envWindowAfterSec, 5*time.Minute, time.Second),
	}
	ctl := controller.New(trg, snk, ctlCfg, nil)

	// 3. Rehydrate active state across crashes.
	if err := ctl.Rehydrate(ctx); err != nil {
		log.WithError(err).Warn("could not rehydrate active set; starting cold")
	} else {
		log.WithField("active", ctl.Active()).Info("active set rehydrated")
	}

	// 4. Periodic prune of in-memory expired entries + main controller loop.
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

	// 5. Run the controller.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ctl.Run(ctx); err != nil && err != context.Canceled {
			log.WithError(err).Error("controller exited with error")
		}
	}()

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

// installPresetScripts fetches Pixie's preset retention scripts and
// installs the ones that aren't already on this cluster. If the cloud
// has no presets registered for the ClickHouse plugin (common on
// self-hosted clouds like AOCC), falls back to a built-in minimum
// set covering the 12 socket_tracer tables.
func installPresetScripts(client *pixie.Client, clusterID, clusterName string) (int, error) {
	presets, err := client.GetPresetScripts()
	if err != nil {
		return 0, fmt.Errorf("get preset scripts: %w", err)
	}
	current, err := client.GetClusterScripts(clusterID, clusterName)
	if err != nil {
		return 0, fmt.Errorf("get cluster scripts: %w", err)
	}
	have := map[string]bool{}
	for _, s := range current {
		have[s.Name] = true
	}
	currentNames := make([]string, 0, len(current))
	for _, s := range current {
		currentNames = append(currentNames, s.Name)
	}
	presetNames := make([]string, 0, len(presets))
	for _, p := range presets {
		presetNames = append(presetNames, p.Name)
	}
	log.WithFields(log.Fields{
		"presets_from_cloud":   len(presets),
		"already_on_cluster":   len(current),
		"cluster_script_names": currentNames,
		"preset_script_names":  presetNames,
	}).Info("preset script install — sources")
	if len(presets) == 0 {
		log.Warn("no preset retention scripts available on this Pixie cloud — falling back to built-in minimum set")
		presets = builtinPresetScripts()
		log.WithField("builtin_count", len(presets)).Info("using built-in preset fallback")
	}
	installed := 0
	for _, p := range presets {
		if have[p.Name] {
			continue
		}
		if err := client.AddDataRetentionScript(clusterID, p.Name, p.Description, p.FrequencyS, p.Script); err != nil {
			log.WithError(err).WithField("script", p.Name).Warn("failed to install preset script")
			continue
		}
		installed++
		log.WithField("script", p.Name).Info("installed retention script")
	}
	return installed, nil
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
	tables := []string{
		"http_events", "dns_events", "redis_events", "mysql_events",
		"pgsql_events", "cql_events", "mongodb_events", "amqp_events",
		"mux_events", "tls_events",
		// http2_messages.beta and kafka_events.beta have dotted names
		// that need backtick-quoting in CH but Pixie's px.display uses
		// the dotted form unchanged. Including with their PxL name.
		"http2_messages.beta", "kafka_events.beta",
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

var _ = fmt.Sprintf
