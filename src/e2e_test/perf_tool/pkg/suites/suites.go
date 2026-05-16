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

package suites

import (
	"fmt"
	"time"

	pb "px.dev/pixie/src/e2e_test/perf_tool/experimentpb"
)

// ExperimentSuite is a group of experiments, represented as a function that returns multiple named experiment specs.
type ExperimentSuite func() map[string]*pb.ExperimentSpec

// ExperimentSuiteRegistry contains all the ExperimentSuite, keyed by name.
var ExperimentSuiteRegistry = map[string]ExperimentSuite{
	"nightly":         nightlyExperimentSuite,
	"http-grid":       httpGridSuite,
	"k8ssandra":       k8ssandraExperimentSuite,
	"clickhouse-exec": clickhouseExecSuite,
	"sovereign-soc":   sovereignSOCSuite,
}

func nightlyExperimentSuite() map[string]*pb.ExperimentSpec {
	defaultMetricPeriod := 30 * time.Second
	preDur := 5 * time.Minute
	dur := 5 * time.Minute
	httpNumConns := 100
	exps := map[string]*pb.ExperimentSpec{
		"http-loadtest/100/100":               HTTPLoadTestExperiment(httpNumConns, 100, defaultMetricPeriod, preDur, dur),
		"http-loadtest/100/3000":              HTTPLoadTestExperiment(httpNumConns, 3000, defaultMetricPeriod, preDur, dur),
		"sock-shop":                           SockShopExperiment(defaultMetricPeriod, preDur, dur),
		"online-boutique":                     OnlineBoutiqueExperiment(defaultMetricPeriod, preDur, dur),
		"kafka":                               KafkaExperiment(defaultMetricPeriod, preDur, dur),
		"app-overhead/http-loadtest/100/3000": HTTPLoadApplicationOverheadExperiment(httpNumConns, 3000, defaultMetricPeriod),
	}
	for _, e := range exps {
		addTags(e, "suite/nightly")
	}
	return exps
}

// Added separate experiment suite for k8ssandra because the perf tool does not currently install the cert-manager
// automatically, which is required for px-k8ssandra.
// To run this experiment, we have to spin up a cluster, install the cert-manager, and
// run the perf tool with --use-local-cluster.
// Tags are added to properly display results in the perf dashboard.
// TODO(@benkilimnik): move to nightly once cert-manager is installed automatically or perf tool workflow changes.
func k8ssandraExperimentSuite() map[string]*pb.ExperimentSpec {
	defaultMetricPeriod := 30 * time.Second
	preDur := 5 * time.Minute
	dur := 40 * time.Minute
	exps := map[string]*pb.ExperimentSpec{
		"px-k8ssandra": K8ssandraExperiment(defaultMetricPeriod, preDur, dur),
	}
	for _, e := range exps {
		addTags(e, "suite/k8ssandra")
	}
	return exps
}

// clickhouseExecSuite covers the two sides of Pixie's ClickHouse integration
// under load: the write/export path and the read/query path. Both experiments
// share the same metric shape (process/heap/clickhouse-operator) so results
// can be compared directly.
//
// The ClickHouse operator metrics are scraped via the prometheus recorder
// named "clickhouse-operator" -- point the CLI at the correct cluster with:
//
//	--prom_recorder_override clickhouse-operator=/path/to/kubeconfig:my-ctx
func clickhouseExecSuite() map[string]*pb.ExperimentSpec {
	defaultMetricPeriod := 30 * time.Second
	preDur := 5 * time.Minute
	// preDur := 2 * time.Minute
	dur := 20 * time.Minute
	// dur := 5 * time.Minute
	httpNumConns := 100
	httpTargetRPS := 3000

	// Tight cadence on the export/read scripts to apply real pressure.
	exportPeriod := 5 * time.Second
	exportWindow := 30 * time.Second
	readPeriod := 5 * time.Second
	readWindow := 5 * time.Minute

	clickhouseDSN := "pixie:pixie_password@clickhouse.forensic.austrianopencloudcommunity.org:9000/default"
	clickhouseTable := "http_events"

	exps := map[string]*pb.ExperimentSpec{
		"clickhouse-export": ClickHouseExportExperiment(
			httpNumConns, httpTargetRPS,
			defaultMetricPeriod,
			exportPeriod, exportWindow,
			clickhouseDSN, clickhouseTable,
			preDur, dur,
		),
		"clickhouse-read": ClickHouseReadExperiment(
			httpNumConns, httpTargetRPS,
			defaultMetricPeriod,
			readPeriod, readWindow,
			clickhouseDSN, clickhouseTable,
			preDur, dur,
		),
	}
	for _, e := range exps {
		addTags(e, "suite/clickhouse-exec")
	}
	return exps
}

