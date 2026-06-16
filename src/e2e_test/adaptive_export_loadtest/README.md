# adaptive_export_loadtest

Fixture-isolation load-test harness for the **adaptive_export (AE)** service. It
injects *controlled* `kubescape_logs` trigger rows (real kubescape is **not**
deployed) and a *counted* traffic band, then asserts exactly how much data AE
writes to `forensic_db` — across many repetitions — so AE's write behaviour is
measured deterministically instead of being lost in infra self-traffic noise.

Lives under `src/e2e_test/` alongside the other load-test harnesses
(`vzconn_loadtest`, `jetstream_loadtest`, `px_cluster` — the last establishes the
shell/python precedent used here).

## The two planes

AE's write surface splits (see `FINDINGS_AND_BACKLOG.md`):
- **Control plane** — `adaptive_attribution`, `trigger_watermark`: a pure
  function of the injected `kubescape_logs` rows. Tested without Pixie.
- **Data plane** — `http_events`/`dns_events`/`pgsql_events`/`conn_stats`: real
  Pixie capture of the generator's sealed band on the fixture's pod. The
  generator's own counts are the ground-truth oracle.

`event_time` is unix **SECONDS** end-to-end (the unit the soc Vector kubescape
sink emits and the CH DDL TTL/PARTITION assume). Fixtures use seconds.

## Validation status (honest)

| Experiment | Plane | Status |
|---|---|---|
| E1 single / E2 dedup / E3 fan-out / E4 boundary / E6 restart-idempotency | control | ✅ proven on a live rig — exactly reproducible (std=0): E1 20/20, E2 10/10, E3 20/20, E4 20/20, E6 1/1 |
| E8 sustained same-pod | control | ✅ reproduced the "writes-stop" bug (F8) + the recovery |
| E5 volume / E8 data-mode | data | ⏳ authored; **pending live validation** on a vizier-registered rig |

The **F8 watermark-poison fix** it found is in `internal/trigger/clickhouse.go`
(this PR) and is validated at the data layer (old filter→0 rows, new→60).

## Layout

```
fixtures/EXPERIMENTS.md   curated kubescape_logs data-set catalog + expected outputs
harness/lib.sh            CH(HTTP)+kubectl helpers, watermark/attribution readers, warmup
harness/inject.sh         HTTP INSERT of kubescape_logs rows with exact event_time (seconds)
harness/exp_control.sh    E1..E4,E6 control-plane reproducibility
harness/exp_e8.sh         E8 sustained same-pod (control proven; data mode pending)
harness/exp_e5.sh         E5 data-plane volume (pending live validation)
harness/ae_config.sh      put AE into single-shot load-test mode
harness/deploy_ae.sh      deploy AE standalone against ClickHouse (pending-rig)
harness/build_gen_image.sh build the generator image (docker, on a PG dev-machine)
harness/run.sh            full-suite orchestrator
harness/stats.py          per-metric distinct/mean/std/CV reproducibility verdict
k8s/                      isolated sinks + per-rep generator pod (no probes)
tools/loadgen/            cleanloadgen + httpsink Go sources + Dockerfile  (see note)
```

The Go unit/e2e tests for AE live with the service:
`src/vizier/services/adaptive_export/internal/{trigger,e2e}/*_test.go`.

## tools/loadgen — pending bazel integration

`tools/loadgen` holds the deterministic traffic generator (`cleanloadgen`) and a
minimal HTTP sink (`httpsink`). They are built into a TTL image via
`harness/build_gen_image.sh` (docker, on a PG dev-machine) today. To match the
`vzconn_loadtest` convention they should become bazel targets — `lib/pq` is
already vendored in the pixie module, so this is a gazelle `BUILD.bazel` + a
`skaffold` file (build-VM work). Tracked in `FINDINGS_AND_BACKLOG.md`.

## Run (control plane — proven, no Pixie needed)

```sh
export KUBECONFIG=<tailscale-direct kubeconfig>   # or run lab-side with CH_NO_PF=1
EXP=E1 REPS=20 OUT=/tmp/E1.csv bash harness/exp_control.sh
EXP=E8 TICKS=25 INTERVAL=3 bash harness/exp_e8.sh   # sustained same-pod
```
Exact reproducibility ⇔ `stats.py` reports every `*_act` metric with one distinct
value (std=0).
