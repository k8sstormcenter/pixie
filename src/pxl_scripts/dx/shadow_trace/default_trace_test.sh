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

# Cluster test for dx/shadow_trace opened at its defaults (no trace selected): the
# per-trace widgets show the most active open trace instead of nothing, say which trace
# that is, and a selected trace still shows exactly its own rows.
#
# Runs ONE panel function per px run, sequentially, at the panel's default window,
# and compares each with the dx_shadow__* views in ClickHouse.
#
# usage: default_trace_test.sh <cluster> <ch-pod> [start_time]
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.
# The ClickHouse DSN is read from this panel's vis.json default and never printed.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
CH_POD=${2:?clickhouse pod (namespace clickhouse)}
WINDOW=${3:--30m}
MIN=${WINDOW#-}; MIN=${MIN%m}
DSN=$(jq -r '.variables[] | select(.name=="clickhouse_dsn") | .defaultValue' "$HERE/vis.json")
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
ch() { kubectl -n clickhouse exec "$CH_POD" -- clickhouse-client -q "$1"; }

calm() { # ClickHouse below 1 core (max 120 s); stop if the vizier is unhealthy
  local i=0 ph c
  while :; do
    ph=$(kubectl -n pl get vizier -o jsonpath='{.items[0].status.vizierPhase}')
    [ "$ph" = Healthy ] || { echo "ERROR vizier $ph, stopping" >&2; exit 2; }
    c=$(kubectl top pod -n clickhouse --no-headers 2>/dev/null | awk -v p="$CH_POD" '$1==p{gsub("m","",$2); print $2}')
    [ "${c:-0}" -lt 1000 ] && return 0
    i=$((i + 1)); [ $i -gt 12 ] && { echo "ERROR ClickHouse stays above 1 core, stopping" >&2; exit 2; }
    sleep 10
  done
}

run_fn() { # $1 = PxL call; prints rows of table "out" as JSON lines
  calm
  { cat "$HERE/shadow_trace.pxl"; printf '\npx.display(%s, "out")\n' "$1"; } > "$TMP/one.pxl"
  if ! timeout 180 px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/out" 2>"$TMP/err"; then
    echo "ERROR px run failed for ${1%%(*}: $(grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/err" | head -1 | sed -E 's/[a-z_]+:[^@ ]*@/<dsn>@/g')" >&2
    touch "$TMP/failed"; exit 2
  fi
  jq -c 'select(._tableName_=="out")' "$TMP/out"
}
guard() { [ -f "$TMP/failed" ] && exit 2; return 0; }

fail=0
near() { # name got want: within 2% or 2 rows (rows keep arriving between the reads)
  local d=$(( $2 > $3 ? $2 - $3 : $3 - $2 )) tol=$(( $3 / 50 > 2 ? $3 / 50 : 2 ))
  if [ "$d" -le "$tol" ]; then echo "PASS $1: got=$2 want=$3"; else echo "FAIL $1: got=$2 want=$3"; fail=1; fi
}

# The trace the panel should fall back to: the open trace with the most activity in
# the last five minutes. (The newest open trace may be quiet: on edge4 it had no rows.)
LATEST="SELECT * FROM forensic_db.dx_shadow__trace WHERE event_time > now() - INTERVAL $MIN MINUTE ORDER BY updated_at DESC LIMIT 1 BY shadow_id"
ACTIVE="SELECT shadow_id FROM forensic_db.dx_shadow__activity WHERE event_time > now() - INTERVAL 5 MINUTE AND shadow_id IN (SELECT shadow_id FROM ($LATEST) WHERE closed_at = 0) GROUP BY shadow_id ORDER BY count() DESC"
NEWEST=$(ch "$ACTIVE LIMIT 1")
OTHER=$(ch "$ACTIVE LIMIT 1 OFFSET 1")
[ -n "$NEWEST" ] || { echo "ERROR no active open trace in the window" >&2; exit 2; }
echo "info: most active open trace ${NEWEST:0:8}, check trace ${OTHER:0:8}"

ARGS="'$WINDOW', '$DSN', ''"
for spec in dc_snoop:dc_snoop conn:conn_stats dns:dns_events http:http_events redis:redis_events \
            pgsql:pgsql_events amqp:amqp_events http2:http2_messages mysql:mysql_events cql:cql_events \
            mongodb:mongodb_events kafka:kafka_events mux:mux_events tls:tls_events creds:creds_change \
            nsswitch:nsswitch; do
  fn=${spec%%:*}; view=${spec#*:}
  rows=$(run_fn "$fn($ARGS)"); guard
  got=$(echo -n "$rows" | grep -c .)
  cap=1000; [ "$fn" = nsswitch ] && cap=1000000
  want=$(ch "SELECT least($cap, count()) FROM forensic_db.dx_shadow__$view WHERE shadow_id = '$NEWEST' AND event_time > now() - INTERVAL $MIN MINUTE")
  near "$fn() with no trace selected shows the most active open trace" "$got" "$want"
  if [ "$got" -gt 0 ]; then
    other=$(echo -n "$rows" | jq -s --arg t "$NEWEST" 'map(select(.trace != $t)) | length')
    [ "$other" = 0 ] && echo "PASS $fn() names the trace it shows" || { echo "FAIL $fn() rows not naming $NEWEST: $other"; fail=1; }
  fi
done

# stacks() groups the rows; it must not be empty when the trace has stack rows.
rows=$(run_fn "stacks($ARGS)"); guard
got=$(echo -n "$rows" | grep -c .)
have=$(ch "SELECT count() FROM forensic_db.dx_shadow__stack_trace WHERE shadow_id = '$NEWEST' AND event_time > now() - INTERVAL $MIN MINUTE")
if [ "$have" -gt 0 ] && [ "$got" -eq 0 ]; then echo "FAIL stacks() empty while the trace holds $have stack rows"; fail=1; else echo "PASS stacks(): got=$got rows for $have stack rows"; fi

# A selected trace is unchanged: dc_snoop of the second open trace, exactly its rows.
if [ -n "$OTHER" ]; then
  got=$(run_fn "dc_snoop('$WINDOW', '$DSN', '$OTHER')" | grep -c .); guard
  want=$(ch "SELECT least(1000, count()) FROM forensic_db.dx_shadow__dc_snoop WHERE shadow_id = '$OTHER' AND event_time > now() - INTERVAL $MIN MINUTE")
  near "dc_snoop() of a selected trace (${OTHER:0:8})" "$got" "$want"
fi
exit $fail
