# RESUME — 3-protocol Pixie+CH perf-eval after VM reboot

Last updated: 2026-05-16 (pre-reboot snapshot).

## Why we're rebooting

Cluster decayed after 6+ days uptime + many hours of sweep runs. Symptoms:
- `kubectl top` → `Metrics API not available` (metrics-server stuck `ContainerCreating`)
- `pgsql-server` readiness probe times out at 1s under any load
- containerd has ~30 stale `containerd-shim-runc-v2` processes from terminated pods;
  after `sudo systemctl restart k3s`, containerd refused to come back up
  (`Waiting for containerd startup: connection refused on /run/k3s/containerd/containerd.sock`)
- Node went `NotReady` after the restart attempt

Full diagnosis is in skill memory at
`~/.claude/projects/-home-constanze/memory/feedback_cluster_state_decays.md`.

## What we set BEFORE rebooting

- **GRUB pinned to kernel 6.8.0-1007-gcp** (the *working* kernel). The newer
  6.17.x kernels are installed but break the prebuilt Pixie PEM eBPF —
  see `feedback_build_from_source` memory. Verified GRUB_DEFAULT now reads:
  `"Advanced options for Ubuntu>Ubuntu, with Linux 6.8.0-1007-gcp"`
  and `update-grub` was run.

## Post-reboot verification (in order)

1. **Kernel sanity check**:
   ```bash
   uname -r
   # MUST be 6.8.0-1007-gcp. If 6.17.x, GRUB rolled back the pin — re-pin
   # and reboot again before doing anything else.
   ```

2. **k3s cluster healthy**:
   ```bash
   export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
   kubectl wait --for=condition=Ready node --all --timeout=180s
   kubectl get pods -n kube-system  # all Running/Ready
   kubectl top pod -n pl            # metrics-server should now work
   ```
   If metrics-server still 0/1: `kubectl rollout restart deploy/metrics-server -n kube-system`.

3. **Pixie healthy**:
   ```bash
   kubectl get pods -n pl
   # Expect: adaptive-export, kelvin, pl-nats-0, vizier-cloud-connector,
   # vizier-metadata-0, vizier-pem-*, vizier-query-broker all 1/1 Running.
   ```

4. **CH still has the historical data** (PVC survives reboot):
   ```bash
   curl -s -G -u pixie:pixie_password \
     --data-urlencode "query=SELECT count() FROM forensic_db.kubescape_logs FORMAT TabSeparated" \
     http://localhost:30123/
   # Should return ~thousands of rows (pre-reboot baseline).
   ```

## Continuation steps (run in this order)

### Step A — re-deploy patched adaptive_export operator

The pre-reboot adaptive_export image (`docker.io/library/adaptive_export:bugfix-prune-grace-180s`)
might NOT have survived in containerd — re-import + roll out:

```bash
cd /home/constanze/code/pixie
./deploy-patched-operator.sh
# Verifies the bazel build, loads to docker, imports to k3s, sets the deployment image, rolls out.
# Patched changes are already in src/vizier/services/adaptive_export/internal/controller/controller.go:
#   - PruneExpired uses grace = 2 * cfg.After
#   - PxL query timeout is 180s (was 30s)
```

### Step B — set up 3-protocol loadtest stack

```bash
./setup-protocol-loadtest.sh
# Creates px-protocol-loadtest ns + applies 6 empty sbobs + labels deployments
# + applies redis/pgsql/http server+client + waits for all 6 pods Ready.
```

### Step C — start synthetic alert injector (so adaptive_export keeps firing)

```bash
setsid bash -c '/home/constanze/code/pixie/inject-fake-alerts.sh 15 > /tmp/inject-fake-alerts.log 2>&1' \
  </dev/null >/dev/null 2>&1 &
disown
# Verify after ~10s:
tail -3 /tmp/inject-fake-alerts.log   # should show "round N injected (6 pods, pid-base+N)"
```

### Step D — run the full sweep

```bash
setsid bash -c '/home/constanze/code/pixie/protocol-sweep.sh 2 4 8 16 20 24 28 32 > /tmp/proto-sweep-stdout.log 2>&1' \
  </dev/null >/dev/null 2>&1 &
disown
# Each multiplier: 30s warmup + 180s measure + ~30s rollout = ~4 min/mult
# 8 multipliers × 4 min = ~32 min total wall clock
# Output dir: /tmp/proto-sweep-<UTC-timestamp>/
#   sweep.log      — human-readable per-mult line
#   metrics.csv    — full instrumented row per mult (all metric categories)
```

Watch the sweep progress with a Monitor (only fires on multiplier boundaries):
```
NEW=$(ls -dt /tmp/proto-sweep-2*/ | head -1)
LOG="${NEW%/}/sweep.log"
prev=""
while true; do
  last=$(grep -E "^=== MULT|^  [0-9]+x|sweep complete" "$LOG" 2>/dev/null | tail -1)
  if [ "$last" != "$prev" ] && [ -n "$last" ]; then echo "$last"; prev="$last"; fi
  grep -q "sweep complete" "$LOG" 2>/dev/null && break
  sleep 15
done
```

### Step E — render the unified scaling.png

