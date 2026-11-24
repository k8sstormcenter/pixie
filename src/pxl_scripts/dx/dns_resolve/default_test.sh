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

# Cluster test for dx/dns_resolve. Opened without an order (its default), the panel
# must show the DNS resolution of the window, one row per edge, not nothing; opened
# from an order, it must still show exactly that order's resolution.
#
# Runs ONE panel function (dns_graph) per px run, sequentially, and compares each
# result with dx_dns_resolve / dx_orders_win in ClickHouse.
#
# usage: default_test.sh <cluster> <ch-pod> [start_time]
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

run_fn() { # $1 = PxL call expression; prints rows as JSON lines of table "out"
  { cat "$HERE/dns_resolve.pxl"; printf '\npx.display(%s, "out")\n' "$1"; } > "$TMP/one.pxl"
  if ! timeout 180 px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/out" 2>"$TMP/err"; then
    echo "ERROR px run failed for ${1%%(*}: $(grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/err" | head -1 | sed -E 's/[a-z_]+:[^@ ]*@/<dsn>@/g')" >&2
    touch "$TMP/failed"; exit 2
  fi
  jq -c 'select(._tableName_=="out")' "$TMP/out"
}
guard() { [ -f "$TMP/failed" ] && exit 2; return 0; }

fail=0
near() { # name got want: equal within 2% or 2 rows (rows keep arriving between the two reads)
  local d=$(( $2 > $3 ? $2 - $3 : $3 - $2 )) tol=$(( $3 / 50 > 2 ? $3 / 50 : 2 ))
  if [ "$d" -le "$tol" ]; then echo "PASS $1: got=$2 want=$3"; else echo "FAIL $1: got=$2 want=$3"; fail=1; fi
}

# 1. Default (no order): one row per edge of the window, capped at 2000.
got=$(run_fn "dns_graph('$WINDOW', '$DSN', '')" | grep -c .); guard
want=$(ch "SELECT least(2000, uniqExact(from_node, to_node, edge_label, kind)) FROM forensic_db.dx_dns_resolve WHERE event_time > now() - INTERVAL $MIN MINUTE")
near "dns_graph(no order) rows == edges in the window" "$got" "$want"

# 2. From an order: that order's rows, as before. A closed order window inside the
#    test window, with resolution in it.
oid=$(ch "SELECT w.order_id FROM (SELECT order_id, lo, hi FROM forensic_db.dx_orders_win WHERE event_time > now() - INTERVAL $MIN MINUTE AND hi < toUnixTimestamp64Nano(now64(9) - INTERVAL 2 MINUTE) AND lo > toUnixTimestamp64Nano(now64(9) - INTERVAL $MIN MINUTE)) w CROSS JOIN (SELECT row_time FROM forensic_db.dx_dns_resolve WHERE event_time > now() - INTERVAL $MIN MINUTE) d WHERE d.row_time >= w.lo AND d.row_time <= w.hi GROUP BY w.order_id, w.hi ORDER BY w.hi DESC LIMIT 1")
if [ -z "$oid" ]; then
  echo "SKIP dns_graph(order): no closed order window with DNS rows in the last $MIN min"
else
  got=$(run_fn "dns_graph('$WINDOW', '$DSN', '$oid')" | grep -c .); guard
  want=$(ch "SELECT least(2000, count()) FROM forensic_db.dx_dns_resolve d, (SELECT lo, hi FROM forensic_db.dx_orders_win WHERE order_id = '$oid') w WHERE d.event_time > now() - INTERVAL $MIN MINUTE AND d.row_time >= w.lo AND d.row_time <= w.hi")
  near "dns_graph(order ${oid:0:8}) rows == that order's rows" "$got" "$want"
fi
exit $fail
