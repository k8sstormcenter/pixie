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

// load_prototype — manual-load harness for the dx_evidence_graph
// PxL stub. Reads a JSON fixture of attackgraph.Edge records (the
// same shape dx-agent writes via WriteAttackGraph in entlein/dx#68)
// and emits a self-contained HTML page that renders the graph with
// cytoscape.js — same column->visual mapping the production
// vispb.Graph spec uses (requestor_pod → responder_pod,
// weight as edge thickness, max_severity as edge colour).
//
// Why HTML and not pxapi: PxL has no literal-table constructor, so
// we can't feed an inline fixture into px.DataFrame today. Once the
// AE → Pixie-table ingest lands (B2 in the PR-62 discussion), this
// tool retires and the visualization goes through Pixie's own UI.
//
// The colour scale matches the discrete CRS severity buckets
// dx-agent uses: 2 = grey, 3 = yellow, 4 = orange, 5 = red.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"sort"
)

// Edge mirrors attackgraph.Edge from entlein/dx#68 — JSON tags are
// the contract. Kept loose on optional fields so future schema
// additions don't break the prototype.
type Edge struct {
	InvestigationID  string  `json:"investigation_id"`
	TS               uint64  `json:"ts"`
	RequestorPod     string  `json:"requestor_pod"`
	ResponderPod     string  `json:"responder_pod"`
	RequestorService string  `json:"requestor_service"`
	ResponderService string  `json:"responder_service"`
	RequestorIP      string  `json:"requestor_ip"`
	ResponderIP      string  `json:"responder_ip"`
	Weight           uint16  `json:"weight"`
	MaxSeverity      uint8   `json:"max_severity"`
	Confidence       float32 `json:"confidence"`
	EdgeKind         string  `json:"edge_kind"`
	Condition        string  `json:"condition"`
	Criteria         string  `json:"criteria"`
	NumFindings      uint32  `json:"num_findings"`
}

// endpointID picks the most-resolved identity available for a side:
// pod (preferred) → service → IP → a per-edge synthetic ID. Mirrors
// how net_flow_graph's vispb.Graph falls back to IPs when the conn
// tracker hasn't resolved a pod yet. The `side` + `edgeIdx` tail on
// the fully-unresolved fallback keeps distinct unknown endpoints
// from collapsing into one shared node (which would silently merge
// unrelated hops).
func endpointID(pod, service, ip, side string, edgeIdx int) string {
	switch {
	case pod != "":
		return pod
	case service != "":
		return service
	case ip != "":
		return ip
	default:
		return fmt.Sprintf("(unknown-%s-%d)", side, edgeIdx)
	}
}

// severityColor matches dx-agent's CRS severity buckets. Same scale
// the production vispb.Graph spec would resolve via edgeColorColumn=
// max_severity.
func severityColor(s uint8) string {
	switch {
	case s >= 5:
		return "#d93025" // red
	case s == 4:
		return "#f29900" // orange
	case s == 3:
		return "#f9ab00" // yellow
	default:
		return "#9aa0a6" // grey
	}
}

type cyNode struct {
	Data map[string]string `json:"data"`
}

type cyEdge struct {
	Data map[string]any `json:"data"`
}

type cyGraph struct {
	Nodes []cyNode `json:"nodes"`
	Edges []cyEdge `json:"edges"`
	Title string   `json:"-"`
}

func buildGraph(edges []Edge, investigationID string) cyGraph {
	if investigationID != "" {
		filtered := edges[:0]
		for _, e := range edges {
			if e.InvestigationID == investigationID {
				filtered = append(filtered, e)
			}
		}
		edges = filtered
	}
	nodeSet := map[string]struct{}{}
	g := cyGraph{Title: investigationID}
	if g.Title == "" {
		g.Title = "all-investigations"
	}
	for i, e := range edges {
		from := endpointID(e.RequestorPod, e.RequestorService, e.RequestorIP, "src", i)
		to := endpointID(e.ResponderPod, e.ResponderService, e.ResponderIP, "dst", i)
		for _, n := range []string{from, to} {
			if _, ok := nodeSet[n]; !ok {
				nodeSet[n] = struct{}{}
				g.Nodes = append(g.Nodes, cyNode{Data: map[string]string{"id": n, "label": n}})
			}
		}
		g.Edges = append(g.Edges, cyEdge{Data: map[string]any{
			"id":           fmt.Sprintf("e%d", i),
			"source":       from,
			"target":       to,
			"weight":       e.Weight,
			"max_severity": e.MaxSeverity,
			"confidence":   e.Confidence,
			"edge_kind":    e.EdgeKind,
			"condition":    e.Condition,
			"criteria":     e.Criteria,
			"num_findings": e.NumFindings,
			"color":        severityColor(e.MaxSeverity),
			"width":        2 + int(e.Weight)/2, // mirrors edgeWeightColumn=weight
		}})
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].Data["id"] < g.Nodes[j].Data["id"] })
	return g
}

