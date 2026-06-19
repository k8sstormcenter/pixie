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

import * as React from 'react';

import { Button } from '@mui/material';
import { useTheme } from '@mui/material/styles';
import { useHistory } from 'react-router-dom';
import {
  data as visData,
  Edge,
  Network,
  Node,
  parseDOTNetwork,
} from 'vis-network/standalone';

import { ClusterContext } from 'app/common/cluster-context';
import { LiveRouteContext } from 'app/containers/App/live-routing';
import { WidgetDisplay } from 'app/containers/live/vis';
import { Relation, SemanticType } from 'app/types/generated/vizierapi_pb';
import { Arguments } from 'app/utils/args-utils';
import { GaugeLevel, getColor, getLatencyNSLevel } from 'app/utils/metric-thresholds';

import { GraphBase } from './graph-base';
import {
  ColInfo,
  colInfoFromName,
  getGraphOptions,
  semTypeToShapeConfig,
} from './graph-utils';
import { formatByDataType, formatBySemType } from '../../format-data/format-data';
import { deepLinkURLFromSemanticType } from '../utils/live-view-params';

interface AdjacencyList {
  toColumn: string;
  fromColumn: string;
}

interface EdgeThresholds {
  mediumThreshold: number;
  highThreshold: number;
}

interface NodeThresholds {
  mediumThreshold: number;
  highThreshold: number;
}

export interface GraphDisplay extends WidgetDisplay {
  readonly dotColumn?: string;
  readonly adjacencyList?: AdjacencyList;
  readonly data?: any[];
  readonly edgeWeightColumn?: string;
  readonly nodeWeightColumn?: string;
  readonly edgeColorColumn?: string;
  readonly edgeThresholds?: EdgeThresholds;
  readonly edgeHoverInfo?: string[];
  readonly edgeLength?: number;
  readonly enableDefaultHierarchy?: boolean;
  readonly edgeLabelColumn?: string;
  readonly nodeLabelColumn?: string;
  readonly nodeColorColumn?: string;
  readonly nodeThresholds?: NodeThresholds;
  readonly nodeHoverInfo?: string[];
}

interface GraphProps {
  dot?: any;
  data?: any[];
  toCol?: ColInfo;
  fromCol?: ColInfo;
  propagatedArgs?: Arguments;
  edgeWeightColumn?: string;
  nodeWeightColumn?: string;
  edgeColorColumn?: ColInfo;
  edgeThresholds?: EdgeThresholds;
  edgeHoverInfo?: ColInfo[];
  edgeLength?: number;
  enableDefaultHierarchy?: boolean;
  edgeLabelColumn?: ColInfo;
  nodeLabelColumn?: ColInfo;
  nodeColorColumn?: ColInfo;
  nodeThresholds?: NodeThresholds;
  nodeHoverInfo?: ColInfo[];
  setExternalControls?: React.RefCallback<React.ReactNode>;
}

interface GraphData {
  nodes: visData.DataSet<Node>;
  edges: visData.DataSet<Edge>;
  idToSemType: { [ key: string ]: SemanticType };
  propagatedArgs?: Arguments;
}

interface GraphWidgetProps {
  display: GraphDisplay;
  data: any[];
  relation: Relation;
  propagatedArgs?: Arguments;
  setExternalControls?: React.RefCallback<React.ReactNode>;
}

const INVALID_NODE_TYPES = [
  SemanticType.ST_SCRIPT_REFERENCE,
  SemanticType.ST_HTTP_RESP_MESSAGE,
];

const LATENCY_TYPES = [
  SemanticType.ST_DURATION_NS,
  SemanticType.ST_THROUGHPUT_PER_NS,
  SemanticType.ST_THROUGHPUT_BYTES_PER_NS,
];

function getColorForEdge(col: ColInfo, val: number, thresholds: EdgeThresholds): GaugeLevel {
  if (!thresholds && LATENCY_TYPES.includes(col.semType)) {
    return getLatencyNSLevel(val);
  }

  const medThreshold = thresholds ? thresholds.mediumThreshold : 100;
  const highThreshold = thresholds ? thresholds.highThreshold : 200;

  if (val < medThreshold) {
    return 'low';
  }
  return val > highThreshold ? 'high' : 'med';
}

function getColorForNode(val: number, thresholds: NodeThresholds): GaugeLevel {
  const medThreshold = thresholds ? thresholds.mediumThreshold : 100;
  const highThreshold = thresholds ? thresholds.highThreshold : 200;

  if (val < medThreshold) {
    return 'low';
  }
  return val > highThreshold ? 'high' : 'med';
}

