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

# exp_row_reconcile.sh — DETERMINISTIC row-level PEM↔CH reconciliation for AE.
#
# WHY: count(CH) >= count(PEM) ("write ⊇ read") is NOT proof — CH can be inflated by
# re-pull dups (C8) while silently MISSING specific rows PEM has. This test proves
# identity at ROW granularity: every individual row Pixie captured (PEM) was written
# to forensic_db (CH) with matching values — no loss, no fabrication.
#
# HOW: Pixie protocol rows have no native UUID, so we MINT one. Each request carries a
# unique probe id  <TAG>-<seq>  in its URL → that string is the row's deterministic UUID,
# visible in http_events.req_path on BOTH sides. We then compare the SET of (uuid|method|
# status) fingerprints from PEM vs CH. This cleanly separates two layers:
#   expected (0..N-1)  --Pixie capture-->  PEM set  --AE fidelity-->  CH set
#   - expected \ PEM = Pixie/eBPF didn't capture it      (Pixie property, NOT AE)
#   - PEM \ CH       = AE LOST a row Pixie had           (← the AE bug we hunt; must be empty)
#   - CH  \ PEM      = AE FABRICATED a row Pixie lacked  (must be empty; dups are same uuid, not new)
#   - mismatched fingerprint for same uuid = value corruption (shows as both loss+fab)
#
# PASS  ⇔  (PEM \ CH) empty AND (CH \ PEM) empty.   Runs NODE-SIDE (kubectl + px local).
set -uo pipefail
N=${N:-300}; NS=${NS:-log4j-poc}; SVC=${SVC:-frontend}
CLUSTER=${CLUSTER:-547d0a15-4004-435e-aea1-c13e596eb976}
CHPOD=${CHPOD:-chi-forensic-soc-db-soc-cluster-0-0-0}
SETTLE=${SETTLE:-180}                       # > two passthrough sweeps (~80s each) + write
O=/tmp/rowrec; mkdir -p "$O"
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
# pxrun relies on the persisted `px auth login` session (auth.json); PX_CLOUD_ADDR is non-secret.
pxrun(){ export PX_CLOUD_ADDR="$(grep -E '^PX_CLOUD_ADDR=' /tmp/pixie-keys.env 2>/dev/null | cut -d= -f2-)"
         px run -f "$1" -c "$CLUSTER" 2>&1 | grep -ivE "PX_|ENV VARS|^\*|Pixie CLI|Cloud|^$|resump"; }

FE=$(kubectl -n "$NS" get svc "$SVC" -o jsonpath='{.spec.clusterIP}')
FEPOD_NSP="$NS/$(kubectl -n "$NS" get pods -l app="$SVC" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
[ "$FEPOD_NSP" = "$NS/" ] && FEPOD_NSP="$NS/$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | awk '/^'"$SVC"'/{print $1; exit}')"
TAG="rr$(date +%s)"                          # unique run tag → isolates THIS run's rows (clock-skew-proof)
echo "TAG=$TAG N=$N target=$FEPOD_NSP fe=$FE" | tee "$O/meta.txt"

# 0. Put AE in passthrough so it captures the frontend (write-fidelity test, not gating).
kubectl -n pl set env ds/adaptive-export ADAPTIVE_PASSTHROUGH=true ADAPTIVE_PASSTHROUGH_WINDOW_SEC=240 ADAPTIVE_PASSTHROUGH_REFRESH_SEC=20 ADAPTIVE_PUSH_PIXIE_ROWS=false >/dev/null 2>&1
kubectl -n pl rollout status ds/adaptive-export --timeout=140s >/dev/null 2>&1
sleep 40   # AE reconnect warm

# 1. Fire N uniquely-tagged requests from a gen pod (gen client may be untraced; we read
#    the TRACED frontend SERVER-side, so every request shows up as one http_events row).
kubectl -n "$NS" delete pod rowgen --ignore-not-found --wait=true >/dev/null 2>&1
kubectl -n "$NS" run rowgen --image=busybox:1.36 --restart=Never --command -- \
  sh -c "for i in \$(seq 0 $((N-1))); do wget -qO- 'http://$FE/api/products?probe=$TAG-'\$i >/dev/null 2>&1; done; echo ROWGEN_DONE; sleep 3600"
for t in $(seq 1 90); do kubectl -n "$NS" logs rowgen 2>/dev/null | grep -q ROWGEN_DONE && break; sleep 2; done
echo "fired $N requests; settling ${SETTLE}s for AE to sweep+write" | tee -a "$O/meta.txt"
sleep "$SETTLE"

