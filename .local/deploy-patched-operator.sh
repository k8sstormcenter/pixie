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

# deploy-patched-operator.sh — bazel-build adaptive_export with our two
# patches (prune-grace + 180s gRPC timeout in controller.go) and roll the
# deployment onto the new image. Idempotent.
set -uo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

TAG=rev3-cr-fixes-2

echo "=== bazel build adaptive_export image ==="
bazel build //src/vizier/services/adaptive_export:adaptive_export_image \
  --config=x86_64_sysroot 2>&1 | tail -3

echo "=== load to docker ==="
OUT=$(./bazel-bin/src/vizier/services/adaptive_export/adaptive_export_image.executable 2>&1 | tail -3)
IMG_ID=$(echo "$OUT" | grep "Loaded image ID" | grep -oE "sha256:[a-f0-9]+" | head -1 | cut -d: -f2)
if [ -z "$IMG_ID" ]; then
  echo "FAIL: image build/load problem"
  echo "$OUT"
  exit 1
fi
echo "img: $IMG_ID"

echo "=== tag + import to k3s containerd ==="
docker tag "$IMG_ID" "adaptive_export:$TAG" >/dev/null
docker save "adaptive_export:$TAG" -o /tmp/adaptive_export_patched.tar
sudo k3s ctr -n k8s.io images import /tmp/adaptive_export_patched.tar 2>&1 | tail -1

echo "=== set deploy image + rollout ==="
kubectl set image -n pl deployment/adaptive-export \
  "adaptive-export=docker.io/library/adaptive_export:$TAG" 2>&1 | head -1
kubectl scale deploy -n pl adaptive-export --replicas=1 >/dev/null 2>&1
kubectl rollout status -n pl deploy/adaptive-export --timeout=120s 2>&1 | tail -1

echo "=== confirm running ==="
kubectl get pod -n pl -l name=adaptive-export -o jsonpath='{.items[0].status.containerStatuses[0].imageID}'
echo ""
