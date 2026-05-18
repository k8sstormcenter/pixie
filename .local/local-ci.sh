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

# local-ci.sh — repeatable end-to-end test for the adaptive_export feature
# (PR #37, branch entlein/adaptive-write).
#
# Verifies the failure mode the user reported ("tables never appear in
# the clickhouse database") by exercising every persistence path the
# operator exposes against a real ClickHouse running in a local k3s.
#
# Phases (default = 0..8; --full adds 9):
#   0  pre-flight tooling (k3s, kubectl, helm, go, golangci-lint)
#   1  unit tests (go test ./src/vizier/services/adaptive_export/...)
#   2  lint (go vet + golangci-lint)
#   3  bring up ClickHouse via soc/clickhouse-lab (Altinity operator
#      + keeper + CHI + soc-side schema for alerts + kubescape_logs)
#   4  sanity: forensic_db / alerts / kubescape_logs exist (soc layer)
#   5  operator's Apply() against live CH — ALL 12 pixie tables +
#      adaptive_attribution must materialise
#   6  VerifyPixieSchema — required columns present on every pixie table
#   7  sink: AttributionRow + WritePixieRows for every PixieTable
#   8  trigger: insert kubescape_logs row, expect a kubescape.Event
#   9  (--full) bazel build + image push + operator deploy + e2e smoke
#
# Modes:
#   ./local-ci.sh                     # phases 0..8
#   ./local-ci.sh --full              # phases 0..9
#   ./local-ci.sh --phases=1,2        # specific phases only
#   ./local-ci.sh --skip-cluster      # skip phase 3 (assume CH up)
#   ./local-ci.sh --teardown          # destroy the CH install + cluster
#   ./local-ci.sh --reset             # teardown then full run
#
# Idempotent: re-running keeps the cluster, ports, and kubeconfig.
# Test rows use unique tags per run so they don't collide.

set -euo pipefail

# --- paths + config -----------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOC_DIR="${SOC_DIR:-/home/constanze/code/soc-clone/soc}"
SOC_CH_DIR="$SOC_DIR/tree/clickhouse-lab"
CH_NS="${CH_NS:-clickhouse}"
CHI_NAME="${CHI_NAME:-forensic-soc-db}"
KEEPER_NAME="${KEEPER_NAME:-forensic-keeper}"
CH_OPERATOR_VERSION="${CH_OPERATOR_VERSION:-0.26.0}"
PORT_FWD_PORT="${PORT_FWD_PORT:-18123}"
SCHEMA_ADMIN_USER="${SCHEMA_ADMIN_USER:-schema_admin}"
SCHEMA_ADMIN_PASS="${SCHEMA_ADMIN_PASS:-localci-admin}"
KUBECONFIG_SRC="/etc/rancher/k3s/k3s.yaml"
KUBECONFIG_DST="$HOME/.kube/local-ci.yaml"
PORT_FWD_PIDFILE="/tmp/local-ci-pf.pid"
PIXIE_REPO="$SCRIPT_DIR"
GO_PKG="px.dev/pixie/src/vizier/services/adaptive_export/..."

# --- presentation -------------------------------------------------------

C_RED=$'\e[31m'; C_GRN=$'\e[32m'; C_YLW=$'\e[33m'; C_BLU=$'\e[36m'; C_RST=$'\e[0m'
PASS=0; FAIL=0
phase()  { echo "${C_BLU}=== $* ===${C_RST}"; }
ok()     { echo "  ${C_GRN}PASS${C_RST}: $*"; PASS=$((PASS+1)); }
fail()   { echo "  ${C_RED}FAIL${C_RST}: $*"; FAIL=$((FAIL+1)); }
info()   { echo "  ${C_YLW}info${C_RST}: $*"; }
need()   { command -v "$1" >/dev/null 2>&1 || { echo "${C_RED}missing tool: $1${C_RST}"; exit 1; }; }
check()  { local label="$1"; shift; if "$@"; then ok "$label"; else fail "$label"; fi; }

