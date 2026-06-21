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

# inject.sh — inject controlled kubescape_logs trigger rows into ClickHouse over
# the HTTP interface, with EXACT control over event_time. This is the only AE
# input under test: real kubescape is NOT deployed for these load-tests.
#
# Row shape mirrors exactly what Vector emits (and what AE's trigger polls):
#   BaseRuntimeMetadata, CloudMetadata, RuleID, RuntimeK8sDetails (JSON string
#   with podName/podNamespace), RuntimeProcessDetails (JSON string with
#   processTree.pid/comm), event, event_time (UInt64 unix-NANOS), hostname.
#
# anomaly_hash = SHA256(pid, comm, pod, namespace)[:16] is computed by AE — NOT
# set here — so per-rep uniqueness comes from a unique --pod (data plane) and a
# unique --hostname (control plane; trigger_watermark is partitioned by host).
#
# Timestamp discipline (PRODUCTION UNIT = SECONDS):
#   event_time is unix SECONDS — the unit the soc Vector kubescape sink emits
#   (`to_unix_timestamp(ts)`, VRL default seconds) and what the CH DDL's
#   `toDateTime(event_time)` TTL/PARTITION assume. (The AE trigger auto-detects
#   s/ms/ns, but the DDL only handles seconds — so fixtures MUST be seconds or
#   the rows are TTL-deleted; see FINDINGS_AND_BACKLOG.md F1/AE-2.)
#   --event-time is the FIRST row's event_time (unix SECONDS). With --count N>1
#   the rows get event_time, event_time+dt, ... (--dt-s, default 1s) so they are
#   DISTINCT + monotone and never collide at the watermark boundary — UNLESS
#   --same-time is given, which deliberately reuses one event_time to exercise
#   the boundary-fingerprint dedup (experiment E4).
set -euo pipefail

ENDPOINT="${CH_ENDPOINT:-http://localhost:8123}"
CH_USER="${CH_USER:-}"
CH_PASS="${CH_PASS:-}"
NS="" ; POD="" ; RULE="R0001" ; PID="1234" ; COMM="java"
EVENT_TIME="" ; HOSTNAME_="" ; COUNT=1 ; DT_S=1 ; SAME_TIME=0
ALERT=""

usage(){ grep '^#' "$0" | sed 's/^# \{0,1\}//' ; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --endpoint)   ENDPOINT="$2"; shift 2;;
    --user)       CH_USER="$2"; shift 2;;
    --pass)       CH_PASS="$2"; shift 2;;
    --ns)         NS="$2"; shift 2;;
    --pod)        POD="$2"; shift 2;;
    --rule)       RULE="$2"; shift 2;;
    --pid)        PID="$2"; shift 2;;
    --comm)       COMM="$2"; shift 2;;
    --event-time) EVENT_TIME="$2"; shift 2;;
    --hostname)   HOSTNAME_="$2"; shift 2;;
    --count)      COUNT="$2"; shift 2;;
    --dt-s)       DT_S="$2"; shift 2;;
    --same-time)  SAME_TIME=1; shift;;
    --alert)      ALERT="$2"; shift 2;;
    -h|--help)    usage 0;;
    *) echo "inject.sh: unknown arg $1" >&2; usage 1;;
  esac
done

[[ -n "$NS" && -n "$POD" && -n "$EVENT_TIME" && -n "$HOSTNAME_" ]] || {
  echo "inject.sh: --ns --pod --event-time --hostname are required" >&2; exit 2; }
[[ -n "$ALERT" ]] || ALERT="$RULE"

# Build the JSONEachRow body. RuntimeK8sDetails / RuntimeProcessDetails are
# JSON-STRING columns, so their inner quotes are escaped (\"). event_time is
# unix SECONDS; --count rows step by DT_S seconds (distinct, monotone).
body=""
for ((i=0; i<COUNT; i++)); do
  if [[ "$SAME_TIME" -eq 1 ]]; then et="$EVENT_TIME"; else et=$(( EVENT_TIME + i*DT_S )); fi
  k8s="{\\\"podName\\\":\\\"${POD}\\\",\\\"podNamespace\\\":\\\"${NS}\\\"}"
  proc="{\\\"processTree\\\":{\\\"pid\\\":${PID},\\\"comm\\\":\\\"${COMM}\\\"}}"
  base="{\\\"alertName\\\":\\\"${ALERT}\\\"}"
  row="{\"BaseRuntimeMetadata\":\"${base}\",\"CloudMetadata\":\"\",\"RuleID\":\"${RULE}\",\"RuntimeK8sDetails\":\"${k8s}\",\"RuntimeProcessDetails\":\"${proc}\",\"event\":\"\",\"event_time\":${et},\"hostname\":\"${HOSTNAME_}\"}"
  body+="${row}"$'\n'
done

auth=()
[[ -n "$CH_USER" ]] && auth=(-u "${CH_USER}:${CH_PASS}")

q='INSERT INTO forensic_db.kubescape_logs FORMAT JSONEachRow'
code=$(curl -sS -o /tmp/inject_resp.$$ -w '%{http_code}' \
  "${auth[@]}" \
  --data-binary "$body" \
  -H 'Content-Type: application/x-ndjson' \
  "${ENDPOINT%/}/?query=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$q")")

if [[ "${code:0:1}" != "2" ]]; then
  echo "inject.sh: INSERT HTTP $code" >&2
  cat /tmp/inject_resp.$$ >&2 || true
  rm -f /tmp/inject_resp.$$
  exit 1
fi
rm -f /tmp/inject_resp.$$
echo "inject.sh: OK count=${COUNT} ns=${NS} pod=${POD} rule=${RULE} host=${HOSTNAME_} t0=${EVENT_TIME} same_time=${SAME_TIME}"
