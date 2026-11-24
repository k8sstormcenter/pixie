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

# Cluster test for dx/evidence_graph opened at its defaults: every function the
# panel runs must finish within the timeout, and the orders table, which is what
# a reader picks from, must not be empty.
#
# Runs the panel's functions ONE per px run, sequentially, each with exactly the
# arguments vis.json gives it at the defaults. Between runs it waits until
# ClickHouse is below one core, and stops if the vizier is not Healthy.
#
# usage: window_test.sh <cluster> <ch-pod> [timeout_s]
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.
# The ClickHouse DSN is read from this panel's vis.json default and never printed.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
CH_POD=${2:?clickhouse pod (namespace clickhouse)}
LIMIT=${3:-180}
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

echo "info: start_time default $(jq -r '.variables[] | select(.name=="start_time") | .defaultValue' "$HERE/vis.json")"
# one line per global function: <output>\t<call expression with default arguments>
# (calls.jq; a function shown by two widgets with the same arguments runs once)
jq -r -f "$HERE/calls.jq" "$HERE/vis.json" | sort -u -t$'\t' -k2,2 > "$TMP/calls"

calm() { # wait for ClickHouse below 1 core (max 120 s); stop if the vizier is unhealthy
  local i=0 ph ch
  while :; do
    ph=$(kubectl -n pl get vizier -o jsonpath='{.items[0].status.vizierPhase}')
    [ "$ph" = Healthy ] || { echo "ERROR vizier $ph, stopping" >&2; exit 2; }
    ch=$(kubectl top pod -n clickhouse --no-headers 2>/dev/null | awk -v p="$CH_POD" '$1==p{gsub("m","",$2); print $2}')
    [ "${ch:-0}" -lt 1000 ] && return 0
    i=$((i + 1)); [ $i -gt 12 ] && { echo "ERROR ClickHouse stays above 1 core, stopping" >&2; exit 2; }
    sleep 10
  done
}

fail=0; total=0; orders=-1
while IFS=$'\t' read -r out call; do
  calm
  { cat "$HERE/evidence_graph.pxl"; printf '\npx.display(%s, "out")\n' "$call"; } > "$TMP/one.pxl"
  t0=$(date +%s)
  timeout "$LIMIT" px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/o" 2>"$TMP/e"; rc=$?
  secs=$(( $(date +%s) - t0 )); total=$((total + secs))
  rows=$(jq -c 'select(._tableName_=="out")' "$TMP/o" 2>/dev/null | grep -c .)
  name=${call%%(*}
  if [ $rc -ne 0 ]; then
    why=$([ $rc -eq 124 ] && echo "timeout ${LIMIT}s" || grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/e" | head -1 | sed -E 's/[a-z_]+:[^@ ]*@/<dsn>@/g' | cut -c1-120)
    echo "FAIL $out=$name rc=$rc secs=$secs ($why)"; fail=1
  else
    echo "ok   $out=$name secs=$secs rows=$rows"
  fi
  [ "$out" = g_orders ] && orders=$([ $rc -eq 0 ] && echo "$rows" || echo -1)
done < "$TMP/calls"

echo "info: total ${total}s over $(grep -c . "$TMP/calls") functions"
[ $fail -eq 0 ] && echo "PASS every function finished within ${LIMIT}s" || echo "FAIL not every function finished within ${LIMIT}s"
# The cloud UI runs all of them at once; the panel finishes when the last does, and
# they share the same ClickHouse. Their summed time is what the panel has to do.
if [ "$total" -le "$LIMIT" ]; then echo "PASS panel (sum of its functions) within ${LIMIT}s: ${total}s"; else echo "FAIL panel (sum of its functions) within ${LIMIT}s: ${total}s"; fail=1; fi
if [ "$orders" -gt 0 ]; then echo "PASS orders table non-empty: rows=$orders"; else echo "FAIL orders table non-empty: rows=$orders"; fail=1; fi
exit $fail
