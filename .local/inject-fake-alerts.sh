#!/usr/bin/env bash
# inject-fake-alerts.sh — write synthetic kubescape_logs rows that point
# at our 6 protocol-loadtest pods so adaptive_export keeps firing its
# pushPixieRows fan-out and the Pixie protocol data lands in CH.
#
# Each pod gets a unique (pid, comm) tuple. Re-running with the SAME pid
# extends the operator's active window (t_end pushed forward) — keeps
# the per-pod goroutine alive. Run in a loop every 60s.
set -uo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
CH='http://localhost:30123'
AUTH='pixie:pixie_password'
HOSTNAME='pixie-worker-node-constanze'
NS='px-protocol-loadtest'

# (pod-name-prefix, container-name, base-pid, fake-comm)
# Real PID is (base + ROUND), so each round produces 6 fresh hashes → 6 new
# pushPixieRows goroutines → continuous fan-out coverage.
PODS=(
  "http-server     app     900000  go-http-server"
  "http-client     client  910000  go-http-client"
  "redis-server    redis   920000  redis-server"
  "redis-client    client  930000  go-redis-client"
  "pgsql-server    postgres 940000 postgres"
  "pgsql-client    client  950000  go-pgsql-client"
)
ROUND=0

inject_round() {
  # Resolve each pod prefix to its actual pod name (still subject to rollouts)
  local insert_body=""
  for line in "${PODS[@]}"; do
    set -- $line
    local prefix="$1" ctr="$2" base_pid="$3" comm="$4"
    local pid=$((base_pid + ROUND))
    local actual=$(kubectl get pods -n "$NS" -l "name=$prefix" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    if [ -z "$actual" ]; then continue; fi
    local ts_ns=$(date -u +%s%N)
    # Pad to 19 digits if shorter
    while [ ${#ts_ns} -lt 19 ]; do ts_ns="${ts_ns}0"; done
    # Build the JSONEachRow line (single line, escape inner quotes)
    local k8s_details=$(printf '{"podName":"%s","podNamespace":"%s","namespace":"%s","containerName":"%s","workloadName":"%s"}' \
      "$actual" "$NS" "$NS" "$ctr" "$prefix")
    local proc_details=$(printf '{"processTree":{"pid":%d,"comm":"%s"}}' "$pid" "$comm")
    # JSONEachRow needs each row as a single JSON object; escape inner JSON strings
    local k8s_q=$(echo "$k8s_details" | sed 's/"/\\"/g')
    local proc_q=$(echo "$proc_details" | sed 's/"/\\"/g')
    insert_body+=$(printf '{"BaseRuntimeMetadata":"","CloudMetadata":"","RuleID":"R0001","RuntimeK8sDetails":"%s","RuntimeProcessDetails":"%s","event":"","event_time":"%s","hostname":"%s","level":"warning","message":"synthetic","msg":"synthetic-alert"}\n' \
      "$k8s_q" "$proc_q" "$ts_ns" "$HOSTNAME")
  done

  # Send to CH via INSERT … FORMAT JSONEachRow
  echo "$insert_body" | curl -s -u "$AUTH" --data-binary @- \
    "$CH/?query=INSERT%20INTO%20forensic_db.kubescape_logs%20FORMAT%20JSONEachRow" 2>&1
}

main() {
  local interval=${1:-30}  # default refresh every 30s
  while true; do
    ROUND=$((ROUND+1))
    inject_round
    echo "[$(date -u +%H:%M:%SZ)] round $ROUND injected (6 pods, pid-base+$ROUND)"
    sleep "$interval"
  done
}

main "$@"
