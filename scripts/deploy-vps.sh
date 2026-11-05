#!/usr/bin/env bash
# NGG VPS deployment helper (systemd-based Linux, no Go/Node needed on the VPS).
#
#   sudo ./scripts/deploy-vps.sh install   --version v1.0.0 --public
#   sudo ./scripts/deploy-vps.sh install   --version v1.0.0 --domain ngg.example.com --with-tls
#   sudo ./scripts/deploy-vps.sh install   --file ./ngg-v1.0.0-linux-amd64.tar.gz
#   ./scripts/deploy-vps.sh docker        # Docker Compose path (Alpine / no systemd)
#   sudo ./scripts/deploy-vps.sh status
#   sudo ./scripts/deploy-vps.sh upgrade   --version v1.0.1
#   sudo ./scripts/deploy-vps.sh backup
#   sudo ./scripts/deploy-vps.sh restore   --file /var/backups/ngg/ngg-backup-....tar.gz
#   sudo ./scripts/deploy-vps.sh doctor
#   sudo ./scripts/deploy-vps.sh uninstall --keep-data --yes
#
# Private repo: export GITHUB_TOKEN=ghp_... (or pass --token). The script first
# tries the public release URL, then falls back to the GitHub API asset
# endpoint, which is what private repositories require.
#
# Environment overrides: PREFIX CONF DATA LOGDIR BACKUPDIR SERVICE_USER SERVICE
# SERVICE PORT NGG_REPO_OWNER NGG_REPO_NAME GITHUB_TOKEN.
set -euo pipefail

OWNER=${NGG_REPO_OWNER:-welkacton}
REPO=${NGG_REPO_NAME:-ngg-next-generation-gateway}
PREFIX=${PREFIX:-/opt/ngg}
CONF=${CONF:-/etc/ngg}
DATA=${DATA:-/var/lib/ngg}
LOGDIR=${LOGDIR:-/var/log/ngg}
BACKUPDIR=${BACKUPDIR:-/var/backups/ngg}
SERVICE_USER=${SERVICE_USER:-ngg}
SERVICE=${SERVICE:-ngg}
PORT=${PORT:-9090}
WEB_PORT=${WEB_PORT:-8080}
PUBLIC_IP=${PUBLIC_IP:-""}
UNIT="/etc/systemd/system/${SERVICE}.service"
GATE_PATH=""
DISTRO="unknown"; PKG_TOOL=""; REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

VERSION=""; FILE=""; TOKEN="${GITHUB_TOKEN:-}"; DOMAIN=""; EMAIL=""
OUTPUT=""; WITH_TLS=0; WITH_NGINX=0; PUBLIC_MODE=0; CONFIGURE_FIREWALL=0; ENABLE_TUN=0
ASSUME_YES=0; KEEP_DATA=0; SKIP_CHECKSUM=0; NO_INSTALL_DEPS=0
TMP=""; PKG=""; TARBALL=""; CHECKSUMS=""; API_TOKEN=""; BASIC_PASS=""

log()  { printf '[ngg] %s\n' "$*"; }
warn() { printf '[ngg][warn] %s\n' "$*" >&2; }
die()  { printf '[ngg][error] %s\n' "$*" >&2; exit 1; }

cleanup() { if [ -n "$TMP" ] && [ -d "$TMP" ]; then rm -rf "$TMP"; fi; }
trap cleanup EXIT

usage() {
  cat <<'USAGE'
用法: deploy-vps.sh <命令> [选项]

命令:
  install     从 GitHub Release（或本地 tar.gz）全新安装并启动 systemd 服务
  docker      用 Docker Compose 部署（不需要 systemd；容器环境首选）
  upgrade     备份后替换二进制与 Web UI，保留配置与数据；失败自动回滚二进制
  backup      打包 /etc/ngg 配置 + 数据目录 + Web UI 到 /var/backups/ngg
  restore     从备份包恢复（会先停止服务）
  status      查看服务状态、健康检查结果与访问地址
  doctor      逐项体检（二进制、配置、unit、端口、TUN、磁盘、防火墙）
  uninstall   停止并移除服务与程序文件（默认需确认；--keep-data 保留数据）

选项:
  --version v1.0.0        Release tag（install / upgrade 必填，除非用 --file）
  --file PATH             使用本地 Release tar.gz（离线部署；跳过下载）
  --token TOKEN           GitHub PAT（私有仓库；也可用环境变量 GITHUB_TOKEN）
  --public                公网访问：Nginx 监听 WEB_PORT，URL = http://公网IP:端口/16位随机后缀
  --web-port N            公网入口端口（默认 8080）
  --public-ip IP          手动指定公网 IP（默认自动探测）
  --domain FQDN           绑定域名（等价于 --public + server_name）
  --with-tls              配合 --domain 用 certbot certonly 申请证书并生成 443 站点
  --email ADDRESS         Let's Encrypt 登记邮箱
  --configure-firewall    ufw/firewalld 放行 SSH/80/443/WEB_PORT（不激活已关闭的防火墙）
  --enable-tun            把 network.tun.autoroute 打开（需 /dev/net/tun）
  --port N                NGG 控制面端口（默认 9090，仅本机监听）
  --no-install-deps       不自动安装系统依赖（自行备好 curl/tar/openssl/nginx）
  --output PATH           backup 输出路径
  --yes                   uninstall 跳过交互确认
  --keep-data             uninstall 保留 /etc/ngg、数据目录与备份
  --skip-checksum         跳过 SHA256 校验（不推荐，仅用于自构建包）

支持发行版: Debian/Ubuntu/Mint/Kali/Deepin/UOS(apt)、RHEL/CentOS/Rocky/Alma/
Fedora/AmazonLinux(dnf/yum)、openSUSE/SLES(zypper)、Arch/Manjaro(pacman)。
Alpine(OpenRC) 无 systemd，请改用 docker 子命令。

安全提示: NGG 控制面本身仍只监听 127.0.0.1，公网入口由 Nginx 反向代理；
公网路径用 16 位随机后缀 + Cookie 门禁 + NGG Bearer Token 三重保护，
绑定域名后请务必 --with-tls 启用 HTTPS。不要把 9090 直接暴露到公网。
USAGE
}

