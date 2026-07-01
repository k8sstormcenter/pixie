# AE load-test — reproducible findings + robustness backlog

Rig `6a3066767841074cd3200495` (k3s, 2 nodes), AE `vizier-adaptive_export_image:0.14.19-aeprod-clean3`,
control plane against ClickHouse (real kubescape NOT deployed; only `kubescape_logs` fixtures injected).
Every finding below is REPRODUCED with the harness in `aeload/`; numbers are measured, not estimated.

## Headline

1. **The "writes stop after initial success, data still on Pixie" bug is REPRODUCED (F8).** AE's trigger
   gates on a strict high-water-mark of the kubescape-supplied `event_time`; **any anomaly with
   `event_time < watermark` is silently dropped**. A single mixed-unit row (nanos/millis) poisons the
   watermark to ~1.78e18 → every subsequent seconds-row is dropped **forever** → AE stops writing
   although Pixie still has the data. Reproduced + recovered (reset watermark + restart) on the rig.
2. AE's **control-plane write surface is EXACTLY reproducible** when event_times are monotonic — `71/71`
   then `20/20` (E1, seconds-native) std=0.
3. **F1 correction:** production (soc Vector) emits `event_time` in **seconds**, for which the DDL TTL is
   correct. My earlier "DDL bug" report was triggered by my fixtures using **nanoseconds**; the real,
   durable issue is that the unit is **not standardized/enforced** (trigger auto-detects s/ms/ns; DDL
   assumes seconds) — which is also the root enabler of the F8 catastrophe.

## Reproducible findings

### F8 — CRITICAL (likely THE production bug): watermark high-water-mark silently drops any `event_time < watermark`
`trigger_watermark` is a monotonic cursor on the kubescape-supplied `event_time` (the trigger SELECT does
`WHERE event_time >= watermark`). It is **content-derived, not ingest-ordered**, so it is fragile to:
1. **Unit heterogeneity (catastrophic):** one anomaly in nanos/millis sets `watermark ≈ 1.78e18`; every
   later seconds-row (`~1.78e9`) is `< watermark` → dropped **forever**. The trigger explicitly supports
   s/ms/ns, so a mixed pipeline guarantees this.
2. **Clock skew / out-of-order alerts:** a late/earlier-stamped anomaly after a newer one → dropped.
3. **Restart re-scan:** on reboot AE loads the persisted watermark (or re-scans to the max existing row),
   so anomalies stamped below that max are never processed.
Effect = "writes succeed initially, then stop, data still on Pixie" (the trigger halts; the data plane
and Pixie are fine). **Reproduced on the rig** (E8 sustained): with the watermark poisoned at a leftover
nanos value (`1781559619170395824`), 25/25 ticks of fresh seconds anomalies → **n_anomalies stayed 0**.
After `ALTER TABLE trigger_watermark DELETE WHERE 1=1` + AE restart, once tick event_times rose above the
re-scanned max, **n_anomalies grew 1→2→3→4, delta=1 per tick** (healthy steady state). Evidence:
`e8_steady.csv` (stalled), `e8_recov.csv` (recovered + steady growth).

