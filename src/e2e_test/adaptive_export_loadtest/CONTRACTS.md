# Adaptive Export (AE) — implied contracts

What AE *currently assumes but does not enforce*. Each ⚠️ is an **implied** contract
(a silent assumption); 🔴 marks ones we've observed violated, with the fix. Grounded
in `src/vizier/services/adaptive_export/` (trigger, controller, sink, config) + the
`forensic_db` DDL.

## End-to-end data flow + where each contract sits

```mermaid
flowchart TD
    subgraph PROD["Producer (per node)"]
      VEC["Vector kubescape_enrich sink<br/>(or load-test fixtures)"]
    end
    subgraph CH1["ClickHouse — input"]
      KL["forensic_db.kubescape_logs<br/>MergeTree ORDER BY (event_time, hostname)<br/>TTL toDateTime(event_time)+30d"]
    end
    subgraph AE["adaptive_export (per node DaemonSet)"]
      TRG["TRIGGER: poll 250ms<br/>WHERE hostname=NODE AND event_time>=watermark<br/>ORDER BY event_time LIMIT N"]
      CTL["CONTROLLER: hash + active-set<br/>window [event_time-Before, now)"]
      PXL["DATA-PLANE: PxL per (ns,pod)×table<br/>refresh every 30s while window open"]
    end
    subgraph VZ["Pixie"]
      QB["vizier-query-broker → PEMs"]
    end
    subgraph CH2["ClickHouse — output (forensic_db)"]
      ATTR["adaptive_attribution<br/>ReplacingMergeTree(t_end)<br/>ORDER BY (hostname, anomaly_hash)"]
      WM["trigger_watermark<br/>ReplacingMergeTree(updated_at)"]
      PROT["http/dns/pgsql/conn_stats/...<br/>plain MergeTree (NO dedup)"]
    end

    VEC -->|"C1 ✅ event_time UNIT = nanoseconds<br/>C2 ⚠️ hostname = k8s node name"| KL
    KL -->|"C3 🔴 event_time monotone ≥ watermark<br/>C4 ⚠️ boundary dedup by content fp"| TRG
    TRG --> CTL
    CTL -->|"C5 ⚠️ anomaly_hash = f(pid,comm,pod,ns) only"| ATTR
    TRG -->|"C6 ⚠️ watermark persist throttled ~5s"| WM
    CTL --> PXL
    PXL -->|"C7 needs registered vizier"| QB
    QB -->|"C8 🔴 plain MergeTree + 30s re-pull → dup"| PROT
    PXL -->|"C9 ⚠️ write only if rows>0"| PROT
    ATTR -. "C10 ⚠️ join: events.pod = ns/pod  ↔  attribution.pod = bare" .- PROT
```

## Boot / dependency contract

```mermaid
flowchart LR
    ENV["ENV (all non-empty or FATAL):<br/>PIXIE_CLUSTER_ID · CLUSTER_NAME<br/>PIXIE_API_KEY · CLICKHOUSE_DSN"] --> BOOT
    CM["cm/pl-cloud-config<br/>PL_CLOUD_ADDR=…:443"] -->|"C11 🔴 missing :443 → crashloop"| BOOT
    BOOT["AE boot"] --> DDL["C12 self-applies forensic_db DDL<br/>(ADAPTIVE_SKIP_APPLY=false)"]
    BOOT --> CTRLPLANE["control plane: CH only"]
    BOOT --> DATAPLANE["data plane: needs query-broker<br/>(C7) + ADAPTIVE_PUSH_PIXIE_ROWS"]
```

## Contract register

