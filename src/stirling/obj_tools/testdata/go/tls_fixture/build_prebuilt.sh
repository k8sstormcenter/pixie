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

set -euo pipefail

if [[ $# -gt 0 ]]; then
  versions=("$@")
else
  versions=(1.25.8 1.26.7)
fi
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

cp "${script_dir}/main.go" "${work_dir}/main.go"
cd "${work_dir}"
go mod init px.dev/tls_fixture >/dev/null 2>&1

for version in "${versions[@]}"; do
  minor="$(echo "${version}" | cut -d. -f1,2 | tr . _)"
  out="${script_dir}/test_go_tls_${minor}_binary"
  GOTOOLCHAIN="go${version}" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -o "${out}" .
  echo "${out}: $(GOTOOLCHAIN="go${version}" go version "${out}" | cut -d' ' -f2)"
done
