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

# protocol-sweep.sh — sweep all 3 Pixie protocol seq-loaders simultaneously.
# Captures ALL metric categories per multiplier (loadgen, pixie, kubescape, CH)
# so render-proto-sweep.py can plot a single log-log scaling.png that exposes
# whichever stage is the bottleneck. See feedback_measure_all_metrics memory.
set -uo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
NS=px-protocol-loadtest
CH_URL='http://localhost:30123'
AUTH='pixie:pixie_password'

HTTP_BASE=1000
REDIS_BASE=1000
PGSQL_BASE=1000

# Per-multiplier timing — overridable for quick health-check sweeps.
# Defaults match the original calibrated sweep (30 + 180 = 210s per mult,
# plus ~30s rollout). Quick: WARMUP_S=15 MEASURE_S=90 → ~135s per mult.
WARMUP_S="${WARMUP_S:-30}"
MEASURE_S="${MEASURE_S:-180}"

if [ $# -eq 0 ]; then
  MULTS=(4 8 16)
else
  MULTS=("$@")
fi

OUT=/tmp/proto-sweep-$(date -u +%Y%m%d-%H%M%S)
mkdir -p "$OUT"
echo "sweep dir: $OUT" | tee "$OUT/sweep.log"
echo "multipliers: ${MULTS[*]}" | tee -a "$OUT/sweep.log"
echo "warmup=${WARMUP_S}s measure=${MEASURE_S}s" | tee -a "$OUT/sweep.log"

# RESET the adaptive_export operator before EVERY sweep. The operator's
# active set carries anomaly windows that persist across mults — and
# across sweeps. The "reset" is TWO steps:
#   (1) TRUNCATE forensic_db.adaptive_attribution — otherwise the
#       operator's `Rehydrate()` on startup pulls back stale pod-name
#       windows from hours-ago sessions and burns its refresh budget
#       querying pixie for pod names that don't exist anymore (verified
#       2026-05-16 — `t_start=16:47` rehydrated into a sweep that
#       started at 18:56; pixie returned 0 rows the whole sweep).
#   (2) kubectl rollout restart — flushes in-memory active set, picks
#       up new env vars, starts polling kubescape_logs fresh.
# Skippable by setting SWEEP_SKIP_OPERATOR_RESET=1.
if [ "${SWEEP_SKIP_OPERATOR_RESET:-0}" != "1" ]; then
  # 3-part reset:
  # (a) TRUNCATE kubescape_logs — otherwise the operator's trigger starts
  #     from watermark=0 and chews through every historical alert
  #     (sometimes >200k rows), creating attribution windows with
  #     t_start values from HOURS ago. Those windows then have the
  #     operator query pixie for 2+ hour wide slices that return 0
  #     and hang for 180s each (verified 2026-05-16 — 55 fan-outs,
  #     0 pushes, 0 errors, all goroutines stuck on giant-slice pixie
  #     queries).
  # (b) TRUNCATE adaptive_attribution — wipes the rehydrate source so
  #     the new operator starts with active set = empty.
  # (c) kubectl rollout restart — flushes in-memory state, picks up env
  #     var changes, fresh trigger goroutine.
  echo "operator reset: TRUNCATE kubescape_logs + adaptive_attribution" | tee -a "$OUT/sweep.log"
  kubectl exec -n clickhouse chi-forensic-soc-db-soc-cluster-0-0-0 -c clickhouse \
    -- clickhouse-client --multiquery --query="TRUNCATE TABLE forensic_db.kubescape_logs; TRUNCATE TABLE forensic_db.adaptive_attribution" \
    >/dev/null 2>&1 || true
  echo "operator reset: kubectl rollout restart adaptive-export" | tee -a "$OUT/sweep.log"
  kubectl rollout restart deployment/adaptive-export -n pl >/dev/null 2>&1 || true
  kubectl rollout status -n pl deploy/adaptive-export --timeout=90s >/dev/null 2>&1 || true
  # Give the new pod ~10s to subscribe to the trigger before starting load
  sleep 10
fi

date -u +"%Y-%m-%dT%H:%M:%SZ start" | tee -a "$OUT/sweep.log"

# Per-multiplier comprehensive CSV — all metrics in one row per mult.
CSV="$OUT/metrics.csv"
echo "mult,t0,t1,window_s,http_target,redis_target,pgsql_target,http_achieved,redis_achieved,pgsql_achieved,loadgen_total,http_srv_cpu_m,redis_srv_cpu_m,pgsql_srv_cpu_m,pem_cpu_m,pem_mem_mi,kelvin_cpu_m,kelvin_mem_mi,querybroker_cpu_m,querybroker_mem_mi,nodeagent_cpu_m,nodeagent_mem_mi,nodeagent_goroutines,ch_http_rate,ch_redis_rate,ch_pgsql_rate,ch_kubescape_rate,ch_attribution_rate,ct_start,ct_end,mult_t_start,mult_t_end" > "$CSV"

scale_client() {
  local dep="$1" rps="$2" conns="$3" msgs="$4"
  kubectl set env -n "$NS" "deployment/$dep" \
    "TARGET_RPS=$rps" "NUM_CONNECTIONS=$conns" "NUM_MESSAGES=$msgs" >/dev/null
}

client_pod_name() {
  # Pick the most-recently-created Running pod for this deployment so we
  # lock onto the NEW rollout (not a Terminating leftover from a prior mult).
  local label="$1"
  kubectl get pods -n "$NS" -l "name=$label" \
    --field-selector=status.phase=Running \
    --sort-by=.metadata.creationTimestamp \
    --no-headers 2>/dev/null \
    | tail -1 | awk '{print $1}'
}

client_count_of() {
  # Read the latest log "count=N" from a SPECIFIC pod (caller pre-resolves
  # at t0 so t1 reads the same pod and the delta is monotonic).
  local pod="$1"
  [ -z "$pod" ] && return
  kubectl logs -n "$NS" "$pod" -c client --tail=200 2>/dev/null | grep -oE "count=[0-9]+" | tail -1 | tr -d 'count='
}

ch_count() {
  curl -s -G -u "$AUTH" --data-urlencode "query=SELECT count() FROM forensic_db.$1 FORMAT TabSeparated" "$CH_URL/" 2>/dev/null
}

# ch_window_count <table> <time_col_expr> <unix_start> <unix_end> — count
# rows in forensic_db.<table> where <time_col_expr> falls inside the
# wall-clock window [unix_start, unix_end]. Used for true per-mult
# attribution: only rows whose pixie-capture / event time landed during
# this mult count toward the mult's ch_*_rate.
ch_window_count() {
  local tbl="$1" col="$2" ts="$3" te="$4"
  curl -s -G -u "$AUTH" --data-urlencode \
    "query=SELECT count() FROM forensic_db.${tbl} WHERE ${col} BETWEEN toDateTime(${ts}) AND toDateTime(${te}) FORMAT TabSeparated" \
    "$CH_URL/" 2>/dev/null
}

# top_cpu_mem <ns> <label_or_pod_match>  → echoes "cpu_m mem_mi" (m / Mi)
# label_or_pod_match is either a label selector "key=val" or a pod-name substring.
top_cpu_mem() {
  local ns="$1" sel="$2"
  local raw
  if echo "$sel" | grep -q '='; then
    raw=$(kubectl top pod -n "$ns" -l "$sel" --no-headers 2>/dev/null | head -1)
  else
    raw=$(kubectl top pod -n "$ns" --no-headers 2>/dev/null | grep "$sel" | head -1)
  fi
  if [ -z "$raw" ]; then echo "0 0"; return; fi
  local cpu=$(echo "$raw" | awk '{gsub("m","",$2); print $2}')
  local mem=$(echo "$raw" | awk '{gsub("Mi","",$3); print $3}')
  echo "${cpu:-0} ${mem:-0}"
}

# Scrape go_goroutines from a pod's /metrics endpoint
goroutines_for_pod() {
  local ns="$1" sel="$2" port="${3:-7888}"
  local ip=$(kubectl get pods -n "$ns" -l "$sel" -o jsonpath='{.items[0].status.podIP}' 2>/dev/null)
  [ -z "$ip" ] && { echo 0; return; }
  curl -s --max-time 2 "http://$ip:$port/metrics" 2>/dev/null \
    | awk '/^go_goroutines /{print int($2); exit}' || echo 0
}

ct_now() { cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null; }

# probe_e2e — during burn-in, verify EVERY CH table that the SOC chains write
# to is actually growing. Two chains:
#   (1) SBOB→kubescape→vector→CH:        forensic_db.kubescape_logs
#   (2) operator pixie fan-out→CH:       forensic_db.{http,redis,pgsql}_events,
#                                        forensic_db.adaptive_attribution
# Samples each table's row count every ~5 s for the full WARMUP_S window.
# Reports per-table growth; per-table PASS/FAIL; overall PASS if at least chain
# (1) flows (chain (2) requires adaptive_export operator deployed — its absence
# is logged as INFO, not FAIL, so a no-operator capacity sweep is still valid).
PROBE_TABLES=(kubescape_logs http_events redis_events pgsql_events adaptive_attribution)
probe_e2e() {
  local samples interval
  interval=5
  samples=$(( WARMUP_S / interval )); [ "$samples" -lt 3 ] && samples=3

  # T0 snapshot of every table.
  # INS = cumulative POSITIVE deltas (insert-rate signal). Absolute (PREV-T0)
  # is unreliable because forensic_db.kubescape_logs is subject to background
  # row removal (TTL merge or external retention process), so its count
  # oscillates even while inserts continue. Sum positives only.
  # NOTE: INS is exported globally (no `local`) so run_mult can fold its
  # cumulative-positive-deltas into ch_*_ins after the measure window.
  declare -A T0 PREV
  declare -gA INS
  for t in "${PROBE_TABLES[@]}"; do
    local v=$(ch_count "$t"); v=${v:-0}
    T0[$t]=$v
    PREV[$t]=$v
    INS[$t]=0
  done
  local v_err0
  v_err0=$(kubectl logs -n honey -l app.kubernetes.io/name=vector --since=60s 2>/dev/null \
           | grep -ciE 'error|timeout|failed')
  v_err0=${v_err0:-0}

  # Operator presence informs interpretation
  local op_ready
  op_ready=$(kubectl get deploy -n pl adaptive-export -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
  op_ready=${op_ready:-0}

  echo "  e2e-probe(warmup): kubescape_logs=${T0[kubescape_logs]} http_events=${T0[http_events]} redis_events=${T0[redis_events]} pgsql_events=${T0[pgsql_events]} adaptive_attribution=${T0[adaptive_attribution]}" | tee -a "$OUT/sweep.log"
  echo "    operator ready_replicas=$op_ready  vector_err_60s_baseline=$v_err0" | tee -a "$OUT/sweep.log"

  for s in $(seq 1 "$samples"); do
    sleep "$interval"
    local line="    +${s}/${samples}:"
    for t in "${PROBE_TABLES[@]}"; do
      local now=$(ch_count "$t"); now=${now:-0}
      local d=$((now - PREV[$t]))
      [ "$d" -gt 0 ] && INS[$t]=$(( INS[$t] + d ))
      line="$line ${t}=${now}(+${d},ins=${INS[$t]})"
      PREV[$t]=$now
    done
    echo "$line" | tee -a "$OUT/sweep.log"
  done

  local v_err1
  v_err1=$(kubectl logs -n honey -l app.kubernetes.io/name=vector --since=60s 2>/dev/null \
           | grep -ciE 'error|timeout|failed')
  v_err1=${v_err1:-0}
  local v_delta=$(( v_err1 - v_err0 ))

  # Per-table verdict — use INS (cumulative positive deltas).
  local ks_grew=0 op_grew=0 op_tables_grew=""
  [ "${INS[kubescape_logs]}" -gt 0 ] && ks_grew=1
  for t in http_events redis_events pgsql_events adaptive_attribution; do
    if [ "${INS[$t]}" -gt 0 ]; then
      op_grew=1
      op_tables_grew="$op_tables_grew ${t}+${INS[$t]}"
    fi
  done

  local verdict="✓"
  local note=""
  if [ "$ks_grew" -eq 0 ]; then
    verdict="⚠"
    note="kubescape_logs FLAT (SBOB/vector/CH path dead)."
  fi
  if [ "$op_ready" -gt 0 ] && [ "$op_grew" -eq 0 ]; then
    verdict="⚠"
    note="${note} operator deployed but no per-table growth (controller/pixie path dead)."
  fi
  if [ "$op_ready" -eq 0 ]; then
    note="${note} operator absent → pixie/adaptive tables expected 0."
  fi
  echo "  ${verdict} e2e-probe: ks_inserts=${INS[kubescape_logs]} op_tables[$op_tables_grew] vector_err_delta=+${v_delta}. ${note}" \
    | tee -a "$OUT/sweep.log"
  if [ "$ks_grew" -eq 0 ] || ([ "$op_ready" -gt 0 ] && [ "$op_grew" -eq 0 ]); then
    echo "    diagnose: kubectl get applicationprofile -n $NS; deploy labels; vector ConfigMap CH endpoint; operator logs" \
      | tee -a "$OUT/sweep.log"
    return 1
  fi
  return 0
}

deploy_redis_ns() { :; }  # already deployed externally — no-op

teardown_redis_ns() { :; }  # we don't tear down — just rollover via env

run_mult() {
  local m="$1"
  local http_rps=$(( HTTP_BASE  * m ))
  local redis_rps=$(( REDIS_BASE * m ))
  local pgsql_rps=$(( PGSQL_BASE * m ))
  local conns=$(( 50 + 50 * m )); [ "$conns" -gt 400 ] && conns=400

  echo "" | tee -a "$OUT/sweep.log"
  echo "=== MULT ${m}x   target: http=$http_rps  redis=$redis_rps  pgsql=$pgsql_rps  conns=$conns ===" | tee -a "$OUT/sweep.log"

  # ---- FULL-MULT CH baseline — snap BEFORE rollout so ch_*_rate covers
  #      the entire mult duration (rollout + warmup-with-probe + measure)
  #      instead of only the measure window. The operator's fan-out
  #      refreshes happen on a 30s cadence; the per-mult measure window
  #      is often shorter than one full refresh, so a measure-only
  #      window can miss inserts that DID happen during the mult.
  local mult_t_start=$(date -u +%s)
  local CH_H_PRE=$(ch_count http_events);          CH_H_PRE=${CH_H_PRE:-0}
  local CH_R_PRE=$(ch_count redis_events);         CH_R_PRE=${CH_R_PRE:-0}
  local CH_P_PRE=$(ch_count pgsql_events);         CH_P_PRE=${CH_P_PRE:-0}
  local CH_K_PRE=$(ch_count kubescape_logs);       CH_K_PRE=${CH_K_PRE:-0}
  local CH_A_PRE=$(ch_count adaptive_attribution); CH_A_PRE=${CH_A_PRE:-0}

  # NUM_MESSAGES is per-conn — total Run() msgs = NUM_MESSAGES * conns.
  # We need Run() to NEVER complete within a 3-min sweep window (otherwise
  # the client's counter resets and produces negative deltas). 1_000_000
  # per conn × 400 conns × 60s per million at 64x = 250+ min. Safe.
  scale_client http-client  "$http_rps"  "$conns" 1000000
  scale_client redis-client "$redis_rps" "$conns" 1000000
  scale_client pgsql-client "$pgsql_rps" "$conns" 1000000
  kubectl rollout status -n "$NS" deployment/http-client  --timeout=60s >/dev/null 2>&1
  kubectl rollout status -n "$NS" deployment/redis-client --timeout=60s >/dev/null 2>&1
  kubectl rollout status -n "$NS" deployment/pgsql-client --timeout=60s >/dev/null 2>&1
  # Force-delete any lingering Terminating pods so client_count's
  # Running-only filter sees exactly one pod per deployment.
  kubectl delete pods -n "$NS" --field-selector=status.phase=Terminating --force --grace-period=0 >/dev/null 2>&1 || true

  # warmup = e2e probe (samples kubescape_logs growth every 5s for WARMUP_S total)
  probe_e2e || true

  # ---- T0 snapshot — lock the pod names so t1 samples the same pod ----
  local HPOD=$(client_pod_name http-client)
  local RPOD=$(client_pod_name redis-client)
  local PPOD=$(client_pod_name pgsql-client)
  local t0=$(date -u +%s)
  local H0=$(client_count_of "$HPOD")
  local R0=$(client_count_of "$RPOD")
  local P0=$(client_count_of "$PPOD")
  local CT0=$(ct_now)
  local CH_H0=$(ch_count http_events)
  local CH_R0=$(ch_count redis_events)
  local CH_P0=$(ch_count pgsql_events)
  local CH_K0=$(ch_count kubescape_logs)
  local CH_A0=$(ch_count adaptive_attribution)

  sleep "$MEASURE_S"  # measure window

  # ---- T1 snapshot (SAME pods as t0) ----
  local t1=$(date -u +%s)
  local H1=$(client_count_of "$HPOD")
  local R1=$(client_count_of "$RPOD")
  local P1=$(client_count_of "$PPOD")
  local CT1=$(ct_now)
  local CH_H1=$(ch_count http_events)
  local CH_R1=$(ch_count redis_events)
  local CH_P1=$(ch_count pgsql_events)
  local CH_K1=$(ch_count kubescape_logs)
  local CH_A1=$(ch_count adaptive_attribution)

  # ---- CPU/Mem (single mid-window snapshot — best effort) ----
  read HSRV_CPU HSRV_MEM <<< "$(top_cpu_mem $NS name=http-server)"
  read RSRV_CPU RSRV_MEM <<< "$(top_cpu_mem $NS name=redis-server)"
  read PSRV_CPU PSRV_MEM <<< "$(top_cpu_mem $NS name=pgsql-server)"
  read PEM_CPU PEM_MEM   <<< "$(top_cpu_mem pl name=vizier-pem)"
  read KEL_CPU KEL_MEM   <<< "$(top_cpu_mem pl kelvin)"
  read QB_CPU  QB_MEM    <<< "$(top_cpu_mem pl query-broker)"
  read NA_CPU  NA_MEM    <<< "$(top_cpu_mem honey app=node-agent)"
  local NA_GO=$(goroutines_for_pod honey app=node-agent 7888)

  local elapsed=$((t1 - t0)); [ "$elapsed" -lt 1 ] && elapsed=1
  local hr=$(( (H1 - H0) / elapsed ))
  local rr=$(( (R1 - R0) / elapsed ))
  local pr=$(( (P1 - P0) / elapsed ))
  local tot=$(( hr + rr + pr ))
  # CH per-protocol rates — **true per-mult attribution**: count rows whose
  # time_ (pixie capture timestamp) falls strictly inside the wall-clock
  # window [mult_t_start, mult_t_end]. This isolates the load this mult
  # actually generated from operator catch-up writes for older alerts.
  # The previous "absolute count delta" approach over-counted by 28-75%
  # because the operator's slice [t_start, t_end) spans the past 5 min,
  # so writes landing during mult N include pixie data captured during
  # mult N-1, N-2, etc. (verified by verify-png-vs-db.sh).
  #
  # kubescape_logs uses event_time (UInt64 nanos), not time_; the others
  # use time_ (DateTime64). adaptive_attribution uses last_seen.
  local mult_t_end=$(date -u +%s)
  local mult_dur=$(( mult_t_end - mult_t_start )); [ "$mult_dur" -lt 1 ] && mult_dur=1
  local ch_h_ins ch_r_ins ch_p_ins ch_k_ins ch_a_ins
  ch_h_ins=$(ch_window_count http_events          time_                                             "$mult_t_start" "$mult_t_end") ; ch_h_ins=${ch_h_ins:-0}
  ch_r_ins=$(ch_window_count redis_events         time_                                             "$mult_t_start" "$mult_t_end") ; ch_r_ins=${ch_r_ins:-0}
  ch_p_ins=$(ch_window_count pgsql_events         time_                                             "$mult_t_start" "$mult_t_end") ; ch_p_ins=${ch_p_ins:-0}
  ch_k_ins=$(ch_window_count kubescape_logs       'fromUnixTimestamp64Nano(event_time::Int64)'      "$mult_t_start" "$mult_t_end") ; ch_k_ins=${ch_k_ins:-0}
  ch_a_ins=$(ch_window_count adaptive_attribution last_seen                                         "$mult_t_start" "$mult_t_end") ; ch_a_ins=${ch_a_ins:-0}
  local ch_h=$(( ch_h_ins / mult_dur ))
  local ch_r=$(( ch_r_ins / mult_dur ))
  local ch_p=$(( ch_p_ins / mult_dur ))
  local ch_k=$(( ch_k_ins / mult_dur ))
  local ch_a=$(( ch_a_ins / mult_dur ))

  # Record mult_t_start in CSV so post-hoc verifier can reconstruct the
  # exact wall-clock window without estimation (column 30, appended).
  echo "$m,$t0,$t1,$elapsed,$http_rps,$redis_rps,$pgsql_rps,$hr,$rr,$pr,$tot,$HSRV_CPU,$RSRV_CPU,$PSRV_CPU,$PEM_CPU,$PEM_MEM,$KEL_CPU,$KEL_MEM,$QB_CPU,$QB_MEM,$NA_CPU,$NA_MEM,$NA_GO,$ch_h,$ch_r,$ch_p,$ch_k,$ch_a,$CT0,$CT1,$mult_t_start,$mult_t_end" >> "$CSV"

  printf "  %dx  loadgen http=%d redis=%d pgsql=%d total=%d  |  CH/s http=%d redis=%d pgsql=%d ks=%d attrib=%d  |  pem=%sm kelvin=%sm qb=%sm na=%sm(go=%s)  |  ct %s→%s\n" \
    "$m" "$hr" "$rr" "$pr" "$tot" \
    "$ch_h" "$ch_r" "$ch_p" "$ch_k" "$ch_a" \
    "${PEM_CPU:-?}" "${KEL_CPU:-?}" "${QB_CPU:-?}" "${NA_CPU:-?}" "${NA_GO:-?}" \
    "$CT0" "$CT1" \
    | tee -a "$OUT/sweep.log"
}

for m in "${MULTS[@]}"; do
  run_mult "$m"
done

echo "" | tee -a "$OUT/sweep.log"
date -u +"%Y-%m-%dT%H:%M:%SZ end" | tee -a "$OUT/sweep.log"
echo "=== sweep complete ===" | tee -a "$OUT/sweep.log"
echo "results: $OUT" | tee -a "$OUT/sweep.log"
echo "csv: $CSV" | tee -a "$OUT/sweep.log"