# 2. PEM fingerprints: (uuid|method|status) Pixie captured for the frontend, filtered by TAG.
cat > "$O/pem.pxl" <<PXL
import px
df = px.DataFrame('http_events', start_time='-900s')
df.pod = px.upid_to_pod_name(df.upid)
df = df[px.contains(df.req_path, '$TAG-')]
px.display(df[['req_path','req_method','resp_status','pod']], 'pem')
PXL
pxrun "$O/pem.pxl" > "$O/pem.raw"
# Build fingerprint uuid|method|status; req_path carries the uuid, no spaces in any field.
awk -v tag="$TAG" '
  { for(i=1;i<=NF;i++){ if($i ~ tag"-[0-9]+"){ uuid=$i; sub(/^.*(/tag"-[0-9]+/).*/,"",uuid) } } }' /dev/null 2>/dev/null
grep -oE "$TAG-[0-9]+" "$O/pem.raw" | sort -u > "$O/pem.uuids"
# fingerprint with method+status (parse columns around the probe token)
python3 - "$O/pem.raw" "$TAG" > "$O/pem.fp" <<'PY'
import sys,re
tag=sys.argv[2]
seen=set()
for ln in open(sys.argv[1]):
    m=re.search(re.escape(tag)+r"-(\d+)",ln)
    if not m: continue
    meth=("GET" if " GET " in " "+ln+" " or "GET" in ln else "?")
    st=re.search(r"\b([1-5]\d\d)\b",ln); st=st.group(1) if st else "?"
    seen.add(f"{tag}-{m.group(1)}|{meth}|{st}")
print("\n".join(sorted(seen)))
PY

# 3. CH fingerprints: what AE actually wrote (distinct, dedup'd) for the same TAG.
chq "SELECT DISTINCT concat(extract(req_path,'($TAG-[0-9]+)'),'|',req_method,'|',toString(resp_status))
     FROM forensic_db.http_events
     WHERE pod='$FEPOD_NSP' AND req_path LIKE '%$TAG-%'
     ORDER BY 1 FORMAT TSV" 2>/dev/null | grep -E "$TAG-[0-9]+\|" | sort -u > "$O/ch.fp"
grep -oE "$TAG-[0-9]+" "$O/ch.fp" | sort -u > "$O/ch.uuids"
CH_TOTAL=$(chq "SELECT count() FROM forensic_db.http_events WHERE pod='$FEPOD_NSP' AND req_path LIKE '%$TAG-%'")

# 4. Reconcile.
seq 0 $((N-1)) | sed "s/^/$TAG-/" | sort -u > "$O/expected.uuids"
LOSS=$(comm -23 "$O/pem.fp" "$O/ch.fp" | wc -l)     # in PEM not CH = AE LOST  (must be 0)
FAB=$(comm -13 "$O/pem.fp" "$O/ch.fp" | wc -l)      # in CH not PEM = AE FABRICATED/value-mismatch (must be 0)
MATCH=$(comm -12 "$O/pem.fp" "$O/ch.fp" | wc -l)
PIXIE_MISS=$(comm -23 "$O/expected.uuids" "$O/pem.uuids" | wc -l)   # Pixie didn't capture (NOT AE)
PEM_U=$(wc -l < "$O/pem.uuids"); CH_U=$(wc -l < "$O/ch.uuids")
DUP="n/a"; [ "$CH_U" -gt 0 ] && DUP=$(awk "BEGIN{printf \"%.2f\", $CH_TOTAL/$CH_U}")

{
echo "================ ROW-LEVEL RECONCILE (TAG=$TAG, N=$N) ================"
echo "Pixie captured (PEM distinct uuids): $PEM_U / $N   (expected\\PEM = $PIXIE_MISS not captured by eBPF)"
echo "AE wrote        (CH distinct uuids): $CH_U     (CH total rows=$CH_TOTAL → dup factor ${DUP}x)"
echo "fingerprint matched (uuid|method|status): $MATCH"
echo "AE LOSS  (PEM\\CH, MUST be 0): $LOSS"
echo "AE FAB   (CH\\PEM, MUST be 0): $FAB"
[ "$LOSS" -gt 0 ] && { echo '--- LOST rows (Pixie had, AE did NOT write): ---'; comm -23 "$O/pem.fp" "$O/ch.fp" | head -20; }
[ "$FAB"  -gt 0 ] && { echo '--- FABRICATED/mismatched rows (in CH, not in PEM): ---'; comm -13 "$O/pem.fp" "$O/ch.fp" | head -20; }
if [ "$LOSS" -eq 0 ] && [ "$FAB" -eq 0 ] && [ "$PEM_U" -gt 0 ]; then
  echo "VERDICT: PASS — every row Pixie captured was written to CH with matching values."
else
  echo "VERDICT: FAIL — AE write-set != Pixie read-set at row granularity."
fi
} | tee "$O/RESULT.txt"
kubectl -n "$NS" delete pod rowgen --ignore-not-found --wait=false >/dev/null 2>&1
