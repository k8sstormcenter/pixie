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
# inject-referral.sh — the "no kubescape on a VM" referral source.
#
# POSTs a synthetic, enriched-kubescape-shaped alert to the dx S2 receiver
# (:9099/findings, JSON array). This is the exact shape a real feed must emit
# (dx internal/receiver → referral.FromKubescapeRow). Used by the showcase + the
# smoke test; also a template for the PEM→referral synthesizer (README §3.2).
#
# Usage: inject-referral.sh <scenario> [dx_url]
#   scenarios: argocd-render | log4shell-spawn | cred-escalation | benign
#   dx_url default http://127.0.0.1:9099
set -euo pipefail
scenario="${1:-argocd-render}"
DX="${2:-http://127.0.0.1:9099}"
now="$(date +%s)"
ns="poc"
pod="app-vm-0"

# one enriched-kubescape row; RuntimeProcessDetails carries the process tree,
# RuntimeK8sDetails the (VM: synthetic) pod scope. event_time in seconds.
row() { # rule comm message [file]
  local rule="$1" comm="$2" msg="$3" file="${4:-}"
  cat <<JSON
{"RuleID":"$rule","event_time":$now,"anomaly_hash":"vm-$rule-$comm-$now",
 "message":"$msg","RuntimeK8sDetails":{"namespace":"$ns","podName":"$pod"},
 "RuntimeProcessDetails":{"comm":"$comm","pid":4242,"ppid":1,"path":"$file"}}
JSON
}

post() { # json-array-body
  curl -sf -m 10 -X POST "$DX/findings" -H 'Content-Type: application/json' -d "$1" \
    && echo "  injected -> $DX/findings"
}

case "$scenario" in
  # completed argocd-style RCE: unexpected spawn (R0001) + sensitive-file read
  # (R0010) on the same pod → dx correlates → argocd-malignant-render.
  argocd-render)
    post "[$(row R0001 mal.sh 'Unexpected process launched: mal.sh')]"
    post "[$(row R0010 mal.sh 'Unexpected sensitive file access: /etc/shadow' /etc/shadow)]"
    ;;
  # log4shell contained-spawn: R0001 spawn (the JVM child). The http/ldap evidence
  # would come from the PEM pull; here the spawn alone drives the invasion signal.
  log4shell-spawn)
    post "[$(row R0001 sh 'Unexpected process launched: sh (spawned by java)')]"
    ;;
  # privilege escalation via the credential subsystem (R0004 capabilities).
  cred-escalation)
    post "[$(row R0004 app 'Unexpected capabilities: CAP_SYS_ADMIN raised')]"
    ;;
  # a benign baseline referral (expected: not malignant / discharged).
  benign)
    post "[$(row R0002 sh 'File access: /var/log/app.log' /var/log/app.log)]"
    ;;
  *) echo "unknown scenario: $scenario (argocd-render|log4shell-spawn|cred-escalation|benign)"; exit 2;;
esac