need_root()    { [ "$(id -u)" -eq 0 ] || die "请用 root 运行（sudo $0 $*）"; }
need_cmd()     { command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"; }
need_systemd() { command -v systemctl >/dev/null 2>&1 || die "需要 systemd（systemctl）"; }

# ---- Multi-distro support -------------------------------------------------
detect_distro() {
  local id="" like="" pretty=""
  if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    id="${ID:-}"; like="${ID_LIKE:-}"; pretty="${PRETTY_NAME:-$id}"
  fi
  case "$id $like" in
    *debian*|*ubuntu*|*mint*|*raspbian*|*pop*|*kali*|*deepin*|*uos*|*elementary*)
      DISTRO=debian; PKG_TOOL=apt ;;
    *rhel*|*fedora*|*centos*|*rocky*|*almalinux*|*amzn*|*ol*|*scientific*|*virtuozzo*)
      DISTRO=rhel; PKG_TOOL=dnf
      if ! command -v dnf >/dev/null 2>&1 && command -v yum >/dev/null 2>&1; then PKG_TOOL=yum; fi ;;
    *suse*|*opensuse*|*sles*) DISTRO=suse;   PKG_TOOL=zypper ;;
    *arch*|*manjaro*|*endeavour*|*garuda*) DISTRO=arch;   PKG_TOOL=pacman ;;
    *alpine*) DISTRO=alpine; PKG_TOOL=apk ;;
    *) DISTRO=unknown; PKG_TOOL="" ;;
  esac
  log "发行版: ${pretty} → ${DISTRO} / ${PKG_TOOL:-未知}"
  # Alpine has no systemd. It is fine for the docker subcommand, but the
  # systemd install path must refuse it explicitly.
}

# Install a list of distro-specific package names.
pkg_install() {
  [ "$#" -gt 0 ] || return 0
  log "安装系统依赖: $*"
  case "$PKG_TOOL" in
    apt)
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -qq && apt-get install -y --no-install-recommends "$@" ;;
    dnf|yum) "$PKG_TOOL" install -y "$@" ;;
    zypper)  zypper --non-interactive install --no-recommends "$@" ;;
    pacman)  pacman -Sy --noconfirm --needed "$@" ;;
    apk)     apk add --no-cache "$@" ;;
    *) warn "无法识别包管理器，请手动安装: $*"; return 1 ;;
  esac
}

# Generic dependency name -> distro package name.
map_pkg() {
  case "$1:$DISTRO" in
    curl:*)            printf 'curl' ;;
    tar:*)             printf 'tar' ;;
    openssl:*)         printf 'openssl' ;;
    python3:*)         printf 'python3' ;;
    nginx:*)           printf 'nginx' ;;
    htpasswd:debian)   printf 'apache2-utils' ;;
    htpasswd:rhel)     printf 'httpd-tools' ;;
    htpasswd:suse)     printf 'apache2-utils' ;;
    htpasswd:arch)     printf 'apache-tools' ;;
    htpasswd:*)        printf 'apache2-utils' ;;
    certbot:debian)    printf 'certbot python3-certbot-nginx' ;;
    certbot:rhel)      printf 'certbot python3-certbot-nginx' ;;
    certbot:suse)      printf 'certbot python3-certbot-nginx' ;;
    certbot:arch)      printf 'certbot certbot-nginx' ;;
    certbot:*)         printf 'certbot' ;;
    ufw:*)             printf 'ufw' ;;
    firewalld:*)       printf 'firewalld' ;;
    *)                 printf '%s' "$1" ;;
  esac
}

# Only install what is actually missing; never touch the system when
# --no-install-deps is given.
ensure_deps() {
  [ "$NO_INSTALL_DEPS" -eq 1 ] && { log "跳过依赖安装（--no-install-deps）"; return 0; }
  [ "$PKG_TOOL" = "" ] && { warn "未知发行版：跳过自动安装依赖，缺失时脚本会明确报错"; return 0; }
  local missing=() p
  for p in curl tar openssl; do
    command -v "$p" >/dev/null 2>&1 || missing+=("$(map_pkg "$p")")
  done
  if ! command -v python3 >/dev/null 2>&1 && ! command -v jq >/dev/null 2>&1; then
    missing+=("$(map_pkg python3)")
  fi
  if [ "$WITH_NGINX" -eq 1 ]; then
    command -v nginx >/dev/null 2>&1 || missing+=("$(map_pkg nginx)")
    if [ "$WITH_TLS" -eq 1 ] && ! command -v certbot >/dev/null 2>&1; then
      # certbot map may return two packages
      read -r -a cb <<<"$(map_pkg certbot)"; missing+=("${cb[@]}")
    fi
  fi
  if [ "$CONFIGURE_FIREWALL" -eq 1 ]; then
    if [ "$DISTRO" = debian ]; then
      command -v ufw >/dev/null 2>&1 || missing+=("$(map_pkg ufw)")
    elif ! command -v firewall-cmd >/dev/null 2>&1; then
      missing+=("$(map_pkg firewalld)")
    fi
  fi
  if [ "${#missing[@]}" -gt 0 ]; then
    # shellcheck disable=SC2086
    pkg_install "${missing[@]}" || warn "部分依赖安装失败，后续步骤会如实报错"
  else
    log "系统依赖已齐备"
  fi
}

