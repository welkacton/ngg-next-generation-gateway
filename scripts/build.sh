#!/usr/bin/env bash
# Build NGG for one or more targets. Linux x86_64 is the Phase-1 priority.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p bin
TARGETS=("linux/amd64")
if [[ $# -gt 0 ]]; then TARGETS=("$@"); fi
for target in "${TARGETS[@]}"; do
  os=${target%/*}; arch=${target#*/}
  out="bin/nggd-${os}-${arch}"
  [[ "$os" == "windows" ]] && out="${out}.exe"
  echo "building $out"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$out" ./cmd/nggd
done
echo "done"
