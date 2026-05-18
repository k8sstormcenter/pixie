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

# chained-sweep.sh — wait for an in-flight perf-sweep to finish, then kick
# off a second (independent) sweep into a fresh /tmp/perf-sweep-<ts>/ dir
# with its own watcher. Use this when you want a clean before/after pair
# without having to be at the keyboard when the first one ends.
#
# Usage:
#   ./chained-sweep.sh <first-sweep-dir>
#   ./chained-sweep.sh /tmp/perf-sweep-20260514-114224
set -euo pipefail

FIRST="${1:?need path to first sweep dir}"
LOG=/tmp/chained-sweep.log
exec > >(tee -a "$LOG") 2>&1

echo "$(date -Is) waiting for first sweep to finish: $FIRST"
# perf-sweep.sh writes "sweep complete in N s — <dir>" as the last line
# of sweep.log when all multipliers landed.
while ! grep -q "sweep complete" "$FIRST/sweep.log" 2>/dev/null; do
  sleep 30
done
echo "$(date -Is) first sweep finished"

# Kick off second sweep (perf-sweep.sh creates its own timestamped dir).
# Tag the sweep.log with a header so it's obvious in the watcher output
# that this is the "after" run.
echo "$(date -Is) launching second sweep"
/home/constanze/code/pixie/perf-sweep.sh > /tmp/perf-sweep-second.stdout 2>&1 &
SWEEP_PID=$!

# Give perf-sweep.sh a moment to create its dir + sweep.log.
sleep 8
NEW=$(ls -dt /tmp/perf-sweep-2*/ 2>/dev/null | head -1)
NEW="${NEW%/}"
if [[ -z "$NEW" || "$NEW" == "$FIRST" ]]; then
  echo "$(date -Is) ERROR: second sweep dir not detected"
  exit 1
fi
echo "$(date -Is) second sweep dir: $NEW"

# Watcher for the new sweep (auto-exits when its sweep.log shows complete).
setsid bash /home/constanze/code/pixie/render-sweep-watch.sh "$NEW" \
  </dev/null > /tmp/render-watch-second.log 2>&1 &
disown
echo "$(date -Is) watcher launched for $NEW"

wait "$SWEEP_PID"
echo "$(date -Is) second sweep done"
