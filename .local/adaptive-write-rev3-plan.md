# Adaptive Write rev-3 — node-local streaming design

## Architectural pivot

| Dimension | rev-2 (today) | rev-3 (proposed) |
|---|---|---|
| **Data motion** | operator PULLs pixie data on schedule per hash per table (N×10 PxL queries every 30 s) | pixie PUSHes data continuously to operator via N long-lived streams (10 total per node) |
| **Cloud contact** | every PxL query goes through cloud passthrough OR direct-mode gRPC, per hash per table | one-time at boot to enable plugin; then zero cloud chatter |
| **Concurrency** | `O(active_hashes × tables)` goroutines, unbounded by design | `O(tables)` goroutines per node, regardless of hash count |
| **Stop semantics** | each `pushPixieRows` loop independently re-decides whether to keep pulling | one decision plane (active_set); plugin streams while the set is non-empty for that pod |
| **Failure under latency** | every PxL hit DeadlineExceeded → entire fan-out for that hash misses | long-lived streams absorb latency; only NEW activations need a fresh PxL submission |
| **CH writes** | per-hash, per-table, per-pass → many small batches | per-stream batched writes → fewer, larger batches |

## Core invariant

The first kubescape anomaly for a workload creates an **ACTIVE** entry in `adaptive_attribution`. That entry is "alive" until `t_end` is in the past *and* no new anomaly extends it. While alive, a node-local stream from pixie continuously emits that workload's protocol-table rows into CH. When it dies, the stream stops emitting for that workload (filter excludes it).

There is **no second polling loop**. The stream is the data path; the active-set is just a filter applied to that stream.

## Components

```
┌─────────────────────────── adaptive_export pod (per node) ──────────────────────────┐
│                                                                                      │
│  ┌────────────────┐    ┌────────────────────┐    ┌─────────────────────────────┐   │
│  │ Trigger        │───►│ AttributionMgr     │───►│ ActiveSet (in-mem + CH)     │   │
│  │ (1 goroutine)  │    │ (1 goroutine)      │    │  pod|namespace → t_end      │   │
│  │ polls          │    │ maintains active   │    │  + version counter          │   │
│  │ kubescape_logs │    │ map + writes       │    │  fan-out broadcast on Δ     │   │
│  └────────────────┘    │ adaptive_attribution│    └────────────┬────────────────┘   │
│         (same as       └────────────────────┘                  │                    │
│          today)                                                ▼                    │
│                                                ┌───────────────────────────┐       │
│                                                │ StreamSupervisor (1)      │       │
│                                                │ owns N table streams      │       │
│                                                │ pushes filter updates     │       │
│                                                └────┬──────────────────────┘       │
│                                                     │ filter updates                │
│                       ┌─────────────────────────────┴──────────────┐                │
│                       ▼                                             ▼                │
│  ┌───────────────────────────────┐         ┌───────────────────────────────┐       │
│  │ TableStream[http_events]      │ . . . . │ TableStream[pgsql_events]     │       │
│  │ (1 goroutine, long-lived gRPC)│         │ (1 goroutine, long-lived gRPC)│       │
│  │ → vizier-query-broker LOCAL   │         │ → vizier-query-broker LOCAL   │       │
│  │ → CH batched writer           │         │ → CH batched writer           │       │
│  └───────────────────────────────┘         └───────────────────────────────┘       │
│           × 10 tables                                                                │
│                                                                                      │
│  ┌────────────────┐         ┌────────────────┐         ┌────────────────┐          │
│  │ Pruner         │         │ WMPersist      │         │ Healthcheck    │          │
│  │ (1 goroutine)  │         │ (1 goroutine)  │         │ (1 goroutine)  │          │
│  │ evicts dead    │         │ trigger        │         │ stream liveness│          │
│  │ from ActiveSet │         │ watermark      │         │ + restarts     │          │
│  └────────────────┘         └────────────────┘         └────────────────┘          │
│                                                                                      │
└──────────────────────────────────────────────────────────────────────────────────────┘
```

### 1. Trigger (unchanged)
- Polls `kubescape_logs` for this node's rows.
- Same persistent watermark, LIMIT, partial-read tolerance as today.
- Emits `kubescape.Event` to AttributionMgr via buffered channel.
- **1 goroutine. Bounded I/O.**

