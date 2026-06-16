#!/usr/bin/env bash
# build_gen_image.sh — build the aeload image (cleanloadgen + httpsink) on the PG
# dev-machine's native amd64 docker and push to ttl.sh. Same pattern used for the
# dx images (this ARM agent VM can't build amd64 / has no bazel). Prints the
# pushed tag on the last line (AELOAD_IMAGE=...).
#
# Usage: PG=<id> ./build_gen_image.sh
set -euo pipefail
PG="${PG:?set PG=<playground id>}"
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/../tools/loadgen" && pwd)"   # in-repo generator build context
TS="$(date -u +%Y%m%d-%H%M%S)"
TAG="ttl.sh/aeload-${TS}:24h"

echo "[build] packing $SRC"
tar -C "$SRC" --exclude='.git' --exclude='harness/__pycache__' -czf /tmp/aeload-ctx.tgz .
echo "[build] cp to $PG dev-machine"
labctl cp -m dev-machine /tmp/aeload-ctx.tgz "$PG:/tmp/aeload-ctx.tgz" </dev/null

echo "[build] docker build + push on dev-machine (-> $TAG)"
OUT=$(labctl ssh "$PG" -m dev-machine -- bash -s <<EOF 2>&1 || true
set -euo pipefail
rm -rf /tmp/aeloadctx && mkdir -p /tmp/aeloadctx
tar -C /tmp/aeloadctx -xzf /tmp/aeload-ctx.tgz
cd /tmp/aeloadctx
docker build -t "$TAG" -f Dockerfile . >/dev/null
docker push "$TAG" >/dev/null
echo __GEN_IMAGE_PUSHED__
EOF
)
echo "$OUT" | grep -q __GEN_IMAGE_PUSHED__ || { echo "[build] FAIL:"; echo "$OUT" | tail -20; exit 1; }
echo "[build] OK"
echo "AELOAD_IMAGE=$TAG"
