#!/usr/bin/env bash
# lib-probe.sh — burn-in e2e probe helpers shared between protocol-sweep.sh
# and protocol-sweep-test.sh. Pure functions only — no top-level side effects.
#
# Required globals/env set by the caller before invoking probe_e2e:
#   PROBE_TABLES — array of CH table names to sample (e.g. kubescape_logs ...)
#   WARMUP_S     — total warmup duration in seconds (probe runs for this long)
#   NS           — loadtest namespace (for the diagnose hint)
#   OUT          — sweep output dir (sweep.log gets the probe trace appended)
#
# Required callable shims (caller defines these — testable via mocking):
#   ch_count <table>  -> echoes current row count
#   vector_err_count  -> echoes count of vector ERROR/timeout/failed lines in last 60s
#   operator_ready    -> echoes adaptive-export deployment readyReplicas (0 if absent)
#
# probe_e2e walks WARMUP_S seconds in 5s ticks, sampling each table; sums
# POSITIVE per-tick deltas into INS[t] (so background row-removal e.g. TTL
# merges doesn't mask insert activity). Returns 0 if PASS, 1 if FAIL.
#
# After return, INS[t] is populated for each table.

probe_e2e() {
  local samples interval
  interval=${PROBE_INTERVAL_S:-5}
  samples=$(( WARMUP_S / interval )); [ "$samples" -lt 3 ] && samples=3

  declare -gA INS
  declare -A T0 PREV
  for t in "${PROBE_TABLES[@]}"; do
    local v
    v=$(ch_count "$t"); v=${v:-0}
    T0[$t]=$v
    PREV[$t]=$v
    INS[$t]=0
  done

  local v_err0
  v_err0=$(vector_err_count); v_err0=${v_err0:-0}

  local op_ready
  op_ready=$(operator_ready); op_ready=${op_ready:-0}

  echo "  e2e-probe(warmup): kubescape_logs=${T0[kubescape_logs]:-0} http_events=${T0[http_events]:-0} redis_events=${T0[redis_events]:-0} pgsql_events=${T0[pgsql_events]:-0} adaptive_attribution=${T0[adaptive_attribution]:-0}" | tee -a "${OUT:-/dev/null}/sweep.log" 2>/dev/null
  echo "    operator ready_replicas=$op_ready  vector_err_60s_baseline=$v_err0" | tee -a "${OUT:-/dev/null}/sweep.log" 2>/dev/null

  local s
  for s in $(seq 1 "$samples"); do
    sleep "$interval"
    local line="    +${s}/${samples}:"
    local t
    for t in "${PROBE_TABLES[@]}"; do
      local now=$(ch_count "$t"); now=${now:-0}
      local d=$((now - PREV[$t]))
      [ "$d" -gt 0 ] && INS[$t]=$(( INS[$t] + d ))
      line="$line ${t}=${now}(+${d},ins=${INS[$t]})"
      PREV[$t]=$now
    done
    echo "$line" | tee -a "${OUT:-/dev/null}/sweep.log" 2>/dev/null
  done

  local v_err1
  v_err1=$(vector_err_count); v_err1=${v_err1:-0}
  local v_delta=$(( v_err1 - v_err0 ))

  local ks_grew=0 op_grew=0 op_tables_grew=""
  [ "${INS[kubescape_logs]:-0}" -gt 0 ] && ks_grew=1
  for t in http_events redis_events pgsql_events adaptive_attribution; do
    if [ "${INS[$t]:-0}" -gt 0 ]; then
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
  echo "  ${verdict} e2e-probe: ks_inserts=${INS[kubescape_logs]:-0} op_tables[$op_tables_grew] vector_err_delta=+${v_delta}. ${note}" | tee -a "${OUT:-/dev/null}/sweep.log" 2>/dev/null

  if [ "$ks_grew" -eq 0 ] || ([ "$op_ready" -gt 0 ] && [ "$op_grew" -eq 0 ]); then
    echo "    diagnose: kubectl get applicationprofile -n ${NS:-px-protocol-loadtest}; deploy labels; vector ConfigMap CH endpoint; operator logs" | tee -a "${OUT:-/dev/null}/sweep.log" 2>/dev/null
    return 1
  fi
  return 0
}
