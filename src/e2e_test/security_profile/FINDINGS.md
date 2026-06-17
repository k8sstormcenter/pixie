# Security-profile findings — DNS recon **retention**, not detection

> **Correction (post-review):** the original draft of this file framed
> the flag deltas as "+46 pp DNS coverage" — that framing was wrong.
> What the flags improved was **table-store retention window**, not
> Stirling's ability to capture the queries. Stirling captures 100 %
> of the queries on both profiles; what changes is how long the rows
> survive in the PEM ring buffer before being evicted by rotation,
> which the verifier then races against.

> **A 2 GB-per-PEM table-store is too expensive to recommend.**
> Listed here for the record only. Real recon-detection work needs
> either a tighter Stirling sampling cadence, a per-table sizing
> knob, or a streaming consumer that drains the ring before it
> rotates. None of those are envvar-tunable today.

## Test shape

`dnsprobe` (Go) fires N salted A-queries at rate R q/s against a
pinned resolver (NXDOMAIN expected). `dnsverify` (Go + pxapi) reads
`dns_events` from the PEM via its direct port and computes the
set intersection of sent vs seen names. **Coverage % is a retention
metric**: how many sent names survive in the ring buffer until the
verifier reads them, ~5–10 s after the probe ends.

## Profiles measured

| profile | TableStore | http % | control-bw / cpu | datastream buf | DNS tracing |
|---|---:|---:|---:|---:|:---:|
| default          | 1024 MB | 40 % | 5 MiB/s  | 1 MiB | on |
| security_runtime | 2048 MB | 25 % | 20 MiB/s | 4 MiB | on |

All envvar-settable. No source changes. No TLS (gh#2095, deferred).

## What I observed

**Default profile retention cliff** (24 cells, N ∈ {100…200 k} × R ∈ {100…20 k} q/s):

* ≤ 50 k queries: ≥99.5 % retained at every rate.
* 100 k queries: ~85 % retained — ~15 k evicted before verifier reads.
* 200 k queries: ~53 % retained — ~93 k evicted.

**Rate-independent loss**: 5 k vs 20 k q/s give the same retention.
This rules out per-CPU control-bw as the bottleneck and confirms
**Stirling captured everything**; the loss is downstream, in the
table-store rotation between capture and read.

**security_runtime "fix"** (10 cells, N ∈ {50 k…200 k} q/s × R ∈ {5 k…20 k}):

* 100 k @ 5 k/s: 85.13 % → 99.93 % (+14.8 pp retention)
* 200 k @ 5 k/s: 53.44 % → 99.88 % (+46.4 pp retention)
* 200 k @ 20 k/s: 53.53 % → 99.87 % (+46.3 pp retention)

This is **buying retention with RAM** — +1 GB table-store per PEM
to fit a longer history. It does not change Stirling's BPF
sampling, the perf buffer, the parser, or the queue draining.
A reader that polled every 5 s would see 100 % on the default
30 MB buffer too.

## Why this is not a detection improvement

`PL_TABLE_STORE_DATA_LIMIT_MB`, `PL_TABLE_STORE_HTTP_EVENTS_PERCENT`,
`STIRLING_SOCKET_TRACER_TARGET_CONTROL_BW_PERCPU`, and
`PL_DATASTREAM_BUFFER_SIZE` all live downstream of capture. They
control how long a row survives, not whether it was captured. The
upstream control — Stirling's perf-buffer poll cadence — is
**`kSamplingPeriod = 200 ms`**, a `static constexpr` in
`src/stirling/source_connectors/socket_tracer/socket_trace_connector.h`.
It is not envvar-settable; tuning it requires a custom PEM build.

That cadence governs how often Stirling drains the BPF perf buffer.
Under bursty recon (≫ a few k events in one 200 ms window) the
kernel-side perf buffer overflows and **the loss happens before
the row ever reaches the table-store**. That is the genuine
detection cliff and the next thing to measure — see the follow-up
section.

## Follow-up: measure kSamplingPeriod for DNS only

Planned isolation: vary `kSamplingPeriod` alone — say {50 ms,
100 ms, 200 ms, 400 ms} — against a bursty DNS load that stresses
the perf buffer (50 k queries fired in 50–200 ms). Hold table-store
at default (1024 MB) throughout. Measure:

1. `dns_events` row count (does Stirling drop pre-table?).
2. Stirling-side perf-buffer drop counters
   (`bpf_map_lookup_elem`-derived stats already exported).
3. PEM CPU delta — faster polling buys nothing if the cost is
   prohibitive.

This requires a small Stirling patch: either expose
`kSamplingPeriod` as a gflag, or wire a `PX_STIRLING_DNS_SAMPLING_MS`
override into `SocketTraceConnector::InitImpl()`. Build via
`bazel --config=x86_64_sysroot //src/vizier/services/agent:pem_image`,
import to k3s with `k3s ctr image import`.

Hypothesis: kSamplingPeriod is a real detection knob; bigger
table-store is not. The retention numbers above will be repeated
unchanged, but the test will finally distinguish *captured* from
*retained*.

## Reproducing what is in this file

```bash
go build -o /tmp/dnsprobe   ./src/e2e_test/security_profile/tools/dnsprobe
go build -o /tmp/dnsverify  ./src/e2e_test/security_profile/tools/dnsverify

salt=$(/tmp/dnsprobe -n 200000 -rate 5000 -workers 64 \
    -domain secprof.invalid -resolver 1.1.1.1:53 \
    -out /tmp/sent.csv)
/tmp/dnsverify -addr <pem-host>:12345 -direct -salt "$salt" \
    -lookback 240 -out /tmp/seen.csv

source src/e2e_test/security_profile/harness/lib.sh
coverage_stats /tmp/sent.csv /tmp/seen.csv   # expect ~53 % on default 1024 MB
```
