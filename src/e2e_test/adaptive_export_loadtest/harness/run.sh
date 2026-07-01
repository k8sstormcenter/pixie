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

# run.sh — drive the full AE fixture-isolation suite on a live rig and produce
# the reproducibility evidence (per-experiment CSV + stats verdicts).
#
# Prereqs:
#   KUBECONFIG   = tailscale-direct kubeconfig (make kubeconfig PG=<id>)
#   AELOAD_IMAGE = ttl.sh/aeload-<ts>:24h (built on the PG dev-machine)
#   AE in single-shot load-test mode (this script runs ae_config.sh).
#
# Usage: KUBECONFIG=... AELOAD_IMAGE=... EVID=/path ./run.sh
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$HERE/lib.sh"

[[ -n "${AELOAD_IMAGE:-}" ]] || die "AELOAD_IMAGE not set"
EVID="${EVID:-/home/croedig/biz/PoC/log4j/evidence/datavolume/aeload_$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$EVID"
REPS_CTRL="${REPS_CTRL:-100}"
REPS_E5="${REPS_E5:-100}"
REPS_E6="${REPS_E6:-10}"
log "evidence dir: $EVID"

# 1) AE into single-shot load-test mode (idempotent).
bash "$HERE/ae_config.sh"

# 2) Control-plane experiments (no Pixie/gen needed).
for e in E1 E2 E3 E4; do
  log "=== control $e (reps=$REPS_CTRL) ==="
  EXP="$e" REPS="$REPS_CTRL" OUT="$EVID/${e}.csv" bash "$HERE/exp_control.sh"
done
log "=== control E6 idempotency (reps=$REPS_E6) ==="
EXP=E6 REPS="$REPS_E6" OUT="$EVID/E6.csv" bash "$HERE/exp_control.sh"

# 3) Data-plane experiment (real Pixie capture of the counted band).
log "=== data-plane E5 (reps=$REPS_E5) ==="
REPS="$REPS_E5" OUT="$EVID/E5.csv" bash "$HERE/exp_e5.sh"

# 4) Aggregate verdicts.
log "=== aggregate ==="
python3 "$HERE/stats.py" "$EVID"/*.csv | tee "$EVID/VERDICT.txt"
log "DONE -> $EVID"