public_ip() {
  if [ -n "$PUBLIC_IP" ]; then printf '%s' "$PUBLIC_IP"; return 0; fi
  local ip=""
  ip="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
  [ -z "$ip" ] && ip="$(curl -fsS --max-time 5 https://ifconfig.me/ip 2>/dev/null || true)"
  if [ -z "$ip" ] && command -v ip >/dev/null 2>&1; then
    ip="$(ip -4 route get 1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')"
  fi
  [ -z "$ip" ] && ip="<VPS公网IP>"
  printf '%s' "$ip"
}

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# Read the token from a config file. yaml.v3 may rewrite it quoted or plain,
# so strip surrounding double quotes after taking the second field.
read_token() {
  local cfg="${1:-$CONF/ngg.yaml}" t=""
  [ -f "$cfg" ] || { printf ''; return 0; }
  t="$(awk '/^[[:space:]]*token:/{print $2; exit}' "$cfg")"
  t="${t#\"}"; t="${t%\"}"
  printf '%s' "$t"
}

make_tmp() { TMP="$(mktemp -d)"; }

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   ARCH=amd64 ;;
    aarch64|arm64)  ARCH=arm64 ;;
    armv7l|armv6l)  ARCH=arm ;;
    i386|i686)      ARCH=386 ;;
    *) die "不支持的 CPU 架构: $(uname -m)（可自行交叉编译后使用 --file）" ;;
  esac
  ASSET="ngg-${VERSION}-linux-${ARCH}.tar.gz"
}

# Download one release asset. Public URL first, GitHub API fallback for private repos.
fetch_asset() {
  local name="$1" out="$2"
  local auth=()
  [ -n "$TOKEN" ] && auth=(-H "Authorization: Bearer $TOKEN")
  local url="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/${name}"
  if curl -fsSL --retry 2 --connect-timeout 15 "${auth[@]+"${auth[@]}"}" -o "$out" "$url"; then
    if [ -s "$out" ] && [ "$(head -c 1 "$out")" != "<" ]; then
      return 0
    fi
  fi
  log "直链下载失败（私有仓库常见），改用 GitHub API: $name"
  local api="https://api.github.com/repos/${OWNER}/${REPO}"
  curl -fsSL --connect-timeout 15 "${auth[@]+"${auth[@]}"}" \
    -H "Accept: application/vnd.github+json" \
    "${api}/releases/tags/${VERSION}" -o "$TMP/release.json" \
    || die "无法读取 Release ${VERSION}；私有仓库请设置 GITHUB_TOKEN，或改用 --file"
  local asset_id=""
  if command -v python3 >/dev/null 2>&1; then
    asset_id="$(python3 - "$TMP/release.json" "$name" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as f:
        data = json.load(f)
    for a in data.get("assets", []):
        if a.get("name") == sys.argv[2]:
            print(a["id"])
            break
except Exception as exc:
    print("parse error: %s" % exc, file=sys.stderr)
PY
)"
  elif command -v jq >/dev/null 2>&1; then
    asset_id="$(jq -r --arg n "$name" '.assets[] | select(.name == $n) | .id' "$TMP/release.json" | head -n1)"
  else
    die "私有仓库下载需要 python3 或 jq；也可在有网机器下载后用 --file"
  fi
  [ -n "$asset_id" ] && [ "$asset_id" != "null" ] || die "Release ${VERSION} 中找不到资产 $name"
  curl -fsSL --connect-timeout 15 "${auth[@]+"${auth[@]}"}" \
    -H "Accept: application/octet-stream" \
    -o "$out" "${api}/releases/assets/${asset_id}" \
    || die "资产下载失败: $name"
  [ -s "$out" ] || die "下载内容为空: $name"
}

prepare_bundle() {
  make_tmp
  if [ -n "$FILE" ]; then
    [ -f "$FILE" ] || die "找不到本地包: $FILE"
    TARBALL="$TMP/$(basename "$FILE")"
    cp "$FILE" "$TARBALL"
    local dir; dir="$(cd "$(dirname "$FILE")" && pwd)"
    if [ -f "$dir/SHA256SUMS" ]; then
      cp "$dir/SHA256SUMS" "$TMP/SHA256SUMS"; CHECKSUMS="$TMP/SHA256SUMS"
    elif [ -f "$dir/$(basename "$FILE").sha256" ]; then
      cp "$dir/$(basename "$FILE").sha256" "$TMP/asset.sha256"; CHECKSUMS="$TMP/asset.sha256"
    fi
    log "使用本地包: $FILE"
  else
    [ -n "$VERSION" ] || die "请指定 --version vX.Y.Z 或 --file <tar.gz>"
    detect_arch
    TARBALL="$TMP/$ASSET"
    log "下载 Release 资产: $ASSET"
    fetch_asset "$ASSET" "$TARBALL"
    if fetch_asset "SHA256SUMS" "$TMP/SHA256SUMS"; then
      CHECKSUMS="$TMP/SHA256SUMS"
    elif fetch_asset "${ASSET}.sha256" "$TMP/asset.sha256"; then
      CHECKSUMS="$TMP/asset.sha256"
    else
      die "Release 中找不到 SHA256SUMS / ${ASSET}.sha256；如为自构建包可加 --skip-checksum"
    fi
  fi

  if [ "$SKIP_CHECKSUM" -eq 1 ]; then
    warn "已跳过 SHA256 校验（仅限你完全信任的包）"
  else
    [ -n "$CHECKSUMS" ] || die "找不到校验文件；可用 --skip-checksum 显式跳过"
    local expected=""
    case "$CHECKSUMS" in
      *SHA256SUMS)
        # sha256sum prints "hash  name" (text) or "hash *name" (binary);
        # some releases also prefix names with "./". Normalize all forms.
        expected="$(awk -v n="$(basename "$TARBALL")" '
          { f = $2; sub(/^\*/, "", f); sub(/^\.\//, "", f);
            if (f == n) { print $1; exit } }' "$CHECKSUMS")" ;;
      *)
        expected="$(awk 'NF >= 1 {print $1; exit}' "$CHECKSUMS")" ;;
    esac
    [ -n "$expected" ] || die "校验文件中没有 $(basename "$TARBALL") 的摘要"
    printf '%s  %s\n' "$expected" "$TARBALL" | sha256sum -c - >/dev/null \
      || die "SHA256 校验失败，已中止（包可能被篡改或损坏）"
    log "SHA256 校验通过"
  fi

  mkdir -p "$TMP/pkg"
  tar -xzf "$TARBALL" -C "$TMP/pkg" || die "解包失败：不是有效的 NGG Release tar.gz"
  if [ -d "$TMP/pkg/ngg" ]; then PKG="$TMP/pkg/ngg"; else PKG="$TMP/pkg"; fi
  [ -f "$PKG/nggd" ] || die "包内缺少 nggd 二进制（目录结构不符合 Release 规范）"
}

