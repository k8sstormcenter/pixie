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

# perf-sweep.sh — run the sovereign-soc load-multiplier sweep on the local
# k3s by invoking local-ci.sh phase 9 once per multiplier. Each run is ~25
# min (30 s setup + 2 m BURNIN + 20 m RUN + ~3 m teardown), so the full
# 5-multiplier sweep takes ~2h05m.
#
# Output: a single timestamped sweep dir under /tmp/perf-sweep-<ts>/,
# with one parquet output subdir + one perf_tool log per multiplier:
#
#   /tmp/perf-sweep-20260514-…/
#     1x/   2026/…/results_0000.parquet  spec.parquet  perf_tool.log
#     2x/   …
#     4x/   …
#     8x/   …
#     16x/  …
#     sweep.log     ← top-level log of which multiplier started/finished when
#
# Usage:
#   ./perf-sweep.sh                  # run all five 1×, 2×, 4×, 8×, 16×
#   ./perf-sweep.sh 4x 16x           # just those two
#
# Stops on the first failure so a broken 1× run doesn't waste 1h45m on the
# rest.
set -euo pipefail

SWEEP_DIR=/tmp/perf-sweep-$(date +%Y%m%d-%H%M%S)
mkdir -p "$SWEEP_DIR"
SWEEP_LOG="$SWEEP_DIR/sweep.log"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() { printf '%(%Y-%m-%dT%H:%M:%S)T %s\n' -1 "$*" | tee -a "$SWEEP_LOG"; }

if [[ $# -eq 0 ]]; then
  # Default sweep matches the multipliers wired into
  # pixie/src/e2e_test/perf_tool/pkg/suites/suites.go → sovereignSOCSuite().
  # When the suite list changes, this list must change too — perf_tool
  # exits 1 if `--experiment_name=redis-attack-Nx` isn't in the
  # registry.
  MULTIPLIERS=(2x 4x 8x 16x 32x 64x)
else
  MULTIPLIERS=("$@")
fi
log "sweep dir: $SWEEP_DIR"
log "multipliers: ${MULTIPLIERS[*]}"

t_start=$(date +%s)
for m in "${MULTIPLIERS[@]}"; do
  EXP="redis-attack-${m}"
  OUT="$SWEEP_DIR/${m}"
  mkdir -p "$OUT"
  log "=== START $EXP → $OUT ==="
  iter_start=$(date +%s)
  if PERF_EXPERIMENT_NAME="$EXP" \
       PERF_OUT_DIR="$OUT" \
       PERF_LOG_LEVEL="${PERF_LOG_LEVEL:-info}" \
       "$SCRIPT_DIR/local-ci.sh" --phases=9 \
       > "$OUT/local-ci.log" 2>&1; then
    iter_end=$(date +%s)
    log "=== DONE  $EXP ($((iter_end - iter_start)) s)"
  else
    rc=$?
    iter_end=$(date +%s)
    log "=== FAIL  $EXP (exit=$rc, $((iter_end - iter_start)) s) — see $OUT/local-ci.log"
    log "aborting sweep — fix and rerun missing multipliers individually"
    exit "$rc"
  fi
done
t_end=$(date +%s)
log "sweep complete in $((t_end - t_start)) s — $SWEEP_DIR"