const tmplStr = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>dx attack graph — {{.Title}}</title>
<script src="https://unpkg.com/cytoscape@3.30.2/dist/cytoscape.min.js"></script>
<style>
  html, body { margin:0; padding:0; height:100%; font-family:system-ui, sans-serif; background:#0e1116; color:#e6e6e6; }
  #cy { width:100vw; height:calc(100vh - 60px); }
  header { padding:14px 22px; background:#171a21; border-bottom:1px solid #303641; display:flex; align-items:center; gap:24px; }
  header h1 { margin:0; font-size:14px; font-weight:600; }
  header .legend { display:flex; gap:14px; font-size:12px; align-items:center; }
  header .legend span.swatch { width:14px; height:14px; display:inline-block; border-radius:3px; margin-right:6px; vertical-align:middle; }
  #detail { position:absolute; right:20px; top:80px; max-width:380px; background:#171a21; border:1px solid #303641; border-radius:6px; padding:14px 18px; font-size:12px; line-height:1.4; display:none; }
  #detail h2 { margin:0 0 8px; font-size:12px; font-weight:600; text-transform:uppercase; letter-spacing:.5px; color:#9aa0a6; }
  #detail .row { margin:2px 0; }
  #detail .row b { color:#9aa0a6; font-weight:500; min-width:110px; display:inline-block; }
</style>
</head>
<body>
<header>
  <h1>dx attack graph &mdash; {{.Title}}</h1>
  <div class="legend">
    <span><span class="swatch" style="background:#d93025"></span>severity 5</span>
    <span><span class="swatch" style="background:#f29900"></span>severity 4</span>
    <span><span class="swatch" style="background:#f9ab00"></span>severity 3</span>
    <span><span class="swatch" style="background:#9aa0a6"></span>severity ≤2</span>
  </div>
  <div style="margin-left:auto; font-size:11px; color:#9aa0a6">edge thickness ∝ weight (Σ CRS severity)</div>
</header>
<div id="cy"></div>
<div id="detail"></div>
<script>
const G = {{.JSON}};
const cy = cytoscape({
  container: document.getElementById('cy'),
  elements: { nodes: G.nodes, edges: G.edges },
  style: [
    { selector: 'node', style: {
        'background-color': '#3c4150',
        'label': 'data(label)',
        'color': '#e6e6e6',
        'font-size': 11,
        'text-margin-y': -8,
        'text-wrap': 'wrap',
        'text-max-width': '160px',
        'width': 24, 'height': 24,
    }},
    { selector: 'edge', style: {
        'curve-style': 'bezier',
        'target-arrow-shape': 'triangle',
        'arrow-scale': 1.2,
        'line-color': 'data(color)',
        'target-arrow-color': 'data(color)',
        'width': 'data(width)',
        'label': 'data(edge_kind)',
        'font-size': 10,
        'color': '#9aa0a6',
        'text-rotation': 'autorotate',
        'text-margin-y': -6,
    }},
  ],
  layout: { name: 'cose', animate: false, padding: 40, idealEdgeLength: 180, nodeRepulsion: 4500 },
});
const detail = document.getElementById('detail');
// Edge payload values come from the fixture JSON — never trust them
// to be markup-safe. Build the detail panel with DOM APIs so values
// land as text, not parsed HTML.
function renderDetail(d) {
  detail.replaceChildren();
  const h2 = document.createElement('h2');
  h2.textContent = 'edge ' + d.id;
  detail.appendChild(h2);
  const rows = [
    ['kind', d.edge_kind], ['condition', d.condition], ['criteria', d.criteria],
    ['weight', d.weight], ['max_severity', d.max_severity], ['confidence', d.confidence],
    ['num_findings', d.num_findings], ['source', d.source], ['target', d.target],
  ];
  for (const [key, val] of rows) {
    const row = document.createElement('div');
    row.className = 'row';
    const b = document.createElement('b');
    b.textContent = key;
    row.appendChild(b);
    row.appendChild(document.createTextNode(String(val)));
    detail.appendChild(row);
  }
}
cy.on('tap', 'edge', e => {
  renderDetail(e.target.data());
  detail.style.display = 'block';
});
cy.on('tap', e => { if (e.target === cy) { detail.style.display = 'none'; }});
</script>
</body>
</html>
`

func main() {
	var (
		fixturePath     = flag.String("fixture", "fixtures/sample.json", "JSON fixture of []Edge")
		investigationID = flag.String("investigation_id", "", "filter to this id (empty = render all)")
		outPath         = flag.String("out", "/tmp/dx_attack_graph.html", "HTML output path")
	)
	flag.Parse()

	raw, err := os.ReadFile(*fixturePath)
	if err != nil {
		die("read fixture: %v", err)
	}
	var edges []Edge
	if err := json.Unmarshal(raw, &edges); err != nil {
		die("parse fixture: %v", err)
	}
	g := buildGraph(edges, *investigationID)
	gJSON, err := json.Marshal(map[string]any{"nodes": g.Nodes, "edges": g.Edges})
	if err != nil {
		die("encode graph: %v", err)
	}

	tmpl, err := template.New("g").Parse(tmplStr)
	if err != nil {
		die("parse template: %v", err)
	}
	f, err := os.Create(*outPath)
	if err != nil {
		die("create out: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := tmpl.Execute(f, map[string]any{
		"Title": g.Title,
		"JSON":  template.JS(gJSON),
	}); err != nil {
		die("render: %v", err)
	}
	fmt.Fprintf(os.Stderr, "load_prototype: %d nodes, %d edges -> %s\n", len(g.Nodes), len(g.Edges), *outPath)
}

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }
