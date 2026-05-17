#!/usr/bin/env bash
# verify-protocol-coverage.sh — for every pod with at least 1 kubescape alert
# in the last 5 min, count rows in each forensic_db protocol table for THAT
# pod over the same 5-min window. A coverage matrix tells us which protocols
# the operator's per-alert fan-out actually populated.
#
# Expected after the parallel-fan-out controller change: for each pod that
# had an alert, AT LEAST the protocol table matching that pod's workload has
# rows (http-server → http_events, redis-server → redis_events,
# pgsql-server → pgsql_events). Pods that legitimately don't speak a
# protocol (e.g. redis-server has no HTTP traffic) will show 0 there — that
# is NOT a failure, just a fact about the workload.
#
# Exit 0 if every pod with an alert has rows in AT LEAST one protocol table
# (i.e. operator fan-out reached at least one downstream table per pod).
# Exit 1 if any pod-with-alert has 0 rows across all protocol tables → that
# pod's anomaly window is dead-on-arrival and the operator chain is broken
# for it.
#
# Usage:  ./verify-protocol-coverage.sh           # 5-min window, all pods
#         ./verify-protocol-coverage.sh 600       # 10-min window
#         ./verify-protocol-coverage.sh 300 redis # 5-min, only "redis*" pods
set -euo pipefail

WINDOW_S="${1:-300}"
POD_FILTER="${2:-}"

export KUBECONFIG="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
CHEX="kubectl exec -n clickhouse chi-forensic-soc-db-soc-cluster-0-0-0 -c clickhouse -- clickhouse-client --query"

PROTOCOLS=(http_events redis_events pgsql_events)

echo "=== alerted pods (kubescape_logs last ${WINDOW_S}s) ==="
PODS_RAW=$($CHEX "
  SELECT DISTINCT JSONExtractString(RuntimeK8sDetails, 'podName') AS pod
  FROM forensic_db.kubescape_logs
  WHERE fromUnixTimestamp64Nano(event_time::Int64) > now() - ${WINDOW_S}
  ORDER BY pod
  FORMAT TabSeparated" 2>/dev/null)

if [ -z "$PODS_RAW" ]; then
  echo "FAIL: no pods alerted in last ${WINDOW_S}s — SBOB chain dead, can't validate coverage"
  exit 1
fi

PODS=()
while IFS= read -r p; do
  [ -z "$p" ] && continue
  if [ -n "$POD_FILTER" ] && ! echo "$p" | grep -q "$POD_FILTER"; then continue; fi
  PODS+=("$p")
done <<< "$PODS_RAW"

echo "${#PODS[@]} alerted pod(s)$( [ -n "$POD_FILTER" ] && echo " (filter: $POD_FILTER)" )"
echo

# Header
printf '%-45s' 'pod'
for t in "${PROTOCOLS[@]}"; do printf '%14s' "$t"; done
printf '%14s\n' 'coverage'

# Body
FAIL_PODS=()
PASS_PODS=()
for pod in "${PODS[@]}"; do
  printf '%-45s' "$pod"
  any_nonzero=0
  declare -A counts=()
  for tbl in "${PROTOCOLS[@]}"; do
    # NOTE: protocol tables filter on `time_` (the pixie capture timestamp,
    # DateTime64(9)), NOT on `event_time` which the operator's sink leaves
    # unset (1970-01-01 default). `time_` is the only column with real
    # wall-clock values for pixie-sourced rows.
    n=$($CHEX "
      SELECT count() FROM forensic_db.${tbl}
      WHERE (pod = '${pod}' OR pod LIKE '%/${pod}')
        AND time_ > now() - ${WINDOW_S}
      FORMAT TabSeparated" 2>/dev/null)
    n=${n:-0}
    counts[$tbl]=$n
    printf '%14d' "$n"
    [ "$n" -gt 0 ] && any_nonzero=1
  done
  if [ "$any_nonzero" -eq 1 ]; then
    matched=""
    for tbl in "${PROTOCOLS[@]}"; do
      [ "${counts[$tbl]}" -gt 0 ] && matched="$matched ${tbl%_events}"
    done
    printf '%14s\n' "✓${matched}"
    PASS_PODS+=("$pod")
  else
    printf '%14s\n' '⚠ DEAD'
    FAIL_PODS+=("$pod")
  fi
done

echo
echo "=== summary ==="
echo "PASS pods (>=1 protocol table populated): ${#PASS_PODS[@]}"
echo "FAIL pods (operator chain dead):          ${#FAIL_PODS[@]}"

if [ "${#FAIL_PODS[@]}" -gt 0 ]; then
  echo
  echo "FAILED pods:"
  for p in "${FAIL_PODS[@]}"; do echo "  - $p"; done
  exit 1
fi

echo
echo "PASS: all alerted pods have non-zero rows in at least one protocol table"
exit 0