export const Graph = React.memo<GraphProps>(({
  dot, toCol, fromCol, data, propagatedArgs, edgeWeightColumn,
  nodeWeightColumn, edgeColorColumn, edgeThresholds, edgeHoverInfo, edgeLength, enableDefaultHierarchy,
  edgeLabelColumn, nodeLabelColumn, nodeColorColumn, nodeThresholds, nodeHoverInfo,
  setExternalControls,
}) => {
  const theme = useTheme();

  const { selectedClusterName } = React.useContext(ClusterContext);
  const history = useHistory();

  const [hierarchyEnabled, setHierarchyEnabled] = React.useState<boolean>(enableDefaultHierarchy);
  const [network, setNetwork] = React.useState<Network>(null);
  const [graph, setGraph] = React.useState<GraphData>(null);

  // Movable edge-label overlay state.
  //  edgeLabels: per-edge label text (built from edgeLabelColumn).
  //  selfLoopAngles: starting angle around the node for each self-loop's
  //    label so multiple loops on the same pod don't stack on top of
  //    each other before any user dragging.
  //  labelOffsets: persistent drag-offset per edge id, in DOM px.
  //  labelLayout: most-recent DOM-space label positions for rendering.
  const [edgeLabels, setEdgeLabels] = React.useState<Map<string, string>>(() => new Map());
  const [edgeSelfLoopAngles, setEdgeSelfLoopAngles] = React.useState<Map<string, number>>(() => new Map());
  const [labelOffsets, setLabelOffsets] = React.useState<Map<string, { dx: number, dy: number }>>(() => new Map());
  const [labelLayout, setLabelLayout] = React.useState<Array<{
    id: string,
    text: string,
    x: number,
    y: number,
  }>>([]);

  const { embedState } = React.useContext(LiveRouteContext);

  const doubleClickCallback = React.useCallback((params?: any) => {
    if (params.nodes.length > 0 && !embedState.widget) {
      const nodeID = params.nodes[0];
      const semType = graph.idToSemType[nodeID];
      const url = deepLinkURLFromSemanticType(semType, nodeID, selectedClusterName, embedState,
        propagatedArgs);
      if (url) {
        history.push(url);
      }
    }
  }, [history, selectedClusterName, graph, propagatedArgs, embedState]);

  const ref = React.useRef<HTMLDivElement>();

  const toggleHierarchy = React.useCallback(() => {
    setHierarchyEnabled(!hierarchyEnabled);
  }, [hierarchyEnabled]);

  // Load the graph.
  React.useEffect(() => {
    if (dot) {
      const dotData = parseDOTNetwork(dot);
      setGraph(dotData);
      return;
    }

    const edges = new visData.DataSet<Edge>();
    const nodes = new visData.DataSet<Node>();
    const idToSemType = {};
    // Per-edge label text — rendered as a draggable HTML overlay in
    // the JSX below so users can pull stacked labels (esp. on self-
    // loops) apart and read what's underneath. vis-network's native
    // edge label is suppressed for any edge that lands here.
    const labelMap = new Map<string, string>();
    const selfLoopCounts = new Map<string, number>();
    const selfLoopAngles = new Map<string, number>();

    const upsertNode = (label: string, st: SemanticType, weight: number) => {
      if (!idToSemType[label]) {
        const node = {
          ...semTypeToShapeConfig(st),
          id: label,
          label,
        };

        if (weight !== -1) {
          node.value = weight;
        }

        nodes.add(node);
        idToSemType[label] = st;
      }
    };
    data.forEach((d, idx) => {
      const nt = d[toCol.name];
      const nf = d[fromCol.name];

      let nodeWeight = -1;
      if (nodeWeightColumn && nodeWeightColumn !== '') {
        nodeWeight = d[nodeWeightColumn];
      }

      upsertNode(nt, toCol?.semType, nodeWeight);
      upsertNode(nf, fromCol?.semType, nodeWeight);

      // Stable per-row id so the overlay can keep its drag offset
      // attached even when the underlying network re-stabilises.
      const edgeId = `e${idx}`;
      const edge = {
        id: edgeId,
        from: nf,
        to: nt,
      } as Edge;

      if (edgeWeightColumn && edgeWeightColumn !== '') {
        edge.value = d[edgeWeightColumn];
      }

      if (edgeColorColumn) {
        const level = getColorForEdge(edgeColorColumn, d[edgeColorColumn.name], edgeThresholds);
        edge.color = getColor(level, theme);
      }

      if (edgeLabelColumn) {
        // DON'T set edge.label — the overlay renders it. Track text +
        // (for self-loops) a per-edge angle so labels around the same
        // pod start at distinct positions before the user drags them.
        labelMap.set(edgeId, String(d[edgeLabelColumn.name]));
        if (nf === nt) {
          const seen = selfLoopCounts.get(nf) || 0;
          selfLoopCounts.set(nf, seen + 1);
          // Fan out around the node: 60° apart, starting at 30°.
          selfLoopAngles.set(edgeId, ((seen * Math.PI) / 3) + (Math.PI / 6));
        }
      }

      if (edgeHoverInfo && edgeHoverInfo.length > 0) {
        let edgeInfo = '';
        edgeHoverInfo.forEach((info, i) => {
          if (info != null) {
            let val: string;
            if (info.semType === SemanticType.ST_NONE || info.semType === SemanticType.ST_UNSPECIFIED) {
              val = formatByDataType(info.type, d[info.name]);
            } else {
              const valWithUnits = formatBySemType(info.semType, d[info.name]);
              val = `${valWithUnits.val} ${valWithUnits.units}`;
            }
            edgeInfo = `${edgeInfo}${i === 0 ? '' : '<br>'} ${info.name}: ${val}`;
          }
        });
        edge.title = edgeInfo;
      }

      edges.add(edge);
    });

    setGraph({
      nodes, edges, idToSemType,
    });
    setEdgeLabels(labelMap);
    setEdgeSelfLoopAngles(selfLoopAngles);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dot, data, toCol, fromCol]);

  // Load the data.
  React.useEffect(() => {
    if (!graph) {
      return;
    }
    const opts = getGraphOptions(theme, edgeLength);

    if (hierarchyEnabled) {
      opts.layout.hierarchical = {
        enabled: true,
        levelSeparation: 400,
        nodeSpacing: 10,
        treeSpacing: 50,
        direction: 'LR',
        sortMethod: 'directed',
      };
    }

    const n = new Network(ref.current, graph, opts);
    n.on('doubleClick', doubleClickCallback);

    n.on('stabilizationIterationsDone', () => {
      n.setOptions({ physics: false });
    });
    setNetwork(n);

  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [graph, doubleClickCallback, hierarchyEnabled]);

  // Recompute label DOM positions whenever the network repaints
  // (drag, zoom, physics tick). canvasToDOM gives us the on-screen
  // pixel for the canvas-space midpoint of each edge.
  React.useEffect(() => {
    if (!network || edgeLabels.size === 0) {
      setLabelLayout([]);
      return undefined;
    }
    let raf = 0;
    const recompute = () => {
      if (raf) cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        const next: Array<{ id: string, text: string, x: number, y: number }> = [];
        edgeLabels.forEach((text, edgeId) => {
          // getConnectedNodes(edgeId) returns [from, to] for a normal
          // edge and [self] for a self-loop. Used instead of reaching
          // into network.body (which the public typings don't expose).
          const ends = network.getConnectedNodes(edgeId) as Array<string | number>;
          if (!ends || ends.length === 0) return;
          const fromId = String(ends[0]);
          const toId = String(ends.length > 1 ? ends[1] : ends[0]);
          const fromPos = network.getPositions([fromId])[fromId];
          const toPos = network.getPositions([toId])[toId];
          if (!fromPos || !toPos) return;
          let cx: number;
          let cy: number;
          if (fromId === toId) {
            // Self-loop: pin label to a per-edge angle around the node
            // so two loops on the same pod don't start in the same spot.
            const angle = edgeSelfLoopAngles.get(edgeId) || 0;
            const radius = 55;
            cx = fromPos.x + Math.cos(angle) * radius;
            cy = fromPos.y + Math.sin(angle) * radius;
          } else {
            cx = (fromPos.x + toPos.x) / 2;
            cy = (fromPos.y + toPos.y) / 2;
          }
          const dom = network.canvasToDOM({ x: cx, y: cy });
          const off = labelOffsets.get(edgeId) || { dx: 0, dy: 0 };
          next.push({ id: edgeId, text, x: dom.x + off.dx, y: dom.y + off.dy });
        });
        setLabelLayout(next);
      });
    };
    network.on('afterDrawing', recompute);
    recompute();
    return () => {
      network.off('afterDrawing', recompute);
      if (raf) cancelAnimationFrame(raf);
    };
  }, [network, edgeLabels, edgeSelfLoopAngles, labelOffsets]);

  const onLabelPointerDown = React.useCallback((edgeId: string) => (e: React.PointerEvent) => {
    e.stopPropagation();
    e.preventDefault();
    const startX = e.clientX;
    const startY = e.clientY;
    const initial = labelOffsets.get(edgeId) || { dx: 0, dy: 0 };
    const move = (ev: PointerEvent) => {
      setLabelOffsets((prev) => {
        const nextMap = new Map(prev);
        nextMap.set(edgeId, {
          dx: initial.dx + ev.clientX - startX,
          dy: initial.dy + ev.clientY - startY,
        });
        return nextMap;
      });
    };
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }, [labelOffsets]);

  const controls = React.useMemo(() => (
    <Button
      size='small'
      onClick={toggleHierarchy}
    >
      {hierarchyEnabled ? 'Disable hierarchy' : 'Enable hierarchy'}
    </Button>
  ), [hierarchyEnabled, toggleHierarchy]);

  return (
    <div style={{ position: 'relative', width: '100%', height: '100%' }}>
      <GraphBase
        network={network}
        visRootRef={ref}
        showZoomButtons={true}
        setExternalControls={setExternalControls}
        additionalButtons={controls}
      />
      <div style={{
        position: 'absolute',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        pointerEvents: 'none',
        overflow: 'hidden',
      }}>
        {labelLayout.map(({ id, text, x, y }) => (
          <div
            key={id}
            onPointerDown={onLabelPointerDown(id)}
            style={{
              position: 'absolute',
              left: `${x}px`,
              top: `${y}px`,
              transform: 'translate(-50%, -50%)',
              padding: '1px 6px',
              background: 'rgba(38, 38, 42, 0.85)',
              color: '#ffffff',
              fontSize: '11px',
              fontFamily: 'Roboto, sans-serif',
              borderRadius: '3px',
              cursor: 'grab',
              pointerEvents: 'auto',
              userSelect: 'none',
              whiteSpace: 'nowrap',
              touchAction: 'none',
            }}
          >
            {text}
          </div>
        ))}
      </div>
    </div>
  );
});
Graph.displayName = 'Graph';

