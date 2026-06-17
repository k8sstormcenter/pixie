# kSamplingPeriod isolation — DNS recon coverage vs PEM stability

The first FINDINGS.md page reframed the original PR result: the
table-store knobs only bought *retention*, not *detection*. This page
isolates `SocketTraceConnector::kSamplingPeriod` — the Stirling
perf-buffer poll cadence — as the candidate detection knob.

Setup:

* Stirling patch exposes the cadence as
  `PX_STIRLING_SOCKET_TRACER_SAMPLING_PERIOD_MS` (`socket_trace_connector.cc:130`),
  clamped to `[1, 60000]` ms. `0` keeps the compiled-in 200 ms default.
* Custom standalone-pem image built (`bazel
  //src/experimental/standalone_pem:standalone_pem_image
  --config=x86_64_sysroot`), imported to k3s containerd as
  `ghcr.io/k8sstormcenter/vizier-standalone_pem_image:0.14.18-secprof-sampling`.
* Cadence varied across `{default (200), 400, 100, 50}` ms with full
  PEM restart between phases, 90 s settle, no other PEM env touched.
* Probe: `tools/dnsprobe` against the local cluster DNS / 1.1.1.1
  (~3500 q/s ceiling at 128 workers).
* `tools/dnsverify` reads `dns_events` after a 3 s flush window.
* Restart counter (`kubectl get pod ... restartCount`) recorded
  per cell — `pem_restarts_seen > 0` means PEM aborted.
* Iter 1 is the trustworthy column (PEM clean). Iter 2 is recorded
  for the record but contaminated whenever iter 1 crashed.

## Iter-1 result matrix

| Cadence (ms) | 10 k @ 1 k/s | 30 k @ 1.5 k/s | 60 k @ 2.5 k/s | 100 k @ 3.5 k/s |
|---:|---:|---:|---:|---:|
| 200 (default) | 99.74 % | 99.82 % | **CRASH** | 0 % (post-crash) |
| 400 | 99.94 % | 99.89 % | **CRASH** | 0 % (post-crash) |
| **100** | 99.92 % | 99.90 % | **57.26 %** ✓ | **34.40 %** ✓ |
| 50 | 99.89 % | 99.93 % | **CRASH** | 0 % (post-crash) |

CRASH cells all carry the same Stirling FATAL:

```
F source_connector.cc:64] Failed to push data.
  Message = RowBatch size (69014615) is bigger than maximum table size (39478886).
```

## What this means

* **Light loads (≤ 30 k / 1.5 k/s):** cadence makes no measurable
  difference. Coverage is 99.7–99.9 % everywhere. The 200 ms default
  is fine in the steady-state regime.
* **Burst loads (≥ 60 k):** the default cadence triggers a Stirling
  abort. The per-`TransferData` RowBatch grows past the per-table
  store cap (≈ 39 MB for the dns_events share of a 1024 MB
  table-store), Stirling panics, the PEM pod restarts. **This is a
  denial-of-service failure mode, not "loss of resolution".**
* **100 ms cadence uniquely survived** the burst cells (57 % / 34 %
  retained, no abort). It halves the per-TransferData RowBatch
  size, which lands the push under the per-table cap. Coverage
  drops well below 100 % — those are perf-buffer drops between
  polls — but the PEM stays up.
* **50 ms did *not* help.** Same FATAL as default. The relationship
  is not monotone in cadence. Best current guess: at 50 ms the
  TransferData overhead alone consumes the budget, so a backlog
  builds and the next push includes ≥ 2 windows of data, exceeding
  the cap again. Iter 2 of every cadence aborted within one cell —
  consistent with a Stirling state machine that does not recover
  cleanly from a crash.

## What this changes about the PR

The detection knob is **not** what we exposed.
`SocketTraceConnector::kSamplingPeriod` controls perf-buffer poll
cadence, but the abort condition is a function of `kPushPeriod`
(`socket_trace_connector.h:119`, hard-coded 1000 ms) and the per-table
RowBatch cap (`source_connector.cc:64`). The minimum-viable fix is
probably to lower `kPushPeriod` (so each push is smaller) and/or to
raise the per-table cap dynamically when a push would overflow.

Both of those are one-line code changes, not env-var tuning. The
existing `PX_STIRLING_SOCKET_TRACER_SAMPLING_PERIOD_MS` env override
stays useful as an operator escape hatch — it converts an abort into
silent loss at 100 ms — but it does not raise the achievable detection
ceiling.

## Suggested next experiment

1. Expose `kPushPeriod` the same way:
   `PX_STIRLING_SOCKET_TRACER_PUSH_PERIOD_MS`. Test `{1000, 500, 250, 100}` ms.
2. Hypothesis: 250 ms push period at 60 k / 2.5 k/s gives clean
   100 % capture without aborts, at modest CPU cost.
3. If that holds, the deployable security-detection profile is
   *one* env-var, not five.

## Reproducing

```bash
# 1. Build the patched standalone_pem image
bazel build --config=x86_64_sysroot \
    //src/experimental/standalone_pem:standalone_pem_image
/.../standalone_pem_image.executable      # load into docker
docker tag <loaded-id> ghcr.io/k8sstormcenter/vizier-standalone_pem_image:0.14.18-secprof-sampling
docker save ... -o /tmp/sp.tar
sudo k3s ctr image import /tmp/sp.tar

# 2. Roll the DS
kubectl -n <ns> set image ds/standalone-pem \
    standalone-pem=ghcr.io/k8sstormcenter/vizier-standalone_pem_image:0.14.18-secprof-sampling
# 3. Run the sweep (sweep_sampling.sh wraps env-set + cells)
PEM_NS=<ns> PEM_DS=standalone-pem PEM_ADDR=<host:port> \
DNSPROBE=/tmp/dnsprobe DNSVERIFY=/tmp/dnsverify \
  bash src/e2e_test/security_profile/harness/sweep_sampling.sh
```
