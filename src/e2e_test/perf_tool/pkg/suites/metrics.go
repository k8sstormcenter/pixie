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
	// Necessary to use go:embed directive.
	_ "embed"
	"time"

	"github.com/gogo/protobuf/types"

	pb "px.dev/pixie/src/e2e_test/perf_tool/experimentpb"
)

//go:embed scripts/process_stats.pxl
var processStatsScript string

//go:embed scripts/heap_size.pxl
var heapSizeScript string

//go:embed scripts/http_data_loss.pxl
var httpDataLossScript string

//go:embed scripts/clickhouse_export.pxl
var clickhouseExportScript string

//go:embed scripts/clickhouse_read.pxl
var clickhouseReadScript string

//go:embed scripts/forensic_alerts.pxl
var forensicAlertsScript string

// ClickHouseOperatorPromRecorderName is the canonical name used by the CLI's
// --prom_recorder_override flag to retarget the ClickHouse operator scraper at
// a different cluster (kubeconfig/kube_context).
const ClickHouseOperatorPromRecorderName = "clickhouse-operator"

// KubescapeNodeAgentPromRecorderName is the canonical name used by the CLI's
// --prom_recorder_override flag to retarget the kubescape node-agent scraper
// at a different cluster.
const KubescapeNodeAgentPromRecorderName = "kubescape-node-agent"

// ProcessStatsMetrics adds a metric spec that collects process stats such as rss,vsize, and cpu_usage.
func ProcessStatsMetrics(period time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_PxL{
			PxL: &pb.PxLScriptSpec{
				Script:           processStatsScript,
				Streaming:        false,
				CollectionPeriod: types.DurationProto(period),
				TableOutputs: map[string]*pb.PxLScriptOutputList{
					"*": {
						Outputs: []*pb.PxLScriptOutputSpec{
							singleMetricOutputWithPodNodeName("rss"),
							singleMetricOutputWithPodNodeName("vsize"),
							singleMetricOutputWithPodNodeName("cpu_usage"),
						},
					},
				},
			},
		},
	}
}

// HeapMetrics collects metrics around heap usage and amount of data stored in the table store.
func HeapMetrics(period time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_PxL{
			PxL: &pb.PxLScriptSpec{
				Script:           heapSizeScript,
				Streaming:        false,
				CollectionPeriod: types.DurationProto(period),
				TableOutputs: map[string]*pb.PxLScriptOutputList{
					"*": {
						Outputs: []*pb.PxLScriptOutputSpec{
							singleMetricOutputWithPodNodeName("table_size"),
							singleMetricOutputWithPodNodeName("current_allocated_bytes", "heap_allocated_bytes"),
							singleMetricOutputWithPodNodeName("heap_size_bytes"),
							singleMetricOutputWithPodNodeName("free_bytes", "heap_free_bytes"),
						},
					},
				},
			},
		},
	}
}

