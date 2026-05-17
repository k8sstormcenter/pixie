#!/usr/bin/env bash
# setup-protocol-loadtest.sh — idempotent setup for the 3-protocol perf
# rig. Deploys redis/pgsql/http servers + clients + empty sbobs + labels
# so kubescape alerts from t=0 and adaptive_export drains for all 6 pods.
#
# Re-runnable. Apply, wait for ready, return 0. If anything's already
# deployed, `kubectl apply` updates in place.
set -uo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SRC="$REPO_ROOT/src/e2e_test/protocol_loadtest"
NS=px-protocol-loadtest

echo "=== ensure namespace ==="
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "=== apply empty sbobs ==="
kubectl apply -f "$SRC/k8s/sbobs.yaml" >/dev/null
kubectl get applicationprofiles -n "$NS" --no-headers

echo "=== apply server + client deployments ==="
kubectl apply -f "$SRC/k8s/redis_client/deploy.yaml" >/dev/null
kubectl apply -f "$SRC/k8s/pgsql_client/deploy.yaml" >/dev/null
kubectl apply -f "$SRC/k8s/http/deploy.yaml"         >/dev/null

echo "=== ensure user-defined-profile label on each deployment ==="
declare -A LABEL_MAP=(
  [http-server]=http-server-empty
  [http-client]=http-client-empty
  [redis-server]=redis-server-empty
  [redis-client]=redis-client-empty
  [pgsql-server]=pgsql-server-empty
  [pgsql-client]=pgsql-client-empty
)
for d in "${!LABEL_MAP[@]}"; do
  prof="${LABEL_MAP[$d]}"
  kubectl patch deployment -n "$NS" "$d" --type=strategic \
    -p "{\"spec\":{\"template\":{\"metadata\":{\"labels\":{\"kubescape.io/user-defined-profile\":\"$prof\"}}}}}" >/dev/null 2>&1
done

echo "=== wait for all pods Ready ==="
for i in $(seq 1 60); do
  ready=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null | awk '$2 ~ /^1\/1$/' | wc -l)
  total=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null | wc -l)
  printf "[%02d] ready=%d/%d\n" "$i" "$ready" "$total"
  if [ "$ready" -ge 6 ] && [ "$total" -eq 6 ]; then
    echo "=== all 6 pods ready ==="
    kubectl get pods -n "$NS" --no-headers
    exit 0
  fi
  sleep 3
done
echo "=== TIMED OUT waiting for pods ==="
kubectl get pods -n "$NS"
exit 1
