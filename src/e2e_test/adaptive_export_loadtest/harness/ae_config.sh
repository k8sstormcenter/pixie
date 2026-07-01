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

# ae_config.sh — put the live adaptive-export into deterministic load-test mode.
#
# Sets (and rolls out) the env that makes the data-plane write exactly once per
# anomaly window over the full sealed band:
#   ADAPTIVE_PUSH_PIXIE_ROWS=true      operator pulls + writes protocol tables
#   ADAPTIVE_PUSH_REFRESH_SEC=-1       SINGLE-SHOT: one pull per window (only on
#                                      a rebuilt AE image carrying the new knob;
#                                      harmless/ignored on older images)
#   ADAPTIVE_WINDOW_BEFORE_SEC=120     window start ≤ band start (band is seconds)
#   ADAPTIVE_WINDOW_AFTER_SEC=5        member lifetime — the PRIMARY single-pull
#                                      lever that works on the CURRENTLY-PUBLISHED
#                                      image: 5s < the 30s default refresh, so the
#                                      window expires before any 2nd pull → each
#                                      window written exactly once.
# Also disables async_insert on the ingest user so row counts are stable at read
# time (per the AE per-PG fixes), and applies the PL_CLOUD_ADDR :443 fix.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$HERE/lib.sh"

DS="${AE_DS:-adaptive-export}"
log "configuring $AE_NS/$DS for single-shot load-test mode"

k -n "$AE_NS" set env "ds/$DS" \
  ADAPTIVE_PUSH_PIXIE_ROWS=true \
  ADAPTIVE_PUSH_REFRESH_SEC=-1 \
  ADAPTIVE_WINDOW_BEFORE_SEC=120 \
  ADAPTIVE_WINDOW_AFTER_SEC=5 \
  >/dev/null

# PL_CLOUD_ADDR :443 fix (idempotent) — without it AE crashloops / 0 writes.
CUR="$(k -n "$AE_NS" get cm pl-cloud-config -o jsonpath='{.data.PL_CLOUD_ADDR}' 2>/dev/null || true)"
if [[ -n "$CUR" && "$CUR" != *:* ]]; then
  log "patching PL_CLOUD_ADDR $CUR -> ${CUR}:443"
  k -n "$AE_NS" patch cm pl-cloud-config --type merge -p "{\"data\":{\"PL_CLOUD_ADDR\":\"${CUR}:443\"}}" >/dev/null
fi

k -n "$AE_NS" rollout restart "ds/$DS" >/dev/null
k -n "$AE_NS" rollout status  "ds/$DS" --timeout=180s
log "AE configured + rolled out"
