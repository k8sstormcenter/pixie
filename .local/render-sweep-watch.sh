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

# render-sweep-watch.sh — poll the sweep dir; re-render PNGs whenever a new
# Nx/.../results_*.parquet appears.
#
# Usage:
#   ./render-sweep-watch.sh                  # watch the latest perf-sweep-*
#   ./render-sweep-watch.sh /tmp/perf-sweep-20260514-114224
#
# Idempotent — running this twice on the same dir produces the same PNGs.
# Stops auto-rendering once the sweep is done (sweep.log shows "sweep complete").
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PY="${PY:-/home/constanze/.venvs/render/bin/python}"
RENDER="$SCRIPT_DIR/render-sweep.py"

if [[ ${1:-} ]]; then
  SWEEP="$1"
else
  SWEEP=$(ls -dt /tmp/perf-sweep-2*/ 2>/dev/null | head -1)
fi
[[ -z "${SWEEP:-}" || ! -d "$SWEEP" ]] && { echo "no sweep dir"; exit 1; }
SWEEP="${SWEEP%/}"
echo "watching: $SWEEP"

prev_signature=""
while true; do
  # Build a signature from the modification times of all results parquets;
  # whenever one is added or grows, the signature changes and we re-render.
  signature=$(find "$SWEEP" -name 'results_*.parquet' -printf '%p:%T@:%s\n' \
              2>/dev/null | sort)
  if [[ "$signature" != "$prev_signature" ]]; then
    echo "$(date -Is) — rendering ($(echo "$signature" | wc -l) parquets)"
    "$PY" "$RENDER" "$SWEEP" || echo "(render failed — keeping watcher alive)"
    prev_signature="$signature"
  fi
  # If sweep is done, render once more and exit so the process doesn't linger.
  if grep -q "sweep complete" "$SWEEP/sweep.log" 2>/dev/null; then
    echo "$(date -Is) — sweep complete, final render done, exiting"
    "$PY" "$RENDER" "$SWEEP" || true
    exit 0
  fi
  sleep 30
done
