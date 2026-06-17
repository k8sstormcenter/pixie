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

# Isolation sweep — varies ONLY PX_STIRLING_SOCKET_TRACER_SAMPLING_PERIOD_MS
# (the new env override for SocketTraceConnector::kSamplingPeriod).
# Keeps the table-store at the chart default (1024 MB), no other knob
# touched. Measures (retention coverage, PEM CPU, PEM RSS) per cell.
#
# Requires a custom PEM image (bazel
# //src/experimental/standalone_pem:standalone_pem_image) deployed to
# the `honey/standalone-pem` daemonset; see README.md.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$here/lib.sh"

OUT=${OUT:-$here/results/sampling_sweep.csv}
PEM_ADDR=${PEM_ADDR:-10.0.2.12:12345}
PEM_NS=${PEM_NS:-honey}
PEM_DS=${PEM_DS:-standalone-pem}
DNSPROBE=${DNSPROBE:-/tmp/dnsprobe}
DNSVERIFY=${DNSVERIFY:-/tmp/dnsverify}

mkdir -p "$(dirname "$OUT")"
echo "sampling_ms,N,rate,sent,seen,captured,coverage_pct,pem_cpu_m,pem_mem_mi,duration_s" > "$OUT"

# Bursty cells: short wall-time, very high instantaneous rate. These
# are the ones where 200 ms polling should drop and 50 ms should not.
cells=(
  "10000 5000"     # 2 s burst
  "20000 10000"    # 2 s burst, twice the queue depth
  "50000 25000"    # 2 s burst, 5x default cap
  "100000 50000"   # 2 s burst, 10x — perf-buffer territory
)

set_sampling() {
  local ms="$1"
  if [[ "$ms" == "default" ]]; then
    kc -n "$PEM_NS" set env "daemonset/$PEM_DS" \
      PX_STIRLING_SOCKET_TRACER_SAMPLING_PERIOD_MS- >&2
  else
    kc -n "$PEM_NS" set env "daemonset/$PEM_DS" \
      "PX_STIRLING_SOCKET_TRACER_SAMPLING_PERIOD_MS=$ms" >&2
  fi
  kc -n "$PEM_NS" rollout status "ds/$PEM_DS" --timeout=180s >&2
  # Stirling needs a few seconds after rollout for InitImpl + BPF
  # attach to finish. 15 s is conservative.
  sleep 15
}

pem_top() {
  kc -n "$PEM_NS" top pod -l name="$PEM_DS" --no-headers 2>/dev/null \
    | awk '{print $2","$3}' | head -1
}

run_cell() {
  local ms="$1" N="$2" R="$3"
  local sent="/tmp/sampsweep-sent.csv" seen="/tmp/sampsweep-seen.csv"
  local t0 dur salt stats cov s u c pem
  t0=$(date +%s)
  salt=$("$DNSPROBE" -n "$N" -rate "$R" -workers 64 \
      -domain secprof.invalid -resolver 1.1.1.1:53 \
      -out "$sent" 2>>"$here/results/sampling_sweep.log" | tail -1)
  # Read sooner than the default 5 s sleep — at burst rates the ring
  # rotates fast, so the dnsverify needs to land quickly to make the
  # comparison fair across cadences.
  "$DNSVERIFY" -addr "$PEM_ADDR" -direct -salt "$salt" \
      -lookback 180 -out "$seen" 2>>"$here/results/sampling_sweep.log"
  dur=$(( $(date +%s) - t0 ))
  pem=$(pem_top || echo ",")
  stats=$(coverage_stats "$sent" "$seen")
  s=$(echo "$stats" | awk -F= '/^sent_unique/{print $2}')
  u=$(echo "$stats" | awk -F= '/^seen_unique/{print $2}')
  c=$(echo "$stats" | awk -F= '/^captured_unique/{print $2}')
  cov=$(echo "$stats" | awk -F= '/^coverage_pct/{print $2}')
  local cpu mem
  cpu=$(echo "$pem" | awk -F, '{gsub(/m$/,"",$1); print $1}')
  mem=$(echo "$pem" | awk -F, '{gsub(/Mi$/,"",$2); print $2}')
  echo "$ms,$N,$R,$s,$u,$c,$cov,$cpu,$mem,$dur" | tee -a "$OUT"
}

for ms in default 400 200 100 50; do
  echo "==> sampling_period=$ms" >&2
  set_sampling "$ms"
  for cell in "${cells[@]}"; do
    read -r N R <<< "$cell"
    run_cell "$ms" "$N" "$R"
  done
done

echo
echo "wrote $OUT"
