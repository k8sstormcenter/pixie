#!/bin/bash
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

# Cluster test for dx/shadow_trace "KUBERNETES API ON THIS POD": a request belongs
# to a traced pod only when its path addresses that pod: the apiserver's
# /namespaces/<ns>/pods/<name>[/...] or the kubelet's
# /{exec,attach,portForward,containerLogs,run}/<ns>/<name>[/...]. A pod name that
# merely occurs in the path (its namespace has the same name, a PVC is named after
# it, an exec command mentions it) is not that pod.
#
# Runs ONE panel function (api_on_pod) once, at the panel's default windows.
#
# A positive control comes first: one read-only GET of a pod with an open trace,
# which must then show up as that pod's row.
#
# usage: api_test.sh <cluster> <ch-pod> [start_time] [live_window]
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.
# The ClickHouse DSN is read from this panel's vis.json default and never printed.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
CH_POD=${2:?clickhouse pod (namespace clickhouse)}
START=${3:--30m}
LIVE=${4:--30m}
DSN=$(jq -r '.variables[] | select(.name=="clickhouse_dsn") | .defaultValue' "$HERE/vis.json")
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

# Positive control: a pod with an open trace, addressed once through the apiserver.
CTRL=$(kubectl -n clickhouse exec "$CH_POD" -- clickhouse-client -q "SELECT pod FROM forensic_db.dx_shadow_trace WHERE closed_at = 0 AND pod LIKE '%/%' ORDER BY updated_at DESC LIMIT 1")
if [ -z "$CTRL" ]; then echo "ERROR no open trace to use as a control" >&2; exit 2; fi
kubectl -n "${CTRL%%/*}" get pod "${CTRL#*/}" -o name >/dev/null 2>&1 || { echo "ERROR control pod $CTRL not found" >&2; exit 2; }
echo "info: control GET on $CTRL at $(date -u +%T)"
sleep 20  # the sensor's rows reach the tables within seconds; leave room

{ cat "$HERE/shadow_trace.pxl"; printf '\npx.display(api_on_pod(%s), "out")\n' "'$START', '$DSN', '', '$LIVE'"; } > "$TMP/one.pxl"
if ! timeout 180 px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/out" 2>"$TMP/err"; then
  echo "ERROR px run failed: $(grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/err" | head -1 | sed -E 's/[a-z_]+:[^@ ]*@/<dsn>@/g')" >&2
  exit 2
fi
jq -r 'select(._tableName_=="out") | [.pod, .req_path] | @tsv' "$TMP/out" > "$TMP/rows"

python3 -I - "$TMP/rows" "$CTRL" <<'EOF'
import re, sys
api = re.compile(r'^/api/v1/namespaces/([^/]+)/pods/([^/?]+)')
kl = re.compile(r'^/(?:exec|attach|portForward|portforward|containerLogs|run)/([^/]+)/([^/?]+)')
n = bad = 0
examples = []
for line in open(sys.argv[1]):
    pod, path = line.rstrip('\n').split('\t', 1)
    n += 1
    m = api.match(path) or kl.match(path)
    if not m or f'{m.group(1)}/{m.group(2)}' != pod:
        bad += 1
        if len(examples) < 3:
            examples.append(f'{pod}  <-  {path[:90]}')
print(f'info: rows={n}')
for e in examples:
    print(f'info: not this pod: {e}')
ctrl = sys.argv[2]
seen = sum(1 for line in open(sys.argv[1]) if line.split('\t', 1)[0] == ctrl)
ok1 = bad == 0
ok2 = seen > 0
print(f'{"PASS" if ok1 else "FAIL"} rows whose path does not address their pod: got={bad} want=0')
print(f'{"PASS" if ok2 else "FAIL"} control pod {ctrl} has its own row(s): got={seen} want>0')
sys.exit(0 if ok1 and ok2 else 1)
EOF