ensure_user_dirs() {
  if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    # nologin lives in /sbin on RHEL-family, /usr/sbin on Debian-family.
    local nologin="/usr/sbin/nologin"
    [ -x /sbin/nologin ] && nologin="/sbin/nologin"
    if command -v useradd >/dev/null 2>&1; then
      useradd --system --no-create-home --shell "$nologin" "$SERVICE_USER" \
        || die "创建系统用户 $SERVICE_USER 失败"
    elif command -v adduser >/dev/null 2>&1; then
      adduser -S -D -H -s "$nologin" "$SERVICE_USER" \
        || die "创建系统用户 $SERVICE_USER 失败"
    else
      die "找不到 useradd/adduser，无法创建系统用户 $SERVICE_USER"
    fi
  fi
  install -d -m 0755 "$PREFIX" "$PREFIX/web" "$CONF" "$CONF/cores.d" "$DATA" "$LOGDIR"
  chown -R "$SERVICE_USER":"$SERVICE_USER" "$DATA" "$LOGDIR" "$CONF" "$PREFIX/web"
}

copy_payload() {
  install -m 0755 "$PKG/nggd" "$PREFIX/nggd"
  if [ -d "$PKG/web-dist" ]; then
    rm -rf "$PREFIX/web/dist"
    cp -r "$PKG/web-dist" "$PREFIX/web/dist"
  elif [ -d "$PKG/web/dist" ]; then
    rm -rf "$PREFIX/web/dist"
    cp -r "$PKG/web/dist" "$PREFIX/web/dist"
  else
    warn "包内没有 Web UI（web-dist）；管理界面将显示『尚未构建』状态页"
  fi
  if [ -d "$PKG/configs/cores.d" ]; then
    cp -r "$PKG/configs/cores.d/." "$CONF/cores.d/"
  fi
  if [ -d "$PKG/scripts" ]; then
    # Keep the full scripts/ tree so /api/v1/update/apply can find
    # <prefix>/scripts/ngg-update.sh for the in-app updater.
    mkdir -p "$PREFIX/scripts"
    cp -r "$PKG/scripts/." "$PREFIX/scripts/"
    if [ -f "$PREFIX/scripts/deploy-vps.sh" ]; then chmod 0755 "$PREFIX/scripts/deploy-vps.sh"; fi
    if [ -f "$PREFIX/scripts/ngg-update.sh" ]; then chmod 0755 "$PREFIX/scripts/ngg-update.sh"; fi
  elif [ -f "$PKG/scripts/deploy-vps.sh" ]; then
    install -m 0755 "$PKG/scripts/deploy-vps.sh" "$PREFIX/deploy-vps.sh"
  fi
  chown -R "$SERVICE_USER":"$SERVICE_USER" "$CONF" "$PREFIX/web"
}

render_config() {
  local cfg="$CONF/ngg.yaml"
  if [ -f "$cfg" ]; then
    log "保留已有配置 $cfg（不覆盖 Token）"
    API_TOKEN="$(read_token "$cfg")"
    [ -n "$API_TOKEN" ] || die "无法从 $cfg 读取 API Token；请检查 token 字段"
    return 0
  fi
  local tpl="$PKG/configs/ngg.example.yaml"
  [ -f "$tpl" ] || tpl="$PKG/configs/ngg.yaml.example"
  [ -f "$tpl" ] || die "包内缺少配置模板 configs/ngg.example.yaml"
  API_TOKEN="$(random_hex)"
  sed \
    -e "s#^\([[:space:]]*dir:[[:space:]]*\)data[[:space:]]*\$#\1$DATA#" \
    -e "s#^\([[:space:]]*dir:[[:space:]]*\)data/logs[[:space:]]*\$#\1$LOGDIR#" \
    -e "s#^\([[:space:]]*web_dir:[[:space:]]*\).*\$#\1$PREFIX/web/dist#" \
    -e "s#^\([[:space:]]*descriptor_dir:[[:space:]]*\).*\$#\1$CONF/cores.d#" \
    -e "s#^\([[:space:]]*token:[[:space:]]*\).*\$#\1\"$API_TOKEN\"#" \
    "$tpl" > "$cfg"
  if [ "$ENABLE_TUN" -eq 1 ]; then
    [ -e /dev/net/tun ] || warn "/dev/net/tun 不存在：统一 TUN 不会生效（userspace 入口仍可用）"
    sed -i "s#^\([[:space:]]*autoroute:[[:space:]]*\)false#\1true#" "$cfg"
    log "已开启 network.tun.autoroute（内核安装后可在 Web UI 验证）"
  fi
  chown "$SERVICE_USER":"$SERVICE_USER" "$cfg"
  chmod 0600 "$cfg"
  log "已生成 $cfg（绝对路径 + 随机 API Token，权限 0600）"
}

