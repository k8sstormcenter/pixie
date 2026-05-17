#!/usr/bin/env bash
# matrix-runner-3.sh — comprehensive instrumented runner.
# Always uses max_everything app-stack base (api 8×4cpu, redis 8cpu, pg 8cpu).
# Varies (a) multiplier and (b) loadgen-replica/k6-qps split.
# Per run, samples host + pod + db internals every 5s into CSV and emits
# both a single summary line + the CSV for plotting.
set -uo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PY=/home/constanze/.venvs/render/bin/python
BASE=/tmp/matrix-base
SRC="$REPO_ROOT/src/e2e_test/perf_tool/pkg/suites/k8s/sovereign-soc"
OUT=/tmp/matrix3-$(date -u +%Y%m%d-%H%M%S)
mkdir -p "$OUT"
echo "matrix3 dir: $OUT" | tee "$OUT/matrix.log"

CH_URL='http://localhost:30123'
CH_AUTH='pixie:pixie_password'

restore_base() {
  cp "$BASE/api-backend.yaml"      "$SRC/api-backend.yaml"
  cp "$BASE/redis-vulnerable.yaml" "$SRC/redis-vulnerable.yaml"
  cp "$BASE/postgres.yaml"         "$SRC/postgres.yaml"
  cp "$BASE/loadgen-k6.yaml"       "$SRC/loadgen-k6.yaml"
}

# Apply max_everything app stack — fixed for all runs in matrix3.
apply_max_everything() {
  restore_base
  "$PY" - "$SRC" <<'PYEOF'
import sys, yaml, os, re
src = sys.argv[1]
def load(p):
    with open(p) as f: return list(yaml.safe_load_all(f))
def save(p, docs):
    with open(p, 'w') as f: yaml.safe_dump_all(docs, f, sort_keys=False)
def deploys(docs):
    return [d for d in docs if d and d.get('kind') in ('Deployment','StatefulSet')]
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

api   = load(os.path.join(src, 'api-backend.yaml'))
redis = load(os.path.join(src, 'redis-vulnerable.yaml'))
pg    = load(os.path.join(src, 'postgres.yaml'))

for d in deploys(api):
    if d['metadata']['name']=='api':
        c = container(d,'api')
        setres(c, cpu_lim="4", mem_lim="2Gi")
        d['spec']['replicas']=8
        setargs_gunicorn(c, 8, 32)
        setpool(c, 4, 32)
for d in deploys(redis):
    if d['metadata']['name']=='redis':
        setres(container(d,'redis'), cpu_lim="8", mem_lim="1Gi")
for d in deploys(pg):
    if d['metadata']['name']=='postgres':
        setres(container(d,'postgres'), cpu_lim="8", mem_lim="2Gi")

save(os.path.join(src, 'api-backend.yaml'), api)
save(os.path.join(src, 'redis-vulnerable.yaml'), redis)
save(os.path.join(src, 'postgres.yaml'), pg)
PYEOF
}

# Set the loadgen Deployment to N replicas with K6_QPS per pod.
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

