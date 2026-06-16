#!/usr/bin/env bash
# exp_control.sh — control-plane reproducibility (E1..E4, E6). No Pixie, no gen:
# inject curated kubescape_logs fixtures and assert the deterministic control
# surface (adaptive_attribution FINAL + uniqExact(anomaly_hash) + watermark).
#
# Live-AE constraint: hostname MUST be a real node (AE polls per-node). Per-rep
# isolation is by UNIQUE POD (distinct anomaly_hash) + monotone event_time.
#
# Usage: EXP=E1 REPS=100 OUT=/tmp/e1.csv ./exp_control.sh   (EXP in E1 E2 E3 E4 E6)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$HERE/lib.sh"
INJECT="$HERE/inject.sh"

EXP="${EXP:-E1}"
REPS="${REPS:-100}"
NODE="${NODE:-$(first_node)}"
OUT="${OUT:-/tmp/aeload_${EXP}.csv}"

ch_portforward_up
[[ -n "$NODE" ]] || die "no node resolved (set NODE=)"
log "EXP=$EXP node=$NODE reps=$REPS"
warmup "$NODE"   # absorb AE trigger cold-start so rep 1 is steady-state

inj(){ "$INJECT" --endpoint "$CH_HTTP" --user "$CH_RW_USER" --pass "$CH_RW_PASS" --hostname "$NODE" "$@" >&2; }
# settle: give AE's 250ms trigger poll + write time to land.
settle(){ sleep "${SETTLE_S:-3}"; }

echo "rep,exp,uniq_exp,uniq_act,attrib_exp,attrib_act,wm_exp,wm_act,pass" | tee "$OUT"
WM_PREV=0       # for monotonicity check (trigger_watermark persists on a ~5s throttle)
BASE="$(now_s)" # event_time is unix SECONDS (production unit)

for rep in $(seq 1 "$REPS"); do
  # Per-rep 10s slot → distinct, monotone second-granularity event_times with
  # room for E2's 10 rows / E3's 8 pods without cross-rep collision.
  T=$(( BASE + rep*10 ))
  R="$(printf '%03d' "$rep")"   # zero-pad → collision-proof LIKE filters
  filt=""; uexp=1; aexp=1; wmexp="$T"; idemp=""
  case "$EXP" in
    E1) # single anomaly
      filt="cp-e1-${R}"
      inj --ns aeload --pod "$filt" --rule R0001 --pid 1234 --comm java --event-time "$T" || { echo "$rep,$EXP,,,,,,,INJECT_FAIL"|tee -a "$OUT"; continue; }
      ;;
    E2) # dedup / extend: 10 rows, same target, 1s apart → 1 hash, 1 row
      filt="cp-e2-${R}"; wmexp="$((T + 9))"
      inj --ns aeload --pod "$filt" --rule R0001 --pid 1234 --comm java --event-time "$T" --count 10 --dt-s 1 || { echo "$rep,$EXP,,,,,,,INJECT_FAIL"|tee -a "$OUT"; continue; }
      sleep 8  # let all 10 rows (spanning 9s) be polled before measuring
      ;;
    E3) # fan-out: 8 distinct pods → 8 hashes, 8 rows
      filt="cp-e3-${R}-"; K=8; uexp="$K"; aexp="$K"; wmexp=""
      ok=1
      for j in $(seq 1 "$K"); do
        inj --ns aeload --pod "${filt}${j}" --rule R0001 --pid "$((1234+j))" --comm java --event-time "$((T + j))" || ok=0
      done
      [[ "$ok" == 1 ]] || { echo "$rep,$EXP,,,,,,,INJECT_FAIL"|tee -a "$OUT"; continue; }
      ;;
    E4) # boundary collision: 2 rows, same event_time, different RuleID, same target → 1 hash
      filt="cp-e4-${R}"
      inj --ns aeload --pod "$filt" --rule R0001 --pid 1234 --comm java --event-time "$T" --same-time || true
      inj --ns aeload --pod "$filt" --rule R0010 --pid 1234 --comm java --event-time "$T" --same-time || { echo "$rep,$EXP,,,,,,,INJECT_FAIL"|tee -a "$OUT"; continue; }
      ;;
    E6) # watermark idempotency across AE restart
      filt="cp-e6-${R}"
      inj --ns aeload --pod "$filt" --rule R0001 --pid 1234 --comm java --event-time "$T" || { echo "$rep,$EXP,,,,,,,INJECT_FAIL"|tee -a "$OUT"; continue; }
      wait_attrib "$NODE" "$filt" 1 20 >/dev/null
      a1="$(attrib_count "$NODE" "$filt")"
      k -n "$AE_NS" rollout restart "ds/${AE_DS:-adaptive-export}" >/dev/null 2>&1 || true
      k -n "$AE_NS" rollout status  "ds/${AE_DS:-adaptive-export}" --timeout=180s >/dev/null 2>&1 || true
      sleep 8
      # idempotency: attribution still exactly 1 after restart (no double-count)
      [[ "$a1" == "1" ]] || idemp="FAIL_idemp_a1=${a1}"
      ;;
    *) die "unknown EXP=$EXP";;
  esac

  # Poll until AE has written the expected attribution rows (steady-state),
  # then read the deterministic counts. wm is persistence-throttled (~5s) so it
  # is reported + checked for MONOTONICITY only, never a hard gate.
  aact="$(wait_attrib "$NODE" "$filt" "$aexp" "${MEAS_TIMEOUT:-25}")"
  uact="$(uniq_hashes "$NODE" "$filt")"
  wm="$(watermark_of "$NODE")"

  pass="PASS"
  [[ "$uact" == "$uexp" ]] || pass="FAIL_uniq"
  [[ "$aact" == "$aexp" ]] || pass="${pass}|FAIL_attrib"
  [[ -z "$idemp" ]] || pass="${pass}|${idemp}"
  # watermark: must never go backwards (persisted value lags but is monotone).
  if [[ "${wm:-0}" -lt "${WM_PREV:-0}" ]]; then pass="${pass}|FAIL_wm_regress"; fi
  WM_PREV="$wm"

  echo "$rep,$EXP,$uexp,$uact,$aexp,$aact,$wmexp,$wm,$pass" | tee -a "$OUT"
done

log "$EXP done -> $OUT"
python3 "$HERE/stats.py" "$OUT" || true