```bash
/home/constanze/.venvs/render/bin/python /home/constanze/code/pixie/render-allmetrics.py /tmp/proto-sweep-<dir>
# Produces:
#   $DIR/scaling.png   — 4×5 panel grid, log-log, ALL metric categories
# OR if you ran the new instrumented sweep, render-proto-sweep.py also works:
/home/constanze/.venvs/render/bin/python /home/constanze/code/pixie/render-proto-sweep.py /tmp/proto-sweep-<dir>
```

### Step F — expected findings to verify against prior baseline

Pre-reboot baseline from `/tmp/proto-sweep-20260515-172755/`:

| mult | loadgen total | PEM CPU % | http_events CH/s | redis_events CH/s | pgsql_events CH/s |
|---|---|---|---|---|---|
| 2x  | 5,970   | 80.5%  | 0     | 0   | 0  |
| 4x  | 11,912  | 158.9% | 111   | 111 | 0  |
| 8x  | 24,303  | 227.7% | 70    | 0   | 0  |
| 16x | **47,994** (peak) | **230.2%** (sustained) | 159 | 0 | 0 |
| 32x | 16,999  | 7.3%   | 0     | 0   | 0  (← PEM eBPF buffer back-pressure, system collapses) |
| 64x | 26,685  | 6.1%   | 0     | 0   | 0  |

Post-reboot sweep should:
- Match or beat 16× loadgen total of ~48 K ops/sec (3-protocol simultaneous)
- Show PEM CPU plateau around 230% at 16×
- **Show real growth in `forensic_db.http_events / redis_events / pgsql_events`** at every multiplier ≤ 16× — that's what tasks the patched operator was supposed to fix
- Collapse should still happen between 16× and 20× (eBPF buffer ceiling — kernel-level constraint, not fixable in userspace)

## Key files & scripts

| path | purpose |
|---|---|
| `setup-protocol-loadtest.sh` | idempotent: ns + sbobs + server/client deployments + labels + wait Ready |
| `deploy-patched-operator.sh` | bazel-build adaptive_export → import → set image → rollout |
| `protocol-sweep.sh` | full sweep with all metric instrumentation per mult |
| `inject-fake-alerts.sh` | every 15s injects 6 fresh-PID kubescape_logs rows to keep adaptive_export firing |
| `render-allmetrics.py` | retroactive renderer (queries CH for time-matched windows) — works on old sweep dirs |
| `render-proto-sweep.py` | renderer for new instrumented sweeps (reads metrics.csv) |
| `src/vizier/services/adaptive_export/internal/controller/controller.go` | patched: PruneExpired grace + 180s timeout |
| `src/e2e_test/protocol_loadtest/{redis_client,pgsql_client}/` | new Go seq-loader binaries |
| `src/e2e_test/vizier/seq_tests/client/pkg/{redisclient,pgsqlclient}/` | seq-loader libraries |
| `src/e2e_test/protocol_loadtest/k8s/{redis_client,pgsql_client,http,sbobs}.yaml` | k8s manifests |

## Memory rules to honor (loaded from MEMORY.md automatically)

- **feedback-kubescape-empty-profile**: every loadtest pod needs an empty ApplicationProfile + `kubescape.io/user-defined-profile=<name>` label. Without it: no kubescape alert → no adaptive_export query → no CH row. Already applied in `sbobs.yaml`.
- **feedback-measure-all-metrics**: every sweep captures Loadgen + Pixie + Kubescape + CH per multiplier; renders ALL in ONE log-log scaling.png.
- **feedback-cluster-state-decays**: if `kubectl top` fails or pods sit `ContainerCreating > 5 min`, REBOOT before authoritative measurement runs.
- **feedback-build-from-source**: never use ttl.sh or other prebuilt PEM images — kernel ABI mismatch. Always `bazel --config=x86_64_sysroot` + `k3s ctr image import`.
- **feedback-no-binary-commits**: never `git add` 78 MB blobs left in `bazel-bin/`. Pre-push grep for >5 MB blobs after any rebase/cherry-pick.

## Open task

- **#36 (pending)**: Diagnose operator's CH-write asymmetry under heavy load.
  Pre-reboot evidence: only http_events flowed to CH (~159/s); redis/pgsql/kubescape_logs/adaptive_attribution all 0 within time-matched windows even with the prune-grace + 180s timeout patches applied. Hypothesis: the per-table fan-out queries 10 tables per alert and most return 0 rows, but the time spent serializing the empty results blocks the goroutine from servicing the protocol-matching table for that pod. Fix would be: in `pushPixieRows`, only query the table matching the pod's likely protocol (use pod-name heuristic: pod contains "redis" → redis_events only; "pgsql" / "postgres" → pgsql_events; else http_events).

## How to know the resume succeeded

After running through Step A → Step E, you should have:
- `/tmp/proto-sweep-<timestamp>/scaling.png` — single PNG with 4×5 log-log panels showing **non-zero** values in the CH category panels (the key delta vs pre-reboot data)
- `metrics.csv` with all metric columns populated (CPU/mem non-zero now that metrics-server works again)
- The collapse-zone clearly visible between 16× and 20× as before

Files left uncommitted per standing rule. **DO NOT** auto-commit.
