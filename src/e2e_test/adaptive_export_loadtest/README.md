# Adaptive Export (AE) load-test suite

A table-driven Go test suite for the AE write surface. Each experiment is a
**fixture** (curated input); each measurement is a named **KPI** asserted with
`testify/require`; one runner drives them against a deployed AE image on a rig.

It replaces the former shell harness (`harness/*.sh` + `stats.py`) — the
experiments, the measurement scripts, and the reproducibility statistic are now
Go fixtures, KPI helpers, and asserts under `suite/`.

## Layout

| Path | What |
|------|------|
| `suite/harness.go` | primitives: ClickHouse-over-HTTP client, kubescape-row injector, control-surface reads, kubectl helpers |
| `suite/fixtures.go` | the experiment table (control-plane reproducibility cases) + the per-rep runner |
| `suite/kpi.go` | KPI asserts: `RequireReproducible`, `RequireReconcile`, `RequireReductionAtLeast`, `RequireExact` |
| `suite/suite_test.go` | the tests: control-plane reproducibility (live), data-plane reconcile + volume reduction (staged) |
| `tools/loadgen/` | the counted signal generator (nested Go module) for the data-plane KPIs |
| `k8s/` | sinks + generator pod templates |
| `CONTRACTS.md` | the C1–C15 AE implied-contract register |
| `fixtures/EXPERIMENTS.md` | the experiment catalog + expected outputs |
| `FINDINGS_AND_BACKLOG.md` | observed contract violations + backlog |

## KPIs

| KPI | Asserts | Was |
|-----|---------|-----|
| **Reproducibility** | a metric is one distinct value across all reps (std = 0) | `stats.py` |
| **Reconcile** | read == wrote == ClickHouse, per protocol table (no loss) | `exp_row_reconcile.sh` |
| **Reduction** | steered volume ≪ firehose volume for one signal window | `exp_matrix.sh` |
| **NFR / WriteDuration** | throughput/mem/latency; window stays open until `t_end` (C15) | `nfr.sh` / `exp_e8.sh` |

## Running

The suite is **live-only**: it drives a deployed AE image, so `go test ./...`
skips unless enabled. Run it on the rig's dev-machine (which has Go + kubectl):

```bash
cd suite && go mod tidy            # first run only, resolves testify
AELOAD_LIVE=1 \
AELOAD_CH_URL=http://<clickhouse>:8123 \
AELOAD_CH_WUSER=ingest_writer AELOAD_CH_WPASS=<pass> \
KUBECONFIG=/path/to/kubeconfig \
go test -v -run TestControlPlaneReproducibility ./...
```

Environment:

| Var | Default | Meaning |
|-----|---------|---------|
| `AELOAD_LIVE` | — | must be `1` to run (else skip) |
| `AELOAD_CH_URL` | `http://127.0.0.1:8123` | ClickHouse HTTP endpoint (port-forward or in-cluster svc) |
| `AELOAD_CH_USER` / `_PASS` | default user | read credentials |
| `AELOAD_CH_WUSER` / `_WPASS` | `ingest_writer` | ingest (write) credentials |
| `AELOAD_AE_NS` / `_DS` | `pl` / `adaptive-export` | AE namespace + DaemonSet |
| `AELOAD_NODE` | first node | the node whose hostname AE polls |

The data-plane reconcile and volume-reduction tests are staged (they need the
counted signal generator / a lab-owned signal) and skip with a reason until
`AELOAD_DATAPLANE=1` / `AELOAD_REDUCTION=1` wire them in.

## Vocabulary

Fixtures carry no CVE identifiers and no adversarial verbs. The control plane is
pure kubescape-metadata bookkeeping, so its fixtures are named by the property
under test. Live incident signals (data-plane / reduction) are emitted by the SOC
lab under its own naming (`java-poc`, `pathogen-ns`, `disease-*`, `Specimen`) and
triggered here through a lab hook — no payload literals live in this tree.

**External wire stays literal** and is never renamed: kubescape RuleIDs (`R0001`,
`R0010`, …), Kubernetes API keywords, ClickHouse column/table names, and the
`forensic_db` schema.
