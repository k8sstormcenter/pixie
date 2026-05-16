#!/usr/bin/env bash
# matrix-runner-4.sh — same as matrix-3 but NO in-run sampler (only k6 + ct snapshot
# at t0/t1). Tests whether matrix-3's sampler degraded throughput.
set -uo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PY=/home/constanze/.venvs/render/bin/python
BASE=/tmp/matrix-base
SRC="$SCRIPT_DIR/src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc"
OUT=/tmp/matrix4-$(date -u +%Y%m%d-%H%M%S)
mkdir -p "$OUT"
echo "matrix4 dir: $OUT" | tee "$OUT/matrix.log"

restore_base() {
  cp "$BASE/api-backend.yaml"      "$SRC/api-backend.yaml"
  cp "$BASE/redis-vulnerable.yaml" "$SRC/redis-vulnerable.yaml"
  cp "$BASE/postgres.yaml"         "$SRC/postgres.yaml"
  cp "$BASE/loadgen-k6.yaml"       "$SRC/loadgen-k6.yaml"
}

apply_max_everything() {
  restore_base
  "$PY" - "$SRC" <<'PYEOF'
import sys, yaml, os, re
src = sys.argv[1]
def load(p):
    with open(p) as f: return list(yaml.safe_load_all(f))
def save(p, docs):
    with open(p, 'w') as f: yaml.safe_dump_all(docs, f, sort_keys=False)
api   = load(os.path.join(src, 'api-backend.yaml'))
redis = load(os.path.join(src, 'redis-vulnerable.yaml'))
pg    = load(os.path.join(src, 'postgres.yaml'))
def container(d, name):
    for c in d['spec']['template']['spec']['containers']:
        if c['name']==name: return c
def setres(c, cpu_lim=None, mem_lim=None):
    c.setdefault('resources', {})
    c['resources'].setdefault('limits', {})
    if cpu_lim: c['resources']['limits']['cpu']=cpu_lim
    if mem_lim: c['resources']['limits']['memory']=mem_lim
def setargs_gunicorn(c, w, t):
    a = c['args'][0]
    a = re.sub(r'-w \d+', f'-w {w}', a)
    a = re.sub(r'--threads \d+', f'--threads {t}', a)
    c['args'][0] = a
def setpool(c, minc, maxc):
    a = c['args'][0]
    a = re.sub(r'minconn=\d+', f'minconn={minc}', a)
    a = re.sub(r'maxconn=\d+', f'maxconn={maxc}', a)
    c['args'][0] = a
for d in [x for x in api if x and x.get('kind')=='Deployment' and x['metadata']['name']=='api']:
    c = container(d,'api'); setres(c, "4", "2Gi"); d['spec']['replicas']=8
    setargs_gunicorn(c, 8, 32); setpool(c, 4, 32)
for d in [x for x in redis if x and x.get('kind')=='Deployment' and x['metadata']['name']=='redis']:
    setres(container(d,'redis'), "8", "1Gi")
for d in [x for x in pg if x and x.get('kind')=='Deployment' and x['metadata']['name']=='postgres']:
    setres(container(d,'postgres'), "8", "2Gi")
save(os.path.join(src, 'api-backend.yaml'), api)
save(os.path.join(src, 'redis-vulnerable.yaml'), redis)
save(os.path.join(src, 'postgres.yaml'), pg)
PYEOF
}

set_loadgen() {
  local replicas=$1 qps=$2 vus=$3 maxvus=$4
  "$PY" - "$SRC/loadgen-k6.yaml" "$replicas" "$qps" "$vus" "$maxvus" <<'PYEOF'
import sys, yaml
p, replicas, qps, vus, maxvus = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]
with open(p) as f: docs = list(yaml.safe_load_all(f))
for d in docs:
    if not d: continue
    if d.get('kind')=='Deployment' and d['metadata']['name']=='loadgen':
        d['spec']['replicas'] = replicas
        for c in d['spec']['template']['spec']['containers']:
            if c['name']=='k6':
                for e in c['env']:
                    if e['name']=='K6_QPS':     e['value']=qps
                    if e['name']=='K6_VUS':     e['value']=vus
                    if e['name']=='K6_MAX_VUS': e['value']=maxvus
with open(p, 'w') as f: yaml.safe_dump_all(docs, f, sort_keys=False)
PYEOF
}

