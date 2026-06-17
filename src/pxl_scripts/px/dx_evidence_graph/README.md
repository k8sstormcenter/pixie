# dx_evidence_graph

A Pixie UI dashboard that renders one dx-agent investigation as a
**severity-weighted, all-protocol pod-to-pod attack graph**. Replaces
the latency-weighted HTTP service map in `cluster_overview` for
security work.

* Nodes = pods. Falls back to service → IP, mirroring `net_flow_graph`.
* Edges = the attack path emitted by dx (delivery → egress →
  execution → collection → exfil → pivot).
* Display spec: `vispb.Graph`. **`edgeWeightColumn = weight`**
  (open-ended UInt16 sum of CRS severity → edge thickness),
  **`edgeColorColumn = max_severity`** (discrete 2-5 heat → edge
  colour).
* Read source: `forensic_db.dx_attack_graph` via `px.DataFrame`'s
  `clickhouse_dsn` kwarg (`src/carnot/planner/objects/dataframe.cc:43`).

## Schema — `forensic_db.dx_attack_graph`

Locked with dx-agent in PR #62 / `entlein/dx#68`. The
`attackgraph.Edge` Go struct is the single source of truth for the
JSON wire format, the ClickHouse row, and the test fixture.

| Column | Type | Role |
|---|---|---|
| `investigation_id` | String | one graph per dx verdict / pivot incident (UI filter key) |
| `ts` | UInt64 | unix nanos |
| `requestor_pod` / `responder_pod` | String | the hop (`ns/pod`); `""` if only an IP is known |
| `requestor_service` / `responder_service` | String | |
| `requestor_ip` / `responder_ip` | String | peer IP when pod unresolved |
| `weight` | UInt16 | Σ CRS severity on the hop — `edgeWeightColumn` |
| `max_severity` | UInt8 | top single-criterion severity (2-5) — `edgeColorColumn` |
| `confidence` | Float32 | verdict confidence |
| `edge_kind` | String | `delivery`/`egress`/`execution`/`collection`/`exfil`/`pivot` |
| `condition` / `criteria` | String | ruled-in condition + criterion label(s) |
| `num_findings` | UInt32 | |

Table DDL (mirrors `kubescape_logs` partition/TTL convention):

```sql
CREATE TABLE forensic_db.dx_attack_graph ( ...columns above... )
ENGINE = MergeTree
PARTITION BY toYYYYMM(fromUnixTimestamp64Nano(ts))
ORDER BY (investigation_id, requestor_pod, responder_pod)
TTL toDateTime(fromUnixTimestamp64Nano(ts)) + INTERVAL 30 DAY DELETE;
```

## Per-rig ClickHouse DSN

The bundled `vis.json` ships with `clickhouse_dsn` **empty** — the
default is intentionally non-credentialed so the bundle stays
portable across clusters. Operators fill the DSN in via the Pixie
UI script-args panel at run time.

For the in-cluster soc deployment the DSN is:

```
forensic_analyst:changeme-analyst@clickhouse-forensic-soc-db.clickhouse.svc.cluster.local:9000/forensic_db
```

`forensic_analyst` has read-only SELECT on `forensic_db`; same
credential the existing `soc/analysis/px_clickhouse/kubescape/observe.pxl`
script uses for `kubescape_logs`. Override in the UI for other rigs.

## Manual-load prototype

`tools/load_prototype/` is a Go helper that renders the `Edge`
schema from a JSON fixture into a standalone HTML page using
cytoscape.js. Same column→visual mapping the production
`vispb.Graph` spec uses. Useful when ClickHouse isn't reachable
from the UI (offline review, fixture validation).

```bash
go run ./tools/load_prototype \
    -fixture fixtures/sample.json \
    -investigation_id log4shell-6a32ea57 \
    -out /tmp/dx_log4shell.html
```

The fixture in `fixtures/sample.json` is dx-agent's real
log4shell + argocd verdicts from the rig run that locked the
schema. `fixtures/screenshots/dx_log4shell.html` and
`fixtures/screenshots/dx_argocd.html` are the pre-rendered pages
for review without running the tool.

The tool retires once the AE live-write (`WriteAttackGraph` →
`forensic_db.dx_attack_graph`) is on every cluster running this
bundle.

## Deploy

Bundle build path:

1. `//src/pxl_scripts:script_bundle` walks every `*.pxl` + `vis.json`
   under `src/pxl_scripts/` and emits `bundle-oss.json`
   (`src/pxl_scripts/BUILD.bazel:34`).
2. `//src/cloud/proxy:proxy_server_image` bakes the bundle in as a
   container layer at `/bundle`
   (`src/cloud/proxy/BUILD.bazel:36`).
3. `skaffold run -f skaffold/skaffold_cloud.yaml` rebuilds the
   cloud-proxy image and applies the Deployment.

Vizier / PEM / standalone-pem images are unaffected — this is a
UI-bundle-only change.

## Out of scope for v1

* `conn_stats` overlay (the "render the benign neighbourhood + light
  up the attack path" view). Ship the attack-path-only graph first;
  add the join in v2 once the visual has been used on a real
  incident.
* Time anchoring relative to `ts` rather than free-form `start_time`.
  Operators today use `-15m` defaults; a future widget could centre
  the window on the investigation's first `ts`.
