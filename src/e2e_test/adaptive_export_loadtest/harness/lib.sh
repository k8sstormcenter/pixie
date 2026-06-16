#!/usr/bin/env bash
# lib.sh — shared helpers for the AE fixture-isolation load-tests (L3, live rig).
#
# Connectivity model (per the labctl-session-discipline rule): all kubectl runs
# LOCALLY over the tailscale-direct kubeconfig (make kubeconfig), and ClickHouse
# is reached over a local port-forward — NO long-held labctl ssh sessions.
#
# Required env (export before sourcing or pass through):
#   KUBECONFIG   tailscale-direct kubeconfig for the rig (make kubeconfig)
#   AELOAD_IMAGE ttl.sh/aeload-<ts>:24h (built on the PG dev-machine)
# Optional:
#   CH_NS (default clickhouse), AE_NS (default pl), AELOAD_NS (default aeload)
#   CH_HTTP (default http://127.0.0.1:8123 via the port-forward this lib opens)
#   CH_RO_USER / CH_RO_PASS  (SELECT creds; default = empty → default user)
#   CH_RW_USER / CH_RW_PASS  (INSERT creds; default ingest_writer/changeme-ingest)
set -uo pipefail

CH_NS="${CH_NS:-clickhouse}"
AE_NS="${AE_NS:-pl}"
AELOAD_NS="${AELOAD_NS:-aeload}"
CH_HTTP="${CH_HTTP:-http://127.0.0.1:8123}"
CH_RO_USER="${CH_RO_USER:-}"
CH_RO_PASS="${CH_RO_PASS:-}"
CH_RW_USER="${CH_RW_USER:-ingest_writer}"
CH_RW_PASS="${CH_RW_PASS:-changeme-ingest}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
K8S_DIR="$(cd "$HERE/../k8s" && pwd)"

_PF_PID=""

die(){ echo "[aeload] FATAL: $*" >&2; exit 1; }
log(){ echo "[aeload] $*" >&2; }

# k — kubectl over the tailscale kubeconfig.
k(){ kubectl "$@"; }

# ch_svc — resolve the ClickHouse service name (first svc exposing 8123).
ch_svc(){
  k -n "$CH_NS" get svc -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.ports[*].port}{"\n"}{end}' \
    | awk '/8123/{print $1; exit}'
}

# ch_portforward_up — start a background port-forward 8123 -> CH HTTP.
# Set CH_NO_PF=1 when running LAB-SIDE (on the PG dev-machine): there kubectl is
# native and ClickHouse is reachable in-cluster, so point CH_HTTP straight at the
# service (e.g. http://<chsvc>.<ns>.svc:8123) and skip the forward entirely. This
# is the disciplined path — no long-held labctl ssh / no tailnet dependency.
ch_portforward_up(){
  if [[ "${CH_NO_PF:-0}" == "1" ]]; then
    # Auto-fill CH_HTTP from the in-cluster service if left at the default.
    if [[ "$CH_HTTP" == "http://127.0.0.1:8123" ]]; then
      local svc; svc="$(ch_svc)"; [[ -n "$svc" ]] || die "no ClickHouse svc exposing 8123 in ns $CH_NS"
      CH_HTTP="http://${svc}.${CH_NS}.svc:8123"
    fi
    log "lab-side mode: CH_HTTP=$CH_HTTP (no port-forward)"
    curl -fsS "$CH_HTTP/ping" >/dev/null 2>&1 || die "CH not reachable at $CH_HTTP"
    return 0
  fi
  local svc; svc="$(ch_svc)"; [[ -n "$svc" ]] || die "no ClickHouse svc exposing 8123 in ns $CH_NS"
  log "port-forward svc/$svc 8123 (ns $CH_NS)"
  k -n "$CH_NS" port-forward "svc/$svc" 8123:8123 >/tmp/aeload-pf.log 2>&1 &
  _PF_PID=$!
  for _ in $(seq 1 30); do
    curl -fsS "$CH_HTTP/ping" >/dev/null 2>&1 && { log "port-forward ready"; return 0; }
    sleep 0.5
  done
  die "port-forward to CH did not become ready (see /tmp/aeload-pf.log)"
}
ch_portforward_down(){ [[ -n "$_PF_PID" ]] && kill "$_PF_PID" 2>/dev/null || true; }
trap ch_portforward_down EXIT

