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

# Shared helpers for the security_profile harness. Sourced by run.sh.

set -euo pipefail

SECPROF_NS="${SECPROF_NS:-pl}"          # PEM lives here
SECPROF_PROBE_NS="${SECPROF_PROBE_NS:-default}"
SECPROF_PEM_DS="${SECPROF_PEM_DS:-vizier-pem}"
SECPROF_PEM_STANDALONE_DS="${SECPROF_PEM_STANDALONE_DS:-standalone-pem}"
SECPROF_PEM_STANDALONE_NS="${SECPROF_PEM_STANDALONE_NS:-honey}"
SECPROF_PEM_DIRECT_PORT="${SECPROF_PEM_DIRECT_PORT:-12345}"

kc() { sudo k3s kubectl "$@"; }

# Apply an env-var profile to the PEM DaemonSet — accepts a .env file
# with `K=V` lines, comments ignored. Restarts the rollout and waits
# for it to settle.
apply_pem_env() {
  local env_file="$1" ds="${2:-$SECPROF_PEM_DS}" ns="${3:-$SECPROF_NS}"
  local -a args=()
  while IFS='=' read -r k v; do
    [[ -z "$k" || "${k:0:1}" == "#" ]] && continue
    v="${v%$'\r'}"  # strip stray CR
    args+=("$k=$v")
  done < "$env_file"
  if [[ ${#args[@]} -gt 0 ]]; then
    kc -n "$ns" set env "daemonset/$ds" "${args[@]}" >&2
  fi
  kc -n "$ns" rollout restart "daemonset/$ds" >&2
  kc -n "$ns" rollout status "daemonset/$ds" --timeout=180s >&2
}

# Strip the profile back to defaults (clears the env vars we manage).
clear_pem_env() {
  local ds="${1:-$SECPROF_PEM_DS}" ns="${2:-$SECPROF_NS}"
  kc -n "$ns" set env "daemonset/$ds" \
    PX_STIRLING_ENABLE_DNS_TRACING- \
    PL_TABLE_STORE_DATA_LIMIT_MB- \
    PL_TABLE_STORE_HTTP_EVENTS_PERCENT- \
    STIRLING_SOCKET_TRACER_TARGET_CONTROL_BW_PERCPU- \
    STIRLING_SOCKET_TRACER_TARGET_DATA_BW_PERCPU- \
    STIRLING_SOCKET_TRACER_MAX_TOTAL_DATA_BW- \
    PL_DATASTREAM_BUFFER_SIZE- >&2
  kc -n "$ns" rollout restart "daemonset/$ds" >&2
  kc -n "$ns" rollout status "daemonset/$ds" --timeout=180s >&2
}

# Wait until the PEM logs report 'Stirling successfully initialized.'
# (BPF programs attached, ready to capture). Bounded by `timeout_s`.
wait_pem_ready() {
  local ds="${1:-$SECPROF_PEM_DS}" ns="${2:-$SECPROF_NS}" timeout_s="${3:-180}"
  local pod
  pod=$(kc -n "$ns" get pod -l "name=$ds" -o jsonpath='{.items[0].metadata.name}')
  for ((i=0; i<timeout_s; i+=3)); do
    if kc -n "$ns" logs "$pod" 2>/dev/null | grep -q 'Stirling successfully initialized'; then
      return 0
    fi
    sleep 3
  done
  echo "wait_pem_ready: timed out after ${timeout_s}s" >&2
  return 1
}

# Compute coverage stats from sent + seen CSVs. Sent column 2 is a
# bare FQDN (with trailing dot); seen column 2 is the dns_events
# req_body which is a JSON envelope {"queries":[{"name":...}]}.
# We strip the trailing dot from sent and extract every "name":"…"
# value from seen, then compute set intersection.
coverage_stats() {
  local sent="$1" seen="$2"
  python3 - "$sent" "$seen" <<'PY'
import csv
import json
import sys

sent_path, seen_path = sys.argv[1], sys.argv[2]

def normalize(name: str) -> str:
    return name.rstrip(".").lower()

sent_names = set()
with open(sent_path) as f:
    r = csv.reader(f)
    next(r, None)
    for row in r:
        if len(row) >= 2:
            sent_names.add(normalize(row[1]))

seen_names = set()
seen_unresolved = 0
with open(seen_path) as f:
    r = csv.reader(f)
    next(r, None)
    for row in r:
        if len(row) >= 2:
            try:
                payload = json.loads(row[1])
                for q in payload.get("queries", []):
                    if "name" in q:
                        seen_names.add(normalize(q["name"]))
            except (json.JSONDecodeError, TypeError):
                seen_names.add(normalize(row[1]))
        if len(row) >= 3 and (row[2] == "" or row[2] == "00000000-0000-0000-0000-000000000000"):
            seen_unresolved += 1

captured = sent_names & seen_names
print(f"sent_unique={len(sent_names)}")
print(f"seen_unique={len(seen_names)}")
print(f"captured_unique={len(captured)}")
cov = (len(captured) / len(sent_names)) if sent_names else 0
print(f"coverage_pct={cov*100:.2f}")
print(f"unresolved_rows={seen_unresolved}")
PY
}