ch_q() {
  curl -s -G -u "$CH_AUTH" --data-urlencode "query=$1 FORMAT TabSeparated" "$CH_URL/" | tr -d '\n'
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

# Background sampler — writes 1 row every 5s to $1 CSV.
start_sampler() {
  local csv="$1"
  local stopfile="$csv.stop"
  rm -f "$stopfile"
  (
    echo "ts,conntrack_count,sock_used,sock_tw,sock_orphan,redis_ops_s,redis_cpu_user,redis_cpu_sys,redis_conn_recv,pg_xact_commit,pg_tup_inserted,pg_blks_read,api_access_lines,pem_cpu_m,pem_mem_mi,k6sa_cpu_m,k6sa_mem_mi,coredns_q_total,coredns_cache_miss" > "$csv"
    # Resolve coredns pod IP once
    local coredns_ip=$(kubectl get pods -n kube-system -l k8s-app=kube-dns -o jsonpath='{.items[0].status.podIP}' 2>/dev/null)
    local prev_redis_conn=0 prev_pg_xact=0 prev_pg_tup=0 prev_pg_blks=0 prev_api_lines=0
    local first=1
    while [ ! -f "$stopfile" ]; do
      local ts=$(date -u +%s)
      # Host counters
      local ct=$(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null || echo 0)
      local sockstat=$(cat /proc/net/sockstat 2>/dev/null | grep '^TCP:' | head -1)
      local sused=$(echo "$sockstat" | awk '{for(i=1;i<=NF;i++) if($i=="inuse") print $(i+1)}')
      local stw=$(echo "$sockstat" | awk '{for(i=1;i<=NF;i++) if($i=="tw") print $(i+1)}')
      local sorph=$(echo "$sockstat" | awk '{for(i=1;i<=NF;i++) if($i=="orphan") print $(i+1)}')
      # Redis INFO stats
      local redis_pod=$(kubectl -n redis get pods -l app.kubernetes.io/name=redis -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
      local r_ops="0" r_cu="0" r_cs="0" r_conn="0"
      if [ -n "$redis_pod" ]; then
        local rinfo=$(kubectl -n redis exec "$redis_pod" -c redis -- redis-cli INFO stats,cpu 2>/dev/null)
        r_ops=$(echo "$rinfo" | awk -F: '/^instantaneous_ops_per_sec:/{print $2}' | tr -d '\r')
        r_cu=$(echo "$rinfo" | awk -F: '/^used_cpu_user:/{print $2}' | tr -d '\r')
        r_cs=$(echo "$rinfo" | awk -F: '/^used_cpu_sys:/{print $2}' | tr -d '\r')
        r_conn=$(echo "$rinfo" | awk -F: '/^total_connections_received:/{print $2}' | tr -d '\r')
      fi
      # PG stats — pg_stat_database for 'appdb'
      local pg_pod=$(kubectl -n redis get pods -l app.kubernetes.io/name=postgres -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
      local pgx="0" pgtup="0" pgblk="0"
      if [ -n "$pg_pod" ]; then
        local pgrow=$(kubectl -n redis exec "$pg_pod" -- psql -U app -d appdb -At -F, -c "SELECT xact_commit, tup_inserted, blks_read FROM pg_stat_database WHERE datname='appdb'" 2>/dev/null)
        pgx=$(echo "$pgrow" | awk -F, '{print $1}')
        pgtup=$(echo "$pgrow" | awk -F, '{print $2}')
        pgblk=$(echo "$pgrow" | awk -F, '{print $3}')
      fi
      # API gunicorn access log line count (aggregate across all api pods)
      local api_lines=0
      for pod in $(kubectl -n redis get pods -l app.kubernetes.io/name=api -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
        n=$(kubectl -n redis logs "$pod" -c api --tail=-1 2>/dev/null | grep -c " HTTP/" || true)
        api_lines=$((api_lines + n))
      done
      # pem + kubescape-node-agent CPU
      local pem_top=$(kubectl top pod -n pl -l name=vizier-pem --no-headers 2>/dev/null | head -1)
      local pem_cpu=$(echo "$pem_top" | awk '{print $2}' | tr -d 'm')
      local pem_mem=$(echo "$pem_top" | awk '{print $3}' | tr -d 'Mi')
      local k6sa_top=$(kubectl top pod -n honey -l app=node-agent --no-headers 2>/dev/null | head -1)
      local k6sa_cpu=$(echo "$k6sa_top" | awk '{print $2}' | tr -d 'm')
      local k6sa_mem=$(echo "$k6sa_top" | awk '{print $3}' | tr -d 'Mi')
      # CoreDNS counters
      local cd_q="0" cd_miss="0"
      if [ -n "$coredns_ip" ]; then
        local cdmetrics=$(curl -s --max-time 2 "http://$coredns_ip:9153/metrics" 2>/dev/null)
        cd_q=$(echo "$cdmetrics" | awk '/^coredns_dns_request_duration_seconds_count\{.*zone="\."\}/{print int($NF)}' | head -1)
        cd_miss=$(echo "$cdmetrics" | awk '/^coredns_cache_misses_total/{print int($NF)}' | head -1)
      fi
      echo "$ts,$ct,${sused:-0},${stw:-0},${sorph:-0},${r_ops:-0},${r_cu:-0},${r_cs:-0},${r_conn:-0},${pgx:-0},${pgtup:-0},${pgblk:-0},$api_lines,${pem_cpu:-0},${pem_mem:-0},${k6sa_cpu:-0},${k6sa_mem:-0},${cd_q:-0},${cd_miss:-0}" >> "$csv"
      sleep 5
    done
    rm -f "$stopfile"
  ) >/dev/null 2>&1 &
  echo $!
}

stop_sampler() {
  local csv="$1"
  touch "$csv.stop"
  sleep 1
}

run_one() {
  # $1=name (e.g. max_16x), $2=loadgen_replicas, $3=k6_qps_per_pod
  local name="$1" replicas="$2" qps="$3"
  local vus=$(( qps / 10 ))  # rough: vus=qps/10
  local maxvus=$(( qps / 2 + 200 ))
  [ "$vus" -lt 50 ] && vus=50
  [ "$maxvus" -lt 200 ] && maxvus=200
  local total_target=$(( replicas * qps ))

  apply_max_everything
  set_loadgen "$replicas" "$qps" "$vus" "$maxvus"

  local logdir="$OUT/$name"
  mkdir -p "$logdir"

  echo "" | tee -a "$OUT/matrix.log"
  echo "=== RUN: $name (loadgen×$replicas @ $qps qps/pod = ${total_target}/s target) ===" | tee -a "$OUT/matrix.log"
  date -u +"%Y-%m-%dT%H:%M:%SZ start" | tee -a "$OUT/matrix.log"

  teardown_redis_ns
  deploy_redis_ns

  # Wait for ANY loadgen pod
  local k6pod=""
  for i in $(seq 1 120); do
    k6pod=$(kubectl -n redis get pods -l app.kubernetes.io/name=loadgen -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    [ -n "$k6pod" ] && break
    sleep 1
  done
  if [ -z "$k6pod" ]; then
    echo "  FAIL: no k6 pod after 120s" | tee -a "$OUT/matrix.log"
    return
  fi
  for i in $(seq 1 180); do
    kubectl -n redis logs "$k6pod" -c k6 --tail=20 2>/dev/null | grep -qE "running \(|api reachable" && break
    sleep 1
  done

  sleep 60  # warmup

  local csv="$logdir/samples.csv"
  start_sampler "$csv" >/dev/null

  local t0=$(date -u +%s)
  local k6_iters0_total=0
  for pod in $(kubectl -n redis get pods -l app.kubernetes.io/name=loadgen -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    n=$(kubectl -n redis logs "$pod" -c k6 --tail=200 2>/dev/null | grep -oE "[0-9]+ complete" | tail -1 | grep -oE "[0-9]+" || echo 0)
    k6_iters0_total=$((k6_iters0_total + n))
  done

  sleep 180

  local t1=$(date -u +%s)
  local k6_iters1_total=0
  for pod in $(kubectl -n redis get pods -l app.kubernetes.io/name=loadgen -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    kubectl -n redis logs "$pod" -c k6 --tail=400 2>/dev/null > "$logdir/k6-$pod.log"
    n=$(grep -oE "[0-9]+ complete" "$logdir/k6-$pod.log" | tail -1 | grep -oE "[0-9]+" || echo 0)
    k6_iters1_total=$((k6_iters1_total + n))
  done

  stop_sampler "$csv"
  kubectl top pods -n redis 2>/dev/null > "$logdir/top-final.txt" || true

  local elapsed=$((t1 - t0))
  [ "$elapsed" -lt 1 ] && elapsed=1
  local k6_delta=$((k6_iters1_total - k6_iters0_total))
  local k6_rate=$(( k6_delta / elapsed ))

  # Compute summary stats from CSV
  local stats=$("$PY" - "$csv" <<'PYEOF'
import sys, csv
rows=[]
with open(sys.argv[1]) as f:
    r=csv.DictReader(f)
    for row in r:
        rows.append(row)
def col(name, cast=float):
    vs=[cast(row[name]) for row in rows if row.get(name) and row[name] not in ('','0') ]
    return vs
def stats(name):
    vs=col(name)
    if not vs: return ('?','?','?')
    return (f"{min(vs):.0f}", f"{sum(vs)/len(vs):.0f}", f"{max(vs):.0f}")
def delta(name):
    vs=col(name, float)
    if len(vs)<2: return '?'
    return f"{int(max(vs)-min(vs))}"
def rate(name, elapsed):
    vs=col(name, float)
    if len(vs)<2: return '?'
    return f"{int((max(vs)-min(vs))/elapsed)}"

elapsed = 180
import os
print(f"ct_max={stats('conntrack_count')[2]}")
print(f"sused_max={stats('sock_used')[2]}")
print(f"stw_max={stats('sock_tw')[2]}")
print(f"r_ops_max={stats('redis_ops_s')[2]}")
print(f"r_conn_rate={rate('redis_conn_recv', elapsed)}")
print(f"pg_commit_rate={rate('pg_xact_commit', elapsed)}")
print(f"pg_insert_rate={rate('pg_tup_inserted', elapsed)}")
print(f"api_req_rate={rate('api_access_lines', elapsed)}")
print(f"pem_cpu_max={stats('pem_cpu_m')[2]}")
print(f"pem_cpu_avg={stats('pem_cpu_m')[1]}")
print(f"k6sa_cpu_max={stats('k6sa_cpu_m')[2]}")
print(f"coredns_q_rate={rate('coredns_q_total', elapsed)}")
print(f"coredns_miss_rate={rate('coredns_cache_miss', elapsed)}")
PYEOF
)
  # Print headline summary
  echo "  k6=${k6_rate}/s (target=${total_target}/s)  |  ${stats}" | tr '\n' ' ' | tee -a "$OUT/matrix.log"
  echo "" | tee -a "$OUT/matrix.log"
  date -u +"%Y-%m-%dT%H:%M:%SZ end" | tee -a "$OUT/matrix.log"
}

# Reference points
run_one max_16x        1 8000
run_one max_32x        1 16000

# Multi-loadgen splits
run_one split_2x4k     2 4000   # 2 × 4000 = 8000 total (== 16x equivalent)
run_one split_4x2k     4 2000   # 4 × 2000 = 8000 total
run_one split_4x4k     4 4000   # 4 × 4000 = 16000 total (== 32x equivalent)
run_one split_8x2k     8 2000   # 8 × 2000 = 16000 total

restore_base
teardown_redis_ns
echo "" | tee -a "$OUT/matrix.log"
echo "=== matrix3 complete ===" | tee -a "$OUT/matrix.log"
echo "results: $OUT" | tee -a "$OUT/matrix.log"