# chq <sql> — run a read query, return the raw result (default user / RO creds).
chq(){
  local sql="$1" auth=()
  [[ -n "$CH_RO_USER" ]] && auth=(-u "${CH_RO_USER}:${CH_RO_PASS}")
  curl -sS "${auth[@]}" --data-binary "$sql" "$CH_HTTP/" 2>/dev/null
}

# count_pod <table> <pod_unique> — rows for this rep's pod (globally-unique pod
# name → safe LIKE). Returns an integer (0 if table/rows absent).
count_pod(){
  local table="$1" uniq="$2"
  local n; n="$(chq "SELECT count() FROM forensic_db.${table} WHERE pod LIKE '%${uniq}%'" )"
  echo "${n:-0}" | tr -dc '0-9' | head -c 18
}

# NOTE: the live AE DaemonSet polls kubescape_logs WHERE hostname=<node name>,
# so every injected fixture's hostname MUST be a real node. Per-rep isolation is
# therefore by UNIQUE POD (distinct anomaly_hash), not by hostname. The helpers
# below scope to (hostname=node, pod LIKE the rep's unique pod). adaptive_
# attribution stores the BARE pod name (kubescape podName), unlike the protocol
# tables whose pod is "<ns>/<pod>" (upid_to_pod_name).

# attrib_count <node> <pod_unique> — adaptive_attribution rows (FINAL) for a rep.
attrib_count(){
  local node="$1" pod="$2" n
  n="$(chq "SELECT count() FROM (SELECT 1 FROM forensic_db.adaptive_attribution FINAL WHERE hostname='${node}' AND pod LIKE '%${pod}%')")"
  echo "${n:-0}" | tr -dc '0-9' | head -c 18
}
uniq_hashes(){
  local node="$1" pod="$2" n
  n="$(chq "SELECT uniqExact(anomaly_hash) FROM forensic_db.adaptive_attribution WHERE hostname='${node}' AND pod LIKE '%${pod}%'")"
  echo "${n:-0}" | tr -dc '0-9' | head -c 18
}
# watermark_of <node> — current trigger watermark for that node (monotone across
# reps that share a node; equals the most-recently-injected event_time).
watermark_of(){
  local node="$1" n
  n="$(chq "SELECT watermark FROM forensic_db.trigger_watermark FINAL WHERE hostname='${node}' AND table_name='kubescape_logs'")"
  echo "${n:-0}" | tr -dc '0-9' | head -c 20
}

# attr_field <node> <pod-exact> <field> — read one adaptive_attribution FINAL
# column (e.g. n_anomalies, toUnixTimestamp(last_seen)) for a single pod.
attr_field(){
  local node="$1" pod="$2" field="$3" n
  n="$(chq "SELECT ${field} FROM forensic_db.adaptive_attribution FINAL WHERE hostname='${node}' AND pod='${pod}'")"
  echo "${n:-0}" | tr -dc '0-9' | head -c 20
}

