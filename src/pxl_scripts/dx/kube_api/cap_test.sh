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

# Cluster test for dx/kube_api: the Requests table, cut to max_num_records, says so.
# Every row carries the number of requests before the cut (requests_total) and
# whether the cut applied (capped). The reference is the panel's own summary, which
# counts the same requests without a cut.
#
# Runs ONE panel function per px run (kube_api, then kube_api_summary) at the
# panel's defaults.
#
# usage: cap_test.sh <cluster>
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
d() { jq -r --arg n "$1" '.variables[] | select(.name==$n) | .defaultValue' "$HERE/vis.json"; }
START=$(d start_time); API=$(d apiserver_port); KL=$(d kubelet_port); CAP=$(d max_num_records)

run_fn() {
  { cat "$HERE/kube_api.pxl"; printf '\npx.display(%s, "out")\n' "$1"; } > "$TMP/one.pxl"
  if ! timeout 180 px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/out" 2>"$TMP/err"; then
    echo "ERROR px run failed for ${1%%(*}: $(grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/err" | head -1)" >&2
    touch "$TMP/failed"; exit 2
  fi
  jq -c 'select(._tableName_=="out")' "$TMP/out"
}
guard() { [ -f "$TMP/failed" ] && exit 2; return 0; }

fail=0
rows=$(run_fn "kube_api('$START', $API, $KL, '', '', $CAP)"); guard
truth=$(run_fn "kube_api_summary('$START', $API, $KL, '', '')" | jq -s 'map(.requests) | add // 0'); guard
got=$(echo -n "$rows" | grep -c .)
echo "info: kube_api() rows=$got cap=$CAP summary requests=$truth (window $START)"
total=$(echo -n "$rows" | jq -s 'map(.requests_total) | unique | if length == 1 then .[0] else "missing-or-mixed" end')
capped=$(echo -n "$rows" | jq -s 'map(.capped) | unique | if length == 1 then .[0] else "missing-or-mixed" end')
if [ "$total" = "missing-or-mixed" ] || [ "$total" = "null" ]; then
  echo "FAIL rows state the true count (requests_total): $total"; fail=1
else
  dlt=$(( total > truth ? total - truth : truth - total )); tol=$(( truth / 10 > 5 ? truth / 10 : 5 ))
  # the summary runs a few seconds later over a sliding window
  [ "$dlt" -le "$tol" ] && echo "PASS requests_total matches the summary: $total ~ $truth" || { echo "FAIL requests_total vs summary: $total vs $truth"; fail=1; }
fi
if [ "$truth" -gt "$CAP" ]; then
  [ "$capped" = "true" ] && echo "PASS the table says it was capped" || { echo "FAIL cut to $got of ~$truth requests without saying so (capped=$capped)"; fail=1; }
else
  [ "$capped" = "false" ] && echo "PASS the table says it was not capped" || { echo "FAIL capped flag wrong: $capped"; fail=1; }
fi
exit $fail