| # | Contract (implied) | Enforced? | Status / fix |
|---|---|---|---|
| C1 | `kubescape_logs.event_time` is unix **nanoseconds** (one unit end-to-end) | ✅ DDL converts with `fromUnixTimestamp64Nano`; trigger keeps magnitude-normalization as a defensive net | Vector emits ns; DDL + harness aligned to ns (was the F1/F8 seconds-vs-ns root) |
| C2 | `hostname` = the k8s **node** name (AE polls `WHERE hostname=node`) | ❌ convention only | ⚠️ fixtures must use a real node, else no AE ever reads them |
| C3 | every new anomaly's `event_time` ≥ current watermark (monotone) | ❌ strict HWM filter | 🔴 **F8** — a larger-unit / out-of-order / future row poisons the HWM → all later rows silently dropped. **Fix (PR #53):** normalize cursor to nanos (`chNormEventTimeNanos`); AE-9: ingest-order cursor / bounded-lookback+dedup + below-watermark metric |
| C4 | rows sharing `event_time` at the boundary are deduped by content fingerprint | ✅ `seenAtBoundary` | ok |
| C5 | `anomaly_hash = SHA256(pid,comm,pod,ns)[:16]` — identity is the **workload**, independent of event_time/RuleID | ✅ | ok (N events for one target → 1 attribution row) |
| C6 | `trigger_watermark` persisted value tracks the live cursor | ❌ throttled ~5s | ⚠️ external readers/restart see up to 5s stale; AE-7 flush-on-shutdown |
| C7 | data-plane requires a **registered** vizier query-broker | ❌ | ⚠️ control plane works without it; data plane silently does nothing |
| C8 | re-pulling a window is idempotent | ❌ protocol tables plain MergeTree (no dedup) + 30s re-pull | 🔴 duplicate inflation. **Fix:** single-shot (`ADAPTIVE_PUSH_REFRESH_SEC=-1`, or `AFTER<refresh`); AE-6 ReplacingMergeTree protocol tables |
| C9 | a protocol table row is written only if Pixie returned ≥1 row | ✅ `WritePixieRows len==0 → nil` | ok (empty workload → 0 rows, by design) |
| C10 | join key: `events.pod` = `"ns/pod"` (upid_to_pod_name) vs `adaptive_attribution.pod` = **bare** pod | ❌ asymmetric | ⚠️ consumers must `concat(namespace,'/',pod)` to join (burned the volume tool) |
| C11 | `PL_CLOUD_ADDR` carries `:443` | ❌ | 🔴 missing → AE crashloops / 0 writes (per-PG fix) |
| C12 | AE owns + self-applies the `forensic_db` DDL | ✅ when `ADAPTIVE_SKIP_APPLY=false` | ok; but DDL TTL/PARTITION assume seconds (C1) |
| C13 | `adaptive_attribution` / protocol writes are durable | ❌ best-effort: logged, non-fatal, **not retried** | 🔴 silent loss under CH hiccup; AE-4 retry+count |
| C14 | **DX⊇AE invariant**: AE write-set ⊇ DX read-set (AE persists everything dx queries) | ❌ by convention | ⚠️ validated per-table in the load-test, not enforced in code |
| C15 | **Write-duration (the one DX steers on):** once an anomaly opens a pod's window, AE **keeps re-pulling + writing that pod's forensic data continuously** until `t_end` expires OR DX explicitly stops it. `t_end = now + After`, extended by each new anomaly for the hash. | ❌ partial | 🔴 **last week's "wrote then stopped" bug.** Premature stop modes under investigation (E8-data RCA): (a) F8 — extension anomalies dropped → `t_end` not extended → expires early; (b) EmptyResultSkip negative cache skips a (pod,table) mid-window after N empty pulls; (c) prune/in-flight race; (d) my `PUSH_REFRESH=-1` single-shot is a TEST affordance that *violates* this contract (writes once) — production must re-pull. |

## DX steering contract (what DX can rely on / control)

```mermaid
sequenceDiagram
  participant DX
  participant AE
  participant Pixie
  participant CH as forensic_db
  Note over AE: anomaly (or DX referral) opens window [t_start, t_end=now+After]
  loop every PushRefreshInterval until t_end OR DX stop  (C15)
    AE->>Pixie: PxL per table for (ns,pod), slice since last_upper
    Pixie-->>AE: rows
    AE->>CH: write rows (write ⊇ DX read, C14)
  end
  DX->>AE: StartExport / StopExport / extend t_end  (control surface, CONTROL_ADDR)
  Note over AE: stop ONLY on t_end or DX stop — never silently early (C15)
```

- **DX controls:** (1) open/extend a window (each referral/anomaly extends `t_end`), (2) explicit **StopExport** via the control surface (`CONTROL_ADDR`, design rev-3 — confirm wired), (3) the active set (which pods AE over-captures).
- **DX relies on:** C5 (stable hash identity), C14 (write ⊇ read), **C15 (no premature stop)**, C9 (0 rows only when the workload is genuinely silent), C10 (the `ns/pod` ↔ bare join). For DX to steer dependably, C3/C8/C13/C15 must move from 🔴 to ✅.

## Legend
✅ enforced in code · ⚠️ implied (assumed, not checked) · 🔴 observed violated (fix noted).
Full repro + backlog: `FINDINGS_AND_BACKLOG.md`. The fixes for C3/C1 are on PR #53 (`ae-prod`).
