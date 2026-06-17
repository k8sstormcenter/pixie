# Pixie security-detection profile — flag-only investigation

Goal: prove what fraction of short-lived DNS lookups the stock Pixie
PEM captures today, then tune the existing runtime flags until we hit
the practical ceiling. Code changes only after we know what flags
alone leave on the table.

This investigation deliberately does **not** turn TLS tracing on
(`PX_STIRLING_ENABLE_TLS_TRACING`) — see gh#2095: enabling TLS would
suppress the SSL_read/SSL_write uprobe plaintext capture on the same
connection, which is a regression we don't want until the dual-mode
conn_tracker exists.

## Test plan

1. Stand up a probe pod that fires a known number `N` of DNS queries
   with a controlled rate against an off-cluster resolver. Each query
   uses a unique random label so we can match what the PEM captured
   row-by-row.
2. Read back `dns_events` for that pod's window and compute coverage =
   `unique captured query_names ∩ unique sent / unique sent`.
3. Repeat under three configurations:
   - **default** — out-of-the-box PEM env.
   - **security-runtime** — all the relevant flags that are envvar-
     tunable today (see `harness/flags_security_runtime.env`).
   - **security-aggressive** — same plus the per-CPU bandwidth caps
     raised.
4. Sweep `N ∈ {100, 1000, 5000, 10000}` and rate `R ∈ {100, 1000, 5000} q/s`.

## Reported numbers (per (config, N, R) cell)

- `coverage` — fraction of sent queries that landed in `dns_events`.
- `dropped_conn_resolved` — captured rows where `dns_events.upid` is
  populated (conn_tracker found the source).
- `dropped_conn_unresolved` — captured rows where `upid` is empty
  (conn-tracker resolution failed; line in `conn_tracker.cc:1004`).
- `pem_cpu_avg` / `pem_mem_rss` — averaged over the sweep window.
- `socket_tracer_drop_count` — from PEM's
  `stirling_error.bytes_dropped` if it surfaces.

## What's in the tree

| path | purpose |
|---|---|
| `harness/run.sh` | one-shot entry: deploys probe + reads dns_events + emits per-cell CSV |
| `harness/lib.sh` | helpers (wait-for-pod, port-forward, retry, kubectl wrappers) |
| `harness/flags_default.env` | no overrides |
| `harness/flags_security_runtime.env` | the env-var-only tuning per FINDINGS.md |
| `harness/flags_security_aggressive.env` | runtime + raised per-CPU bw caps |
| `harness/stats.py` | summarise the per-cell CSV → markdown table |
| `tools/dnsprobe` | Go binary: fires N queries at rate R, writes a manifest of (timestamp, query_name) tuples |
| `tools/dnsverify` | Go binary: pulls `dns_events` from the PEM (broker or direct) and emits a CSV of captured (ts, query_name) tuples |
| `k8s/probe-pod.yaml` | the `dnsprobe` runner pod (alpine + dig + the binary) |
| `k8s/pem-overlay-runtime.yaml` | kustomize patch that applies `flags_security_runtime.env` to the PEM DaemonSet |
| `FINDINGS.md` | results (filled in as the sweeps run; **commit on top of the harness PR**) |

## Anti-goals

- Not testing the Go-uprobe path (no `_DISABLE_GOLANG_TLS_TRACING`
  flipping).
- Not testing TLS tracing (see above).
- Not measuring CPU/memory regression on neighbouring tables (e.g.
  http_events). That's a follow-up — this PR is a coverage-first
  investigation.
- Not changing any source file under `src/stirling/` — flag-only.
