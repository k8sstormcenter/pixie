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

# Cluster test for dx/rogue_state: a firing artefact keeps its node. Host artefacts
# carry no pod, so without the node every node's host artefacts fold into one row and
# a reader cannot tell which node is rogue.
#
# Runs ONE panel function per px run, sequentially, at the panel's default window,
# and compares each result with kubescape_rogue_lifecycle in ClickHouse.
#
# usage: node_test.sh <cluster> <ch-pod> [start_time]
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.
# The ClickHouse DSN is read from this panel's vis.json default and never printed.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
CH_POD=${2:?clickhouse pod (namespace clickhouse)}
WINDOW=${3:--6h}
HOURS=${WINDOW#-}; HOURS=${HOURS%h}

DSN=$(jq -r '.variables[] | select(.name=="clickhouse_dsn") | .defaultValue' "$HERE/vis.json")
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

ch() { kubectl -n clickhouse exec "$CH_POD" -- clickhouse-client -q "$1"; }

run_fn() { # $1 = PxL call expression; prints rows as JSON lines of table "out"
  { cat "$HERE/rogue_state.pxl"; printf '\npx.display(%s, "out")\n' "$1"; } > "$TMP/one.pxl"
  if ! timeout 180 px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/out" 2>"$TMP/err"; then
    # A failed run must not read as "0 rows": stop instead of scoring it.
    echo "ERROR px run failed for $1: $(grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/err" | head -1)" >&2
    touch "$TMP/failed"; exit 2
  fi
  jq -c 'select(._tableName_=="out")' "$TMP/out"
}

fail=0
guard() { [ -f "$TMP/failed" ] && exit 2; return 0; }
check() { # name got want
  if [ "$2" = "$3" ]; then echo "PASS $1: got=$2 want=$3"; else echo "FAIL $1: got=$2 want=$3"; fail=1; fi
}

WHERE="phase = 'Firing' AND event_time > now() - INTERVAL $HOURS HOUR"
ARGS="'$WINDOW', '$DSN'"

# Groups ClickHouse holds: one per (namespace, pod, container, node).
want_groups=$(ch "SELECT count() FROM (SELECT DISTINCT namespace, pod_name, container_name, node FROM forensic_db.kubescape_rogue_lifecycle WHERE $WHERE)")
want_hostnodes=$(ch "SELECT uniqExact(node) FROM forensic_db.kubescape_rogue_lifecycle WHERE $WHERE AND container_name = 'host'")
want_firing=$(ch "SELECT count() FROM forensic_db.kubescape_rogue_lifecycle WHERE $WHERE")

# 1. rogue() ("Currently firing"): one row per group, the node named on each row,
#    and the per-row counts still add up to every firing artefact.
rows=$(run_fn "rogue($ARGS)"); guard
check "rogue() rows == (ns,pod,container,node) groups" "$(echo -n "$rows" | grep -c .)" "$want_groups"
check "rogue() host rows == nodes with a firing host artefact" \
  "$(echo -n "$rows" | jq -s 'map(select(.container_name=="host")) | length')" "$want_hostnodes"
check "rogue() rows without a node" "$(echo -n "$rows" | jq -s 'map(select((.node // "") == "")) | length')" "0"
check "rogue() sum(firing) == firing artefacts" "$(echo -n "$rows" | jq -s 'map(.firing) | add // 0')" "$want_firing"

# 2. firing() ("Ungoverned containers"): every firing artefact listed with its node.
rows=$(run_fn "firing($ARGS)"); guard
check "firing() rows == firing artefacts" "$(echo -n "$rows" | grep -c .)" "$want_firing"
check "firing() rows without a node" "$(echo -n "$rows" | jq -s 'map(select((.node // "") == "")) | length')" "0"

exit $fail