### 2. AttributionMgr (current controller, slimmed)
- Reads from trigger's channel.
- Maintains in-memory `active` map (as today).
- Writes `adaptive_attribution` rows to CH (as today).
- **Difference vs today**: instead of spawning `pushPixieRows`, it publishes the activation/extension/expiry to ActiveSet.
- **1 goroutine. O(events/sec) work.**

### 3. ActiveSet (NEW, in-memory + CH-backed)
- Authoritative list of `(pod, namespace) → t_end` pairs currently being streamed.
- Two interfaces:
  - `Upsert(pod, namespace, t_end)` — called by AttributionMgr per event.
  - `Subscribe() <-chan Delta` — called by StreamSupervisor; emits `{added, removed}` deltas.
- Internally maintains a monotonic `version` counter; consumers re-fetch the full set when they see a version bump.
- Periodically reconciled with `adaptive_attribution FINAL` to recover from process restart.
- **0 dedicated goroutines.** Pure mutex-guarded state + channels.

### 4. StreamSupervisor (NEW)
- Owns the `len(PushPixieTables)` = 10 `TableStream`s.
- Subscribes to ActiveSet deltas; pushes a **filter update** message to every TableStream on change.
- A TableStream handles its own re-submission on filter change (no global re-spawn).
- **1 goroutine. O(deltas/sec) work — usually << 1 Hz.**

### 5. TableStream[T] (NEW, the load-bearing piece)
- One per pixie table (10 total).
- Holds ONE long-lived gRPC stream to the **local** vizier-query-broker (`vizier-query-broker-svc.pl.svc.cluster.local:50300` via direct-mode JWT — already implemented).
- PxL script shape:
  ```python
  import px
  df = px.DataFrame(table='<T>', start_time='-60s')   # bounded window
  df.namespace = px.upid_to_namespace(df.upid)
  df.pod = px.upid_to_pod_name(df.upid)
  df = df[df.pod.in_(['ns1/pod1', 'ns2/pod2', ...])]  # active set whitelist
  px.display(df, '<T>')
  ```
- **Re-submitted only when the active set changes or every 60 s** (whichever sooner) — bounds the staleness of the whitelist embedded in PxL.
- Receives results in batches; forwards to a **per-table batched CH writer** (target: 1 INSERT per 5 s or per 10 k rows, whichever first).
- **1 goroutine per table = 10 per node, regardless of active hash count.**
- Empty-active-set is a special case: skip submission entirely (`if len(filter) == 0: sleep until delta`). This is the rev-3 form of the `empty_skip` knob — automatic, not hand-tuned.

### 6. Pruner (unchanged)
- Periodic timer: evict `active[hash]` whose `t_end + grace` is past.
- Triggers an ActiveSet delta (removal).
- **1 goroutine.**

### 7. WMPersist (encapsulated in trigger today; can stay there)
- Throttled watermark INSERT.

### 8. Healthcheck (NEW)
- Per-table stream liveness probe: did this TableStream emit a record OR fail in the last `N` seconds?
- Restarts a dead stream (idempotent because the PxL is re-submittable).
- **1 goroutine.**

## Lifecycle of one anomaly window

```
t=0:    kubescape alert for pod=ns1/pgsql-server
        → Trigger emits Event
        → AttributionMgr writes adaptive_attribution row (t_end = t0 + 5min)
        → AttributionMgr.Upsert("ns1/pgsql-server", t0+5min) on ActiveSet
        → ActiveSet emits delta {added: "ns1/pgsql-server"}
        → StreamSupervisor pushes filter update to all 10 TableStreams
        → TableStream[pgsql_events] re-submits PxL with new whitelist
        → vizier-query-broker accepts, starts streaming pgsql_events for that pod
        → batches flow into CH via TableStream's writer

t=30s:  more kubescape alerts for same pod
        → AttributionMgr extends t_end in-place; NO ActiveSet delta (set is unchanged)
        → TableStreams keep streaming; no broker chatter

t=5min:  no fresh anomaly; Pruner evicts
        → ActiveSet emits delta {removed: "ns1/pgsql-server"}
        → StreamSupervisor pushes filter update
        → TableStreams re-submit PxL with shorter whitelist
        → pgsql rows for that pod stop arriving
```

## Goroutine inventory + scaling

