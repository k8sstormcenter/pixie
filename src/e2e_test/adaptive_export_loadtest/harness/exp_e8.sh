#!/usr/bin/env bash
# exp_e8.sh — SUSTAINED same-pod-over-time: the bug-hunt for "writes succeed
# initially, then STOP, while the data is still on the Pixie side."
#
# One long-lived pod keeps producing NEW kubescape anomalies over time. A healthy
# AE keeps processing every new anomaly: adaptive_attribution.n_anomalies grows,
# last_seen advances, the active window stays open, and (data mode) protocol rows
# keep being written. A STALL — n_anomalies / last_seen freezing while we keep
# injecting — reproduces the production symptom.
#
# MODE=control (default): inject anomalies + track n_anomalies/last_seen/watermark
#   over TICKS. No Pixie needed. Catches a trigger/watermark/dedup-side stall.
# MODE=data: ALSO run a held gen pod producing continuous HTTP/DNS/PGSQL traffic,
#   and track per-pod protocol-table row growth (needs a registered vizier).
#
# event_time is unix SECONDS (production unit). BURST>1 injects BURST anomalies at
# the SAME event_time per tick — the realistic "many R0001 in one second" shape
# that probes the watermark-boundary fingerprint dedup (prime suspect).
#
# Usage: MODE=control TICKS=40 INTERVAL=3 BURST=1 OUT=/tmp/e8.csv ./exp_e8.sh
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$HERE/lib.sh"
INJECT="$HERE/inject.sh"

MODE="${MODE:-control}"
TICKS="${TICKS:-40}"
INTERVAL="${INTERVAL:-3}"     # seconds between ticks
BURST="${BURST:-1}"           # anomalies per tick (same event_time if >1)
NODE="${NODE:-$(first_node)}"
OUT="${OUT:-/tmp/aeload_e8_${MODE}.csv}"
POD="${POD:-sus-$(now_s)}"    # the one sustained pod under test

ch_portforward_up
[[ -n "$NODE" ]] || die "no node resolved"
log "E8 sustained: mode=$MODE node=$NODE pod=$POD ticks=$TICKS interval=${INTERVAL}s burst=$BURST"
warmup "$NODE"

GEN=""
if [[ "$MODE" == "data" ]]; then
  apply_sinks
  GEN="$POD"   # the gen pod name == the fixture pod (df.pod filter isolates it)
  hip="$(svc_ip http-sink)"; pip="$(svc_ip pg-sink)"
  # Long-lived gen that keeps firing: we re-fire by leaving it running and
  # re-injecting triggers; the gen's band is its startup burst, but the active
  # window re-queries the SAME pod each tick. (Continuous-traffic gen variant is
  # a follow-up; this already exercises sustained re-query of one pod.)
  fire_gen "$GEN" "${HTTP_N:-100}" "${DNS_N:-100}" "${PGSQL_N:-100}" >/dev/null || die "gen fire failed"
  node="$(k -n "$AELOAD_NS" get pod "$GEN" -o jsonpath='{.spec.nodeName}' 2>/dev/null)"; [[ -n "$node" ]] && NODE="$node"
  log "data mode: gen $GEN on node $NODE"
fi

echo "tick,t_unix,event_time,n_anomalies,last_seen,watermark,http_rows,delta_n,status" | tee "$OUT"
prev_n=0
for tick in $(seq 1 "$TICKS"); do
  T="$(now_s)"
  # Inject BURST anomalies for the SAME pod at this tick's event_time.
  if [[ "$BURST" -gt 1 ]]; then
    "$INJECT" --endpoint "$CH_HTTP" --user "$CH_RW_USER" --pass "$CH_RW_PASS" \
      --hostname "$NODE" --ns "$AELOAD_NS" --pod "$POD" --rule R0001 --pid 1234 --comm java \
      --event-time "$T" --count "$BURST" --same-time >&2 || true
  else
    "$INJECT" --endpoint "$CH_HTTP" --user "$CH_RW_USER" --pass "$CH_RW_PASS" \
      --hostname "$NODE" --ns "$AELOAD_NS" --pod "$POD" --rule R0001 --pid 1234 --comm java \
      --event-time "$T" >&2 || true
  fi
  sleep "$INTERVAL"

  n="$(attr_field "$NODE" "$POD" n_anomalies)"
  ls="$(attr_field "$NODE" "$POD" 'toUnixTimestamp(last_seen)')"
  wm="$(watermark_of "$NODE")"
  http="0"; [[ "$MODE" == "data" ]] && http="$(count_pod http_events "$POD")"
  delta=$(( ${n:-0} - prev_n ))
  status="OK"
  [[ "$tick" -gt 1 && "$delta" -le 0 ]] && status="STALL"   # n_anomalies stopped growing
  prev_n="${n:-0}"
  echo "$tick,$T,$T,$n,$ls,$wm,$http,$delta,$status" | tee -a "$OUT"
done

[[ "$MODE" == "data" && -n "$GEN" ]] && del_gen "$GEN"
log "E8 done -> $OUT"
# Summary: did it ever stall, and at which tick?
awk -F, 'NR>1{tot++; if($9=="STALL")stall++} END{printf "[aeload] E8 %s: %d ticks, %d STALL ticks (%s)\n", "'"$MODE"'", tot, stall+0, (stall+0==0?"sustained-OK":"STALLED — reproduces writes-stop")}' "$OUT"
