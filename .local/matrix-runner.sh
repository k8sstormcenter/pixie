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

# matrix-runner.sh — direct kubectl variant cycles, no perf_tool.
# Each variant: patch yamls → deploy redis ns → wait 60s warmup → measure
# 180s → tear down → summary line. Captures k6 achieved iters, Pixie
# redis_events ingest delta, kubescape alerts delta.
set -uo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PY=/home/constanze/.venvs/render/bin/python
BASE=/tmp/matrix-base
SRC="$REPO_ROOT/src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc"
OUT=/tmp/matrix-$(date -u +%Y%m%d-%H%M%S)
mkdir -p "$OUT"
echo "matrix dir: $OUT" | tee "$OUT/matrix.log"

CH_URL='http://localhost:30123'
CH_AUTH='pixie:pixie_password'

restore_base() {
  cp "$BASE/api-backend.yaml"      "$SRC/api-backend.yaml"
  cp "$BASE/redis-vulnerable.yaml" "$SRC/redis-vulnerable.yaml"
  cp "$BASE/postgres.yaml"         "$SRC/postgres.yaml"
  cp "$BASE/loadgen-k6.yaml"       "$SRC/loadgen-k6.yaml"
}

apply_variant() {
  local expr="$1"
  restore_base
  "$PY" - "$SRC" "$expr" <<'PYEOF'
import sys, yaml, os, re
src, expr = sys.argv[1], sys.argv[2]
def load(p):
    with open(p) as f: return list(yaml.safe_load_all(f))
def save(p, docs):
    with open(p, 'w') as f: yaml.safe_dump_all(docs, f, sort_keys=False)
api   = load(os.path.join(src, 'api-backend.yaml'))
redis = load(os.path.join(src, 'redis-vulnerable.yaml'))
pg    = load(os.path.join(src, 'postgres.yaml'))
k6    = load(os.path.join(src, 'loadgen-k6.yaml'))
def deploys(docs):
    return [d for d in docs if d and d.get('kind') in ('Deployment','StatefulSet')]
def container(d, name):
    for c in d['spec']['template']['spec']['containers']:
        if c['name']==name: return c
def setres(c, cpu_lim=None, mem_lim=None):
    c.setdefault('resources', {})
    c['resources'].setdefault('limits', {})
    if cpu_lim is not None: c['resources']['limits']['cpu']=cpu_lim
    if mem_lim is not None: c['resources']['limits']['memory']=mem_lim
def replicas(d, n):
    d['spec']['replicas']=n
def setargs_gunicorn(c, workers, threads):
    a = c['args'][0]
    a = re.sub(r'-w \d+', f'-w {workers}', a)
    a = re.sub(r'--threads \d+', f'--threads {threads}', a)
    c['args'][0] = a
def setpool(c, minc, maxc):
    a = c['args'][0]
    a = re.sub(r'minconn=\d+', f'minconn={minc}', a)
    a = re.sub(r'maxconn=\d+', f'maxconn={maxc}', a)
    c['args'][0] = a
ns = dict(api=api, redis=redis, pg=pg, k6=k6, deploys=deploys, container=container,
          setres=setres, replicas=replicas,
          setargs_gunicorn=setargs_gunicorn, setpool=setpool)
exec(expr, ns)
save(os.path.join(src, 'api-backend.yaml'), api)
save(os.path.join(src, 'redis-vulnerable.yaml'), redis)
save(os.path.join(src, 'postgres.yaml'), pg)
save(os.path.join(src, 'loadgen-k6.yaml'), k6)
PYEOF
}

set_k6_qps() {
  local qps=$1 vus=$2 maxvus=$3
  "$PY" - "$SRC/loadgen-k6.yaml" "$qps" "$vus" "$maxvus" <<'PYEOF'
import sys, yaml
p, qps, vus, maxvus = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
with open(p) as f: docs = list(yaml.safe_load_all(f))
for d in docs:
    if not d: continue
    if d.get('kind')=='Deployment' and d['metadata']['name']=='loadgen':
        for c in d['spec']['template']['spec']['containers']:
            if c['name']=='k6':
                for e in c['env']:
                    if e['name']=='K6_QPS':     e['value']=qps
                    if e['name']=='K6_VUS':     e['value']=vus
                    if e['name']=='K6_MAX_VUS': e['value']=maxvus
with open(p, 'w') as f: yaml.safe_dump_all(docs, f, sort_keys=False)
PYEOF
}

ch_q() {
  curl -s -G -u "$CH_AUTH" --data-urlencode "query=$1 FORMAT TabSeparated" "$CH_URL/" | tr -d '\n'
}

deploy_redis_ns() {
  kubectl create namespace redis --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl label namespace redis kubescape.io/ignore- --overwrite=true >/dev/null 2>&1 || true
  kubectl apply -f "$SRC/redis-sbob.yaml"        >/dev/null 2>&1 || true
  kubectl apply -f "$SRC/redis-client-sbob.yaml" >/dev/null 2>&1 || true
  kubectl apply -f "$SRC/postgres-sbob.yaml"     >/dev/null 2>&1 || true
  kubectl apply -f "$SRC/api-sbob.yaml"          >/dev/null 2>&1 || true
  kubectl apply -f "$SRC/redis-vulnerable.yaml"  >/dev/null
  kubectl apply -f "$SRC/postgres.yaml"          >/dev/null
  kubectl apply -f "$SRC/api-backend.yaml"       >/dev/null
  kubectl apply -f "$SRC/loadgen-k6.yaml"        >/dev/null
}