deploy_redis_ns() {
  kubectl create namespace redis --dry-run=client -o yaml | kubectl apply -f - >/dev/null
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
  for i in $(seq 1 30); do
    n=$(kubectl get pods -n redis --no-headers 2>/dev/null | wc -l)
    [ "$n" = "0" ] && return
    sleep 2
  done
}

run_one() {
  local name="$1" replicas="$2" qps="$3"
  local vus=$(( qps / 10 )); [ "$vus" -lt 50 ] && vus=50
  local maxvus=$(( qps / 2 + 200 )); [ "$maxvus" -lt 200 ] && maxvus=200
  local total_target=$(( replicas * qps ))
  apply_max_everything
  set_loadgen "$replicas" "$qps" "$vus" "$maxvus"
  local logdir="$OUT/$name"
  mkdir -p "$logdir"
  echo "" | tee -a "$OUT/matrix.log"
  echo "=== RUN: $name (loadgen×$replicas @ $qps qps/pod = ${total_target}/s target, NO SAMPLER) ===" | tee -a "$OUT/matrix.log"
  date -u +"%Y-%m-%dT%H:%M:%SZ start" | tee -a "$OUT/matrix.log"

  teardown_redis_ns
  deploy_redis_ns

  local k6pod=""
  for i in $(seq 1 120); do
    k6pod=$(kubectl -n redis get pods -l app.kubernetes.io/name=loadgen -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    [ -n "$k6pod" ] && break
    sleep 1
  done
  for i in $(seq 1 180); do
    kubectl -n redis logs "$k6pod" -c k6 --tail=20 2>/dev/null | grep -qE "running \(|api reachable" && break
    sleep 1
  done

  sleep 60  # warmup

  # ONLY collect t0/t1 conntrack snapshots — NO mid-run sampler
  local ct0=$(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null)
  local t0=$(date -u +%s)
  local k6_iters0_total=0
  for pod in $(kubectl -n redis get pods -l app.kubernetes.io/name=loadgen -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    n=$(kubectl -n redis logs "$pod" -c k6 --tail=200 2>/dev/null | grep -oE "[0-9]+ complete" | tail -1 | grep -oE "[0-9]+" || echo 0)
    k6_iters0_total=$((k6_iters0_total + n))
  done

  sleep 180

  local ct1=$(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null)
  local t1=$(date -u +%s)
  local k6_iters1_total=0
  for pod in $(kubectl -n redis get pods -l app.kubernetes.io/name=loadgen -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    kubectl -n redis logs "$pod" -c k6 --tail=400 2>/dev/null > "$logdir/k6-$pod.log"
    n=$(grep -oE "[0-9]+ complete" "$logdir/k6-$pod.log" | tail -1 | grep -oE "[0-9]+" || echo 0)
    k6_iters1_total=$((k6_iters1_total + n))
  done

  kubectl top pods -n redis 2>/dev/null > "$logdir/top.txt" || true
  kubectl top pod -n pl -l name=vizier-pem --no-headers 2>/dev/null > "$logdir/top-pem.txt"

  local elapsed=$((t1 - t0)); [ "$elapsed" -lt 1 ] && elapsed=1
  local k6_delta=$((k6_iters1_total - k6_iters0_total))
  local k6_rate=$(( k6_delta / elapsed ))
  local pem_cpu=$(awk '{gsub("m","",$2); print $2}' "$logdir/top-pem.txt" 2>/dev/null | head -1)

  printf "  k6=%s/s (target=%s/s)  | ct0=%s ct1=%s pem_cpu=%sm\n" \
    "$k6_rate" "$total_target" "${ct0:-?}" "${ct1:-?}" "${pem_cpu:-?}" \
    | tee -a "$OUT/matrix.log"
  date -u +"%Y-%m-%dT%H:%M:%SZ end" | tee -a "$OUT/matrix.log"
}

# Re-run the two key configs WITHOUT sampler
run_one max_16x      1 8000
run_one split_2x4k   2 4000

restore_base
teardown_redis_ns
echo "" | tee -a "$OUT/matrix.log"
echo "=== matrix4 complete ===" | tee -a "$OUT/matrix.log"
echo "results: $OUT" | tee -a "$OUT/matrix.log"
