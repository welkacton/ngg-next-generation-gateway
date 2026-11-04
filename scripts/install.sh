#!/usr/bin/env bash
# Linux x86_64 systemd installation (run as root).
set -euo pipefail
PREFIX=${PREFIX:-/opt/ngg}
CONF=${CONF:-/etc/ngg}
DATA=${DATA:-/var/lib/ngg}
SERVICE_USER=${SERVICE_USER:-ngg}
cd "$(dirname "$0")/.."
go build -trimpath -ldflags="-s -w" -o bin/nggd-linux-amd64 ./cmd/nggd
id -u "$SERVICE_USER" >/dev/null 2>&1 || useradd -r -s /usr/sbin/nologin "$SERVICE_USER"
install -d -m 0755 "$PREFIX" "$CONF" "$DATA" "$CONF/cores.d"
install -m 0755 bin/nggd-linux-amd64 "$PREFIX/nggd"
[[ -f "$CONF/ngg.yaml" ]] || install -m 0600 configs/ngg.example.yaml "$CONF/ngg.yaml"
[[ -d "$PREFIX/web/dist" ]] || { [[ -d web/dist ]] && cp -r web/dist "$PREFIX/web/"; } || true
chown -R "$SERVICE_USER":"$SERVICE_USER" "$DATA" "$CONF"
cat > /etc/systemd/system/ngg.service <<UNIT
[Unit]
Description=NGG Next-Generation Gateway (Phase 1)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
WorkingDirectory=$PREFIX
ExecStart=$PREFIX/nggd -config $CONF/ngg.yaml
Restart=on-failure
RestartSec=3
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
NoNewPrivileges=false

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
echo "installed. Edit $CONF/ngg.yaml, then: systemctl enable --now ngg"
echo "API token will be generated on first start and written to $CONF/ngg.yaml"