teardown_redis_ns() {
  kubectl delete deployment,statefulset,service,configmap -n redis --all --wait=false >/dev/null 2>&1 || true
  kubectl delete applicationprofile -n redis --all --wait=false >/dev/null 2>&1 || true
  # Wait for k6 + api + redis + pg to terminate so the next run sees a clean slate.
  for i in $(seq 1 30); do
    n=$(kubectl get pods -n redis --no-headers 2>/dev/null | wc -l)
    [ "$n" = "0" ] && return
    sleep 2
  done
}

run_one() {
  local name="$1" mult="$2"
  local m_int=${mult%x}
  local qps=$(( 500 * m_int ))
  local vus=$(( 50 * m_int ))
  local maxvus=$(( 200 * m_int ))
  set_k6_qps "$qps" "$vus" "$maxvus"

  local logdir="$OUT/$name/$mult"
  mkdir -p "$logdir"

  teardown_redis_ns
  deploy_redis_ns

  # Wait for loadgen pod
  local k6pod=""
  for i in $(seq 1 120); do
    k6pod=$(kubectl -n redis get pods -l app.kubernetes.io/name=loadgen -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    [ -n "$k6pod" ] && break
    sleep 1
  done
  if [ -z "$k6pod" ]; then
    echo "  $mult | FAIL: no k6 pod after 120s" | tee -a "$OUT/matrix.log"
    return
  fi
  # Wait for k6 to start running (or api ready)
  for i in $(seq 1 150); do
    kubectl -n redis logs "$k6pod" -c k6 --tail=20 2>/dev/null | grep -qE "running \(|api reachable" && break
    sleep 1
  done

  sleep 60  # warmup

  local t0=$(date -u +%s)
  local rev0=$(ch_q "SELECT count() FROM default.redis_events")
  local alerts0=$(ch_q "SELECT count() FROM forensic_db.kubescape_logs")
  local k6_iters0=$(kubectl -n redis logs "$k6pod" -c k6 --tail=200 2>/dev/null | grep -oE "[0-9]+ complete" | tail -1 | grep -oE "[0-9]+" || echo 0)

  sleep 180  # measure

  local t1=$(date -u +%s)
  local rev1=$(ch_q "SELECT count() FROM default.redis_events")
  local alerts1=$(ch_q "SELECT count() FROM forensic_db.kubescape_logs")
  kubectl -n redis logs "$k6pod" -c k6 --tail=400 2>/dev/null > "$logdir/k6.log"
  local k6_iters1=$(grep -oE "[0-9]+ complete" "$logdir/k6.log" | tail -1 | grep -oE "[0-9]+" || echo 0)
  local k6_vus=$(grep -oE "[0-9]+/[0-9]+ VUs" "$logdir/k6.log" | tail -1)

  # Collect pod CPU/mem snapshot for diagnostics
  kubectl top pods -n redis 2>/dev/null > "$logdir/top.txt" || true

  local elapsed=$((t1 - t0))
  [ "$elapsed" -lt 1 ] && elapsed=1
  local rev_delta=$((rev1 - rev0))
  local alert_delta=$((alerts1 - alerts0))
  local k6_iters_delta=$((k6_iters1 - k6_iters0))
  local rev_rate=$(( rev_delta / elapsed ))
  local k6_rate=$(( k6_iters_delta / elapsed ))
  local alert_rate=$(awk -v a="$alert_delta" -v e="$elapsed" 'BEGIN{printf "%.1f", a/e}')

  printf "  %-3s | k6=%-6s/s vus=%-12s | redis_ev=%-6s/s | alerts=%-5s/s | win=%ds\n" \
    "$mult" "$k6_rate" "${k6_vus:-?}" "$rev_rate" "$alert_rate" "$elapsed" \
    | tee -a "$OUT/matrix.log"
}

run_variant() {
  local name="$1" expr="$2"
  echo "" | tee -a "$OUT/matrix.log"
  echo "=== VARIANT: $name ===" | tee -a "$OUT/matrix.log"
  echo "patch: $expr" | tee -a "$OUT/matrix.log"
  apply_variant "$expr"
  date -u +"%Y-%m-%dT%H:%M:%SZ start" | tee -a "$OUT/matrix.log"
  for mult in 4x 8x 16x; do
    run_one "$name" "$mult"
  done
  date -u +"%Y-%m-%dT%H:%M:%SZ end" | tee -a "$OUT/matrix.log"
}

run_variant baseline 'pass'
run_variant gunicorn_cpu8 '[(setres(container(d,"api"), cpu_lim="8", mem_lim="2Gi"), setargs_gunicorn(container(d,"api"), 8, 32), setpool(container(d,"api"), 4, 32)) for d in deploys(api) if d["metadata"]["name"]=="api"]'
run_variant api_rep8 '[replicas(d, 8) for d in deploys(api) if d["metadata"]["name"]=="api"]'
run_variant pg_cpu8 '[setres(container(d,"postgres"), cpu_lim="8", mem_lim="2Gi") for d in deploys(pg) if d["metadata"]["name"]=="postgres"]'
run_variant everything_big '[(setres(container(d,"api"), cpu_lim="4", mem_lim="2Gi"), replicas(d, 4), setargs_gunicorn(container(d,"api"), 8, 32), setpool(container(d,"api"), 4, 32)) for d in deploys(api) if d["metadata"]["name"]=="api"]; [setres(container(d,"redis"), cpu_lim="4", mem_lim="1Gi") for d in deploys(redis) if d["metadata"]["name"]=="redis"]; [setres(container(d,"postgres"), cpu_lim="4", mem_lim="2Gi") for d in deploys(pg) if d["metadata"]["name"]=="postgres"]'

restore_base
teardown_redis_ns
echo "" | tee -a "$OUT/matrix.log"
echo "=== matrix complete ===" | tee -a "$OUT/matrix.log"
echo "results: $OUT" | tee -a "$OUT/matrix.log"
