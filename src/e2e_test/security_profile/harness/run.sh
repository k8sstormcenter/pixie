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

# run.sh — orchestrates one full security_profile sweep across the
# {default, security-runtime, security-aggressive} PEM profiles for a
# matrix of (N, R) DNS-probe sizes.
#
# Layout of the result tree:
#
#   results/<profile>/N=<n>_R=<r>/
#     dnsprobe-sent.csv
#     dnsverify-seen.csv
#     stats.txt   # K=V from coverage_stats
#     pem-top.txt # `kubectl top` snapshot before/after
#
# A summary.csv (one row per cell) lands at the top of results/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=src/e2e_test/security_profile/harness/lib.sh
source "$here/lib.sh"

RESULTS_ROOT="${RESULTS_ROOT:-$here/results}"
PROBE_POD="${PROBE_POD:-dnsprobe}"
PROBE_NS="${PROBE_NS:-default}"
PEM_DIRECT_HOST="${PEM_DIRECT_HOST:-127.0.0.1}"

# default sweep — small Ns first so a broken profile fails fast.
read -r -a N_LIST <<< "${N_LIST:-100 1000 5000 10000}"
read -r -a R_LIST <<< "${R_LIST:-100 1000 5000}"
read -r -a PROFILES <<< "${PROFILES:-default security_runtime security_aggressive}"

mkdir -p "$RESULTS_ROOT"
summary="$RESULTS_ROOT/summary.csv"
echo "profile,n,rate,sent_unique,seen_unique,captured_unique,coverage_pct,unresolved_rows" > "$summary"

run_one() {
  local profile="$1" n="$2" rate="$3"
  local cell_dir="$RESULTS_ROOT/$profile/N=${n}_R=${rate}"
  mkdir -p "$cell_dir"
  local sent="$cell_dir/dnsprobe-sent.csv"
  local seen="$cell_dir/dnsverify-seen.csv"

  echo "==> $profile N=$n R=$rate"
  local salt
  salt=$(kc -n "$PROBE_NS" exec "$PROBE_POD" -- \
    /usr/local/bin/dnsprobe -n "$n" -rate "$rate" -out /tmp/sent.csv)
  kc -n "$PROBE_NS" cp "$PROBE_POD:/tmp/sent.csv" "$sent"

  # Let the PEM flush its 200ms sampling window + table-store push.
  sleep 5
  /usr/local/bin/dnsverify -addr "$PEM_DIRECT_HOST:$SECPROF_PEM_DIRECT_PORT" \
    -direct -salt "$salt" -lookback 120 -out "$seen"

  coverage_stats "$sent" "$seen" | tee "$cell_dir/stats.txt"
  # Snapshot PEM resource use — `top` is best-effort; if metrics-server
  # is wedged we still want the rest of the run.
  kc -n "$SECPROF_NS" top pod -l "name=$SECPROF_PEM_DS" \
    > "$cell_dir/pem-top.txt" 2>&1 || true

  # Append to summary.
  awk -F= -v p="$profile" -v n="$n" -v r="$rate" '
    {kv[$1]=$2}
    END {
      printf "%s,%d,%d,%s,%s,%s,%s,%s\n", p, n, r,
        kv["sent_unique"], kv["seen_unique"], kv["captured_unique"],
        kv["coverage_pct"], kv["unresolved_rows"]
    }' "$cell_dir/stats.txt" >> "$summary"
}

for profile in "${PROFILES[@]}"; do
  case "$profile" in
    default)              apply_pem_env "$here/flags_default.env" ;;
    security_runtime)     apply_pem_env "$here/flags_security_runtime.env" ;;
    security_aggressive)  apply_pem_env "$here/flags_security_aggressive.env" ;;
    *) echo "unknown profile: $profile"; exit 2 ;;
  esac
  # shellcheck disable=SC2119
  wait_pem_ready
  # Give the PEM 15s to settle before the first cell — its first
  # control-event window is half-empty otherwise.
  sleep 15

  for n in "${N_LIST[@]}"; do
    for r in "${R_LIST[@]}"; do
      run_one "$profile" "$n" "$r"
    done
  done
done

echo
echo "summary: $summary"
column -t -s, "$summary"
