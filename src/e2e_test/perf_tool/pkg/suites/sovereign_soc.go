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

// KubescapeVectorWorkload installs Kubescape (eBPF runtime-detection node
// agent + storage + operator) and Vector (DaemonSet shipping Kubescape node-
// agent logs into ClickHouse) on the experiment cluster. Manifests are
// pre-rendered from upstream Helm charts so PrerenderedDeploy can apply them
// statically — see k8s/sovereign-soc/helm-rendered/README.md for the
// re-render recipe.
//
// Treated as long-lived infrastructure (similar to the cert-manager
// prerequisite of the k8ssandra suite). All steps set
// SkipNamespaceDelete=true so teardown never tries to delete `honey` or
// `kube-system`. The first run installs; subsequent runs idempotently
// re-apply (Pixie's ApplyResources skips with IsAlreadyExists or falls
// through to Update). Manual cleanup is only required if you change the
// rendered YAML in a backwards-incompatible way.
//
// The workload is tagged with action_selector="infra" and the experiment
// schedules a START_WORKLOADS{Name:"infra"} action before
// START_METRIC_RECORDERS. That ordering is load-bearing: the kubescape
// node-agent's prometheus exporter is gated by a ConfigMap that this
// workload writes, and the perf_tool's prometheus recorder pre-flights
// port-forwards at recorder-start time. If recorders ran first, they
// would connect to an old node-agent pod with no listener on :8080 and
// the recorder would error out before the experiment even started
// measuring.
//
// Layout:
//  1. kubescape.rendered.yaml — honey namespace, main install + 5 CRDs at
//     the top of the file (rendered with --include-crds so kubescape's
//     `crds/` chart directory is emitted).
//  2. kubescape.rendered.kube-system.yaml — the one RoleBinding kubescape
//     needs in kube-system (storage-auth-reader) for API aggregation auth.
//  3. kubescape-default-rules.yaml — the built-in runtime rule set.
//  4. vector.rendered.yaml — Vector DaemonSet + RBAC that tails Kubescape
//     node-agent logs into forensic_db.kubescape_logs. Endpoint is the
//     external forensic CH URL so any experiment cluster can write to it.
// SovereignSOCInfraSelector is the action_selector tagged onto the
// kubescape-vector workload so it runs in a dedicated START_WORKLOADS
// phase before START_METRIC_RECORDERS — see the docstring on
// KubescapeVectorWorkload.
const SovereignSOCInfraSelector = "infra"

func KubescapeVectorWorkload() *pb.WorkloadSpec {
	return &pb.WorkloadSpec{
		Name:           "kubescape-vector",
		ActionSelector: SovereignSOCInfraSelector,
		DeploySteps: []*pb.DeployStep{
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						YAMLPaths: []string{
							fmt.Sprintf("%s/helm-rendered/kubescape.rendered.yaml", sovereignSOCYAMLRoot),
						},
						SkipNamespaceDelete: true,
					},
				},
			},
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						YAMLPaths: []string{
							fmt.Sprintf("%s/helm-rendered/kubescape.rendered.kube-system.yaml", sovereignSOCYAMLRoot),
						},
						SkipNamespaceDelete: true,
					},
				},
			},
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						YAMLPaths: []string{
							fmt.Sprintf("%s/helm-rendered/kubescape-default-rules.yaml", sovereignSOCYAMLRoot),
						},
						SkipNamespaceDelete: true,
					},
				},
			},
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						YAMLPaths: []string{
							fmt.Sprintf("%s/helm-rendered/vector.rendered.yaml", sovereignSOCYAMLRoot),
						},
						SkipNamespaceDelete: true,
					},
				},
			},
		},
		Healthchecks: []*pb.HealthCheck{
			{
				CheckType: &pb.HealthCheck_K8S{
					K8S: &pb.K8SPodsReadyCheck{
						Namespace: "honey",
					},
				},
			},
		},
	}
}

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
			// Kubescape + Vector first so the node-agent is running and
			// Vector's log pipeline is live before any attack traffic is
			// generated. Vector ships node-agent logs to
			// forensic_db.kubescape_logs on the external forensic CH.
			KubescapeVectorWorkload(),
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
					// Deploy kubescape+vector first so the node-agent's
					// prometheus listener on :8080 is up before the
					// metric recorder pre-flights port-forwards. Without
					// this ordering, the recorder errors out at startup.
					Type: pb.START_WORKLOADS,
					Name: SovereignSOCInfraSelector,
				},
				{
					Type: pb.START_METRIC_RECORDERS,
				},
				{
					Type:     pb.BURNIN,
					Duration: types.DurationProto(predeployDur),
				},
				{
					// Default selector (empty) catches the redis +
					// bobctl-attack workloads.
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
