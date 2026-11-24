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

# Cluster test for dx/process_forest: a table cut to its row cap says so. Every row
# carries the number of rows before the cap (rows_total) and whether the cap cut
# them (capped), so a reader of FOREST, PROCESSES (forest(), cap 2000) and EXECS
# (execs(), cap 1000) can tell "this is all" from "this is the first N".
#
# Runs ONE panel function per px run at the panel's default window and scope, and
# compares rows_total with dx_forest__process in ClickHouse.
#
# usage: cap_test.sh <cluster> <ch-pod> [start_time]
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.
# The ClickHouse DSN is read from this panel's vis.json default and never printed.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
CH_POD=${2:?clickhouse pod (namespace clickhouse)}
WINDOW=${3:--15m}
MIN=${WINDOW#-}; MIN=${MIN%m}
DSN=$(jq -r '.variables[] | select(.name=="clickhouse_dsn") | .defaultValue' "$HERE/vis.json")
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
ch() { kubectl -n clickhouse exec "$CH_POD" -- clickhouse-client -q "$1"; }

run_fn() { # $1 = PxL call; prints rows of table "out" as JSON lines
  { cat "$HERE/process_forest.pxl"; printf '\npx.display(%s, "out")\n' "$1"; } > "$TMP/one.pxl"
  if ! timeout 180 px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/out" 2>"$TMP/err"; then
    echo "ERROR px run failed for ${1%%(*}: $(grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/err" | head -1 | sed -E 's/[a-z_]+:[^@ ]*@/<dsn>@/g')" >&2
    touch "$TMP/failed"; exit 2
  fi
  jq -c 'select(._tableName_=="out")' "$TMP/out"
}
guard() { [ -f "$TMP/failed" ] && exit 2; return 0; }

fail=0
pass() { echo "PASS $*"; }
flunk() { echo "FAIL $*"; fail=1; }
near() { local d=$(( $1 > $2 ? $1 - $2 : $2 - $1 )) tol=$(( $2 / 20 > 2 ? $2 / 20 : 2 )); [ "$d" -le "$tol" ]; }

check() { # fn cap where
  local fn=$1 cap=$2 where=$3 rows got total capped truth
  rows=$(run_fn "$fn('$WINDOW', '$DSN', '', '', '', 'pods')"); guard
  got=$(echo -n "$rows" | grep -c .)
  truth=$(ch "SELECT count() FROM forensic_db.dx_forest__process WHERE event_time > now() - INTERVAL $MIN MINUTE AND pod != '' $where")
  echo "info: $fn() rows=$got cap=$cap store=$truth"
  [ "$got" -le "$cap" ] && pass "$fn() keeps its cap: $got <= $cap" || flunk "$fn() over its cap: $got > $cap"
  total=$(echo -n "$rows" | jq -s 'map(.rows_total) | unique | if length == 1 then .[0] else "missing-or-mixed" end')
  capped=$(echo -n "$rows" | jq -s 'map(.capped) | unique | if length == 1 then .[0] else "missing-or-mixed" end')
  if [ "$total" = "missing-or-mixed" ] || [ "$total" = "null" ]; then
    flunk "$fn() rows state the true count (rows_total): $total"
  elif near "$total" "$truth"; then
    pass "$fn() rows_total matches the store: $total ~ $truth"
  else
    flunk "$fn() rows_total vs store: $total vs $truth"
  fi
  if [ "$truth" -gt "$cap" ]; then
    [ "$capped" = "true" ] && pass "$fn() says it was capped" || flunk "$fn() cut to $cap of $truth rows without saying so (capped=$capped)"
  else
    [ "$capped" = "false" ] && pass "$fn() says it was not capped" || flunk "$fn() capped flag wrong: $capped"
  fi
}

check forest 2000 ""
check execs 1000 "AND origin = 'exec'"
exit $fail
