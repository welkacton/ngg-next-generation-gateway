#!/usr/bin/env bash
# NGG in-place updater (systemd hosts). Called by /api/v1/update/apply or by
# hand from a root shell. It backs up, replaces, restarts, health-checks and
# rolls back on failure. It never claims success without a real 200 from
# /api/v1/health.
set -euo pipefail

STAGE=""; ASSET=""; PREFIX="/opt/ngg"; CONF="/etc/ngg/ngg.yaml"; SERVICE="ngg"; PORT=9090
log() {
  printf '[ngg-update] %s\n' "$*"
  [ -n "${LOG_FILE:-}" ] && printf '[ngg-update] %s\n' "$*" >> "$LOG_FILE"
  return 0
}
die() {
  printf '[ngg-update][error] %s\n' "$*" >&2
  [ -n "${LOG_FILE:-}" ] && printf '[ngg-update][error] %s\n' "$*" >> "$LOG_FILE"
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --stage)   STAGE="${2:-}"; shift 2 ;;
    --asset)   ASSET="${2:-}"; shift 2 ;;
    --prefix)  PREFIX="${2:-}"; shift 2 ;;
    --conf)    CONF="${2:-}"; shift 2 ;;
    --service) SERVICE="${2:-}"; shift 2 ;;
    --port)    PORT="${2:-}"; shift 2 ;;
    --log)     LOG_FILE="${2:-}"; shift 2 ;;
    *) die "unknown option: $1" ;;
  esac
done

[ -n "$STAGE" ] && [ -n "$ASSET" ] || die "usage: ngg-update.sh --stage DIR --asset NAME [--prefix PREFIX] [--conf FILE] [--service NAME]"
ASSET_PATH="$STAGE/$ASSET"
if [ -n "${LOG_FILE:-}" ]; then mkdir -p "$(dirname "$LOG_FILE")"; fi
[ -f "$ASSET_PATH" ] || die "staged asset not found: $ASSET_PATH"
[ -f "$STAGE/SHA256SUMS" ] || die "missing SHA256SUMS; refusing to apply"

expected="$(awk -v n="$ASSET" '{ f=$2; sub(/^\*/,"",f); sub(/^\.\//,"",f); if (f==n) {print $1; exit} }' "$STAGE/SHA256SUMS")"
[ -n "$expected" ] || die "SHA256SUMS has no entry for $ASSET"
actual="$(sha256sum "$ASSET_PATH" | awk '{print $1}')"
[ "$actual" = "$expected" ] || die "SHA256 mismatch ($actual != $expected)"

TS="$(date +%Y%m%dT%H%M%S)"
BACKUP="$PREFIX/backup-$TS"
TMP="$(mktemp -d)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

log "stopping $SERVICE"
systemctl stop "$SERVICE" || true
mkdir -p "$BACKUP"
if [ -f "$PREFIX/nggd" ]; then cp -a "$PREFIX/nggd" "$BACKUP/nggd" || true; fi
if [ -d "$PREFIX/web/dist" ]; then cp -a "$PREFIX/web/dist" "$BACKUP/web-dist" || true; fi

rollback() {
  log "update failed - rolling back"
  if [ -f "$BACKUP/nggd" ]; then cp -a "$BACKUP/nggd" "$PREFIX/nggd" || true; fi
  if [ -d "$BACKUP/web-dist" ]; then
    rm -rf "$PREFIX/web/dist" || true
    cp -a "$BACKUP/web-dist" "$PREFIX/web/dist" || true
  fi
  systemctl start "$SERVICE" || true
  return 0
}

case "$ASSET" in
  *.tar.gz)
    tar -xzf "$ASSET_PATH" -C "$TMP" || { rollback; die "extract failed"; }
    PKG="$TMP/ngg"; [ -d "$TMP/ngg" ] || PKG="$TMP"
    [ -f "$PKG/nggd" ] || { rollback; die "bundle has no nggd"; }
    install -m 0755 "$PKG/nggd" "$PREFIX/nggd"
    if [ -d "$PKG/web-dist" ]; then
      rm -rf "$PREFIX/web/dist"; cp -r "$PKG/web-dist" "$PREFIX/web/dist"
    fi
    for d in scripts deploy installer docs; do
      if [ -d "$PKG/$d" ]; then
        mkdir -p "$PREFIX/$d" || true
        cp -r "$PKG/$d/." "$PREFIX/$d/" || true
      fi
    done
    ;;
  *) rollback; die "unsupported asset on Linux: $ASSET (use the linux tarball)" ;;
esac

log "starting $SERVICE"
systemctl start "$SERVICE" || { rollback; die "service failed to start"; }

TOKEN="$(awk '/^[[:space:]]*token:/{print $2; exit}' "$CONF" | tr -d '"')"
OK=0
for _ in $(seq 1 30); do
  if curl -fsS --max-time 2 -H "Authorization: Bearer $TOKEN" \
    "http://127.0.0.1:${PORT}/api/v1/health" >/dev/null 2>&1; then OK=1; break; fi
  sleep 1
done
if [ "$OK" -ne 1 ]; then rollback; die "health check failed after update"; fi

log "update applied successfully: $ASSET (backup: $BACKUP)"