write_unit() {
  cat > "$UNIT" <<UNIT
[Unit]
Description=NGG Next-Generation Gateway
Documentation=https://github.com/${OWNER}/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$PREFIX
Environment=NGG_PORT=$PORT
ExecStart=$PREFIX/nggd -config $CONF/ngg.yaml
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
# Stage 2 统一 TUN / 低端口绑定需要；不启用 TUN 时可移除这两项能力
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
NoNewPrivileges=false
# 最小可写面：配置、状态与日志目录
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$CONF $DATA $LOGDIR
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT
  chmod 0644 "$UNIT"
  systemctl daemon-reload
}

wait_healthy() {
  local token="$1" i
  need_cmd curl
  for i in $(seq 1 30); do
    if curl -fsS --max-time 2 -H "Authorization: Bearer $token" \
      "http://127.0.0.1:${PORT}/api/v1/health" >/dev/null 2>&1; then
      log "健康检查通过 (/api/v1/health)"
      return 0
    fi
    sleep 1
  done
  warn "健康检查未通过，最近日志："
  journalctl -u "$SERVICE" -n 60 --no-pager 2>/dev/null || true
  return 1
}

configure_firewall() {
  log "配置防火墙（只放行 SSH / 80 / 443 / 公网入口 ${WEB_PORT}，绝不放行 9090）"
  if command -v ufw >/dev/null 2>&1; then
    ufw allow OpenSSH >/dev/null 2>&1 || ufw allow 22/tcp >/dev/null 2>&1 || true
    ufw allow 80/tcp >/dev/null 2>&1 || true
    ufw allow 443/tcp >/dev/null 2>&1 || true
    [ "$WITH_NGINX" -eq 1 ] && ufw allow "${WEB_PORT}/tcp" >/dev/null 2>&1 || true
    if ufw status 2>/dev/null | grep -qi '^Status: active'; then
      ufw reload >/dev/null 2>&1 || true
      log "ufw 规则已应用（防火墙原本就是开启状态）"
    else
      warn "ufw 当前未启用，规则已写入但未激活；确认 SSH 放行后再执行: ufw enable"
    fi
  elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-service=ssh >/dev/null 2>&1 || true
    firewall-cmd --permanent --add-service=http >/dev/null 2>&1 || true
    firewall-cmd --permanent --add-service=https >/dev/null 2>&1 || true
    if [ "$WITH_NGINX" -eq 1 ]; then
      firewall-cmd --permanent --add-port="${WEB_PORT}/tcp" >/dev/null 2>&1 || true
    fi
    firewall-cmd --reload >/dev/null 2>&1 || true
    log "firewalld 规则已应用"
  else
    warn "未检测到活动的 ufw/firewalld，请在云厂商安全组放行 22/80/443/${WEB_PORT}（按实际需要）"
  fi
}

# Append one nginx server block. The secret path sets a cookie; all other
# paths require that cookie. This keeps the Web UI's absolute /assets and /api
# URLs working while hiding the panel behind a 16-character random suffix.
# NOTE: no auth_basic here on purpose — browsers would replace the Web UI's
# "Authorization: Bearer <token>" header with Basic credentials and nginx
# would return 401 before the request ever reaches NGG.
render_gate_server() {
  local listen="$1" tls="$2" file="$3"
  {
    printf 'server {\n'
    printf '    listen %s;\n' "$listen"
    if [ -n "$tls" ]; then
      printf '    ssl_certificate     /etc/letsencrypt/live/%s/fullchain.pem;\n' "$DOMAIN"
      printf '    ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;\n' "$DOMAIN"
      printf '    ssl_protocols TLSv1.2 TLSv1.3;\n'
    fi
    printf '    server_name %s;\n' "${DOMAIN:-_}"
    printf '    client_max_body_size 32m;\n'
    printf '    location /.well-known/acme-challenge/ { root /var/www/html; }\n'
    printf '    location = /%s {\n' "$GATE_PATH"
    local secure=""
    [ -n "$tls" ] && secure="; Secure"
    printf '        add_header Set-Cookie "ngg_gate=%s; Path=/; HttpOnly; SameSite=Strict%s; Max-Age=31536000" always;\n' "$GATE_PATH" "$secure"
    printf '        return 302 /;\n'
    printf '    }\n'
    printf '    location / {\n'
    printf '        if ($ngg_gate_ok = 0) { return 404; }\n'
    printf '        proxy_pass http://127.0.0.1:%s;\n' "$PORT"
    printf '        proxy_http_version 1.1;\n'
    printf '        proxy_set_header Host $host;\n'
    printf '        proxy_set_header X-Real-IP $remote_addr;\n'
    printf '        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n'
    printf '        proxy_set_header X-Forwarded-Proto $scheme;\n'
    printf '        proxy_set_header Upgrade $http_upgrade;\n'
    printf '        proxy_set_header Connection "upgrade";\n'
    printf '        proxy_read_timeout 300s;\n'
    printf '        proxy_send_timeout 300s;\n'
    printf '    }\n'
    printf '}\n\n'
  } >> "$file"
}

