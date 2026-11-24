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

# Cluster test for dx/tls_handshakes "Negotiated parameters by endpoint": one row
# per endpoint, role, service port and negotiated parameters. A client's local
# port is ephemeral (a new one per connection), so keying client rows by it gives
# one row per connection instead of one per thing negotiated.
#
# Runs ONE panel function (tls_summary) per px run at the panel's default window.
#
# usage: summary_test.sh <cluster> [start_time]
#   env: PX_CLOUD_ADDR must point at the cloud the cluster is registered with.

set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CLUSTER=${1:?cluster}
WINDOW=${2:-$(jq -r '.variables[] | select(.name=="start_time") | .defaultValue' "$HERE/vis.json")}
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

{ cat "$HERE/tls_handshakes.pxl"; printf '\npx.display(tls_summary(%s), "out")\n' "'$WINDOW', ''"; } > "$TMP/one.pxl"
if ! timeout 180 px run -c "$CLUSTER" -f "$TMP/one.pxl" -o json >"$TMP/out" 2>"$TMP/err"; then
  echo "ERROR px run failed: $(grep -vE '^\s*$|^\*|ENV VARS|PX_CLOUD' "$TMP/err" | head -1)" >&2
  exit 2
fi
jq -c 'select(._tableName_=="out")' "$TMP/out" > "$TMP/rows"

fail=0
check() { # name got want
  if [ "$2" = "$3" ]; then echo "PASS $1: got=$2 want=$3"; else echo "FAIL $1: got=$2 want=$3"; fail=1; fi
}
rows=$(grep -c . "$TMP/rows")
clients=$(jq -s 'map(select(.role=="client")) | length' "$TMP/rows")
echo "info: window=$WINDOW rows=$rows client_rows=$clients"
if [ "$rows" = 0 ]; then echo "ERROR no TLS handshakes in the window; nothing to judge" >&2; exit 2; fi

# The port a row is keyed by: the panel's "port" column, or local_port before the fix.
key='(.port // .local_port)'
check "client rows keyed by an ephemeral port (32768-65535)" \
  "$(jq -s "map(select(.role==\"client\" and ($key >= 32768))) | length" "$TMP/rows")" "0"
check "rows unique on endpoint, role, port and parameters" \
  "$(jq -s "map([.endpoint, .role, $key, .version, .cipher_suite, .group]) | unique | length" "$TMP/rows")" "$rows"
check "every row names its service port" \
  "$(jq -s "map(select(($key // 0) <= 0)) | length" "$TMP/rows")" "0"
exit $fail
