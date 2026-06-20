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

# deploy_ae.sh — deploy adaptive-export STANDALONE (no log4j/dx/kubescape) on the
# rig and put it straight into single-shot load-test mode. One-shot orchestration
# from this VM (labctl cp + a single ssh) — no long-held session.
#
# Usage: PG=<id> [AE_IMG=...] ./deploy_ae.sh
set -euo pipefail
PG="${PG:?set PG=<playground id>}"
AE_IMG="${AE_IMG:-ghcr.io/k8sstormcenter/vizier-adaptive_export_image:0.14.19-aeprod-clean3}"
CH_DSN="${CH_DSN:-ingest_writer:changeme-ingest@clickhouse-forensic-soc-db.clickhouse.svc.cluster.local:9000/forensic_db}"
KEYS_ENV="${KEYS_ENV:-$HOME/.pixie/keys.prod.env}"
MANIFEST="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/../entlein-dx/deploy/adaptive-export.yaml"
[[ -f "$MANIFEST" ]] || MANIFEST="$HOME/entlein-dx/deploy/adaptive-export.yaml"
[[ -f "$MANIFEST" ]] || { echo "no AE manifest at $MANIFEST"; exit 1; }
[[ -f "$KEYS_ENV" ]] || { echo "no keys at $KEYS_ENV"; exit 1; }

# Render the manifest with the chosen image, ship manifest + keys to dev-machine.
sed "s#AE_IMAGE_PLACEHOLDER#${AE_IMG}#g" "$MANIFEST" > /tmp/ae-rendered.yaml
labctl cp -m dev-machine /tmp/ae-rendered.yaml "$PG:/tmp/ae-rendered.yaml" </dev/null
labctl cp -m dev-machine "$KEYS_ENV" "$PG:/tmp/keys.env" </dev/null

OUT=$(labctl ssh "$PG" -m dev-machine -- bash -s <<EOF 2>&1 || true
set -uo pipefail
. /tmp/keys.env
[[ -n "\${PX_API_KEY:-}" ]] || { echo "NO_PX_API_KEY"; exit 1; }
kubectl get ns pl >/dev/null 2>&1 || kubectl create ns pl
# secret (api-key + clickhouse-dsn) + SA — never echo the key (stdin yaml).
kubectl -n pl create secret generic pl-adaptive-export-secrets \
  --from-literal=pixie-api-key="\$PX_API_KEY" \
  --from-literal=clickhouse-dsn='${CH_DSN}' \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n pl get sa pl-adaptive-export-service-account >/dev/null 2>&1 || kubectl -n pl create sa pl-adaptive-export-service-account
# PL_CLOUD_ADDR :443 fix (AE crashloops / 0 writes without it).
CUR=\$(kubectl -n pl get cm pl-cloud-config -o jsonpath='{.data.PL_CLOUD_ADDR}' 2>/dev/null || true)
if [[ -n "\$CUR" && "\$CUR" != *:* ]]; then
  kubectl -n pl patch cm pl-cloud-config --type merge -p "{\"data\":{\"PL_CLOUD_ADDR\":\"\${CUR}:443\"}}" >/dev/null
  echo "PL_CLOUD_ADDR patched -> \${CUR}:443"
fi
kubectl apply -f /tmp/ae-rendered.yaml >/dev/null
# Single-shot load-test mode (AFTER=5 < 30s refresh → one pull on any image;
# PUSH_REFRESH=-1 is the explicit equivalent on a rebuilt image).
kubectl -n pl set env ds/adaptive-export \
  ADAPTIVE_SKIP_APPLY=false ADAPTIVE_PUSH_PIXIE_ROWS=true \
  ADAPTIVE_PUSH_REFRESH_SEC=-1 ADAPTIVE_WINDOW_BEFORE_SEC=120 ADAPTIVE_WINDOW_AFTER_SEC=5 \
  EXPORT_MODE=auto >/dev/null
kubectl -n pl rollout status ds/adaptive-export --timeout=180s 2>&1 | tail -1 || echo "rollout slow"
echo "AE_PODS=\$(kubectl -n pl get pods -l name=adaptive-export --no-headers 2>/dev/null | grep -c Running)"
echo __AE_DEPLOYED__
EOF
)
rm -f /tmp/keys.env 2>/dev/null || true
labctl ssh "$PG" -m dev-machine -- "rm -f /tmp/keys.env" </dev/null >/dev/null 2>&1 || true
echo "$OUT" | grep -q __AE_DEPLOYED__ || { echo "[deploy_ae] FAIL:"; echo "$OUT" | tail -20; exit 1; }
echo "[deploy_ae] OK ($(echo "$OUT" | grep -oE 'AE_PODS=[0-9]+'); image=$AE_IMG)"
