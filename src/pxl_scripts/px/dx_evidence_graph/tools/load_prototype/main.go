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

// load_prototype — manual-load harness for the dx_evidence_graph PxL
// stub. Reads a JSON fixture of attackgraph.Edge records (the same
// shape dx-agent writes to AE in PR entlein/dx#68), inlines it as the
// `edges_json` script arg, and executes the script against a Pixie
// PEM via pxapi.
//
// Use this to validate the graph end-to-end before the
// dx_attack_graph table ingest path lands. Once Path A v1 ships,
// this tool retires.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/types"
)

// Edge mirrors attackgraph.Edge from entlein/dx#68 — the JSON tags
// are the contract. Kept loose (interface{}) on optional fields so
// future schema additions don't break the prototype.
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

type rowSink struct{ n int }

func (s *rowSink) AcceptTable(_ context.Context, md types.TableMetadata) (pxapi.TableRecordHandler, error) {
	fmt.Fprintf(os.Stdout, "== table %s ==\n", md.Name)
	return s, nil
}
func (s *rowSink) HandleInit(_ context.Context, _ types.TableMetadata) error { return nil }
func (s *rowSink) HandleRecord(_ context.Context, r *types.Record) error {
	out := ""
	for _, c := range r.TableMetadata.ColInfo {
		d := r.GetDatum(c.Name)
		if d != nil {
			out += c.Name + "=" + d.String() + " "
		}
	}
	fmt.Println(out)
	s.n++
	return nil
}

func (s *rowSink) HandleDone(_ context.Context) error {
	fmt.Fprintf(os.Stdout, "  rows=%d\n", s.n)
	return nil
}

func main() {
	var (
		addr            = flag.String("addr", "127.0.0.1:12345", "PEM direct addr")
		scriptPath      = flag.String("script", "dx_evidence_graph.pxl", "path to the .pxl script")
		fixturePath     = flag.String("fixture", "fixtures/sample.json", "JSON fixture of []Edge")
		investigationID = flag.String("investigation_id", "", "filter to this id (empty = render all)")
	)
	flag.Parse()

	fixtureRaw, err := os.ReadFile(*fixturePath)
	if err != nil {
		die("read fixture: %v", err)
	}
	var edges []Edge
	if err := json.Unmarshal(fixtureRaw, &edges); err != nil {
		die("parse fixture: %v", err)
	}
	if *investigationID != "" {
		filtered := edges[:0]
		for _, e := range edges {
			if e.InvestigationID == *investigationID {
				filtered = append(filtered, e)
			}
		}
		edges = filtered
	}
	fmt.Fprintf(os.Stderr, "load_prototype: %d edges from %s\n", len(edges), *fixturePath)

	scriptRaw, err := os.ReadFile(*scriptPath)
	if err != nil {
		die("read script: %v", err)
	}
	edgesJSON, err := json.Marshal(edges)
	if err != nil {
		die("re-encode edges: %v", err)
	}

	// The v0 PxL stub doesn't (yet) parse edges_json itself — it
	// emits a zero-row placeholder. This tool's real job for v0 is
	// to validate the round-trip: ExecuteScript reaches the PEM,
	// the script compiles, the vispb.Graph spec is well-formed.
	// Once dx-agent's WriteAttackGraph ingest lands, the script
	// reads from a real table and this tool retires.
	pxlSrc := string(scriptRaw) + fmt.Sprintf(`
# load_prototype-injected display of the fixture as a literal table.
import px
_pxl_args = {"investigation_id": %q, "edges_json": %q}
`, *investigationID, string(edgesJSON))

	ctx := context.Background()
	c, err := pxapi.NewClient(ctx,
		pxapi.WithDirectAddr(*addr), pxapi.WithDirectCredsInsecure())
	if err != nil {
		die("NewClient: %v", err)
	}
	v, err := c.NewVizierClient(ctx, "")
	if err != nil {
		die("NewVizierClient: %v", err)
	}
	rs, err := v.ExecuteScript(ctx, pxlSrc, &rowSink{})
	if err != nil && err != io.EOF {
		die("ExecuteScript: %v", err)
	}
	if rs != nil {
		_ = rs.Stream()
		_ = rs.Close()
	}
}

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }
