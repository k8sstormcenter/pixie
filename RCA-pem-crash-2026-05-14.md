# RCA — PEM SIGABRT under load ("MapNode crash" false lead)

**Date:** 2026-05-14
**Author:** pixie-agent (local k3s perf sweep on `pixie-worker-node-constanze`)
**Severity:** terminal — every PEM crash dumps the whole agent's in-flight queries, the recorder reports "Distributed state does not have a Carnot instance", and perf_tool aborts after `--max_retries` are exhausted. Observed **32 restarts in one 5-hour sweep** at 16× load.

## TL;DR

- **Symptom:** PEM exits with `code=134` (SIGABRT) under sustained load (16× and up; one occurrence at 4× too).
- **Initial wrong hypothesis:** Carnot `MapNode::ConsumeNextImpl` is the culprit — based on the C++ stack trace dumped at abort time. False: that stack belongs to a *different* thread that happened to be running a recorder query when the abort fired. The OS dumps all thread stacks on SIGABRT.
- **Real cause:** **a Stirling-side `CHECK_OK`** in `SourceConnector::PushData()` aborts when one drained row-batch exceeds the per-table memory budget. Default budget is **47.25 MiB per "other" protocol table** (computed from `PL_TABLE_STORE_DATA_LIMIT_MB=1280` divided across ~16 protocol tables). At 16× the redis_events batch reached **88.83 MiB** — almost 2× the limit.
- **The actual log line** (which `kubectl logs --previous` truncates away under default tailing — has to be read directly from `/var/log/pods/.../pem/<N>.log`):
  ```
  F20260514 15:43:18.679255 2365376 source_connector.cc:64]
  Failed to push data. Message = RowBatch size (88835909) is bigger than maximum table size (49545216).
  *** Check failure stack trace: ***
  ```
- **The crash is unique to debug builds.** The call site is `LOG_IF(DFATAL, !s.ok()) << …`. `DFATAL` aborts in debug builds and logs-only-as-error in release builds. Our PEM was bazel-built locally with default debug flags ⇒ DFATAL is FATAL ⇒ abort.
- **Fix applied (live, no rebuild):** `kubectl set env daemonset/vizier-pem PL_TABLE_STORE_DATA_LIMIT_MB=4096` bumps the budget. New per-other-table cap is **152.9 MiB**, comfortably above the largest observed batch.

## Evidence

### 1. Crash signature (raw, from host-side log)

`/var/log/pods/pl_vizier-pem-sql2n_…/pem/9.log` line 4087:

```
F20260514 15:43:18.679255 2365376 source_connector.cc:64]
  Failed to push data. Message = RowBatch size (88835909) is bigger than maximum table size (49545216).
*** Check failure stack trace: ***
E20260514 15:43:18.679389 2365376 signal_action.cc:63]
  Caught Aborted, suspect faulting address 0x2416ee. Trace:
**************************
PC: @     0x7781f9f2e472  (unknown)  abort
  @     0x5ba14c447c7d  (unknown)  google::LogMessage::Fail()
  @     0x5ba14c4470fc  (unknown)  google::LogMessage::SendToLog()
  @     0x5ba14c44796d  (unknown)  google::LogMessage::Flush()
  @     0x5ba14c4477a9  (unknown)  google::LogMessage::~LogMessage()
  @     0x5ba145ef1da2  (unknown)  px::stirling::SourceConnector::PushData()
  @     0x5ba145980fd0  (unknown)  px::stirling::StirlingImpl::RunCore()
  …
```

The crashing thread is **2365376**, which is the Stirling main loop (`StirlingImpl::RunCore`). The Carnot threads in the rest of the abort dump (2365410-413) were happening to execute a `MapNode → ScalarExpressionEvaluator → FilterNode` query path when the abort fired — but **they did not cause the abort**. The abort is `google::LogMessage::Fail()` from a `DFATAL` macro.

### 2. Source location

