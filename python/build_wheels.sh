#!/usr/bin/env bash
# Build one platform-tagged wheel per target, each bundling the matching
# static binary. A wheel containing a compiled executable must never be
# published as py3-none-any: pip would hand an arm64 binary to an x86 user.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=${1:-dist}
mkdir -p "$OUT"

build() { # goos goarch wheel-platform-tag
  local goos=$1 goarch=$2 tag=$3 name=cricket-mcp
  [ "$goos" = windows ] && name=cricket-mcp.exe
  rm -rf python/cricket_mcp/bin && mkdir -p python/cricket_mcp/bin
  GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
    -o "python/cricket_mcp/bin/$name" ./cmd/cricket-mcp
  (cd python && python3 -m build --wheel --outdir "../$OUT" >/dev/null &&
     python3 -m wheel tags --platform-tag "$tag" --remove "../$OUT"/cricket_mcp-*-py3-none-any.whl >/dev/null)
  echo "built $goos/$goarch -> $tag"
}

build darwin  arm64 macosx_11_0_arm64
build darwin  amd64 macosx_10_9_x86_64
build linux   amd64 manylinux2014_x86_64
build linux   arm64 manylinux2014_aarch64
build windows amd64 win_amd64
ls -la "$OUT"
