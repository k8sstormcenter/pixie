#!/usr/bin/env bash
# verify-png-vs-db.sh — for every (mult × protocol-table) data point in a
# sweep's scaling.png / metrics.csv, run a direct CH query for the same
# wall-clock window and check the values agree.
set -uo pipefail

SD="${1:-$(ls -dt /tmp/proto-sweep-2* | head -1)}"
[ -z "$SD" ] && { echo "no sweep dir"; exit 2; }
[ ! -f "$SD/metrics.csv" ] && { echo "no metrics.csv in $SD"; exit 2; }

export KUBECONFIG="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
CHEX="kubectl exec -n clickhouse chi-forensic-soc-db-soc-cluster-0-0-0 -c clickhouse -- clickhouse-client --query"

echo "verifying: $SD"
echo

printf '%-3s | %-22s | %10s | %10s | %10s | %s\n' \
  'mlt' 'table' 'csv_rate/s' 'db_count' 'expected' 'verdict'
printf '%s\n' "----+------------------------+------------+------------+------------+--------------------"

FAIL=0
TOTAL=0
TABLES=(http_events redis_events pgsql_events kubescape_logs adaptive_attribution)

while IFS=, read -r mult t0 t1 rest; do
  [ "$mult" = 'mult' ] && continue
  line=$(grep "^${mult}," "$SD/metrics.csv")
  IFS=, read -ra F <<< "$line"
  ch_h_rate=${F[23]}; ch_r_rate=${F[24]}; ch_p_rate=${F[25]}; ch_k_rate=${F[26]}; ch_a_rate=${F[27]}
  csv_rates=("$ch_h_rate" "$ch_r_rate" "$ch_p_rate" "$ch_k_rate" "$ch_a_rate")

  # Newer sweeps record mult_t_start and mult_t_end in cols 30/31. Fall
  # back to the (t0 - 75s) estimate for older CSVs that don't have them.
  if [ -n "${F[30]:-}" ] && [ -n "${F[31]:-}" ]; then
    mult_t_start=${F[30]}
    mult_t_end=${F[31]}
  else
    mult_t_start=$(( t0 - 75 ))
    mult_t_end=$t1
  fi
  mult_dur=$(( mult_t_end - mult_t_start ))
  [ "$mult_dur" -lt 1 ] && mult_dur=1

  for i in 0 1 2 3 4; do
    tbl=${TABLES[$i]}
    csv_rate=${csv_rates[$i]}
    expected_rows=$(( csv_rate * mult_dur ))

    case "$tbl" in
      kubescape_logs)      col='fromUnixTimestamp64Nano(event_time::Int64)' ;;
      adaptive_attribution) col='last_seen' ;;
      *)                    col='time_' ;;
    esac
    db_count=$($CHEX "SELECT count() FROM forensic_db.${tbl} WHERE ${col} BETWEEN toDateTime(${mult_t_start}) AND toDateTime(${mult_t_end}) FORMAT TabSeparated" 2>/dev/null)
    db_count=${db_count:-0}

    verdict='?'
    if [ "$csv_rate" -eq 0 ] && [ "$db_count" -eq 0 ]; then
      verdict='✓ both 0'
    elif [ "$csv_rate" -eq 0 ] && [ "$db_count" -gt 0 ]; then
      verdict="⚠ csv=0 db=${db_count} (csv missed)"
      FAIL=$((FAIL + 1))
    elif [ "$expected_rows" -eq 0 ]; then
      verdict='✓'
    else
      diff=$(( db_count - expected_rows ))
      [ "$diff" -lt 0 ] && diff=$((-diff))
      rel_pct=$(( 100 * diff / expected_rows ))
      if [ "$rel_pct" -le 25 ]; then
        verdict="✓ Δ=${rel_pct}%"
      else
        verdict="⚠ Δ=${rel_pct}% (csv=${expected_rows} db=${db_count})"
        FAIL=$((FAIL + 1))
      fi
    fi
    TOTAL=$((TOTAL + 1))
    printf '%-3s | %-22s | %10d | %10d | %10d | %s\n' \
      "${mult}x" "$tbl" "$csv_rate" "$db_count" "$expected_rows" "$verdict"
  done
done < "$SD/metrics.csv"

echo
echo "TOTAL data points checked: $TOTAL"
echo "MISMATCHES (>25% off):     $FAIL"
[ "$FAIL" -gt 0 ] && exit 1
echo "PASS — every CSV/PNG data point matches its DB-derived counterpart"
exit 0
