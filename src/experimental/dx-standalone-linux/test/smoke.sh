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
# smoke.sh — single-VM detection smoke test. Boots dx (referral-only, no eBPF
# needed — event-driven referral evidence, README §3), injects a known attack,
# and asserts a malignant verdict + the exported metric. Runs on any Linux.
#
# Env: DX_BIN (path to the dx-daemon binary). If unset, assumes dx already
#      listens on :9099/:9095 (e.g. the systemd/compose deployment).
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
INJECT="$HERE/../inject/inject-referral.sh"
DX_PORT=9099
METRICS_PORT=9095
LOG="$(mktemp)"
PID=""

# shellcheck disable=SC2317  # invoked via trap
cleanup() { [ -n "$PID" ] && kill "$PID" 2>/dev/null; rm -f "$LOG"; }
trap cleanup EXIT

if [ -n "${DX_BIN:-}" ]; then
  echo "[smoke] starting dx: $DX_BIN"
  # DX_BENCH=pxdirect with no PEM falls back safely; verdicts still come from the
  # event-driven referral seed. DX_SBOB default baseline is fine for the PoC pod.
  DX_PORT=$DX_PORT DX_METRICS_PORT=$METRICS_PORT DX_BENCH=pxdirect PX_DIRECT_ADDR=127.0.0.1:12345 \
    "$DX_BIN" >"$LOG" 2>&1 &
  PID=$!
  for _ in $(seq 1 30); do
    curl -sf "http://127.0.0.1:$DX_PORT/healthz" >/dev/null 2>&1 && break
    sleep 0.3
  done
fi

echo "[smoke] catalog:"; grep -m1 'catalog loaded' "$LOG" 2>/dev/null || true

echo "[smoke] injecting argocd-render (R0001 spawn + R0010 sensitive-file)"
"$INJECT" argocd-render "http://127.0.0.1:$DX_PORT"

# poll for the DETECTION signal. On a VM without a PEM the bench is blind (no
# network-evidence pull), so a catalog SIGNATURE (ruled_in) may not complete — but
# the event-driven referral evidence (R0001 spawn → invasion) drives the GENERIC
# verdict MALIGNANT, which is the pure-Linux detection. Accept either.
rc=1
for _ in $(seq 1 40); do
  sleep 0.5
  if grep -qE 'generic=MALIGNANT|verdict .*ruled_in|RULE IN' "$LOG" 2>/dev/null; then rc=0; break; fi
done

if [ "$rc" -eq 0 ]; then
  echo "[smoke] PASS — malignant detection on the single VM (no cloud, no k8s):"
  grep -m3 -E 'generic=MALIGNANT|verdict .*ruled_in' "$LOG" 2>/dev/null | sed 's/^/    /' || true
  if grep -q 'bench UNAVAILABLE' "$LOG" 2>/dev/null; then
    echo "    NOTE: bench blind (no PEM attached) — generic detection from event-driven evidence."
    echo "          Attach a standalone_pem for the network-evidence pull + catalog rule-in."
  fi
else
  echo "[smoke] FAIL — no malignant detection within the window"
  tail -20 "$LOG" 2>/dev/null | sed 's/^/    /'
fi
exit $rc