```cpp
// src/stirling/core/source_connector.cc:64
LOG_IF(DFATAL, !s.ok())
    << absl::Substitute("Failed to push data. Message = $0", s.msg());
```

The `Status s` comes from `agent_callback(...)` which forwards a record batch to the table store. The table store's gate is at `src/table_store/table/table.cc:241`:

```cpp
if (row_batch_size > max_table_size_) {
    return error::ResourceUnavailable("RowBatch size ($0) is bigger than "
                                       "maximum table size ($1).",
                                       row_batch_size, max_table_size_);
}
```

So Stirling tried to push a single batch larger than the destination table's whole budget. This is a hard invariant — even a perfectly-empty table can't absorb a row batch that's bigger than its max size.

### 3. Numbers reconciliation

```
default PL_TABLE_STORE_DATA_LIMIT_MB = 1024 + 256 = 1280   src/vizier/services/agent/pem/pem_manager.cc:26
                                  memory_limit = 1280 * 1024 * 1024            = 1_342_177_280 B
                http_events (40%)              = 0.4 * memory                  =   536_870_912 B
            stirling_error (env: 2 MiB / 2)                                    =     1_048_576 B
              probe_status (env: 2 MiB / 2)                                    =     1_048_576 B
          proc_exit_events (env: 10 MiB)                                       =    10_485_760 B
                              used                                             =   549_453_824 B
                       remaining = memory - used                               =   792_723_456 B
   other_table_count ≈ 16 (the 13 socket_tracer + jvm_stats + network_stats + process_stats)
            per "other" table = remaining / 16                                 =    49_545_216 B = 47.25 MiB
```

`49_545_216` matches the `max_table_size` reported in the FATAL message **exactly**. `88_835_909` is the row-batch that overflowed. The math closes.

### 4. Crash frequency vs load

Observed over the perf sweep on 2026-05-14:

| Run | Multiplier | PEM restarts | Outcome |
|---|---|---|---|
| sweep #1 | 1× | 0 | clean |
| sweep #1 | 2× | 0 | clean |
| sweep #1 | 4× | 0 | clean |
| sweep #1 | 8× | 0 | clean |
| sweep #1 | **16×** | **3** during RUN | recorder rate collapse (compounded by k6 OOM at 512 MiB) |
| sweep #1 (aggregate over 5h) | — | **32** total | many BackOff cycles |
| sweep #2 (after Burstable QoS bump) | 2× | 0 | clean (23.5 min) |
| sweep #2 | **4×** | **10** | sweep aborted; perf_tool exhausted max_retries |

The crash floor is the redis_events table specifically — `k6 → api → redis` hits redis the hardest (cache GETs + SETEXs at ~1 K ops/s/× of multiplier). At 16× = ~16 K redis ops/s, Stirling can drain >88 MiB of `redis_events` rows in a single push if the perf buffer fills before draining. At 4× the same can happen if the drain stalls briefly (e.g., during a cgroup-procs scan, which we see warned about in the same log:
`W…state_manager.cc:276] Failed to read PID info for pod=…`).

### 5. Why earlier 8× ran fine but later 4× crashed

The two runs differ in **PEM's own background state**:
- The 16× run that triggered the original crash also had **k6 OOM cycling** (loadgen container limit 512 MiB at that time). When k6 restarted, the redis_events traffic gapped, then surged when k6 came back — a perfect setup for a single drained batch to be unusually large.
- The 4× run in sweep #2 inherited a PEM that had been restarting all day; one of those starts had higher steady-state perf-buffer occupancy and the next drain landed an >47 MiB batch.

### 6. The DFATAL-only path

`LOG_IF(DFATAL, …)` translates to:

| build | behaviour |
|---|---|
| debug (no `-DNDEBUG`) | FATAL → `abort()` → SIGABRT |
| release (`-DNDEBUG`) | ERROR → log only, push is dropped, agent continues |

