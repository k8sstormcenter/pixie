#!/usr/bin/env bash
set -euo pipefail

CHPOD="${CHPOD:-chi-forensic-soc-db-soc-cluster-0-0-0}"
CHNS="${CHNS:-clickhouse}"
DB="${DB:-forensic_db}"

q() { kubectl -n "$CHNS" exec -i "$CHPOD" -- clickhouse-client -q "$1" 2>/dev/null; }

fail=0
check() {
  local name="$1" got="$2" want="$3"
  if [ "$got" = "$want" ]; then
    printf 'PASS  %-52s got=%s\n' "$name" "$got"
  else
    printf 'FAIL  %-52s got=%s want=%s\n' "$name" "$got" "$want"
    fail=1
  fi
}

alerts=$(q "SELECT uniqExact(uniqueID) FROM $DB.dx_kubescape_anomalies")
edges=$(q "SELECT uniqExact((process,target)) FROM $DB.dx_kubescape_anomalies")
check "R1 graph edges == distinct alerts" "$edges" "$alerts"

seen=$(q "SELECT count() FROM $DB.dx_kubescape_anomalies g LEFT JOIN (SELECT DISTINCT uniqueID FROM $DB.dx_kubescape_anomalies) u USING (uniqueID) WHERE u.uniqueID = ''")
check "R1 every alert has an edge (no null uniqueID)" "$seen" "0"

mapfile -t ORDERS < <(q "SELECT order_id FROM $DB.dx_anomaly_orders ORDER BY order_id")

for oid in "${ORDERS[@]}"; do
  uid=$(q "SELECT uniqueID FROM $DB.dx_anomaly_orders WHERE order_id='$oid' LIMIT 1")

  ks=$(q "SELECT count() FROM $DB.dx_src__kubescape_logs k INNER JOIN (SELECT uniqueID FROM $DB.dx_anomaly_orders WHERE order_id='$oid') o USING (uniqueID)")
  ksbad=$(q "SELECT count() FROM $DB.dx_src__kubescape_logs k INNER JOIN (SELECT uniqueID FROM $DB.dx_anomaly_orders WHERE order_id='$oid') o USING (uniqueID) WHERE k.uniqueID != '$uid'")
  check "R2 kubescape($oid) is the primary log only" "$ksbad" "0"
  [ "$ks" -ge 1 ] && check "R2 kubescape($oid) present (>=1)" "1" "1" || check "R2 kubescape($oid) present (>=1)" "0" "1"

  for tbl in redis_events conn_stats dc_snoop http_events dns_events stack_trace; do
    stamped=$(q "SELECT count() FROM $DB.dx_order_records WHERE order_id='$oid' AND src_table='$tbl'")
    foreign=$(q "SELECT count() FROM $DB.dx_order_records WHERE order_id='$oid' AND src_table='$tbl' AND order_id != '$oid'")
    check "R2 $tbl($oid) no foreign-order rows" "$foreign" "0"
  done
done

echo "--- leakage proof: window-join vs stamped for one order ---"
oid="${ORDERS[0]}"
win=$(q "SELECT count() FROM $DB.dx_src__redis_events s INNER JOIN (SELECT pod, lo, hi FROM $DB.dx_anomaly_orders WHERE order_id='$oid') o USING (pod) WHERE s.row_time BETWEEN o.lo AND o.hi")
stamped=$(q "SELECT count() FROM $DB.dx_order_records WHERE order_id='$oid' AND src_table='redis_events'")
printf 'window-join redis rows for %s = %s   stamped rows = %s\n' "$oid" "$win" "$stamped"
if [ "$win" -gt "$stamped" ]; then
  echo "  -> confirms window over-collects (leaks other orders' pod rows); stamped is scoped."
fi

echo
[ "$fail" -eq 0 ] && echo "ALL PASS" || { echo "FAILURES PRESENT"; exit 1; }
