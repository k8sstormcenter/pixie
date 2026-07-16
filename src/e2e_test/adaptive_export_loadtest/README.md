# adaptive_export_loadtest

Load-test + e2e harness for **adaptive_export (AE)** and the dx-steered SOC chain.
There are exactly **two ways to test**, by design — pick by what you're proving:

| family | needs a live SOC stack? | proves | entry point |
|---|---|---|---|
| **A. Fixture-isolation** | No (just ClickHouse) | AE's write behaviour is *deterministic* — injected `kubescape_logs` → exact `forensic_db` rows, across many reps | `harness/run.sh` |
| **B. Live-attack e2e** | Yes (Pixie + kubescape + CH + AE + dx) | the real chain: attack → detection → DX-steered data-volume reduction → no-loss → NFR | `harness/poc_fire.sh` → `exp_matrix.sh` → `nfr.sh` → `exp_row_reconcile.sh` |

`event_time` is unix **SECONDS** end-to-end (the unit the soc Vector kubescape sink emits and the CH DDL TTL/PARTITION assume). Fixtures use seconds.

---

## A. Fixture-isolation (offline AE proof — no Pixie)

Injects *controlled* `kubescape_logs` trigger rows (real kubescape is **not** deployed) and a *counted* traffic band, then asserts exactly how much AE writes — so write behaviour is measured deterministically instead of lost in infra noise.

```sh
export KUBECONFIG=<kubeconfig>            # or run lab-side with CH_NO_PF=1
bash harness/run.sh                       # full suite: ae_config → E1..E4,E6 → E5
EXP=E1 REPS=20 OUT=/tmp/E1.csv bash harness/exp_control.sh   # one experiment
EXP=E8 TICKS=25 INTERVAL=3 bash harness/exp_e8.sh            # sustained same-pod (F8 reproducer)
```
Exact reproducibility ⇔ `harness/stats.py` reports every `*_act` metric with one distinct value (std=0).

**Scripts:** `run.sh` (orchestrator) · `lib.sh` (CH/kubectl helpers) · `inject.sh` (HTTP INSERT of kubescape_logs) · `ae_config.sh` (AE single-shot load-test mode) · `exp_control.sh` (E1–E4,E6) · `exp_e5.sh` (data-plane volume) · `exp_e8.sh` (sustained same-pod / F8) · `stats.py` (reproducibility verdict).

## B. Live-attack e2e (the real chain, on a deployed stack)

Run on a SOC stack (Pixie vizier Healthy + kubescape netStreaming + CH `forensic_db` + AE + dx). Order:

```sh
export KUBECONFIG=<kubeconfig>
# 1. generate the attack signal (idempotent; verifies LDAP egress before returning)
bash harness/poc_fire.sh
# 2. data-volume reduction MATRIX — ALL (firehose) vs DX (steered) × {poc,argocd,react2argo}
CONDITIONS="poc:on react2argo:on" REPS=5 bash harness/exp_matrix.sh
# 3. NFR — throughput, AE+dx memory under load, verdict/query latency
bash harness/nfr.sh
# 4. no-loss — deterministic PEM↔ClickHouse row-level reconciliation for the DX arm
bash harness/exp_row_reconcile.sh
```

**Scripts:** `poc_fire.sh` (attack-signal generator, bob#140-hardened) · `exp_matrix.sh` (reduction matrix, the canonical ALL-vs-DX runner) · `nfr.sh` (throughput/mem/latency) · `exp_row_reconcile.sh` (no-loss).

> The DX arm needs the load-gen pods bound to a **benign User SBoB** (`kubescape.io/managed-by: User`, `rulePolicies.R0002.processAllowed`) or benign noise gets steered and contaminates the reduction — see `biz/PoC/poc/datavolume/denoise_sbobs/`.

---

## Layout
```
fixtures/EXPERIMENTS.md   curated kubescape_logs data-set catalog + expected outputs
harness/                  the two families above
k8s/                      isolated sinks + per-rep generator pod (no probes)
tools/loadgen/            cleanloadgen + httpsink Go sources + Dockerfile
```
Go unit/e2e tests for AE live with the service: `src/vizier/services/adaptive_export/internal/{trigger,e2e}/*_test.go`.

See `CONTRACTS.md` (AE implied contracts) and `FINDINGS_AND_BACKLOG.md` (reproduced findings incl. the F8 watermark-poison bug).

## Validation status (honest)

| Experiment | Plane | Status |
|---|---|---|
| E1 single / E2 dedup / E3 fan-out / E4 boundary / E6 restart-idempotency | control | ✅ exactly reproducible (std=0) on a live rig |
| E8 sustained same-pod | control | ✅ reproduced the F8 "writes-stop" bug + recovery |
| E5 volume / E8 data-mode | data | ⏳ authored; pending live validation |
| Live poc reduction / NFR / no-loss (family B) | data | ✅ validated (aeprod19 + pemdq10 + dx): #33 prefetch verdict 212→18ms; reduction ALL→DX ≫ measured |

## Removed (consolidation 2026-06)
Redundant variants folded into the canonical scripts above — deleted: `ae_vs_all.sh`, `vrun.sh`, `exp_poc_reps.sh`, `exp_datavolume_extreme.sh`, `exp_dx_steering_reduction.sh` (→ `exp_matrix.sh`); `exp_ae_nfr_benchmark.sh` (→ `nfr.sh`); `exp_pipeline_reconcile.sh` (→ `exp_row_reconcile.sh`); `exp_dx_validate.sh` (→ `exp_matrix.sh`); `deploy_ae.sh`, `build_gen_image.sh` (superseded by the live stack / kit).
