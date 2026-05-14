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
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/gogo/protobuf/types"
	log "github.com/sirupsen/logrus"

	pb "px.dev/pixie/src/e2e_test/perf_tool/experimentpb"
)

// existingVizierWorkload returns a VizierSpec that skips the deploy/skaffold
// rebuild but still binds the existing cluster's UUID to the Pixie context.
// Used when SOC_VIZIER_EXISTING=1 — e.g., the local-ci.sh phase 9 path where
// Pixie is already running in `pl` and connected to AOCC over Tailscale.
//
// The single PxCLIDeploy step has empty Args (so it does NOT redeploy) but
// SetClusterID=true, which makes pxDeployImpl.Deploy() call `px get cluster
// --id` and feed the result into pxCtx.SetClusterID. Without that, every
// subsequent NewVizierClient call errors with "must call SetClusterID
// before calling NewVizierClient on Context" — observed as a silent
// healthcheck loop until the 10-min backoff times out.
func existingVizierWorkload() *pb.WorkloadSpec {
	return &pb.WorkloadSpec{
		Name: "vizier",
		DeploySteps: []*pb.DeployStep{
			{
				DeployType: &pb.DeployStep_Px{
					Px: &pb.PxCLIDeploy{
						SetClusterID: true,
					},
				},
			},
		},
		Healthchecks: VizierHealthChecks(),
	}
}

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
// ApplicationProfile and the intentionally vulnerable Redis 7.2.10 pod
// that bobctl-attack targets. Both YAMLs land in the `redis` namespace.
//
// Tagged as `infra` so it deploys BEFORE START_METRIC_RECORDERS. The
// redis_events table only registers in Pixie after the PEM observes a
// RESP packet; with MultiTierAppWorkload running in the same selector,
// the api backend's redis cache traffic provides that first packet
// before any metric script probes the table. (Previously a separate
// redis-warmer Deployment served this role, but k6 → api → redis under
// MultiTierAppWorkload drives orders of magnitude more traffic and
// makes the warmer redundant.)
//
// Assumes the target cluster has Kubescape (honey/node-agent) preinstalled
// — the k8ssandra suite has the same "external prerequisite" shape.
func RedisVulnerableWorkload() *pb.WorkloadSpec {
	return &pb.WorkloadSpec{
		Name:           "redis-vulnerable",
		ActionSelector: SovereignSOCInfraSelector,
		DeploySteps: []*pb.DeployStep{
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						// sbob ApplicationProfiles MUST precede the
						// Deployments — kubescape only honours the
						// `kubescape.io/user-defined-profile` label if
						// the named profile already exists when the pod
						// is admitted; otherwise it silently falls back
						// to auto-learning and the t0-alerting we're
						// trying to enable doesn't happen. See
						// feedback_kubescape_empty_profile.
						YAMLPaths: []string{
							fmt.Sprintf("%s/redis-sbob.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/redis-client-sbob.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/redis-vulnerable.yaml", sovereignSOCYAMLRoot),
						},
					},
				},
			},
		},
		Healthchecks: redisHealthChecks("redis"),
	}
}

// MultiTierAppWorkload deploys a three-tier HTTP stack into the `redis`
// namespace whose request mix exercises four Pixie protocol decoders
// at the same time (http_events, redis_events, pgsql_events, dns_events):
//
//	loadgen (k6)
//	  │
//	  ▼ HTTP /api/item/{id}, /api/event        ─→ http_events
//	api-backend (Flask + gunicorn × 2 replicas)
//	  │                       │
//	  ▼ Redis GET/SETEX/DEL   ▼ PostgreSQL SELECT/INSERT
//	redis (existing)        postgres (new)
//	redis_events            pgsql_events
//
// `qps` is k6's constant-arrival-rate target; `vus` the steady-state
// worker pool; `maxVUs` the burst cap. The base loadgen-k6.yaml ships
// configured for qps=500 / vus=50 / maxVUs=200 (the 1× profile); higher
// multipliers are wired in via a strategic-merge env patch on the
// loadgen Deployment, so the same three YAMLs serve all load levels.
// Kustomize merges env entries by `name`, replacing the relevant values
// in place without touching API_URL or anything else.
//
// Tagged `infra` so the redis + postgres + http traffic starts BEFORE
// the metric recorders' PxL healthcheck queries Pixie's protocol
// tables — without that ordering, the healthcheck loops on
// `Table 'redis_events' not found`.
func MultiTierAppWorkload(qps, vus, maxVUs int) *pb.WorkloadSpec {
	envPatch := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: loadgen
  namespace: redis
spec:
  template:
    spec:
      containers:
      - name: k6
        env:
        - {name: K6_QPS,     value: "%d"}
        - {name: K6_VUS,     value: "%d"}
        - {name: K6_MAX_VUS, value: "%d"}
`, qps, vus, maxVUs)
	return &pb.WorkloadSpec{
		Name:           "multi-tier-app",
		ActionSelector: SovereignSOCInfraSelector,
		DeploySteps: []*pb.DeployStep{
			{
				DeployType: &pb.DeployStep_Prerendered{
					Prerendered: &pb.PrerenderedDeploy{
						// sbob ApplicationProfiles first — same reasoning
						// as RedisVulnerableWorkload: the user-defined-
						// profile label only takes effect if the named
						// profile already exists at pod-admission time.
						// loadgen is intentionally NOT profiled — it
						// carries `kubescape.io/ignore: true` because it
						// IS the adversary surface for k6 traffic.
						YAMLPaths: []string{
							fmt.Sprintf("%s/postgres-sbob.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/api-sbob.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/postgres.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/api-backend.yaml", sovereignSOCYAMLRoot),
							fmt.Sprintf("%s/loadgen-k6.yaml", sovereignSOCYAMLRoot),
						},
						Patches: []*pb.PatchSpec{
							{
								Target: &pb.PatchTarget{
									Kind:      "Deployment",
									Name:      "loadgen",
									Namespace: "redis",
								},
								YAML: envPatch,
							},
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
	qpsMultiplier int,
) *pb.ExperimentSpec {
	vizierSpec := VizierWorkload()
	if os.Getenv("SOC_VIZIER_EXISTING") == "1" {
		vizierSpec = existingVizierWorkload()
	}
	// Three-tier load profile. 1× = 500 k6 QPS / 50 preallocated VUs /
	// 200 maxVUs (k6's own runtime cap). Each multiplier scales all
	// three linearly — VUs > QPS would just sit idle, and maxVUs needs
	// to stay above VUs to leave headroom for tail latency.
	qps := 500 * qpsMultiplier
	vus := 50 * qpsMultiplier
	maxVUs := 200 * qpsMultiplier
	e := &pb.ExperimentSpec{
		VizierSpec: vizierSpec,
		WorkloadSpecs: []*pb.WorkloadSpec{
			// Kubescape + Vector first so the node-agent is running and
			// Vector's log pipeline is live before any attack traffic is
			// generated. Vector ships node-agent logs to
			// forensic_db.kubescape_logs on the external forensic CH.
			KubescapeVectorWorkload(),
			RedisVulnerableWorkload(),
			// Three-tier loadgen → api → (redis + postgres) lights up
			// http/redis/pgsql/dns events simultaneously at the chosen
			// QPS multiplier.
			MultiTierAppWorkload(qps, vus, maxVUs),
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
		fmt.Sprintf("parameter/load_multiplier/%dx", qpsMultiplier),
		fmt.Sprintf("parameter/k6_qps/%d", qps),
	)
	return e
}
