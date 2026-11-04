#!/bin/sh
# First boot: create a config from the tracked example. NGG generates a random
# API token and writes it into this mounted config; read it from the logs.
set -eu
mkdir -p /app/configs /app/data
if [ ! -f /app/configs/ngg.yaml ]; then
  cp /app/defaults/ngg.example.yaml /app/configs/ngg.yaml
  chmod 600 /app/configs/ngg.yaml
  echo "NGG: created /app/configs/ngg.yaml from the example template"
fi
exec /app/nggd -config /app/configs/ngg.yaml
