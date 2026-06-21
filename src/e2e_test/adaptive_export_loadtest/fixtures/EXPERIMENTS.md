# AE load-test experiment catalog

Each experiment is a curated `kubescape_logs` data set (injected via `inject.sh`,
real kubescape NOT deployed) plus the deterministic AE output it must produce.
Run each ×100; **exact reproducibility ⇔ every metric has std = 0 / one distinct
value across the 100 reps.**

Two planes (see `project_ae_repro_planes`):
- **Control plane** — `adaptive_attribution`, `trigger_watermark`: a pure
  function of the injected rows. No Pixie, no traffic gen needed.
- **Data plane** — `http_events`/`dns_events`/`pgsql_events`/`conn_stats`: real
  Pixie capture of `cleanloadgen`'s sealed band; gen manifest counts are the
  oracle. Requires the L3 topology + single-pull AE config.

Per-rep isolation: unique `--hostname aw-<exp>-<rep>` (control, watermark is
host-partitioned) and unique `--pod gen-<exp>-<rep>` (data, AE's `df.pod` filter
isolates each rep even with overlapping windows). Timestamps are explicit unix
nanos — fixtures NEVER use wall-clock `now()`.

| # | Plane | Injected data set | Expected (per rep, exact unless noted) |
|---|---|---|---|
| **E1** single anomaly | control | 1 row: rule R0001, target (ns,pod), pid/comm fixed, `event_time=T` | `uniqExact(anomaly_hash)=1`; `adaptive_attribution` FINAL `=1`; watermark `=T` |
| **E2** dedup / extend | control | 10 rows, SAME (pid,comm,pod,ns), distinct ↑ `event_time` (`--count 10`) | hashes `=1`; attribution FINAL `=1` (t_end extended, not multiplied); watermark `=T+9·dt` |
| **E3** fan-out | control | K=8 rows, distinct (pod,ns), 1 each | hashes `=8`; attribution FINAL `=8` |
| **E4** boundary collision | control | 2 rows, identical `event_time`, different RuleID, same target (`--same-time`) | deterministic fingerprint-dedup: both surface (distinct fp), hashes `=1`; watermark `=T` |
| **E5** data-plane volume | data | 1 anomaly, `pod=gen-…`, `event_time=B1` from gen manifest; gen fires HTTP_N=100/DNS_N=100/PGSQL_N=100 in band `[B0,B1]` | `Δhttp_events=100`, `Δdns_events=100`, `Δpgsql_events=100`; `Δattribution=1`; `conn_stats` within tolerance; single-pull (no MergeTree dup inflation) |
| **E6** watermark idempotency | control | inject E1 set, let AE process, restart AE (watermark persisted), re-run | 2nd pass: `Δ` everything `=0` (no double-count) |
| **E7** passthrough A/B | data | canned band; `ADAPTIVE_PASSTHROUGH` 1 then 0, same load+window | exact firehose/filter ratio per table; reproducible across reps |

## Timestamp coordination (data-plane, E5/E7)

1. gen fires → sealed band `[B0,B1]` (node clock == Pixie `time_` == kubescape
   `event_time`; no skew).
2. inject fixture `--event-time B1 --pod gen-<exp>-<rep>`.
3. AE config: `ADAPTIVE_WINDOW_BEFORE_SEC ≥ (B1−B0)/1e9 + margin` so window start
   `≤ B0`; `ADAPTIVE_WINDOW_AFTER_SEC` small → window expires after ONE pull
   (protocol tables are plain MergeTree — repeated pulls would re-insert dups).
4. measure forensic_db deltas BEFORE the band ages out of Pixie retention.
5. delete `gen-<exp>-<rep>` (held alive until here so upid resolves).

## Default knobs

- `HTTP_N=DNS_N=PGSQL_N=100` (low enough for 100% Pixie sampling, no drops).
- `conn_stats` tolerance: `Δconn ∈ [HTTP_N, HTTP_N+5]` (new-conn-per-req + 1 pg).
- `async_insert=0` on the ingest user so counts are stable at read time.