# --- arg parsing --------------------------------------------------------

PHASES_ARG=""
SKIP_CLUSTER=0
TEARDOWN=0
RESET=0
FULL=0
for arg in "$@"; do
  case "$arg" in
    --phases=*)        PHASES_ARG="${arg#--phases=}" ;;
    --skip-cluster)    SKIP_CLUSTER=1 ;;
    --teardown)        TEARDOWN=1 ;;
    --reset)           RESET=1 ;;
    --full)            FULL=1 ;;
    -h|--help)         sed -n '2,30p' "$0"; exit 0 ;;
    *)                 echo "unknown arg: $arg"; exit 1 ;;
  esac
done

# --- kubeconfig + sudo helper -------------------------------------------

setup_kubeconfig() {
  if [[ ! -f "$KUBECONFIG_SRC" ]]; then
    echo "${C_RED}k3s kubeconfig not found at $KUBECONFIG_SRC; is k3s installed?${C_RST}"
    exit 1
  fi
  mkdir -p "$(dirname "$KUBECONFIG_DST")"
  if [[ ! -f "$KUBECONFIG_DST" || "$KUBECONFIG_SRC" -nt "$KUBECONFIG_DST" ]]; then
    sudo cat "$KUBECONFIG_SRC" > "$KUBECONFIG_DST"
    chmod 600 "$KUBECONFIG_DST"
  fi
  export KUBECONFIG="$KUBECONFIG_DST"
}

cleanup_port_forward() {
  if [[ -f "$PORT_FWD_PIDFILE" ]]; then
    local pid; pid=$(cat "$PORT_FWD_PIDFILE" 2>/dev/null || true)
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
    rm -f "$PORT_FWD_PIDFILE"
  fi
}
trap cleanup_port_forward EXIT

# --- teardown -----------------------------------------------------------

teardown() {
  setup_kubeconfig
  phase "teardown"
  cleanup_port_forward
  kubectl delete chi "$CHI_NAME" -n "$CH_NS" --wait --ignore-not-found
  kubectl delete chk "$KEEPER_NAME" -n "$CH_NS" --wait --ignore-not-found 2>/dev/null || true
  helm uninstall clickhouse-operator -n "$CH_NS" 2>/dev/null || true
  kubectl delete pvc -n "$CH_NS" --all --wait --ignore-not-found 2>/dev/null || true
  kubectl delete ns "$CH_NS" --wait --ignore-not-found 2>/dev/null || true
  echo "${C_GRN}torn down${C_RST}"
}

if [[ "$TEARDOWN" -eq 1 ]]; then
  teardown
  exit 0
fi
if [[ "$RESET" -eq 1 ]]; then
  teardown || true
fi

# --- which phases? ------------------------------------------------------

if [[ -n "$PHASES_ARG" ]]; then
  IFS=',' read -ra PHASES <<<"$PHASES_ARG"
else
  PHASES=(0 1 2 3 4 5 6 7 8)
  [[ "$FULL" -eq 1 ]] && PHASES+=(9)
  [[ "$SKIP_CLUSTER" -eq 1 ]] && PHASES=("${PHASES[@]/3}")
fi
in_phase() { local p="$1"; for x in "${PHASES[@]}"; do [[ "$x" == "$p" ]] && return 0; done; return 1; }

# --- phase 0: pre-flight ------------------------------------------------

if in_phase 0; then
  phase "0/9 pre-flight tooling"
  need go; need golangci-lint; need kubectl; need helm; need curl; need jq
  if ! systemctl is-active --quiet k3s; then
    fail "k3s is not running (systemctl is-active k3s)"
    echo "  install with: curl -sfL https://get.k3s.io | sudo INSTALL_K3S_EXEC='server --write-kubeconfig-mode=644 --disable=traefik' sh -"
    exit 1
  fi
  ok "k3s active"
  setup_kubeconfig
  kubectl get nodes >/dev/null && ok "kubectl can reach k3s"
