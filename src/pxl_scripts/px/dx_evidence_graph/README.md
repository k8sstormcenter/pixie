# dx evidence graph — coordination stub

**Status:** stub. Not functional. Coordination placeholder so the
dx-agent and the pixie-side viz work can converge on a schema and a
behaviour before either side ships code.

## What this script will be

A Pixie UI dashboard that replaces the latency-weighted HTTP service
map in `cluster_overview` with a **severity-weighted, all-protocol
pod-to-pod graph** built from dx-agent evidence.

* Nodes = pods.
* Edges = any observed pod→pod hop in the window (HTTP, gRPC, DNS,
  Kafka, MySQL, PgSQL, raw TCP) sourced from `conn_stats` (so the
  result is protocol-agnostic by construction).
* Edge weight = severity contribution from dx evidence whose pod
  participates in the edge.
* Display spec: `vispb.Graph` with `edgeWeightColumn=weight`,
  `edgeColorColumn=weight` — same primitive as `net_flow_graph`,
  not the HTTP-only `RequestGraph`.

## Why a stub PR

The dx-agent is building the evidence data model right now. The
pixie-side script needs to know:

1. Where the evidence sits at query time (Pixie table vs ClickHouse
   vs script-arg). Path B in the plan keeps it as script-arg for v1;
   Path A migrates to a Pixie table in v2.
2. The exact fields available per evidence row.
3. How severity is encoded.

This file is the contract. Update it as decisions land; the `.pxl`
and `vis.json` follow once the contract is firm.

## Schema contract (proposed — open for dx-agent input)

What the pixie script needs per evidence record:

| Field | Type | Required | Used for |
|---|---|:---:|---|
| `time_` | TIME64NS | yes | window anchor |
| `pod` | STRING (`namespace/pod`) | yes | node identity |
| `upid` | UINT128 | optional | fallback if pod name not yet resolved |
| `severity` | INT64 | yes | edge weight + node colour |
| `criterion` | STRING (e.g. `R0002`) | yes | filter, hover text |
| `source` | STRING (`kubescape` / `pixie`) | yes | filter |
| `confidence` | FLOAT64 (0..1) | optional | tooltip only in v1 |
| `raw` | STRING (JSON blob) | optional | drill-down on click in v2 |

Field names match `dx/internal/vectors/Finding` and
`dx/internal/symptom/Verdict.Severity` from the dx repo. If dx
emits something differently I will rename rather than fight it —
this table is a proposal, not a demand.

## Where evidence comes from at query time

Two paths (full reasoning in `/home/constanze/dx-evidence-graph-PLAN.md`):

* **Path B (v1, no Pixie changes):** the script takes evidence as
  arguments — one pod + one severity per invocation, or a
  comma-separated list of `pod:severity` pairs. The dx UI (or a
  Slack alert link) deep-links into Pixie's URL with these args
  filled in. Ships fast.
* **Path A (v2):** dx-agent (or AE) writes evidence into a Pixie
  table `dx_evidence` whose schema matches the contract above. PxL
  script joins `dx_evidence` × `conn_stats` directly. Self-serve.

v1 ships first to validate the visual; the contract above is forward
compatible to v2.

## Open decisions — please weigh in

| # | Question | Default I'd pick |
|---|---|---|
| 1 | Edge severity inheritance: A→B with only B flagged — full / half / zero? | full |
| 2 | Time anchor: relative to evidence.T ± window, or free-form start/end? | anchor ± 2 min, free-form fallback |
| 3 | Hop depth cap from the evidence pod? | 2 (`pod-to-pod-to-pod` = neighbourhood-of-2) |
| 4 | Aggregating multiple evidence items on one edge: sum, max, both? | sum for weight, max for colour |
| 5 | Script placement: upstream `src/pxl_scripts/px/`, or private `dx/scripts/`? | this PR assumes upstream; reversible |

Any of these dx-agent answers differently → flip the default in this
file, not anywhere else; the .pxl reads from this contract.

## Open questions for dx-agent (data model side)

* Is `severity` stable across kubescape rule revisions, or do we need
  a per-criterion normaliser?
* Will dx emit evidence per upid (process) or per pod (rollup)? The
  pixie script can do either — but only one. Confirm.
* Does dx emit a "chain" record (multiple findings stitched into one
  Diagnosis), or one row per `vectors.Finding`? If a chain, we need
  a `diagnosis_id` foreign key.
* For Path A: would dx push into a Pixie table via a new Stirling
  source connector, via the AE adaptive_export sink, or via the
  standalone-pem data-ingestion gRPC?

## What lands in this PR

* This README — the contract above.
* `dx_evidence_graph.pxl` — stub with TODO markers naming the
  unresolved schema fields. Not runnable.
* `vis.json` — stub mapping `edgeWeightColumn=weight`,
  `edgeColorColumn=weight` against a placeholder table. Not runnable.

No working code until decisions 1-5 are settled. Once they are, v1
is ~1-2 days of work; replacement of `cluster_overview` is a follow-up.
