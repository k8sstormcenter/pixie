# DX Evidence Graph — 3-level zoom (`dx/evidence_graph`)

A **standalone** Pixie Live View bundle (PxL + `vis.json`, no Pixie UI source
changes) that renders the dx evidence graph in Pixie's existing `GraphWidget` and
lets an analyst zoom from an investigation down to the individual forensic rows dx
consulted.

## What it shows — the three levels

| Level | Widget | PxL func | Content |
|------|--------|----------|---------|
| 1 | Graph | `evidence_graph` | Severity-weighted, all-protocol **pod → pod** edge list for the malignant (ruled-in) investigations. Edge weight = `confidence`, colour = `max_severity`, label = `edge_kind`, hover = investigation_id / condition / criteria / num_findings. |
| 2 | Table | `investigation_detail` | The **manifest** row(s) for the zoomed investigation: `verdict`, `condition`, `confidence`/`posterior`, case-window bounds (`win_lo`/`win_hi`, plucked from the `case_window` JSON), `evidence_hash`, raw `findings`. |
| 3 | Table | `consulted_rows` | The **§H reconstruction**: the raw `dc_snoop` (default) process rows for the alert pod in the window — the individual rows dx consulted. Repoint `raw_table` at `redis_events` / `kubescape_logs` for the other §H tables. |

## The drill-down model

- **Pod-node double-click → `px/pod` (built-in, no code change).** `evidence_graph`
  stamps the `from_entity`/`to_entity` node columns with the pod semantic type via
  `px.Pod(...)` (registered as a `STRING → ST_POD_NAME` cast in
  `src/carnot/planner/objects/pixie_module.cc:560`). The GraphWidget's built-in
  `doubleClickCallback` → `deepLinkURLFromSemanticType` (`graph.tsx` ~line 170) then
  deep-links any `ST_POD_NAME` node to `px/pod`. We rely on that path; nothing under
  `src/ui` is modified. Caveat: the column is stamped pod-typed even when an endpoint
  resolves to a service/IP (pod > service > ip fallback), so a non-pod node double-click
  deep-links to `px/pod?pod=<value>` — harmless, and in the demo every endpoint is a pod.
- **Investigation zoom = the `investigation_id` script var.** Copy an `investigation_id`
  from a graph-edge hover into the `investigation_id` variable (and set `pod_filter` to
  the alert pod). Levels 2 and 3 re-run scoped to that investigation. `investigation_filter`
  independently narrows the graph itself.

## How to load it into a running Pixie UI (no rebuild)

This is a self-contained scripts bundle — deploy it without touching the UI:

1. **Custom Live View (fastest).** In the Live UI, open the script editor (the
   `</>` **Scratch Pad** / "Edit script" pane), paste `evidence_graph.pxl` into the
   **PxL** tab and `vis.json` into the **Vis Spec** tab, set the variables
   (at minimum `clickhouse_dsn`), and Run.
2. **Bundled script.** The directory (`evidence_graph.pxl` + `vis.json` +
   `manifest.yaml`) is globbed into `bundle-oss.json` by
   `src/pxl_scripts/BUILD.bazel` (the `**/*.pxl|json|yaml` filegroup), so it ships as
   the script id **`dx/evidence_graph`** wherever that bundle is served. No registry
   edit is required.
3. **`px` CLI.** `px run -f evidence_graph.pxl` (table output) for a non-UI smoke test.

Set the `clickhouse_dsn` variable to your forensic_db DSN (default:
`ingest_writer:changeme-ingest@clickhouse-forensic-soc-db.clickhouse.svc.cluster.local:9000/forensic_db`).

## Feasibility: can a PxL Live View read ClickHouse via `clickhouse_dsn`? — **YES**

Confirmed against the fork, not assumed:

- `px.DataFrame(table, clickhouse_dsn=..., start_time=...)` is a first-class reader:
  the arg is registered on the DataFrame op
  (`src/carnot/planner/objects/dataframe.cc:189-197,558-559`) and executed by the PEM
  through `ClickHouseSourceNode` (`src/carnot/exec/clickhouse_source_node.cc`).
- It is **already in production use** by the shipped `px/dx_evidence_graph` bundle
  reading these exact tables, and `schema.sql` documents the tables as *"read by the
  Pixie dx_evidence_graph UI via px.DataFrame(clickhouse_dsn=...)"*.
- The reader maps `String / Int8..64 / UInt8..64 / Float32/64 / DateTime / DateTime64`
  → Pixie types (`clickhouse_source_node.cc:112-370`). Every column projected here is
  in that set.

So this bundle is built directly on `clickhouse_dsn`. **No alternative execution path
is needed.**

### Real constraints (documented, not blockers)

1. **Templated read, not arbitrary SQL.** The reader issues
   `SELECT … FROM <table> WHERE <ts_col> >= … [AND hostname = <PEM host>] ORDER BY <ts_col> LIMIT …`.
   It **cannot** run the `JSONExtract*` / `ARRAY JOIN` SQL from demo.md §H. The bundle
   therefore re-implements the reconstruction in PxL: JSON columns
   (`case_window`, `findings`) are parsed with `px.pluck_int64` / `px.pluck`, and
   row-scoping is done with PxL filters (`px.contains`) instead of a SQL join.
2. **`hostname` partition filter.** When a `hostname` column exists the reader appends
   `AND hostname = <the executing PEM's hostname>` (`clickhouse_source_node.cc:429-434`).
   Rows are only visible from the PEM whose host wrote them — a multi-node caveat.
   `dc_snoop` is node-scoped so this is expected; for the dx tables ensure the reading
   PEM matches the writing host (the shipped `px/dx_evidence_graph` operates under the
   same rule).
3. **`start_time` is a no-op on nanosecond `event_time` tables.** The reader converts
   `start_time` ns→seconds (`clickhouse_source_node.cc:69-75`) and compares it to
   `event_time`. For tables whose `event_time` is `UInt64` **nanoseconds**
   (`dx_evidence_graph`, `dx_evidence_manifest`, `kubescape_logs`) the seconds-scale
   threshold is always ≤ the nanosecond values, so **all** in-TTL rows return (no time
   narrowing — bounded by the 30-day TTL + `LIMIT`). On `dc_snoop` (`DateTime64(9)`)
   `start_time` filtering does apply. This is why Level 3's exact window is read from
   the **manifest** (`win_lo`/`win_hi`, Level 2) rather than from `start_time`.

## Needs live-UI validation on a cluster with the dx evidence tables

Statically validated here: `vis.json` is valid JSON; every variable is referenced by
a `globalFunc`; every widget binds to a declared `globalFunc`; func names and per-arg
names match the PxL signatures; projected columns exist in `schema.sql`; the `px.Pod`
/ `px.pluck_int64` / `px.contains` builtins used all exist in the fork.

Cannot be run against a live PEM from here — validate on a cluster carrying
`forensic_db` (e.g. a PG with the SOC stack + AE):

- All three funcs **compile and execute** through the PEM's `clickhouse_dsn` reader.
- `px.Pod(...)` produces `ST_POD_NAME` nodes and a **double-click deep-links to `px/pod`**.
- `px.pluck_int64(case_window, 'lo'|'hi')` returns the correct window bounds (Level 2).
- Level 3 `raw_table` swaps (`redis_events`, `kubescape_logs`) project without a
  column-name error (their schemas differ from `dc_snoop`; adjust the projection if so).
- Graph rendering: `edgeColorColumn`/`edgeThresholds` colour by `max_severity`, hover
  shows the investigation fields.