| Goroutine | Count formula | Per-node count for 100 active hashes |
|---|---|---|
| Trigger | 1 | 1 |
| AttributionMgr | 1 | 1 |
| StreamSupervisor | 1 | 1 |
| TableStream | `len(tables)` = 10 | 10 |
| Per-table CH writer (inside TableStream) | 1 each | 10 |
| Pruner | 1 | 1 |
| Healthcheck | 1 | 1 |
| **Total per node** | **constant** | **~25** |

Compare today: `1 + 1 + 1 (prune) + active_hashes × 10` = **1,003 goroutines for the same load**, each holding a separate gRPC connection.

## Scaling characteristics

| Variable | rev-2 behavior | rev-3 behavior |
|---|---|---|
| **Active hashes ↑** | quadratic broker pressure (`N × 10` streams × 30s re-submit) | constant — whitelist gets longer, stream count unchanged |
| **Anomalies/sec ↑** | linear pressure on attribution sink (same in rev-3) | unchanged (this path is the same) |
| **High pixie latency** | every `pushPixieRows` pass hits 180s timeout; full reset | stream tolerates latency natively; no per-pass retry storm |
| **CH unreachable transiently** | per-hash retries pile up; goroutines accumulate | per-table writer queues; bounded buffer; backpressure |
| **Operator OOM-restart** | watermark recovery (already in place) + cold-start of every active hash's pushPixieRows | watermark recovery + StreamSupervisor reads ActiveSet from `adaptive_attribution FINAL` and re-submits 10 streams |

## Failure modes (and how rev-3 handles them)

1. **vizier-query-broker dies**: Healthcheck observes no records + stream error → triggers re-submit on a backoff (e.g. 1s, 2s, 5s, …). All 10 streams independently. Active set unaffected.
2. **CH unreachable**: per-table writer's bounded buffer fills → drops oldest, increments a metric (`ae_dropped_rows{table=…}`). Stream continues consuming so backpressure doesn't propagate into the broker.
3. **ActiveSet grows huge** (e.g. cluster-wide attack): filter list inside the PxL grows. PxL has a string-length limit; we'd cap the whitelist at e.g. 500 pods and emit a warning. Beyond that we'd switch to a "no filter" mode (stream everything).
4. **Stream stuck without errors** (silent hang): Healthcheck's "no records in 60s" trigger forces a re-submit.
5. **Operator restart**: same recovery as today — watermark + adaptive_attribution rehydrate.

## What we'd rip out

- All of `pushPixieRows` + the per-hash goroutine spawn in `controller.handle`
- All three throttle knobs (`MaxParallelQueriesPerHash`, `MaxInflightQueriesGlobal`, `EmptyResult*`) — no longer needed
- The `inFlight` map (no longer needed; stream lifecycle is centrally managed)
- The negative-cache (no longer needed; empty-set short-circuit is automatic)

## What we'd keep

- The trigger + persistent watermark (rev-2's biggest real win)
- The attribution sink (`Sink.Write` for `adaptive_attribution`)
- DDL + `Rehydrate` (still needed for ActiveSet startup)
- pixieapi direct-mode (still the transport for the 10 streams)

## Open design decisions for you

1. **Filter granularity in PxL**: `(pod, namespace)` whitelist vs `pod_uid` whitelist vs `upid` whitelist? Pod-name whitelist is simplest but stale if a pod gets recreated (the new pod's traffic would leak through). UID-based is correct but requires `px.pod_uid_to_pod_name` or similar.
2. **PxL re-submission cadence**: stream is logically "forever", but we re-submit on filter changes + periodically (60 s?) to bound staleness. Tradeoff: too frequent = broker chatter; too rare = up-to-60s lag for a new anomaly to start streaming its pod's data.
3. **Per-table CH batch size**: 10 k rows / 5 s is a guess. Larger batches → fewer INSERTs but worse latency-to-CH.
4. **What happens when active_set is permanently large** (e.g. all 100 pods in cluster have anomalies during an incident)? Do we cap and shed? Or fall back to no-filter "stream everything"?

## Migration path

Rev-3 can ship alongside rev-2 behind a feature flag (`ADAPTIVE_WRITE_MODE=streaming` vs `pull`). Both consume the same trigger + sink; only the protocol-table path differs. Validate side-by-side, then delete the rev-2 pull path.
