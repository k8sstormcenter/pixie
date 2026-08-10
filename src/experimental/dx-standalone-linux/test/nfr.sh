#!/usr/bin/env bash
# Copyright 2018- The Pixie Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0
# nfr.sh — single-VM NFR harness. Drives N referrals through dx and reports the
# pure-Linux equivalent of the k8s report's G1/G2: time-to-verdict p50/p95,
# throughput, backpressure drops, and dx RSS/CPU — all from /metrics. The PEM
# eBPF pull is exercised when a PEM is present (pxdirect), else referral-only.
#
# Env: N (default 500), RATE_SLEEP (default 0 = as fast as possible),
#      METRICS (default http://127.0.0.1:9095/metrics),
#      DX (default http://127.0.0.1:9099)
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
INJECT="$HERE/../inject/inject-referral.sh"
N="${N:-500}"
DX="${DX:-http://127.0.0.1:9099}"
METRICS="${METRICS:-http://127.0.0.1:9095/metrics}"

metric() { curl -s "$METRICS" 2>/dev/null | awk -v k="$1" '$1==k {print $2; exit}'; }
hist_q() { # name quantile  → interpolate from cumulative _bucket
  curl -s "$METRICS" 2>/dev/null | python3 -c '
import sys,re
name,q=sys.argv[1],float(sys.argv[2])
b=[];cnt=None
for ln in sys.stdin:
    m=re.match(re.escape(name)+r"_bucket\{le=\"([^\"]+)\"\}\s+(\S+)",ln)
    if m: b.append((float("inf") if m.group(1)=="+Inf" else float(m.group(1)),float(m.group(2))))
    elif ln.startswith(name+"_count"): cnt=float(ln.split()[-1])
if not b or not cnt: print("n/a"); sys.exit()
b.sort(); t=q*cnt
for le,c in b:
    if c>=t: print(le); break
' "$1" "$2"; }

echo "[nfr] baseline metrics"
cpu0="$(metric process_cpu_seconds_total)"; t0="$(date +%s)"
verd0="$(metric dx_verdicts_total || echo 0)"

echo "[nfr] driving $N referrals into $DX ..."
for i in $(seq 1 "$N"); do
  case $((i % 4)) in
    0) "$INJECT" benign "$DX" >/dev/null 2>&1 ;;
    1) "$INJECT" argocd-render "$DX" >/dev/null 2>&1 ;;
    2) "$INJECT" log4shell-spawn "$DX" >/dev/null 2>&1 ;;
    3) "$INJECT" cred-escalation "$DX" >/dev/null 2>&1 ;;
  esac
  [ "${RATE_SLEEP:-0}" != "0" ] && sleep "$RATE_SLEEP"
done
sleep 3  # let the async workups drain

t1="$(date +%s)"; cpu1="$(metric process_cpu_seconds_total)"
elapsed=$(( t1 - t0 )); [ "$elapsed" -lt 1 ] && elapsed=1

echo ""
echo "══════════ single-VM NFR report ══════════"
printf "  referrals driven          : %s over %ss (%s/s)\n" "$N" "$elapsed" "$(( N / elapsed ))"
printf "  time-to-verdict p50 / p95 : %s / %s s\n" "$(hist_q dx_time_to_verdict_seconds 0.50)" "$(hist_q dx_time_to_verdict_seconds 0.95)"
printf "  bench-query p95 (PEM pull): %s s  (n/a = referral-only, no PEM)\n" "$(hist_q dx_bench_query_duration_seconds 0.95)"
printf "  verdicts total            : %s\n" "$(metric dx_verdicts_total || echo 0)"
printf "  referrals dropped (backp.): %s\n" "$(metric dx_referrals_dropped_total || echo 0)"
printf "  bench errors / unavailable: %s / %s\n" "$(metric dx_bench_errors_total || echo 0)" "$(metric dx_bench_unavailable || echo 0)"
printf "  cache hit-rate            : "; curl -s "$METRICS" 2>/dev/null | python3 -c '
import sys,re
h=t=b=0.0
for ln in sys.stdin:
    m=re.match(r"dx_bench_pull_total\{[^}]*result=\"([^\"]+)\"[^}]*\}\s+(\S+)",ln)
    if m:
        v=float(m.group(2)); t+=v
        if m.group(1) in ("querycache_hit","telemetry_hit"): h+=v
print(f"{h/t:.2f}" if t else "n/a")'
printf "  dx RSS                    : %s bytes\n" "$(metric process_resident_memory_bytes || echo n/a)"
printf "  dx CPU (total / per-verdict): %s / %.4f core-s\n" \
  "$cpu1" "$(python3 -c "v=${verd0:-0}; vv=$(metric dx_verdicts_total || echo 0); c0=${cpu0:-0}; c1=${cpu1:-0}; d=vv-v; print((c1-c0)/d if d>0 else 0)")"
echo "═══════════════════════════════════════════"
echo "[nfr] NFR bar (rebaseline on the target VM): p95 time-to-verdict <= 0.5s @ the pinned CPU; drops == 0; bench_unavailable == 0 when a PEM is attached."
