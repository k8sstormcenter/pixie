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
#
# verify-assets.sh — regression guard for the native-systemd deployment defects
# reported in entlein/dx#119:
#   #1 PEM tarball top symlink dangled (absolute /app/... build-container target)
#   #2 systemd .env inline comment swallowed into the value
#   #4 packaged-headers bundle missing (socket_tracer not instantiated → blind)
#
# CI mode (no args): validates the in-repo unit env file (#2) + the unit ExecStart.
# Asset mode: `verify-assets.sh <extracted-pem-dir> [<px-headers-dir>]` validates a
# DOWNLOADED release bundle (#1 symlink resolves, #4 headers present).
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
SYSD="$HERE/../systemd"
fail=0
ok(){ echo "  PASS $1"; }
no(){ echo "  FAIL $1"; fail=1; }

echo "[verify] #2 — no inline comments in dx-standalone.env (systemd keeps them in the value)"
if grep -qE '=[^#]*[[:space:]]+#' "$SYSD/dx-standalone.env"; then
  no "dx-standalone.env has a value with a trailing inline comment"
  grep -nE '=[^#]*[[:space:]]+#' "$SYSD/dx-standalone.env" | sed 's/^/       /'
else
  ok "dx-standalone.env: every comment on its own line"
fi

echo "[verify] unit ExecStart points at the launcher (native binary reality)"
grep -q 'ExecStart=/usr/local/bin/standalone-pem' "$SYSD/standalone-pem.service" \
  && ok "standalone-pem.service execs the launcher" \
  || no "standalone-pem.service ExecStart not the launcher"

# Asset mode — validate a downloaded/extracted bundle.
PEMDIR="${1:-}"; PXDIR="${2:-}"
if [ -n "$PEMDIR" ]; then
  echo "[verify] #1 — PEM tarball top symlink resolves (not the /app build-container path)"
  link="$PEMDIR/standalone_pem/standalone_pem"
  if [ -e "$link" ]; then
    tgt="$(readlink "$link" 2>/dev/null || true)"
    case "$tgt" in
      /*) no "top symlink is ABSOLUTE ($tgt) — dangles off the build host" ;;
      *)  ok "top symlink is relative + resolves ($tgt)" ;;
    esac
  else
    no "top symlink dangles or missing: $link"
  fi
fi
if [ -n "$PXDIR" ]; then
  echo "[verify] #4 — packaged headers present for socket_tracer"
  n=$(ls "$PXDIR"/linux-headers-x86_64-*.tar.gz 2>/dev/null | wc -l)
  [ "$n" -gt 0 ] && ok "$n packaged-header tarballs at $PXDIR" || no "no /px/linux-headers-x86_64-*.tar.gz"
fi

echo "[verify] $([ $fail -eq 0 ] && echo ALL PASS || echo FAILURES)"
exit $fail