fi

# --- phase 1: unit tests ------------------------------------------------

if in_phase 1; then
  phase "1/9 unit tests"
  cd "$PIXIE_REPO"
  if go test -count=1 -timeout 60s "./src/vizier/services/adaptive_export/..."; then
    ok "go test ./src/vizier/services/adaptive_export/..."
  else
    fail "go test"
    [[ "$FAIL" -gt 0 ]] && exit 1
  fi
fi

# --- phase 2: lint ------------------------------------------------------

if in_phase 2; then
  phase "2/9 lint"
  cd "$PIXIE_REPO"
  if go vet ./src/vizier/services/adaptive_export/...; then
    ok "go vet"
  else
    fail "go vet"
  fi
  if golangci-lint run ./src/vizier/services/adaptive_export/...; then
    ok "golangci-lint"
  else
    fail "golangci-lint (see output above)"
    info "lint failures are NOT fatal — phase continues; address before merging PR #37"
  fi
fi

# --- phase 3: ClickHouse bring-up via soc -------------------------------

build_patched_installation_yaml() {
  # Append a schema_admin user (allow_ddl=1) so the operator's Apply()
  # path can be exercised end-to-end via HTTP. Default user is locked
  # to localhost on Altinity images, ingest_writer/forensic_analyst
  # have allow_ddl=0. The patched YAML is written to /tmp/.
  local out=/tmp/local-ci-installation.yaml
  cat "$SOC_CH_DIR/installation.yaml" >"$out"
  # Insert the schema_admin user under spec.configuration.users.
  # Done via Python for reliability — yq isn't always installed.
  python3 - "$out" <<'PY'
import sys, re
path = sys.argv[1]
text = open(path).read()
patch = (
    "\n      # Local-CI admin: DDL-capable, used by the integration tests\n"
    "      schema_admin/profile: default\n"
    "      schema_admin/password: localci-admin\n"
    "      schema_admin/networks/ip: \"::/0\"\n"
    "      schema_admin/quota: default\n"
)
m = re.search(r'^    users:.*?(?=\n  defaults:)', text, re.S | re.M)
if not m:
    sys.exit("could not locate users: section in installation.yaml")
text = text[:m.end()] + patch + text[m.end():]
open(path, 'w').write(text)
PY
  echo "$out"
}

