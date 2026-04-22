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
	// Embed import is required to use go:embed directive.
	_ "embed"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/gogo/protobuf/types"
	log "github.com/sirupsen/logrus"

	pb "px.dev/pixie/src/e2e_test/perf_tool/experimentpb"
)

// Paths are resolved relative to the pixie workspace root; run.go chdirs
// there at startup via BUILD_WORKSPACE_DIRECTORY / `git rev-parse
// --show-toplevel`, so the perf_tool binary always sees these files
// regardless of where the user invoked bazel run from.
const (
	sovereignSOCYAMLRoot = "src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc"
)

//go:embed scripts/healthcheck/redis_data_in_namespace.pxl
var redisDataInNamespaceScript string

// RedisVulnerableWorkload deploys the pre-populated Kubescape
// ApplicationProfile and the intentionally vulnerable Redis 7.2.10 used by
// the sovereign-soc suite. Both YAMLs land in the `redis` namespace.
// Assumes the target cluster has Kubescape (honey/node-agent) preinstalled
// — the k8ssandra suite has the same "external prerequisite" shape.
func RedisVulnerableWorkload() *pb.WorkloadSpec {
	return &pb.WorkloadSpec{
		Name: "redis-vulnerable",
		DeploySteps: []*pb.DeployStep{
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						YAMLPaths: []string{
							fmt.Sprintf("%s/redis-sbob.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/redis-vulnerable.yaml", sovereignSOCYAMLRoot),
						},
					},
				},
			},
		},
		Healthchecks: redisHealthChecks("redis"),
	}
}

// BobctlAttackWorkload deploys a Kubernetes Job that runs `bobctl attack`
// against the vulnerable redis deployment in a tight loop for the
// experiment's duration. The Job's init container downloads the bobctl
// binary from the upstream release; the attack suite is mounted from the
// bob-suite-attack ConfigMap.
func BobctlAttackWorkload() *pb.WorkloadSpec {
	return &pb.WorkloadSpec{
		Name: "bobctl-attack",
		DeploySteps: []*pb.DeployStep{
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						YAMLPaths: []string{
							fmt.Sprintf("%s/bob-suite-attack-cm.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/bobctl-attack-job.yaml", sovereignSOCYAMLRoot),
						},
					},
				},
			},
		},
		Healthchecks: []*pb.HealthCheck{
			{
				CheckType: &pb.HealthCheck_K8S{
					K8S: &pb.K8SPodsReadyCheck{
						Namespace: "redis",
					},
				},
			},
		},
	}
}

// redisHealthChecks mirrors HTTPHealthChecks but asserts on Pixie's
// redis_events table instead of http_events.
func redisHealthChecks(namespace string) []*pb.HealthCheck {
	checks := []*pb.HealthCheck{
		{
			CheckType: &pb.HealthCheck_K8S{
				K8S: &pb.K8SPodsReadyCheck{
					Namespace: namespace,
				},
			},
		},
	}
	t, err := template.New("").Parse(redisDataInNamespaceScript)
	if err != nil {
		log.WithError(err).Fatal("failed to parse Redis healthcheck script")
	}
	buf := &strings.Builder{}
	err = t.Execute(buf, &struct {
		Namespace string
	}{
		Namespace: namespace,
	})
	if err != nil {
		log.WithError(err).Fatal("failed to execute Redis healthcheck template")
	}
	checks = append(checks, &pb.HealthCheck{
		CheckType: &pb.HealthCheck_PxL{
			PxL: &pb.PxLHealthCheck{
				Script:        buf.String(),
				SuccessColumn: "success",
			},
		},
	})
	return checks
}

// SovereignSOCRedisAttackExperiment drives the vulnerable redis deployment
// with a continuous bobctl attack loop while Pixie is running. The
// clickhouse_export PxL script continuously exports a windowed slice of
// redis_events to the forensic ClickHouse cluster; KubescapeNodeAgent and
// ForensicAlertCount track the anomaly side, ProcessStats/Heap/CH operator
// track Pixie and CH health.
//
// exportDSN is the ClickHouse endpoint Kelvin uses for px.export; it MUST
// be reachable from the experiment cluster's network. Pointing this at an
// in-cluster service DNS name of a different cluster will crash Kelvin
// because ClickHouseExportSinkNode::OpenImpl does not catch exceptions
// thrown by the clickhouse-cpp client constructor on DNS failure.
//
// alertsDSN is the ClickHouse endpoint the perf tool reads forensic_db
// alerts from via clickhouse_dsn=. It can be a different cluster/db/user
// from exportDSN. A failure here will only error the forensic-alerts
// metric; it will not crash Kelvin.
func SovereignSOCRedisAttackExperiment(
	metricPeriod time.Duration,
	exportPeriod time.Duration,
	exportWindow time.Duration,
	exportDSN string,
	exportTable string,
	alertsDSN string,
	alertsTable string,
	alertCountWindow time.Duration,
	predeployDur time.Duration,
	dur time.Duration,
) *pb.ExperimentSpec {
	e := &pb.ExperimentSpec{
		VizierSpec: VizierWorkload(),
		WorkloadSpecs: []*pb.WorkloadSpec{
			RedisVulnerableWorkload(),
			BobctlAttackWorkload(),
		},
		MetricSpecs: []*pb.MetricSpec{
			ProcessStatsMetrics(metricPeriod),
			// Stagger the heap query slightly because of known query stability issues.
			HeapMetrics(metricPeriod + (2 * time.Second)),
			ClickHouseExportLoadMetric(exportPeriod, exportDSN, exportTable, exportTable, exportWindow),
			ClickHouseOperatorMetrics(metricPeriod),
			KubescapeNodeAgentMetrics(metricPeriod),
			ForensicAlertCountMetric(metricPeriod, alertsDSN, alertsTable, alertCountWindow),
		},
		RunSpec: &pb.RunSpec{
			Actions: []*pb.ActionSpec{
				{
					Type: pb.START_VIZIER,
				},
				{
					Type: pb.START_METRIC_RECORDERS,
				},
				{
					Type:     pb.BURNIN,
					Duration: types.DurationProto(predeployDur),
				},
				{
					Type: pb.START_WORKLOADS,
				},
				{
					Type:     pb.RUN,
					Duration: types.DurationProto(dur),
				},
				{
					Type: pb.STOP_METRIC_RECORDERS,
				},
			},
		},
		ClusterSpec: DefaultCluster,
	}
	e = addTags(e,
		"workload/sovereign-soc",
		"workload/redis-attack",
		fmt.Sprintf("parameter/export_window/%s", exportWindow),
		fmt.Sprintf("parameter/alert_count_window/%s", alertCountWindow),
	)
	return e
}