### F1 — `kubescape_logs` TTL/PARTITION assume seconds; non-seconds producers are TTL-deleted (unit not enforced)
**Correction to the earlier report:** production (soc Vector, `to_unix_timestamp(ts)` = VRL **seconds**;
confirmed by the AE code comment "Vector's kubescape sink … writes unix SECONDS ~1.7e9") emits **seconds**,
for which `toDateTime(event_time)` is CORRECT — the DDL is **not** buggy in production. The overflow I first
saw was caused by **my fixtures using nanoseconds** (copied from the Go `integration_test`/`e2e` convention,
which use `UnixNano`). The durable issue: the unit is **unstandardized** — the trigger auto-detects
s/ms/ns but the DDL hardcodes seconds, so a millis/nanos producer has ALL its `kubescape_logs`
TTL-deleted. Original (now-superseded) overflow detail follows for the record:
`event_time` is UInt64 **nanoseconds** (all Go code + every fixture + `integration_test.go` use
`UnixNano`). But the DDL (soc `clickhouse-lab/schema.sql` AND AE's embedded
`internal/clickhouse/schema.sql`) does:
```sql
PARTITION BY toYYYYMM(toDateTime(event_time))
TTL toDateTime(event_time) + INTERVAL 30 DAY
```
`toDateTime()` interprets its arg as **seconds**. Reproduced on the rig:
```
toDateTime(1781559074162913804)            = 2106-02-07   (saturates at DateTime max)
toDateTime(1781559074162913804)+30 DAY     = 1970-01-30   (OVERFLOWS past max → wraps to 1970)
(... ) < now()                             = 1            (already_expired)
```
→ every row is born already-expired → CH TTL-deletes `kubescape_logs` on the next merge.
Measured: after injecting ~20 anomalies, `kubescape_logs` held **2** rows; all showed `expired=1`.
The AE trigger (250 ms poll) races the merge: anomalies polled before deletion get an
`adaptive_attribution` row; the rest are **lost with no error logged** (the ~10% E1 miss).
PARTITION is also broken — every row lands in the single `2106-02` partition.

**Fix validated on the rig:**
```sql
ALTER TABLE forensic_db.kubescape_logs
  MODIFY TTL toDateTime(intDiv(event_time, 1000000000)) + INTERVAL 30 DAY;
```
→ `ttl_expiry = 2026-07-15`, `expired = 0` → **E1 re-run = 20/20 PASS, std=0** (was ~9/10).

### F2 — Anomaly loss is silent + unretried
When F1 (or any input-side pruning / transient CH write error) drops an anomaly, AE logs **nothing**
and never retries — `adaptive_attribution` simply lacks the row. There is no `dropped_anomalies` /
`trigger_lag` metric to detect it. Reproduced: rep 8 had 0 attribution, AE log had zero errors/warnings.

### F3 — POSITIVE: control plane is EXACTLY reproducible when processed
With F1 fixed: `uniqExact(anomaly_hash)` and `adaptive_attribution` FINAL counts are **std=0 / CV=0**
across all reps. Dedup is deterministic (N events for one (pid,comm,pod,ns) → 1 hash → 1 row).
Measured (TTL-fixed):
- **E1** single anomaly = **20/20 EXACT** (uniq=1, attrib=1 every rep)
- **E3** fan-out (8 distinct pods) = **20/20 EXACT** (uniq=8, attrib=8 every rep)
- **E4** boundary collision (2 rows, same `event_time`, different RuleID, same target) = **20/20 EXACT**
  (fingerprint-dedup deterministic → 1 hash, 1 row)
- **E2** dedup/extend (10 events, 1 target → 1 row) = **10/10 EXACT** (uniq=1, attrib=1)
- **E6** restart idempotency = **1/1 EXACT** — attribution stayed exactly 1 across an AE rollout-restart
  (no double-count on watermark reload)

**Total: 71/71 control-plane reps EXACT (std=0)** after AE-1.

### F4 — AE cannot boot for ClickHouse-only / control-plane-only operation
AE fatals at config validation without pixie cluster identity, even when only the CH trigger→attribution
path is needed:
```
fatal "missing required env variable 'PIXIE_CLUSTER_ID'"   then  'CLUSTER_NAME'
```
Worked around with a dummy `PIXIE_CLUSTER_ID` + `CLUSTER_NAME` + `ADAPTIVE_PUSH_PIXIE_ROWS=false`.
This couples the (CH-only) control plane to a healthy vizier registration that it does not use.

### F5 — `trigger_watermark` persistence is throttled (~5 s)
The persisted cursor lags the in-memory cursor by up to `ADAPTIVE_WATERMARK_SAVE_SEC` (default 5 s).
Reproduced: queried `watermark` lagged the just-injected `event_time` by one rep repeatedly (the
in-memory cursor + `adaptive_attribution` were already correct). On crash, up to that interval of
progress is lost → reprocessing (dedup-safe, but wasteful + can surprise external observers).