if in_phase 3; then
  phase "3/9 ClickHouse via soc/clickhouse-lab"
  setup_kubeconfig
  kubectl create ns "$CH_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  # Altinity operator
  helm repo add altinity https://helm.altinity.com >/dev/null 2>&1 || true
  helm repo update >/dev/null
  if helm status clickhouse-operator -n "$CH_NS" >/dev/null 2>&1; then
    ok "altinity operator already installed"
  else
    helm upgrade --install clickhouse-operator altinity/altinity-clickhouse-operator \
      --version "$CH_OPERATOR_VERSION" --namespace "$CH_NS" --create-namespace --wait
    ok "altinity operator installed"
  fi

  # Keeper
  kubectl apply -f "$SOC_CH_DIR/keeper.yaml" >/dev/null
  for i in $(seq 1 60); do
    kubectl get pods -n "$CH_NS" -l "clickhouse-keeper.altinity.com/chk=$KEEPER_NAME" --no-headers 2>/dev/null | grep -q Running && break
    sleep 3
  done
  check "keeper running" kubectl get pods -n "$CH_NS" -l "clickhouse-keeper.altinity.com/chk=$KEEPER_NAME" --no-headers -o jsonpath='{.items[0].status.phase}' 2>/dev/null

  # CHI (patched with schema_admin)
  PATCHED_YAML=$(build_patched_installation_yaml)
  kubectl apply -f "$PATCHED_YAML" >/dev/null

  info "waiting for CHI pod to come Ready (up to 5 min)…"
  for i in $(seq 1 100); do
    PHASE=$(kubectl get pods -n "$CH_NS" -l "clickhouse.altinity.com/chi=$CHI_NAME" --no-headers -o jsonpath='{.items[0].status.phase}' 2>/dev/null || true)
    [[ "$PHASE" == "Running" ]] && break
    sleep 3
  done
  CH_POD=$(kubectl get pods -n "$CH_NS" -l "clickhouse.altinity.com/chi=$CHI_NAME" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
  if [[ -z "$CH_POD" ]]; then fail "CHI pod did not start"; exit 1; fi
  ok "CHI pod $CH_POD running"

  for i in $(seq 1 60); do
    R=$(kubectl exec -n "$CH_NS" "$CH_POD" -- clickhouse-client -q "SELECT 1" 2>/dev/null | tr -d '[:space:]') || true
    [[ "$R" == "1" ]] && break
    sleep 2
  done
  check "clickhouse-client responsive in pod" test "$R" = "1"

  # Apply soc-owned schema (alerts + kubescape_logs only after b7f5fe0).
  kubectl exec -i -n "$CH_NS" "$CH_POD" -- clickhouse-client --multiquery <"$SOC_CH_DIR/schema.sql"
  ok "soc schema applied (alerts + kubescape_logs)"
fi

# --- ensure port-forward to CH (used by phases 4..8) --------------------

ensure_port_forward() {
  setup_kubeconfig
  if [[ -f "$PORT_FWD_PIDFILE" ]] && kill -0 "$(cat "$PORT_FWD_PIDFILE")" 2>/dev/null; then
    return 0
  fi
  local svc
  svc=$(kubectl get svc -n "$CH_NS" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | grep -m1 "^chi-$CHI_NAME-" || true)
  [[ -z "$svc" ]] && svc=$(kubectl get svc -n "$CH_NS" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | grep -m1 "$CHI_NAME" || true)
  [[ -z "$svc" ]] && { echo "${C_RED}no CH service found in ns/$CH_NS${C_RST}"; return 1; }
  info "port-forward svc/$svc :$PORT_FWD_PORT → 8123"
  ( kubectl port-forward -n "$CH_NS" "svc/$svc" "$PORT_FWD_PORT:8123" >/tmp/local-ci-pf.log 2>&1 ) &
  echo $! >"$PORT_FWD_PIDFILE"
  for i in $(seq 1 30); do
    curl -sf "http://localhost:$PORT_FWD_PORT/?query=SELECT%201" \
      -u "$SCHEMA_ADMIN_USER:$SCHEMA_ADMIN_PASS" 2>/dev/null | grep -q "^1$" && return 0
    sleep 1
  done
  echo "${C_RED}port-forward never became responsive — check /tmp/local-ci-pf.log${C_RST}"
  return 1
}

ch_count() {
  curl -sf "http://localhost:$PORT_FWD_PORT/?query=$1" \
    -u "$SCHEMA_ADMIN_USER:$SCHEMA_ADMIN_PASS" | tr -d '[:space:]'
}

# --- phase 4: soc-layer sanity ------------------------------------------

if in_phase 4; then
  phase "4/9 soc-layer sanity"
  ensure_port_forward
  for table in alerts kubescape_logs; do
    GOT=$(ch_count "EXISTS%20forensic_db.$table" || echo "")
    if [[ "$GOT" == "1" ]]; then ok "forensic_db.$table exists"; else fail "forensic_db.$table missing (soc/install.sh broken?)"; fi
  done
fi

# --- phase 5: operator Apply() integration ------------------------------

INTEGRATION_ENV=(
  "INTEGRATION_CH_ENDPOINT=http://localhost:$PORT_FWD_PORT"
  "INTEGRATION_CH_USER=$SCHEMA_ADMIN_USER"
  "INTEGRATION_CH_PASSWORD=$SCHEMA_ADMIN_PASS"
)

if in_phase 5; then
  phase "5/9 operator's Apply() against live CH"
  ensure_port_forward
  cd "$PIXIE_REPO"
  if env "${INTEGRATION_ENV[@]}" go test -tags=integration -count=1 -timeout 120s -v \
       -run 'TestApply_Live|TestApply_Idempotent' \
       ./src/vizier/services/adaptive_export/internal/clickhouse/...; then
    ok "Apply() materialises all 13 operator-owned tables"
  else
    fail "Apply() integration test failed — this is the 'tables never appear' bug surface"
  fi
fi

# --- phase 6: VerifyPixieSchema -----------------------------------------

if in_phase 6; then
  phase "6/9 VerifyPixieSchema"
  ensure_port_forward
  cd "$PIXIE_REPO"
  if env "${INTEGRATION_ENV[@]}" go test -tags=integration -count=1 -timeout 60s -v \
       -run TestVerifyPixieSchema_Live \
       ./src/vizier/services/adaptive_export/internal/clickhouse/...; then
    ok "VerifyPixieSchema passes"
  else
    fail "VerifyPixieSchema failed — required columns missing on a pixie table"
  fi
fi

# --- phase 7: sink -------------------------------------------------------

if in_phase 7; then
  phase "7/9 sink: AttributionRow + WritePixieRows"
  ensure_port_forward
  cd "$PIXIE_REPO"
  if env "${INTEGRATION_ENV[@]}" go test -tags=integration -count=1 -timeout 120s -v \
       -run 'TestSinkWriteAttribution_Live|TestSinkWritePixieRows_Live' \
       ./src/vizier/services/adaptive_export/internal/sink/...; then
    ok "sink writes succeed for adaptive_attribution + every pixie table"
  else
    fail "sink integration test failed"
  fi
fi

# --- phase 8: trigger ----------------------------------------------------

if in_phase 8; then
  phase "8/9 trigger: insert kubescape_logs row, expect Event"
  ensure_port_forward
  cd "$PIXIE_REPO"
  if env "${INTEGRATION_ENV[@]}" go test -tags=integration -count=1 -timeout 60s -v \
       -run TestTriggerSubscribe_Live \
       ./src/vizier/services/adaptive_export/internal/trigger/...; then
    ok "trigger surfaces the seeded row"
  else
    fail "trigger integration test failed"
  fi
fi

# --- phase 9: perf-eval-soc-attack end-to-end ---------------------------
#
# Mirrors .github/workflows/perf_soc_attack.yaml, but adapted for a single
# local k3s (the GH workflow targets a remote forensic cluster reachable
# over Tailscale). Differences from the GH workflow:
#   - Exports parquet locally instead of pushing to GCS (no gcloud creds
#     on this VM).
#   - Uses the in-cluster CH NodePort + a local `pixie` user instead of
#     the AOCC public forensic CH (SOC_CH_HOST / SOC_CH_CREDS).
#   - Reuses the Pixie deployment already running in `pl` instead of
#     re-running `px deploy` + skaffold rebuild (SOC_VIZIER_EXISTING=1).
#   - Drops --prom_recorder_override; recorders use the same kubeconfig.
#
# Required env (read from ~/.pixie/keys.env if not pre-exported):
#   PX_API_KEY              — AOCC pixie-cloud API key (NOT exported in
#                             the shell, passed via --api_key).
#   PX_DEPLOY_KEY           — present in keys.env but unused here (the
#                             perf_tool uses the API key for vizier ops).
# Optional:
#   PERF_OUT_DIR            — defaults to /tmp/perf-out-$ts.
#   PERF_TAGS               — extra tags, default "local-ci".

if in_phase 9; then
  phase "9/9 perf-eval-soc-attack (sovereign-soc/redis-attack)"
  setup_kubeconfig
  cd "$PIXIE_REPO"

  # Pixie keys: prefer pre-exported env, else parse PX_API_KEY out of
  # ~/.pixie/keys.env. Avoid `source` — that file may contain a
  # placeholder `TS_AUTH_KEY=<consumed-...>` whose `<>` would trigger a
  # shell syntax error.
  if [[ -z "${PX_API_KEY:-}" && -r "$HOME/.pixie/keys.env" ]]; then
    PX_API_KEY=$(awk -F= '/^PX_API_KEY=/{print substr($0, index($0,"=")+1); exit}' "$HOME/.pixie/keys.env")
    export PX_API_KEY
  fi
  if [[ -z "${PX_API_KEY:-}" ]]; then
    fail "PX_API_KEY not set and ~/.pixie/keys.env did not provide it"
    exit 1
  fi

  # Make sure pixie cloud is reachable over tailscale before we waste
  # 22+ min on a doomed experiment.
  if ! curl -sf --max-time 5 -o /dev/null -w "%{http_code}\n" \
       https://pixie.austrianopencloudcommunity.org/ | grep -qE "^(2|3)"; then
    fail "AOCC pixie-cloud unreachable — is tailscale up? Run: sudo tailscale status"
    exit 1
  fi
  ok "AOCC pixie-cloud reachable over tailscale"

  # CHI NodePort: ensure the service exists (idempotent).
  if ! kubectl -n "$CH_NS" get svc ch-perf-nodeport >/dev/null 2>&1; then
    info "creating NodePort ch-perf-nodeport (CH 8123→30123, 9000→30900)"
    cat <<YAML | kubectl apply -f - >/dev/null
apiVersion: v1
kind: Service
metadata:
  name: ch-perf-nodeport
  namespace: $CH_NS
spec:
  type: NodePort
  selector:
    clickhouse.altinity.com/chi: $CHI_NAME
  ports:
  - {name: http, port: 8123, targetPort: 8123, nodePort: 30123}
  - {name: native, port: 9000, targetPort: 9000, nodePort: 30900}
YAML
  fi
  ok "CH NodePort ready (10.0.2.12:30123 http / :30900 native)"

  # Ensure the `pixie` CH user exists with the grants the suite needs.
  # Created via the `default` user (localhost-only on Altinity images, so
  # this only works via kubectl exec, not from the host).
  CH_POD=$(kubectl get pods -n "$CH_NS" -l "clickhouse.altinity.com/chi=$CHI_NAME" -o jsonpath='{.items[0].metadata.name}')
  kubectl exec -n "$CH_NS" "$CH_POD" -- clickhouse-client --user default --multiquery -q "
    CREATE USER IF NOT EXISTS pixie IDENTIFIED WITH plaintext_password BY 'pixie_password' HOST ANY;
    GRANT SHOW DATABASES, SHOW TABLES ON *.* TO pixie;
    GRANT SELECT, INSERT ON forensic_db.* TO pixie;
    GRANT SELECT, INSERT, CREATE TABLE, DROP TABLE ON default.* TO pixie;
  " >/dev/null
  ok "CH user pixie:pixie_password ready"

  # Pre-create default.redis_events — the clickhouse_export.pxl recorder
  # INSERTs Pixie redis_events rows here every exportPeriod (5s), and
  # Kelvin's ClickHouseExportSinkNode does NOT catch CH-client exceptions:
  # any error (table missing, schema mismatch, OOM) crashes Kelvin with
  # SIGSEGV → "context canceled" on the recorder stream → perf_tool aborts.
  # Columns must match the source PxL DataFrame shape EXACTLY; the px_info_
  # column appears only in debug-built PEM (release builds #ifdef it out).
  # If you swap to a release PEM, drop px_info_ from this DDL.
  kubectl exec -n "$CH_NS" "$CH_POD" -- clickhouse-client --user pixie --password pixie_password --multiquery -q "
    CREATE TABLE IF NOT EXISTS default.redis_events (
        time_       DateTime64(9, 'UTC'),
        upid        String,
        remote_addr String,
        remote_port Int64,
        local_addr  String,
        local_port  Int64,
        trace_role  Int64,
        encrypted   UInt8,
        req_cmd     String,
        req_args    String,
        resp        String,
        latency     Int64,
        px_info_    String,
        hostname    String,
        event_time  DateTime64(3, 'UTC')
    ) ENGINE = MergeTree()
      PARTITION BY toYYYYMM(event_time)
      ORDER BY (hostname, event_time);
  " >/dev/null
  ok "default.redis_events ready (sink target for clickhouse_export.pxl)"

  # Build perf_tool (cached after first run).
  if ! bazel build //src/e2e_test/perf_tool:perf_tool //src/pixie_cli:px >/tmp/perf_tool-build.log 2>&1; then
    fail "bazel build perf_tool/px CLI — see /tmp/perf_tool-build.log"
    exit 1
  fi
  PERF_BIN="bazel-bin/src/e2e_test/perf_tool/perf_tool_/perf_tool"
  PX_BIN="bazel-bin/src/pixie_cli/px_/px"
  # perf_tool's pxDeployImpl shells out to `px` via PATH (RunPXCmd → exec.Command("px")).
  # Make sure the freshly-built binary is the one used.
  if [[ ! -x /usr/local/bin/px || /usr/local/bin/px -ot "$PX_BIN" ]]; then
    sudo install -m 0755 "$PX_BIN" /usr/local/bin/px
  fi
  ok "perf_tool built; px CLI at /usr/local/bin/px"

  PERF_OUT_DIR="${PERF_OUT_DIR:-/tmp/perf-out-$(date +%Y%m%d-%H%M%S)}"
  mkdir -p "$PERF_OUT_DIR"
  COMMIT_SHA="$(git -C "$PIXIE_REPO" rev-parse --short HEAD)"
  PERF_TAGS="${PERF_TAGS:-local-ci}"

  info "experiment: sovereign-soc/redis-attack (BURNIN 2m + RUN 20m + deploy ~5m)"
  info "output: $PERF_OUT_DIR"
  info "commit: $COMMIT_SHA  tags: $PERF_TAGS"

  set +e
  env \
    BUILD_WORKSPACE_DIRECTORY="$PIXIE_REPO" \
    LOG_LEVEL="${PERF_LOG_LEVEL:-info}" \
    SOC_CH_HOST="10.0.2.12:30900" \
    SOC_CH_CREDS="pixie:pixie_password" \
    SOC_VIZIER_EXISTING="1" \
    "$PERF_BIN" run \
      --api_key="$PX_API_KEY" \
      --cloud_addr=pixie.austrianopencloudcommunity.org:443 \
      --commit_sha="$COMMIT_SHA" \
      ${PERF_EXPERIMENT_NAME:+--experiment_name="$PERF_EXPERIMENT_NAME"} \
      --suite=sovereign-soc \
      --use_local_cluster \
      --export_backend=parquet-local \
      --parquet_dir="$PERF_OUT_DIR" \
      --container_repo=ghcr.io/k8sstormcenter \
      --max_retries=3 \
      --tags "$PERF_TAGS" \
      2>&1 | tee "$PERF_OUT_DIR/perf_tool.log"
  RC=${PIPESTATUS[0]}
  set -e

  if [[ "$RC" -eq 0 ]]; then
    PARQUET_COUNT=$(find "$PERF_OUT_DIR" -name "*.parquet" 2>/dev/null | wc -l)
    ok "perf-eval-soc-attack passed; $PARQUET_COUNT parquet files in $PERF_OUT_DIR"
  else
    fail "perf-eval-soc-attack exit=$RC; see $PERF_OUT_DIR/perf_tool.log"
  fi
fi

# --- summary ------------------------------------------------------------

echo
phase "summary"
echo "  passed: $PASS"
echo "  failed: $FAIL"
[[ "$FAIL" -eq 0 ]]