setup_nginx() {
  [ "$WITH_NGINX" -eq 1 ] || return 0
  if ! command -v nginx >/dev/null 2>&1; then
    warn "未安装 nginx，跳过公网入口；可加 --no-install-deps 之外的方式先安装 nginx"
    return 0
  fi
  need_cmd openssl
  # 16-character random suffix (8 random bytes -> 16 hex chars).
  GATE_PATH="$(openssl rand -hex 8)"
  local name="${DOMAIN:-_}"
  local site link=""
  if [ -d /etc/nginx/sites-enabled ]; then
    mkdir -p /etc/nginx/sites-available /etc/nginx/sites-enabled
    site="/etc/nginx/sites-available/ngg.conf"
    link="/etc/nginx/sites-enabled/ngg.conf"
  else
    mkdir -p /etc/nginx/conf.d
    site="/etc/nginx/conf.d/ngg.conf"
  fi

  {
    printf '# Generated by deploy-vps.sh. NGG still binds to 127.0.0.1 only.\n'
    printf 'map $cookie_ngg_gate $ngg_gate_ok {\n'
    printf '    default 0;\n'
    printf '    "%s" 1;\n' "$GATE_PATH"
    printf '}\n\n'
  } > "$site"

  # One HTTP server: public port for IP access (+ port 80 for ACME / domain).
  render_gate_server "0.0.0.0:${WEB_PORT}" "" "$site"
  if [ -n "$DOMAIN" ] && [ "$WEB_PORT" != "80" ]; then
    # Second plain-HTTP listener for the domain (ACME challenge + redirect-free access).
    render_gate_server "80" "" "$site"
  fi

  [ -n "$link" ] && ln -sfn "$site" "$link"
  if ! nginx -t >/dev/null 2>&1; then
    [ -n "$link" ] && rm -f "$link"
    rm -f "$site"
    die "nginx -t 失败，已回滚站点配置（检查是否已有同名 map / 端口冲突）"
  fi
  systemctl reload nginx >/dev/null 2>&1 || systemctl restart nginx >/dev/null 2>&1 || nginx -s reload >/dev/null 2>&1 || true

  if [ "$WITH_TLS" -eq 1 ]; then
    [ -n "$DOMAIN" ] || die "--with-tls 需要 --domain <FQDN>"
    mkdir -p /var/www/html
    if command -v certbot >/dev/null 2>&1; then
      local mail_args=(--register-unsafely-without-email)
      [ -n "$EMAIL" ] && mail_args=(-m "$EMAIL")
      if certbot certonly --webroot -w /var/www/html -d "$DOMAIN" \
          --non-interactive --agree-tos "${mail_args[@]+"${mail_args[@]}"}"; then
        if [ -f "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" ]; then
          render_gate_server "443 ssl" "yes" "$site"
          if nginx -t >/dev/null 2>&1; then
            systemctl reload nginx >/dev/null 2>&1 || systemctl restart nginx >/dev/null 2>&1 || true
            log "SSL 证书已签发并绑定 https://${DOMAIN}/${GATE_PATH}"
          else
            warn "443 站点 nginx -t 失败；证书已签发但 HTTPS 站点未启用，请检查 $site"
          fi
        fi
      else
        warn "certbot 未成功（域名是否已解析到本机？80 端口是否被占用？）；当前仍为 HTTP"
      fi
    else
      warn "未安装 certbot；请先安装后执行: certbot certonly --webroot -w /var/www/html -d $DOMAIN"
    fi
  fi

  local ip; ip="$(public_ip)"
  local http_url="http://${ip}:${WEB_PORT}/${GATE_PATH}"
  {
    printf 'NGG public web gate (generated by deploy-vps.sh)\n'
    printf 'secret path : /%s\n' "$GATE_PATH"
    printf 'IP access   : %s\n' "$http_url"
    [ -n "$DOMAIN" ] && printf 'domain      : http://%s/%s\n' "$DOMAIN" "$GATE_PATH"
    [ "$WITH_TLS" -eq 1 ] && printf 'HTTPS       : https://%s/%s\n' "$DOMAIN" "$GATE_PATH"
    printf 'API token   : %s\n' "$API_TOKEN"
    printf 'NOTE: open the secret path ONCE in your browser to set the gate cookie,\n'
    printf '      then the Web UI loads from /. NGG Bearer token is still required.\n'
  } > "$CONF/web-gate.txt"
  chmod 0600 "$CONF/web-gate.txt"
  log "公网入口已就绪: $http_url"
  if [ -n "$DOMAIN" ]; then log "域名入口: http://${DOMAIN}/${GATE_PATH}（加 --with-tls 可升级为 HTTPS）"; fi
  log "门禁信息已保存到 $CONF/web-gate.txt (0600)"
}

print_access() {
  echo
  log "===== NGG 部署完成 ====="
  log "服务:      systemctl status $SERVICE"
  log "日志:      journalctl -u $SERVICE -f"
  log "配置:      $CONF/ngg.yaml (0600)"
  log "数据:      $DATA"
  log "本机地址:  http://127.0.0.1:${PORT}/?token=${API_TOKEN}"
  log "SSH 隧道:  ssh -L ${PORT}:127.0.0.1:${PORT} <user>@<vps>  然后浏览器打开上面的地址"
  if [ -n "$GATE_PATH" ]; then
    log "公网入口:  http://$(public_ip):${WEB_PORT}/${GATE_PATH}"
    [ -n "$DOMAIN" ] && log "域名入口:  http://${DOMAIN}/${GATE_PATH}"
    [ "$WITH_TLS" -eq 1 ] && log "HTTPS:     https://${DOMAIN}/${GATE_PATH}"
    log "门禁信息:  $CONF/web-gate.txt（首次打开秘密路径会写入 Cookie，之后从 / 访问）"
    log "⚠ 秘密路径 + Cookie 只是外层门禁，NGG Bearer Token 仍然必需；公网请尽快启用 HTTPS"
  fi
  log "⚠ 请把上面的 Token 保存到密码管理器，不要提交到仓库、不要发到聊天里"
  echo
}

cmd_install() {
  need_root; need_systemd
  detect_distro
  [ "$DISTRO" = "alpine" ] && die "Alpine 使用 OpenRC 而非 systemd；请改用: $0 docker（Docker Compose 部署）"
  ensure_deps
  need_cmd curl; need_cmd tar; need_cmd openssl
  prepare_bundle
  ensure_user_dirs
  copy_payload
  render_config
  write_unit
  [ "$CONFIGURE_FIREWALL" -eq 1 ] && configure_firewall
  setup_nginx
  systemctl enable "$SERVICE" >/dev/null 2>&1
  systemctl restart "$SERVICE"
  if ! wait_healthy "$API_TOKEN"; then
    die "服务未能健康运行，请按上方日志排查（配置路径 / 端口占用 / 依赖缺失）"
  fi
  print_access
}