// HTTPDataLossMetric adds a metric that tracks HTTP data loss based on the `X-Px-Seq-Id` header.
func HTTPDataLossMetric(outputPeriod time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_PxL{
			PxL: &pb.PxLScriptSpec{
				Script:    httpDataLossScript,
				Streaming: true,
				TemplateValues: map[string]string{
					"header_name": "X-Px-Seq-Id",
				},
				TableOutputs: map[string]*pb.PxLScriptOutputList{
					"*": {
						Outputs: []*pb.PxLScriptOutputSpec{
							{
								OutputSpec: &pb.PxLScriptOutputSpec_DataLossCounter{
									DataLossCounter: &pb.DataLossCounterOutput{
										TimestampCol: "timestamp",
										MetricName:   "http_data_loss",
										SeqIDCol:     "seq_id",
										OutputPeriod: types.DurationProto(outputPeriod),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// ProtocolLoadtestPromMetrics adds metrics that scrapes prometheus metrics from the protocol loadtest server, collecting process data (cpu usage, rss, vsize).
func ProtocolLoadtestPromMetrics(scrapePeriod time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_Prom{
			Prom: &pb.PrometheusScrapeSpec{
				Namespace:       "px-protocol-loadtest",
				MatchLabelKey:   "name",
				MatchLabelValue: "server",
				Port:            8080,
				ScrapePeriod:    types.DurationProto(scrapePeriod),
				MetricNames: map[string]string{
					"process_cpu_seconds_total":     "cpu_seconds_counter",
					"process_resident_memory_bytes": "rss",
					"process_virtual_memory_bytes":  "vsize",
				},
			},
		},
	}
}

// ClickHouseExportLoadMetric runs the clickhouse export PxL script on a tight
// period to drive load against the ClickHouse write path, and reports the
// row count of each export as a metric. sourceTable is the Pixie events
// table the script reads from (e.g. "http_events", "redis_events");
// destTable is the ClickHouse destination table. Their column shapes must
// be compatible or Kelvin will crash on the first CH server-side column
// mismatch (see ClickHouseExportSinkNode TODO).
func ClickHouseExportLoadMetric(period time.Duration, dsn string, sourceTable string, destTable string, window time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_PxL{
			PxL: &pb.PxLScriptSpec{
				Script:           clickhouseExportScript,
				Streaming:        false,
				CollectionPeriod: types.DurationProto(period),
				TemplateValues: map[string]string{
					"dsn":          dsn,
					"source_table": sourceTable,
					"dest_table":   destTable,
					"window":       window.String(),
				},
				TableOutputs: map[string]*pb.PxLScriptOutputList{
					"*": {
						Outputs: []*pb.PxLScriptOutputSpec{
							singleMetricOutputWithPodNodeName("row_count", "clickhouse_export_rows"),
						},
					},
				},
			},
		},
	}
}

// ClickHouseReadLoadMetric runs the clickhouse read PxL script on a tight
// period to drive load against the ClickHouse read path, and reports the
// row count of each readback as a metric.
func ClickHouseReadLoadMetric(period time.Duration, dsn string, table string, window time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_PxL{
			PxL: &pb.PxLScriptSpec{
				Script:           clickhouseReadScript,
				Streaming:        false,
				CollectionPeriod: types.DurationProto(period),
				TemplateValues: map[string]string{
					"dsn":    dsn,
					"table":  table,
					"window": window.String(),
				},
				TableOutputs: map[string]*pb.PxLScriptOutputList{
					"*": {
						Outputs: []*pb.PxLScriptOutputSpec{
							singleMetricOutputWithPodNodeName("row_count", "clickhouse_read_rows"),
						},
					},
				},
			},
		},
	}
}

// ClickHouseOperatorMetrics scrapes the Altinity clickhouse-operator's
// metrics-exporter sidecar (`ch-metrics` port 8888), which proxies per-shard
// ClickHouse server metrics. Named so the --prom_recorder_override CLI flag
// can point it at a different cluster via kubeconfig/kube_context.
func ClickHouseOperatorMetrics(scrapePeriod time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_Prom{
			Prom: &pb.PrometheusScrapeSpec{
				Name:            ClickHouseOperatorPromRecorderName,
				Namespace:       "clickhouse",
				MatchLabelKey:   "app.kubernetes.io/name",
				MatchLabelValue: "altinity-clickhouse-operator",
				Port:            8888,
				ScrapePeriod:    types.DurationProto(scrapePeriod),
				MetricNames: map[string]string{
					// Gauges: in-flight load on CH servers.
					"chi_clickhouse_metric_Query":                                "clickhouse_active_queries",
					"chi_clickhouse_metric_TCPConnection":                        "clickhouse_tcp_connections",
					"chi_clickhouse_metric_HTTPConnection":                       "clickhouse_http_connections",
					"chi_clickhouse_metric_MemoryTracking":                       "clickhouse_memory_tracking_bytes",
					"chi_clickhouse_metric_BackgroundMergesAndMutationsPoolTask": "clickhouse_background_merge_tasks",
					"chi_clickhouse_metric_PartsActive":                          "clickhouse_parts_active",
					// Counters: throughput and errors.
					"chi_clickhouse_event_Query":               "clickhouse_queries_total",
					"chi_clickhouse_event_InsertedRows":        "clickhouse_inserted_rows_total",
					"chi_clickhouse_event_SelectedRows":        "clickhouse_selected_rows_total",
					"chi_clickhouse_event_FailedQuery":         "clickhouse_failed_queries_total",
					"chi_clickhouse_event_NetworkSendBytes":    "clickhouse_network_send_bytes_total",
					"chi_clickhouse_event_NetworkReceiveBytes": "clickhouse_network_receive_bytes_total",
					// Per-table gauges: storage-side pressure.
					"chi_clickhouse_table_parts_rows":  "clickhouse_table_parts_rows",
					"chi_clickhouse_table_parts_bytes": "clickhouse_table_parts_bytes",
				},
			},
		},
	}
}

// KubescapeNodeAgentMetrics scrapes the Kubescape node-agent DaemonSet
// (the component that runs eBPF hooks and emits runtime anomaly alerts).
// Metrics are exposed on port 8080 of pods with label `app=node-agent` in
// the `honey` namespace, matching the kubescape helm chart defaults.
//
// Named so the --prom_recorder_override CLI flag can point it at a
// different cluster via kubeconfig/kube_context.
func KubescapeNodeAgentMetrics(scrapePeriod time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_Prom{
			Prom: &pb.PrometheusScrapeSpec{
				Name:            KubescapeNodeAgentPromRecorderName,
				Namespace:       "honey",
				MatchLabelKey:   "app",
				MatchLabelValue: "node-agent",
				Port:            8080,
				ScrapePeriod:    types.DurationProto(scrapePeriod),
				// Whitelist is a superset: prometheus_recorder silently drops
				// metrics that are not present in the source, so listing a
				// candidate name that a particular kubescape version has not
				// (yet) exposed is harmless.
				MetricNames: map[string]string{
					// Standard Go/process exporters — always present.
					"process_cpu_seconds_total":     "kubescape_node_agent_cpu_seconds_total",
					"process_resident_memory_bytes": "kubescape_node_agent_rss",
					"process_virtual_memory_bytes":  "kubescape_node_agent_vsize",
					"go_goroutines":                 "kubescape_node_agent_goroutines",
					// Kubescape-specific (names may vary across versions).
					"kubescape_ruleengine_firing_alerts_total":  "kubescape_firing_alerts_total",
					"kubescape_ruleengine_applied_rules_total":  "kubescape_applied_rules_total",
					"kubescape_node_agent_events_seen_total":    "kubescape_events_seen_total",
					"kubescape_node_agent_events_dropped_total": "kubescape_events_dropped_total",
				},
			},
		},
	}
}

// ForensicAlertCountMetric runs a PxL script against the forensic
// ClickHouse cluster (via clickhouse_dsn=…) to count Kubescape anomaly
// alerts that Vector has landed in forensic_db.kubescape_logs. Emits one
// row per invocation with the total count over the windowed time range.
func ForensicAlertCountMetric(period time.Duration, dsn string, table string, window time.Duration) *pb.MetricSpec {
	return &pb.MetricSpec{
		MetricType: &pb.MetricSpec_PxL{
			PxL: &pb.PxLScriptSpec{
				Script:           forensicAlertsScript,
				Streaming:        false,
				CollectionPeriod: types.DurationProto(period),
				TemplateValues: map[string]string{
					"dsn":    dsn,
					"table":  table,
					"window": window.String(),
				},
				TableOutputs: map[string]*pb.PxLScriptOutputList{
					"*": {
						Outputs: []*pb.PxLScriptOutputSpec{
							singleMetricOutputWithPodNodeName("alert_count", "forensic_alert_count"),
						},
					},
				},
			},
		},
	}
}

func singleMetricOutputWithPodNodeName(col string, newName ...string) *pb.PxLScriptOutputSpec {
	metricName := col
	if len(newName) > 0 {
		metricName = newName[0]
	}
	return &pb.PxLScriptOutputSpec{
		OutputSpec: &pb.PxLScriptOutputSpec_SingleMetric{
			SingleMetric: &pb.SingleMetricPxLOutput{
				TimestampCol: "timestamp",
				MetricName:   metricName,
				ValueCol:     col,
				TagCols: []string{
					"node_name",
					"pod",
				},
			},
		},
	}
}
