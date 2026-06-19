#!/usr/bin/env bash
# exp_ae_nfr_benchmark.sh — deep NFR benchmark of AdaptiveExport (passthrough firehose arm).
# Runs NODE-SIDE on the rig dev-machine. Measures, under a fixed steady load:
#
#   1. THROUGHPUT          rows/sec + on-disk bytes/sec written to forensic_db (per table + total)
#   2. CAPTURE COMPLETENESS AE read_count vs broker count() for the SAME window (% captured; F1 cap proof)
#   3. WRITE FIDELITY       read==wrote per cycle + write-error count (ae_reconcile); 0 errs = clean sink
#   4. E2E LATENCY          freshness lag = now() - max(time_) landed in CH (how stale the newest row is)
#   5. RESOURCE FOOTPRINT   AE pod CPU(m)/mem(Mi) idle vs under load (kubectl top)
#   6. PER-CYCLE            cycles in window, mean rows/cycle, query cadence
#
# Prereqs: AE = aeprod11 with ADAPTIVE_RECONCILE=true + ADAPTIVE_PASSTHROUGH=true; px keys at
# /tmp/pixie-keys.env (broker count); kubectl top (metrics-server). Emits a report to /tmp/ae_nfr.txt.
set -uo pipefail
NS=log4j-poc; CHPOD=chi-forensic-soc-db-soc-cluster-0-0-0
WIN=${WIN:-180}; WARM=${WARM:-40}; OUT=/tmp/ae_nfr.txt
TABLES="http_events dns_events conn_stats pgsql_events"
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
snap(){ chq "SELECT table, sum(rows), sum(data_compressed_bytes) FROM system.parts WHERE database='forensic_db' AND active AND table IN ('http_events','dns_events','pgsql_events','conn_stats') GROUP BY table FORMAT TSV"; }
aetop(){ kubectl top pods -n pl -l name=adaptive-export --no-headers 2>/dev/null | awk '{c+=$2+0; m+=$3+0} END{printf "%d %d", c, m}'; }

FE=$(kubectl -n $NS get svc frontend -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
FEP=$(kubectl -n $NS get svc frontend -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)
export PX_CLOUD_ADDR=$(grep -E '^PX_CLOUD_ADDR=' /tmp/pixie-keys.env 2>/dev/null|cut -d= -f2-)
px auth login --use_api_key --api_key=$(grep -E '^PX_API_KEY=' /tmp/pixie-keys.env 2>/dev/null|cut -d= -f2-) -q >/dev/null 2>&1
CID=$(kubectl -n pl get secret pl-cluster-secrets -o jsonpath='{.data.cluster-id}' 2>/dev/null | base64 -d 2>/dev/null)
brokercount(){ printf 'import px\ndf=px.DataFrame("%s",start_time="-%ss")\npx.display(df.agg(n=("time_",px.count)),"o")\n' "$1" "$2" > /tmp/bc.pxl; px run -o json -f /tmp/bc.pxl -c "$CID" 2>/dev/null | grep -oE '"n":[0-9]+' | head -1 | cut -d: -f2; }

echo "AE NFR benchmark  win=${WIN}s warm=${WARM}s  $(date -u +%FT%TZ)" | tee "$OUT"
echo "[5] footprint IDLE (cpu_m mem_Mi): $(aetop)" | tee -a "$OUT"

# steady load
for i in $(seq 1 6); do kubectl -n $NS delete pod nf-$i --ignore-not-found --wait=false >/dev/null 2>&1; done
for i in $(seq 1 6); do kubectl -n $NS run nf-$i --image=busybox:1.36 --labels=run=nf --restart=Never -- \
  sh -c "while true; do wget -qO- http://$FE:$FEP/api/products?q=n$i >/dev/null 2>&1; done" >/dev/null 2>&1; done

sleep "$WARM"
declare -A R0 B0; while IFS=$'\t' read -r t r b; do R0[$t]=$r; B0[$t]=$b; done < <(snap)
s0=$(date +%s)
sleep "$WIN"
s1=$(date +%s); dt=$((s1-s0)); [ "$dt" -lt 1 ] && dt=1
echo "[5] footprint UNDER LOAD (cpu_m mem_Mi): $(aetop)" | tee -a "$OUT"

echo "=== [1] THROUGHPUT + [4] LATENCY (window ${dt}s) ===" | tee -a "$OUT"
printf "  %-13s %10s %12s %10s %12s %10s\n" table d_rows rows_per_s d_bytes bytes_per_s lag_s | tee -a "$OUT"
while IFS=$'\t' read -r t r b; do
  dr=$(( r-${R0[$t]:-0} )); db=$(( b-${B0[$t]:-0} ))
  lag=$(chq "SELECT dateDiff('second', max(time_), now()) FROM forensic_db.$t WHERE time_ > now()-INTERVAL 5 MINUTE"); lag=${lag:-na}
  printf "  %-13s %10d %12.0f %10d %12.0f %10s\n" "$t" "$dr" "$(awk -v x=$dr -v d=$dt 'BEGIN{print x/d}')" "$db" "$(awk -v x=$db -v d=$dt 'BEGIN{print x/d}')" "$lag" | tee -a "$OUT"
done < <(snap)

echo "=== [3] WRITE FIDELITY + [6] PER-CYCLE (ae_reconcile, last ${WIN}s) ===" | tee -a "$OUT"
printf "  %-13s %8s %10s %10s %10s %6s\n" table cycles max_read tot_read tot_wrote errs | tee -a "$OUT"
for t in $TABLES; do
  read -r cyc mr tr tw er < <(chq "SELECT count(), max(read_count), sum(read_count), sum(wrote_count), countIf(write_err!='') FROM forensic_db.ae_reconcile WHERE table_name='$t' AND mode='passthrough' AND ts > now()-INTERVAL ${WIN} SECOND FORMAT TSV")
  printf "  %-13s %8d %10d %10d %10d %6d\n" "$t" "${cyc:-0}" "${mr:-0}" "${tr:-0}" "${tw:-0}" "${er:-0}" | tee -a "$OUT"
done
echo "  NOTE tot_read==tot_wrote & errs==0 ⇒ clean sink (no dropped batches)." | tee -a "$OUT"
echo "  (Capture completeness / 10k-cap proof = the dedicated F1 test: max_read>10000 vs broker count for the SAME window.)" | tee -a "$OUT"

for i in $(seq 1 6); do kubectl -n $NS delete pod nf-$i --ignore-not-found --wait=false >/dev/null 2>&1; done
echo "DONE $(date -u +%FT%TZ)" | tee -a "$OUT"
