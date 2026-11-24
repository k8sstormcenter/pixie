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

# Cluster test for dx/proof_coverage: the GAPS widget and the per-class gap count
# must count every roll-up suspicion that is not proven, as ClickHouse holds them.
#
# Runs ONE panel function per px run, sequentially, at the panel's default window,
# and compares each result with a direct count in dx_src__kpi_proof.
#
# usage: gaps_test.sh <cluster> <ch-pod> [start_time]
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.
# The ClickHouse DSN is read from this panel's vis.json default and never printed.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
CH_POD=${2:?clickhouse pod (namespace clickhouse)}
WINDOW=${3:--24h}
HOURS=${WINDOW#-}; HOURS=${HOURS%h}

DSN=$(jq -r '.variables[] | select(.name=="clickhouse_dsn") | .defaultValue' "$HERE/vis.json")
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

ch() { kubectl -n clickhouse exec "$CH_POD" -- clickhouse-client -q "$1"; }

run_fn() { # $1 = PxL call expression; prints rows as JSON lines of table "out"
  { cat "$HERE/proof_coverage.pxl"; printf '\npx.display(%s, "out")\n' "$1"; } > "$TMP/one.pxl"
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

WHERE="part = '' AND event_time > now() - INTERVAL $HOURS HOUR"
ARGS="'$WINDOW', '$DSN', ''"

# 1. gaps(): one row per roll-up suspicion that is not proven malicious.
got=$(run_fn "gaps($ARGS)" | grep -c .); guard
want=$(ch "SELECT count() FROM forensic_db.dx_src__kpi_proof WHERE $WHERE AND verdict != 'malicious'")
check "gaps() rows == unproven roll-ups" "$got" "$want"

# 2. by_class(): the per-class gap counts add up to the same number.
got=$(run_fn "by_class($ARGS)" | jq -s 'map(.gaps) | add // 0'); guard
check "by_class() sum(gaps) == unproven roll-ups" "$got" "$want"

# 3. coverage(): every roll-up row is listed, and the unproven column is set on exactly
#    the unproven ones.
rows=$(run_fn "coverage($ARGS)"); guard
got=$(echo -n "$rows" | grep -c .)
all=$(ch "SELECT count() FROM forensic_db.dx_src__kpi_proof WHERE $WHERE")
check "coverage() rows == roll-ups" "$got" "$all"
got=$(echo -n "$rows" | jq -s 'map(select(.unproven != "")) | length')
check "coverage() unproven set == unproven roll-ups" "$got" "$want"

exit $fail