func httpGridSuite() map[string]*pb.ExperimentSpec {
	defaultMetricPeriod := 30 * time.Second
	preDur := 5 * time.Minute
	dur := 40 * time.Minute

	conns := []int{
		10,
		100,
		250,
		500,
	}
	rps := []int{
		100,
		1000,
		2500,
		5000,
	}
	type param struct {
		numConns  int
		targetRPS int
	}
	combos := make([]*param, 0, len(conns)*len(rps))
	for _, numConns := range conns {
		for _, targetRPS := range rps {
			combos = append(combos, &param{
				numConns:  numConns,
				targetRPS: targetRPS,
			})
		}
	}

	exps := make(map[string]*pb.ExperimentSpec)
	for _, p := range combos {
		name := fmt.Sprintf("http-loadtest/%d/%d", p.numConns, p.targetRPS)
		exps[name] = HTTPLoadTestExperiment(p.numConns, p.targetRPS, defaultMetricPeriod, preDur, dur)
	}

	for _, e := range exps {
		addTags(e, "suite/http-grid")
	}
	return exps
}

// sovereignSOCSuite drives the Sovereign SOC demo workflow (vulnerable
// Redis 7.2.10 + bobctl attack loop + Kubescape anomaly generation +
// forensic ClickHouse export) under perf_tool orchestration. Assumes the
// target cluster already has Kubescape (honey namespace, app=node-agent
// DaemonSet), an Altinity ClickHouse operator in the `clickhouse` namespace,
// and Vector tailing kubescape logs into forensic_db.alerts — same
// pre-installed-dependency shape as the k8ssandra suite. Point prometheus
// recorders at the forensic cluster via
//
//	--prom_recorder_override clickhouse-operator=:<forensic-ctx>
//	--prom_recorder_override kubescape-node-agent=:<forensic-ctx>
func sovereignSOCSuite() map[string]*pb.ExperimentSpec {
	defaultMetricPeriod := 30 * time.Second
	preDur := 2 * time.Minute
	dur := 20 * time.Minute

	exportPeriod := 5 * time.Second
	exportWindow := 30 * time.Second
	alertCountWindow := 1 * time.Minute

	// Both DSNs target the same external forensic endpoint with the same
	// pixie user (which has been granted SHOW/SELECT/INSERT on forensic_db.*
	// out-of-band). The endpoint MUST be reachable from the experiment
	// cluster's network — the clickhouse-cpp client will crash Kelvin with
	// SIGSEGV if DNS fails (see ClickHouseExportSinkNode TODO).
	//   - exportDSN:  /default       — where Pixie's CH export sink writes.
	//   - alertsDSN:  /forensic_db   — where Vector lands Kubescape alerts.
	// forensic_db must be pre-created via soc/tree/clickhouse-lab/schema.sql;
	// this suite does not bootstrap CH schemas (CH is shared infra).
	const clickhouseHost = "clickhouse.forensic.austrianopencloudcommunity.org:9000"
	const clickhouseCreds = "pixie:pixie_password"
	exportDSN := fmt.Sprintf("%s@%s/default", clickhouseCreds, clickhouseHost)
	alertsDSN := fmt.Sprintf("%s@%s/forensic_db", clickhouseCreds, clickhouseHost)
	exportTable := "redis_events"
	// Vector writes raw kubescape alerts to forensic_db.kubescape_logs (see
	// helm-rendered/vector-values.yaml kubescape_clickhouse sink). A
	// separate forensic_db.alerts materialized view / projection exists in
	// some demo variants but is not populated by the stock Vector config.
	alertsTable := "kubescape_logs"

	exps := map[string]*pb.ExperimentSpec{
		"redis-attack": SovereignSOCRedisAttackExperiment(
			defaultMetricPeriod,
			exportPeriod, exportWindow,
			exportDSN, exportTable,
			alertsDSN, alertsTable,
			alertCountWindow,
			preDur, dur,
		),
	}
	for _, e := range exps {
		addTags(e, "suite/sovereign-soc")
	}
	return exps
}