### F6 — (provisioning / dependability) custom-version vizier never registered
`make pixie` with `VIZIER_VERSION=…-aeprod-clean3` (extract_yaml path) left **`pl-cloud-config`** and
**`pl-cluster-secrets`** uncreated → cert-provisioner crashloops (`pl-cloud-config not found`,
`pl-cluster-secrets does not exist`) → NATS/PEM/query-broker never start → **no data plane**. Hand-created
`pl-cloud-config`; `pl-cluster-secrets` requires cloud registration. This blocks the live **E5 data-plane**
experiments (harness is ready, waiting on a healthy `vizier-query-broker`).

### F7 — single-pull config confirmed
AE boots with `window_after=5s window_before=2m0s poll_interval=250ms` — `AFTER (5s) < refresh (30s)`
forces exactly one pull per window on the published image (so the non-deduping MergeTree protocol tables
aren't re-inserted). The new `ADAPTIVE_PUSH_REFRESH_SEC=-1` knob (added this branch, uncommitted) is the
explicit equivalent.

## Backlog — make AE repeatable, robust, dependable

| ID | Pri | Fix | Why |
|----|-----|-----|-----|
| **AE-9** | **P0** | **Make the trigger cursor robust** — don't gate on the content `event_time` as a strict HWM. Options: (a) cursor on **ingest order** (a monotonic insert id / `_part`+row, or an `inserted_at DEFAULT now64()` column) instead of `event_time`; (b) bounded **lookback window** (re-scan `event_time >= watermark - L`) + **content-dedup** (anomaly fingerprint) so out-of-order/skewed/below-watermark anomalies are still processed exactly once; (c) NORMALIZE `event_time` to one unit before it ever reaches the cursor. Add `dx_anomalies_below_watermark_total` + `trigger_watermark_seconds` metrics + alert. | **F8 — the production "writes stop, data on Pixie" bug.** A single mixed-unit/skewed/out-of-order row poisons the HWM → silent total halt. Highest-impact dependability fix. |
| **AE-2** | **P0** | Standardize `event_time` to ONE documented unit + **normalize-or-reject at ingest** (Vector + AE); remove the trigger's silent s/ms/ns auto-detect (it *enables* F8 + F1). | The unit ambiguity is the root enabler of both F8 (watermark poison) and F1 (TTL delete). |
| **AE-1** | P1 | Make the `kubescape_logs` DDL TTL **and** PARTITION unit-agnostic (e.g. normalize `event_time` in a MATERIALIZED `event_dt DateTime64(9)` used by TTL/PARTITION) so a non-seconds producer isn't silently TTL-deleted. Patch BOTH soc `clickhouse-lab/schema.sql` and AE embedded `internal/clickhouse/schema.sql`. | F1: defense-in-depth — even with AE-2, a stray non-seconds row shouldn't vanish. (Production seconds path is currently correct.) |
| **AE-3** | P1 | Eliminate the retention-vs-trigger race: AE should own `kubescape_logs` deletion (delete only AFTER an anomaly is acked into `adaptive_attribution`), OR decouple trigger progress from row TTL. Add `dx_anomalies_dropped_total` + `trigger_lag_seconds` metrics + alert. | F1/F2: today a pruned-before-polled row is lost invisibly. Observability + ordering guarantee. |
| **AE-4** | P1 | Make `adaptive_attribution` writes durable — retry with backoff, count failures, never silently drop. | F2: best-effort write = unaccounted loss under any CH hiccup. |
| **AE-5** | P1 | Allow CH-only / control-plane boot: make `PIXIE_CLUSTER_ID`/`CLUSTER_NAME`/`PIXIE_API_KEY` optional when `ADAPTIVE_PUSH_PIXIE_ROWS=false` and not streaming/passthrough. | F4: enables AE testing + degraded operation without a healthy vizier. |
| **AE-6** | P2 | Make protocol tables `ReplacingMergeTree` keyed by (hostname,event_time,upid,…) so repeated pulls are idempotent regardless of refresh; keep `ADAPTIVE_PUSH_REFRESH_SEC` (done) for explicit single-shot. | Data-plane robustness: removes the "plain MergeTree + 30s re-pull → duplicate inflation" footgun (the reason single-pull is currently required). |
| **AE-7** | P2 | Flush `trigger_watermark` on shutdown; make the save throttle configurable. | F5: bound crash-reprocessing + give observers a fresh cursor. |
| **AE-8** | P2 | (makefile-agent) `make pixie` for custom `VIZIER_VERSION` must create `pl-cloud-config` and complete cloud registration (`pl-cluster-secrets`). | F6: blocks data-plane e2e + any real deployment of a custom AE build. |

## Fix implemented + validated (F8 / AE-2 unit-normalization)

**Code (working tree, `internal/trigger/clickhouse.go`):** the trigger cursor is now **canonical
nanoseconds**. Added `normalizeEventTimeNanos()` (s/ms/ns → ns, same thresholds as
`controller.eventTimeToTime`) + `chNormEventTimeNanos` (the ClickHouse equivalent). The poll SELECT now
filters + orders on `chNormEventTimeNanos >= <watermark_nanos>` (was raw `event_time >= watermark`);
`maxSeen`, the in-memory watermark, the boundary-dedup compare, and the loaded/persisted watermark are all
normalized. Net: a mixed-unit row can no longer drive the HWM past real rows. Unit test
`clickhouse_internal_test.go` (in-package; runs on a build PG): `TestNormalizeEventTimeNanos` +
`TestFetchSinceFiltersOnNormalizedEventTime`.

**Empirically validated at the data layer on the rig (no AE rebuild needed)** — against the actual
poisoned watermark `1781559619170395824`:
- OLD raw filter `event_time >= wm` → **0 rows** (AE sees nothing = the bug)
- NEW normalized filter `chNormEventTimeNanos >= wm` → **60 rows** (all recovered)
- table held 60 cplane-01 rows the whole time — the filter was the sole cause.

**Still to land:** rebuild + deploy the AE image carrying this Go change (can't `git push` per rules →
hand to build-agent / `gh-pixie-build`), then re-run E8 to confirm no-poison live. AE-9 (out-of-order
lookback + below-watermark metric) and AE-1 (unit-agnostic DDL TTL/PARTITION) remain.

## Reproducibility status

| Layer / experiment | Status |
|---|---|
| Control plane E1 (single) | ✅ **20/20 EXACT (std=0)** after AE-1 fix |
| Control plane E3 (fan-out) | ✅ **20/20 EXACT** (uniq=8, attrib=8) |
| Control plane E4 (boundary collision) | ✅ **20/20 EXACT** (uniq=1, attrib=1) |
| Control plane E2 (dedup) | ✅ **10/10 EXACT** (uniq=1, attrib=1) |
| Control plane E6 (restart idempotency) | ✅ **1/1 EXACT** (attrib stayed 1 across AE restart) |
| **Control plane total** | ✅ **71/71 reps EXACT (std=0)** + **E1 20/20 seconds-native** |
| E8 sustained same-pod (control) | ✅ reproduces F8 (stall when event_time ≤ watermark) + recovers to steady delta=1 growth |
| Data plane E5 + E8-data | ⛔ blocked on F6 (vizier not registered); data-plane rig requested from makefile-agent; harness ready |
| L1 hermetic (`go test`, exact bytes) | 🧰 authored; runs on a build PG (pixie module compile) |

NOTE: harness is now **seconds-native** (production unit). The earlier 71/71 used nanos + a compensating
TTL ALTER; E1 was re-confirmed **20/20 std=0 natively with seconds** + the seconds-correct DDL (no ALTER).