# first_node — a real schedulable node name (fixture hostname for control-plane
# experiments). nodes_list — all node names, newline-separated.
nodes_list(){ k get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'; }
first_node(){ nodes_list | head -n1; }

# now_ns — wall-clock unix nanoseconds (unique-name suffix only).
now_ns(){ date +%s%N; }
# now_s — wall-clock unix SECONDS = the production event_time unit (soc Vector
# emits seconds; the CH DDL TTL/PARTITION assume seconds). ALL fixtures use this.
now_s(){ date +%s; }

# warmup <node> — absorb the AE trigger cold-start on a node. The very first
# poll after AE boots only establishes the watermark baseline, so the first
# real event for a fresh hostname can be missed; a throwaway injection +
# settle primes the per-node trigger so measured reps are steady-state.
warmup(){
  local node="$1" inject="$HERE/inject.sh"
  log "warmup trigger on node=$node"
  "$inject" --endpoint "$CH_HTTP" --user "$CH_RW_USER" --pass "$CH_RW_PASS" \
    --hostname "$node" --ns aeload --pod "warmup-$(now_ns)" --rule R0001 \
    --pid 999 --comm warmup --event-time "$(now_s)" >&2 || true
  sleep "${WARMUP_SETTLE_S:-6}"
}

# wait_attrib <node> <podlike> <want> [timeout_s] — poll adaptive_attribution
# FINAL until it reaches <want> (AE's 250ms poll + write can lag a few seconds;
# a fixed sleep occasionally under-waited). Echoes the final observed count.
wait_attrib(){
  local node="$1" pod="$2" want="$3" to="${4:-20}" n=0
  for _ in $(seq 1 "$to"); do
    n="$(attrib_count "$node" "$pod")"
    [[ "${n:-0}" -ge "$want" ]] && break
    sleep 1
  done
  echo "${n:-0}"
}

# svc_ip <name> — ClusterIP of an aeload service (literal IP for the generator).
svc_ip(){ k -n "$AELOAD_NS" get svc "$1" -o jsonpath='{.spec.clusterIP}'; }

# apply_sinks — bring up the shared aeload ns + http-sink + pg-sink (idempotent).
apply_sinks(){
  [[ -n "${AELOAD_IMAGE:-}" ]] || die "AELOAD_IMAGE not set"
  sed "s#__IMAGE__#${AELOAD_IMAGE}#g" "$K8S_DIR/00-sinks.yaml" | k apply -f -
  k -n "$AELOAD_NS" rollout status deploy/http-sink --timeout=120s
  k -n "$AELOAD_NS" rollout status deploy/pg-sink   --timeout=120s
}

# fire_gen <pod_name> <http_n> <dns_n> <pgsql_n> — create a gen pod, wait for it
# to fire, echo its one-line JSON manifest. Leaves the pod RUNNING (held).
fire_gen(){
  local name="$1" hn="$2" dn="$3" pn="$4"
  local hip pip
  hip="$(svc_ip http-sink)"; pip="$(svc_ip pg-sink)"
  [[ -n "$hip" && -n "$pip" ]] || die "could not resolve sink ClusterIPs"
  # GEN_SETTLE_MS: pre-band warm-up so Pixie/Stirling attaches BEFORE the exact
  # band (exact-count tests). GEN_SUSTAIN_SEC: continuous trickle AFTER the band
  # (sustained "keep writing until t_end" RCA). Defaults suit exact-count runs.
  sed -e "s#__NAME__#${name}#g" -e "s#__IMAGE__#${AELOAD_IMAGE}#g" \
      -e "s#__HTTP_ADDR__#${hip}:8080#g" -e "s#__PG_ADDR__#${pip}:5432#g" \
      -e "s#__HTTP_N__#${hn}#g" -e "s#__DNS_N__#${dn}#g" -e "s#__PGSQL_N__#${pn}#g" \
      -e "s#__SETTLE_PRE_MS__#${GEN_SETTLE_MS:-30000}#g" -e "s#__SUSTAIN_SEC__#${GEN_SUSTAIN_SEC:-0}#g" \
      "$K8S_DIR/gen-pod.tmpl.yaml" | k apply -f - >&2
  # Wait for the FIRED sentinel + grab the manifest line (allow for the warm-up).
  local mani=""
  for _ in $(seq 1 90); do
    if k -n "$AELOAD_NS" logs "$name" 2>/dev/null | grep -q AELOAD_FIRED; then
      mani="$(k -n "$AELOAD_NS" logs "$name" 2>/dev/null | grep AELOAD_MANIFEST | tail -1 | sed 's/^AELOAD_MANIFEST //')"
      break
    fi
    sleep 1
  done
  [[ -n "$mani" ]] || die "gen $name never fired (logs:)\n$(k -n "$AELOAD_NS" logs "$name" 2>/dev/null | tail -20)"
  echo "$mani"
}
del_gen(){ k -n "$AELOAD_NS" delete pod "$1" --grace-period=2 --wait=false >/dev/null 2>&1 || true; }

# jget <json> <key> — tiny JSON field reader (numbers/strings) via python3.
jget(){ python3 -c 'import json,sys;print(json.load(sys.stdin)[sys.argv[1]])' "$2" <<<"$1"; }
