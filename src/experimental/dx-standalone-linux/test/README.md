# Tests — single-VM dx

- `smoke.sh` — boot dx (referral-only; no eBPF), inject a known attack, assert a
  malignant verdict. Runs anywhere. `DX_BIN=/path/to/dx-daemon ./smoke.sh`.
- `nfr.sh` — drive N referrals, report time-to-verdict p50/p95, throughput,
  drops, cache hit-rate, RSS/CPU from /metrics. `N=500 ./nfr.sh` against a running dx.

Pass bars (rebaseline on the target VM): smoke exits 0 (a ruled_in verdict);
nfr p95 time-to-verdict <= 0.5s at the pinned CPU, 0 drops, and — when a real PEM
is attached — bench_unavailable == 0 (else the run is referral-only, stated, not
a silent pass).