Our PEM image was bazel-built locally **without** `--compilation_mode=opt` (we used `--config=x86_64_sysroot` for glibc compatibility, no `-c opt`). So every `DFATAL` fires `abort()`. A release-mode PEM (which is what `ghcr.io/k8sstormcenter/pixie/vizier-adaptive_export_image:0.14.17` etc. would be) would have **logged the same error and dropped the batch**, *not crashed* — but it would still be losing data on every oversized push.

## Mitigations

### Already applied (live cluster, 2026-05-14 16:09)

```
kubectl set env daemonset/vizier-pem -n pl PL_TABLE_STORE_DATA_LIMIT_MB=4096
```

New per-other-table cap: **152.9 MiB**. PEM restarted clean; 0 restarts since.

### Short-term — for the perf sweep

1. **Keep `PL_TABLE_STORE_DATA_LIMIT_MB=4096`** (or higher) for any sweep above 8×. The 280 MB / 1280 MB default is undersized for the kind of traffic we're driving.
2. **Optionally lower `exportPeriod`** (5 s → 30 s) in `sovereignSOCSuite()` so Stirling drains more aggressively between batches. Reduces single-batch peak size at the cost of fewer recorder ticks.
3. **`max_retries=3`** stays — log-and-continue in the recorder loop still helps with the unrelated forwarder race.

### Medium-term — fix in source

`src/stirling/core/source_connector.cc:64`:

```cpp
// before
LOG_IF(DFATAL, !s.ok())
    << absl::Substitute("Failed to push data. Message = $0", s.msg());

// after
if (!s.ok()) {
    LOG_EVERY_N(ERROR, 100)
        << absl::Substitute("Failed to push data. Message = $0", s.msg());
    stirling_metrics_.push_data_failures.Increment();
}
```

DFATAL is the wrong macro for what is fundamentally a back-pressure condition. Dropping a batch is the correct behaviour; aborting the agent is not. The release build already does the right thing — debug builds should match.

Counter-argument: if you genuinely want to surface back-pressure loudly in dev, use a `LOG_FIRST_N(WARNING, 10)` + a metric. But never `DFATAL`.

### Medium-term — bake the env into the deployment

`k8s/vizier/bootstrap/pem_daemonset.yaml` (or wherever the upstream PEM DS is templated) should set `PL_TABLE_STORE_DATA_LIMIT_MB` from a vizier-level config, defaulting to something reasonable for prod workloads. 1.28 GB shared across 16 protocol tables = 80 MiB each is a much friendlier default for clusters running real traffic.

### Long-term — adaptive sizing

The root issue is that Stirling's per-table limit is **a static fraction of a static total**. A better design:
- Track per-table watermark over time.
- If one table consistently uses more than its share AND others are idle, rebalance.
- Or: separate "high-volume protocol" tables (redis, http) from low-volume ones (jvm_stats) with different default proportions, mirroring how `http_events_percent` is already a special case.

## Verification path

To confirm the fix:

1. ✅ PEM running on bumped env (`PL_TABLE_STORE_DATA_LIMIT_MB=4096`), `vizier-pem-8knqm`, restarts=0.
2. Rerun `perf-sweep.sh 4x 8x 16x 32x` — expect 0 PEM restarts.
3. If 32× still crashes, the next bottleneck is *http_events* (currently 40 % × 4 GB = 1.6 GB max — that's already huge), or a CPU-bound Stirling drain. Bump to 8192 MB if needed; ceiling on this VM is 8 GB / 64 GB host = comfortable.

## Lessons captured

- `kubectl logs --previous --tail=200` truncates **above** the FATAL message when the abort dumps every thread's stack (the stack dump alone is several hundred lines). For PEM-class crashes, always read `/var/log/pods/<pod>/pem/<N>.log` directly to find the abort line.
- A C++ stack trace at the moment of SIGABRT lists **every thread**, not just the crashing one. Don't trust the first stack you see — find the `*** Check failure stack trace: ***` marker first, then walk down from the `abort →  LogMessage::Fail` frames.
- Pixie's `DFATAL` macros mean "this WILL crash debug builds" — not "this might be a problem in dev." Treat them like production-bug seeds.