cmd_upgrade() {
  need_root; need_systemd
  [ -f "$UNIT" ] || die "未检测到 systemd 服务，请先执行 install"
  prepare_bundle
  cmd_backup >/dev/null
  cp -a "$PREFIX/nggd" "$PREFIX/nggd.bak"
  if [ -d "$PREFIX/web/dist" ]; then
    rm -rf "$PREFIX/web/dist.bak"; cp -a "$PREFIX/web/dist" "$PREFIX/web/dist.bak"
  fi
  systemctl stop "$SERVICE" || true
  copy_payload
  systemctl start "$SERVICE"
  if ! wait_healthy "$(read_token)"; then
    warn "升级后健康检查失败，回滚二进制与 Web UI"
    systemctl stop "$SERVICE" || true
    cp -a "$PREFIX/nggd.bak" "$PREFIX/nggd"
    if [ -d "$PREFIX/web/dist.bak" ]; then
      rm -rf "$PREFIX/web/dist"; cp -a "$PREFIX/web/dist.bak" "$PREFIX/web/dist"
    fi
    systemctl start "$SERVICE" || true
    die "已回滚到升级前的二进制；请查看 journalctl -u $SERVICE 排查"
  fi
  rm -f "$PREFIX/nggd.bak"; rm -rf "$PREFIX/web/dist.bak"
  log "升级完成，配置与数据未改动；备份位于 $BACKUPDIR"
}

cmd_docker() {
  # Docker path: no systemd, no Go/Node on the host, works on Alpine too.
  need_cmd docker
  local compose=""
  if docker compose version >/dev/null 2>&1; then compose="docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then compose="docker-compose"
  else die "需要 Docker Compose v2（docker compose）或 docker-compose"; fi
  [ -f "$REPO_ROOT/docker-compose.yml" ] || die "找不到 $REPO_ROOT/docker-compose.yml；请在仓库根目录运行"
  detect_distro
  cd "$REPO_ROOT"
  if [ ! -f .env ]; then
    cp .env.example .env
    log "已生成 .env（请按需编辑 NGG_TOKEN / TZ）"
  fi
  # Generate a random token if the .env has no explicit one.
  local token
  token="$(awk -F= '/^NGG_TOKEN=/{print $2; exit}' .env)"
  if [ -z "$token" ]; then
    token="$(random_hex)"
    sed -i "s#^NGG_TOKEN=.*#NGG_TOKEN=${token}#" .env
    API_TOKEN="$token"
    log "已生成随机 API Token 并写入 .env"
  else
    API_TOKEN="$token"
  fi
  log "启动容器: $compose up -d --build"
  $compose up -d --build || die "docker compose 启动失败"
  log "容器状态:"; $compose ps
  log "本机地址: http://127.0.0.1:${PORT}/?token=${API_TOKEN}"
  if [ "$WITH_NGINX" -eq 1 ]; then
    if [ "$(id -u)" -ne 0 ]; then
      warn "公网入口需要 root 配置宿主机 Nginx，请改用: sudo $0 docker --public ..."
    else
      setup_nginx
      [ "$CONFIGURE_FIREWALL" -eq 1 ] && configure_firewall
      [ -n "$GATE_PATH" ] && log "公网入口: http://$(public_ip):${WEB_PORT}/${GATE_PATH}"
    fi
  fi
  log "提示: Docker Desktop 请把 docker-compose.yml 里的 network_mode: host 换成注释掉的 ports 映射"
}

cmd_backup() {
  need_root; need_cmd tar
  mkdir -p "$BACKUPDIR"
  local out="${OUTPUT:-$BACKUPDIR/ngg-backup-$(date +%Y%m%dT%H%M%S).tar.gz}"
  local paths=()
  for p in "$CONF" "$DATA" "$PREFIX/web/dist" "$UNIT"; do
    [ -e "$p" ] && paths+=("${p#/}")
  done
  [ "${#paths[@]}" -gt 0 ] || die "没有可备份的内容"
  tar -czf "$out" -C / "${paths[@]}" 2>/dev/null
  chmod 0600 "$out"
  log "备份完成: $out（含 Token 与节点凭据，请加密保存）"
  printf '%s\n' "$out"
}

cmd_restore() {
  need_root; need_cmd tar
  [ -n "$FILE" ] || die "restore 需要 --file <备份 tar.gz>"
  [ -f "$FILE" ] || die "找不到备份: $FILE"
  systemctl stop "$SERVICE" 2>/dev/null || true
  tar -xzf "$FILE" -C / || die "恢复失败：备份包无效"
  if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    chown -R "$SERVICE_USER":"$SERVICE_USER" "$CONF" "$DATA" "$PREFIX/web" 2>/dev/null || true
  fi
  [ -f "$CONF/ngg.yaml" ] && chmod 0600 "$CONF/ngg.yaml"
  systemctl daemon-reload 2>/dev/null || true
  systemctl start "$SERVICE"
  local token; token="$(read_token)"
  wait_healthy "$token" || die "恢复后健康检查失败"
  log "恢复完成: $FILE"
}

cmd_status() {
  systemctl status "$SERVICE" --no-pager -l 2>/dev/null || true
  if [ -f "$CONF/ngg.yaml" ]; then
    local token; token="$(read_token)"
    if [ -n "$token" ] && curl -fsS --max-time 3 -H "Authorization: Bearer $token" \
      "http://127.0.0.1:${PORT}/api/v1/health" >/dev/null 2>&1; then
      log "健康检查: OK  http://127.0.0.1:${PORT}/?token=${token}"
    else
      warn "健康检查失败；执行 journalctl -u $SERVICE -n 80 查看原因"
    fi
  else
    warn "找不到配置 $CONF/ngg.yaml"
  fi
  if [ -f "$CONF/web-gate.txt" ]; then
    log "公网门禁信息 ($CONF/web-gate.txt):"
    grep -E 'secret path|IP access|domain|HTTPS' "$CONF/web-gate.txt" | sed 's/^/[ngg]   /'
  fi
}

