#!/usr/bin/env bash
# NGG one-command local start. Builds from source when Go/Node are available,
# or downloads a prebuilt release tarball when --release is given.
set -euo pipefail
cd "$(dirname "$0")/.."
RELEASE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --release) RELEASE="${2:-}"; shift 2 ;;
    *) echo "usage: $0 [--release v1.0.0]"; exit 2 ;;
  esac
done
mkdir -p bin runtime/configs runtime/data
if [ -n "$RELEASE" ]; then
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "unsupported arch: $(uname -m)"; exit 1 ;;
  esac
  case "$OS" in
    linux) ASSET="ngg-${RELEASE}-linux-${ARCH}.tar.gz" ;;
    darwin) ASSET="ngg-${RELEASE}-darwin-${ARCH}.tar.gz" ;;
    *) echo "prebuilt assets target linux/darwin; build from source on $OS"; exit 1 ;;
  esac
  URL="https://github.com/welkacton/ngg-next-generation-gateway/releases/download/${RELEASE}/${ASSET}"
  echo "downloading $URL"
  curl -fL --retry 3 -o "/tmp/${ASSET}" "$URL"
  tar -xzf "/tmp/${ASSET}" -C .
  echo "downloaded. Verify SHA256SUMS from the release page before running."
else
  command -v go >/dev/null || { echo "Go 1.26+ is required, or use --release <tag>"; exit 1; }
  command -v npm >/dev/null || { echo "Node 20+ is required for the Web UI"; exit 1; }
  echo "building backend..."; go build -trimpath -o bin/nggd ./cmd/nggd
  echo "building frontend..."; (cd web && npm ci --no-audit --no-fund && npm run build)
fi
if [ ! -f configs/ngg.yaml ]; then
  cp configs/ngg.example.yaml configs/ngg.yaml
  chmod 600 configs/ngg.yaml
  echo "created configs/ngg.yaml (a random API token will be generated on first start)"
fi
echo "starting NGG on http://127.0.0.1:9090"
exec ./bin/nggd -config configs/ngg.yaml
