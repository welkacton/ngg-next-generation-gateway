#!/usr/bin/env bash
# Local development: Go backend on :9090 + Vite dev server on :5173.
set -euo pipefail
cd "$(dirname "$0")/.."
go build -o bin/nggd ./cmd/nggd
./bin/nggd -config configs/ngg.yaml &
BACKEND_PID=$!
trap 'kill "$BACKEND_PID" 2>/dev/null || true' EXIT
cd web
npm install
npm run dev