export const GraphWidget = React.memo<GraphWidgetProps>(({
  display, data, relation, propagatedArgs, setExternalControls,
}) => {
  if (display.dotColumn && data.length > 0) {
    return (
      <Graph dot={data[0][display.dotColumn]} />
    );
  } if (display.adjacencyList && display.adjacencyList.fromColumn && display.adjacencyList.toColumn) {
    let errorMsg = '';

    const toColInfo = colInfoFromName(relation, display.adjacencyList.toColumn);
    if (toColInfo && INVALID_NODE_TYPES.includes(toColInfo.semType)) {
      errorMsg = `${display.adjacencyList.toColumn} cannot be used as the source column`;
    }
    const fromColInfo = colInfoFromName(relation, display.adjacencyList.fromColumn);
    if (fromColInfo && INVALID_NODE_TYPES.includes(fromColInfo.semType)) {
      errorMsg = `${display.adjacencyList.fromColumn} cannot be used as the destination column`;
    }
    const colorColInfo = colInfoFromName(relation, display.edgeColorColumn);
    const labelColInfo = colInfoFromName(relation, display.edgeLabelColumn);
    const nodeLabelColInfo = colInfoFromName(relation, display.nodeLabelColumn);
    const nodeColorColInfo = colInfoFromName(relation, display.nodeColorColumn);
    const edgeHoverInfo = [];
    if (display.edgeHoverInfo && display.edgeHoverInfo.length > 0) {
      for (const e of display.edgeHoverInfo) {
        const info = colInfoFromName(relation, e);
        if (info) { // Only push valid column infos. The user may pass in an invalid column name in the vis spec.
          edgeHoverInfo.push(info);
        }
      }
    }
    const nodeHoverInfo = [];
    if (display.nodeHoverInfo && display.nodeHoverInfo.length > 0) {
      for (const n of display.nodeHoverInfo) {
        const info = colInfoFromName(relation, n);
        if (info) {
          nodeHoverInfo.push(info);
        }
      }
    }
    if (toColInfo && fromColInfo && !errorMsg) {
      return (
        <Graph
          {...display}
          data={data}
          toCol={toColInfo}
          fromCol={fromColInfo}
          edgeColorColumn={colorColInfo}
          edgeLabelColumn={labelColInfo}
          nodeLabelColumn={nodeLabelColInfo}
          nodeColorColumn={nodeColorColInfo}
          propagatedArgs={propagatedArgs}
          edgeHoverInfo={edgeHoverInfo}
          nodeHoverInfo={nodeHoverInfo}
          setExternalControls={setExternalControls}
        />
      );
    }

    if (!toColInfo) {
      errorMsg = `${display.adjacencyList.toColumn} column does not exist`;
    } else if (!fromColInfo) {
      errorMsg = `${display.adjacencyList.fromColumn} column does not exist`;
    }

    return <div>{errorMsg}</div>;
  }
  return <div key={display.dotColumn}>Invalid spec for graph</div>;
});
GraphWidget.displayName = 'GraphWidget';
