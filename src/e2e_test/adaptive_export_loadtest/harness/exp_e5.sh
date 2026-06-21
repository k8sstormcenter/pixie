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

# exp_e5.sh — E5 live data-plane reproducibility: real Pixie captures a counted,
# sealed, pod-pinned band; AE pulls it ONCE; we assert the forensic_db deltas
# equal the generator's ground truth, across REPS reps.
#
# Output CSV (stdout + $OUT): rep,http_exp,http_act,dns_exp,dns_act,pgsql_exp,
#   pgsql_act,conn_est,conn_act,attrib,uniq_hash,wm_exp,wm_act,pass
#
# Usage: REPS=100 HTTP_N=100 DNS_N=100 PGSQL_N=100 OUT=/tmp/e5.csv ./exp_e5.sh
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$HERE/lib.sh"
INJECT="$HERE/inject.sh"

REPS="${REPS:-100}"
HTTP_N="${HTTP_N:-100}"; DNS_N="${DNS_N:-100}"; PGSQL_N="${PGSQL_N:-100}"
CONN_TOL="${CONN_TOL:-5}"            # conn_stats tolerance band above HTTP_N+1
SETTLE_S="${SETTLE_S:-4}"            # Stirling flush settle before injecting
PULL_TIMEOUT="${PULL_TIMEOUT:-40}"   # max wait for AE single-pull to land
OUT="${OUT:-/tmp/aeload_e5.csv}"

ch_portforward_up
apply_sinks
# Absorb the AE trigger cold-start on every node (gen pods may land on any node).
for n in $(nodes_list); do warmup "$n"; done

echo "rep,http_exp,http_act,dns_exp,dns_act,pgsql_exp,pgsql_act,conn_est,conn_act,attrib,uniq_hash,wm_exp,wm_act,pass" | tee "$OUT"

for rep in $(seq 1 "$REPS"); do
  name="gen-e5-$(printf '%03d' "$rep")"   # zero-pad → collision-proof LIKE filter

  mani="$(fire_gen "$name" "$HTTP_N" "$DNS_N" "$PGSQL_N")" || { echo "$rep,,,,,,,,,,,,,FIRE_FAIL" | tee -a "$OUT"; continue; }
  b1="$(jget "$mani" b1)"           # band end, unix NANOS (gen clock)
  b1_s=$(( b1 / 1000000000 ))       # → unix SECONDS = production event_time unit
  http_exp="$(jget "$mani" http)"; dns_exp="$(jget "$mani" dns)"; pgsql_exp="$(jget "$mani" pgsql)"
  conn_est="$(jget "$mani" conn_tcp_est)"
  # Fixture hostname MUST be the node the gen pod landed on, so the AE pod on
  # that node reads the trigger (AE polls kubescape_logs WHERE hostname=node).
  node="$(jget "$mani" node)"
  [[ -n "$node" ]] || { del_gen "$name"; echo "$rep,,,,,,,,,,,,,NO_NODE" | tee -a "$OUT"; continue; }

  sleep "$SETTLE_S"   # let the band flush into Pixie before the window query

  # Inject the single trigger fixture pinned to THIS rep's pod, event_time=B1.
  "$INJECT" --endpoint "$CH_HTTP" --user "$CH_RW_USER" --pass "$CH_RW_PASS" \
    --ns "$AELOAD_NS" --pod "$name" --rule R0001 --pid 1234 --comm java \
    --event-time "$b1_s" --hostname "$node" >&2 \
    || { del_gen "$name"; echo "$rep,,,,,,,,,,,,,INJECT_FAIL" | tee -a "$OUT"; continue; }

  # Wait for AE's single pull to land (http_events for this pod reaches exp, or
  # timeout). The pod stays alive (held) so upid resolves during the pull.
  http_act=0
  for _ in $(seq 1 "$PULL_TIMEOUT"); do
    http_act="$(count_pod http_events "$name")"
    [[ "$http_act" -ge "$http_exp" ]] && break
    sleep 1
  done
  dns_act="$(count_pod dns_events "$name")"
  pgsql_act="$(count_pod pgsql_events "$name")"
  conn_act="$(count_pod conn_stats "$name")"
  attrib="$(attrib_count "$node" "$name")"
  uhash="$(uniq_hashes "$node" "$name")"
  wm_act="$(watermark_of "$node")"

  pass="PASS"
  [[ "$http_act"  == "$http_exp"  ]] || pass="FAIL_http"
  [[ "$dns_act"   == "$dns_exp"   ]] || pass="${pass}|FAIL_dns"
  [[ "$pgsql_act" == "$pgsql_exp" ]] || pass="${pass}|FAIL_pgsql"
  [[ "$attrib"    == "1"          ]] || pass="${pass}|FAIL_attrib"
  # watermark persists on a ~5s throttle → report only (WARN), don't hard-gate.
  [[ "$wm_act"    == "$b1_s"      ]] || pass="${pass}|WARN_wm"
  # conn_stats: tolerance gate (sampled cumulative counters), not exact.
  if [[ "$conn_act" -lt "$conn_est" || "$conn_act" -gt $((conn_est + CONN_TOL)) ]]; then
    pass="${pass}|WARN_conn"
  fi

  echo "$rep,$http_exp,$http_act,$dns_exp,$dns_act,$pgsql_exp,$pgsql_act,$conn_est,$conn_act,$attrib,$uhash,$b1_s,$wm_act,$pass" | tee -a "$OUT"
  del_gen "$name"
done

log "E5 done -> $OUT"
python3 "$HERE/stats.py" "$OUT" || true