listen_entries() {
  if command -v ss >/dev/null 2>&1; then ss -ltn 2>/dev/null
  elif command -v netstat >/dev/null 2>&1; then netstat -ltn 2>/dev/null
  else return 1
  fi
}

cmd_doctor() {
  local ok=0 bad=0
  check() { if eval "$2" >/dev/null 2>&1; then log "[OK]   $1"; ok=$((ok+1)); else warn "[FAIL] $1"; bad=$((bad+1)); fi; }
  check "nggd 二进制存在且可执行" "[ -x $PREFIX/nggd ]"
  check "主配置存在且权限 0600" "[ -f $CONF/ngg.yaml ] && [ \"\$(stat -c %a $CONF/ngg.yaml)\" = 600 ]"
  check "配置使用绝对路径 storage.dir" "grep -qE '^[[:space:]]*dir:[[:space:]]*/' $CONF/ngg.yaml"
  check "配置使用绝对路径 server.web_dir" "grep -qE '^[[:space:]]*web_dir:[[:space:]]*/' $CONF/ngg.yaml"
  check "systemd unit 存在" "[ -f $UNIT ]"
  check "服务已启用" "systemctl is-enabled $SERVICE"
  check "服务正在运行" "systemctl is-active $SERVICE"
  check "Web UI 已构建" "[ -f $PREFIX/web/dist/index.html ]"
  check "控制面端口在监听" "listen_entries | grep -qE '127.0.0.1:${PORT}'"
  check "控制面未监听公网" "! listen_entries | grep -qE '0\\.0\\.0\\.0:${PORT}|\\*:${PORT}'"
  check "/dev/net/tun 可用（TUN 可选）" "[ -e /dev/net/tun ]"
  check "公网门禁信息存在（如启用 --public）" "[ ! -f $CONF/web-gate.txt ] || [ -s $CONF/web-gate.txt ]"
  check "Nginx 配置语法正确（如已安装）" "! command -v nginx >/dev/null 2>&1 || nginx -t"
  check "磁盘剩余 > 1GB" "[ \$(df -Pk $DATA | awk 'NR==2{print \$4}') -gt 1048576 ]"
  log "体检结果: $ok 项通过 / $bad 项未通过（TUN 未通过不影响 userspace 入口）"
}

cmd_uninstall() {
  need_root
  if [ "$ASSUME_YES" -ne 1 ]; then
    warn "即将停止并移除 $SERVICE 服务与 $PREFIX 程序目录"
    [ "$KEEP_DATA" -eq 1 ] && log "--keep-data 已指定：保留 $CONF 与 $DATA"
    printf '确认继续? [y/N] '; read -r ans
    case "$ans" in y|Y|yes|YES) ;; *) log "已取消"; exit 0 ;; esac
  fi
  systemctl stop "$SERVICE" 2>/dev/null || true
  systemctl disable "$SERVICE" 2>/dev/null || true
  rm -f "$UNIT" /etc/nginx/sites-enabled/ngg.conf /etc/nginx/sites-available/ngg.conf /etc/nginx/conf.d/ngg.conf
  systemctl daemon-reload 2>/dev/null || true
  command -v nginx >/dev/null 2>&1 && systemctl reload nginx >/dev/null 2>&1 || true
  rm -rf "$PREFIX"
  if [ "$KEEP_DATA" -ne 1 ]; then
    rm -rf "$CONF" "$DATA" "$LOGDIR"
    log "已删除配置与数据（不可恢复；备份目录 $BACKUPDIR 未删除）"
  else
    log "已保留 $CONF 与 $DATA（含 Token 与凭据，请自行处理）"
  fi
  log "卸载完成；系统用户 $SERVICE_USER 未自动删除，如需清理: userdel $SERVICE_USER"
}

[ $# -gt 0 ] || { usage; exit 2; }
CMD="$1"; shift
while [ $# -gt 0 ]; do
  case "$1" in
    --version)         VERSION="${2:-}"; shift 2 ;;
    --file)            FILE="${2:-}"; shift 2 ;;
    --token)           TOKEN="${2:-}"; shift 2 ;;
    --domain)          DOMAIN="${2:-}"; WITH_NGINX=1; shift 2 ;;
    --public)          PUBLIC_MODE=1; WITH_NGINX=1; shift ;;
    --web-port)        WEB_PORT="${2:-}"; shift 2 ;;
    --public-ip)       PUBLIC_IP="${2:-}"; shift 2 ;;
    --with-nginx)      WITH_NGINX=1; shift ;;
    --with-tls)        WITH_TLS=1; WITH_NGINX=1; shift ;;
    --email)           EMAIL="${2:-}"; shift 2 ;;
    --configure-firewall) CONFIGURE_FIREWALL=1; shift ;;
    --no-install-deps) NO_INSTALL_DEPS=1; shift ;;
    --enable-tun)      ENABLE_TUN=1; shift ;;
    --port)            PORT="${2:-}"; shift 2 ;;
    --output)          OUTPUT="${2:-}"; shift 2 ;;
    --yes|-y)          ASSUME_YES=1; shift ;;
    --keep-data)       KEEP_DATA=1; shift ;;
    --skip-checksum)   SKIP_CHECKSUM=1; shift ;;
    -h|--help)         usage; exit 0 ;;
    *)                 die "未知参数: $1（用 --help 查看用法）" ;;
  esac
done

case "$CMD" in
  install)   cmd_install ;;
  docker)    cmd_docker ;;
  upgrade)   cmd_upgrade ;;
  backup)    cmd_backup ;;
  restore)   cmd_restore ;;
  status)    cmd_status ;;
  doctor)    cmd_doctor ;;
  uninstall) cmd_uninstall ;;
  *)         usage; exit 2 ;;
esac
