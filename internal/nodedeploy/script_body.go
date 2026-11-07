package nodedeploy

// The kernel-specific middle (step 5), the shared config/validate steps, the
// node-service installer, the two units, the firewall step, the operator CLI
// and the self-test.

const realityBodyTpl = `# ---------------------------------------------------------------- 5 凭据
step 5 "校验 REALITY 密钥对"
REALITY_PRIVATE="{{REALITY_PRIVATE}}"
REALITY_PUBLIC="{{REALITY_PUBLIC}}"
[ -n "$REALITY_PRIVATE" ] || die 5 "缺少 REALITY 私钥"
PUB_LINE="$("$KERNEL_BIN" x25519 -i "$REALITY_PRIVATE" 2>/dev/null | grep -i 'public' | head -1)"
GOT_PUB="$(printf '%s' "$PUB_LINE" | sed -E 's/^[^:]*:[[:space:]]*//' | tr -d '[:space:]')"
if [ -z "$GOT_PUB" ]; then
  note "内核未输出公钥，跳过密钥对校验（客户端请以面板显示的公钥为准）"
elif [ "$GOT_PUB" != "$REALITY_PUBLIC" ]; then
  die 5 "REALITY 密钥对不匹配（面板 $REALITY_PUBLIC / 内核 $GOT_PUB）"
else
  log "密钥对校验通过：$GOT_PUB（shortId={{SHORT_ID}}）"
fi
ok 5

`

const tlsBodyTpl = `# ---------------------------------------------------------------- 5 凭据
step 5 "生成自签 TLS 证书（CN=${SNI}，3650 天）"
install -d -m 0700 "{{NODE_DIR}}/tls" || die 5 "无法创建 {{NODE_DIR}}/tls"
if [ -s "{{NODE_DIR}}/tls/cert.pem" ] && [ -s "{{NODE_DIR}}/tls/key.pem" ]; then
  log "已存在证书，复用（如需换新请先删除 {{NODE_DIR}}/tls）"
else
  openssl req -x509 -nodes -newkey rsa:2048 -days 3650 -subj "/CN=${SNI}" \
    -keyout "{{NODE_DIR}}/tls/key.pem" -out "{{NODE_DIR}}/tls/cert.pem" >/dev/null 2>&1 \
    || die 5 "证书生成失败"
fi
chmod 600 "{{NODE_DIR}}/tls/key.pem"
chmod 644 "{{NODE_DIR}}/tls/cert.pem"
note "自签证书不会被公共客户端信任，客户端必须开启 allowInsecure / skip-cert-verify"
ok 5

`

const profileBodyTpl = `# ---------------------------------------------------------------- 5 凭据
step 5 "确认绑定档案"
note "绑定档案「{{PROFILE_NAME}}」，内容由控制面提供，脚本原样写入，不做字段改写"
ok 5

`

const configTpl = `# ---------------------------------------------------------------- 6 配置
step 6 "写入内核配置 ${CONFIG}"
cat > "$CONFIG" <<'` + configDelimiter + `'
{{CONFIG}}
` + configDelimiter + `
chmod 600 "$CONFIG" || die 6 "无法设置配置权限"
ok 6

# ---------------------------------------------------------------- 7 校验
step 7 "用内核校验配置"
# A -test run that hangs used to leave the whole install stuck here forever with
# no output at all, which looks exactly like a broken deploy. Bound it and say
# which config failed, so the operator can see the kernel's complaint.
if command -v timeout >/dev/null 2>&1; then
  TEST_OUT="$(timeout 45 "$KERNEL_BIN" run -test -config "$CONFIG" 2>&1)"
  TEST_RC=$?
else
  TEST_OUT="$("$KERNEL_BIN" run -test -config "$CONFIG" 2>&1)"
  TEST_RC=$?
fi
if [ "$TEST_RC" -ne 0 ]; then
  printf '%s\n' "$TEST_OUT" | tail -8
  if [ "$TEST_RC" -eq 124 ]; then
    die 7 "内核校验超时（45 秒）：配置里可能有需要联网/加载大文件的规则，或内核本身卡住"
  fi
  die 7 "配置校验未通过（见上方内核输出）：可在面板里改用默认 REALITY 模式，或修正「协议配置」文件内容后重新生成脚本"
fi
log "校验通过：$(printf '%s' "$TEST_OUT" | tail -1)"
ok 7

`

// installServiceTpl installs the node API daemon and its certificates. This is
// the piece that replaces the SSH channel: after this step the panel talks to
// the node over mTLS and never needs a shell on the box.
//
// The binary is downloaded from the release and verified against the digest
// the panel embedded. A release build always carries the digests; a dev build
// does not, and says so instead of pretending it verified something.
const installServiceTpl = `# ---------------------------------------------------------------- 8 节点服务
step 8 "安装节点控制服务 ${NODE_BIN}"
case "$NGG_ARCH" in
  amd64) WANT_SHA="$SHA_AMD64" ;;
  arm64) WANT_SHA="$SHA_ARM64" ;;
esac
INSTALLED_VERSION=""
if [ -x "$NODE_BIN" ]; then
  INSTALLED_VERSION="$("$NODE_BIN" -version 2>/dev/null || true)"
fi
if [ "$INSTALLED_VERSION" = "ngg-node {{NGG_VERSION}}" ]; then
  note "已存在同版本节点服务（$INSTALLED_VERSION），跳过下载"
else
  NODE_BIN_URL="${NODE_BIN_BASE}-${NGG_ARCH}"
  log "下载节点服务：$NODE_BIN_URL"
  rm -f /tmp/ngg-node.pkg
  curl -fsSL --retry 3 --connect-timeout 10 -o /tmp/ngg-node.pkg "$NODE_BIN_URL" \
    || die 8 "节点服务下载失败（检查该版本是否已发布 ${NGG_ARCH} 资产）"
  if [ -n "$WANT_SHA" ]; then
    GOT_SHA="$(sha256sum /tmp/ngg-node.pkg | awk '{print $1}')"
    if [ "$GOT_SHA" != "$WANT_SHA" ]; then
      rm -f /tmp/ngg-node.pkg
      die 8 "节点服务校验失败（面板 $WANT_SHA / 下载 $GOT_SHA），已丢弃未执行"
    fi
    log "SHA256 校验通过：$GOT_SHA"
  else
    note "本次面板构建没有为 ${NGG_ARCH} 内置摘要：只按 HTTPS 校验来源，发布版会强制校验"
  fi
  install -m 0755 /tmp/ngg-node.pkg "$NODE_BIN" || die 8 "节点服务安装失败"
  rm -f /tmp/ngg-node.pkg
fi
"$NODE_BIN" -version >/dev/null 2>&1 || die 8 "节点服务无法执行（$("$NODE_BIN" -version 2>&1 | head -1)）"
ok 8

# ---------------------------------------------------------------- 9 证书 + API 单元
step 9 "写入节点证书并启动 ${API_UNIT}"
# 迁移：v2.12.x 把内核跑在 ${API_UNIT} 里，并额外装了上报探针。两者都退出历史。
if [ -f "/etc/systemd/system/${API_UNIT}" ] && grep -qE '^ExecStart=/usr/local/bin/(ngg-)?xray run' "/etc/systemd/system/${API_UNIT}" 2>/dev/null; then
  systemctl disable --now "$API_UNIT" >/dev/null 2>&1 || true
  note "检测到旧版 ${API_UNIT}（内核单元）：已停止，内核改由 ${CORE_UNIT} 承载"
fi
if [ -f /etc/systemd/system/ngg-probe.service ]; then
  systemctl disable --now ngg-probe.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/ngg-probe.service /usr/local/bin/ngg-probe
  note "已移除 v2.12 的上报探针：节点不再向面板推数据，改由面板连入"
fi

install -d -m 0750 "$PKI_DIR" || die 9 "无法创建 $PKI_DIR"
umask 077
cat > "$CERT" <<'` + certDelimiter + `'
{{NODE_CERT_PEM}}
` + certDelimiter + `
cat > "$KEY" <<'` + keyDelimiter + `'
{{NODE_KEY_PEM}}
` + keyDelimiter + `
umask 022
cat > "$CA" <<'` + caDelimiter + `'
{{CA_PEM}}
` + caDelimiter + `
chmod 644 "$CERT" "$CA" || die 9 "无法设置证书权限"
chmod 600 "$KEY" || die 9 "无法设置私钥权限"
openssl x509 -in "$CERT" -noout -subject >/dev/null 2>&1 || die 9 "节点证书无法解析"
log "证书已写入：CN=$(openssl x509 -in "$CERT" -noout -subject 2>/dev/null | sed -E 's/^subject=//')"

cat > "/etc/systemd/system/${API_UNIT}" <<NGG_API_UNIT
[Unit]
Description=NGG node API ${NODE_ID} (mTLS :${NODE_PORT})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${NODE_BIN} -listen :${NODE_PORT} -cert ${CERT} -key ${KEY} -ca ${CA} -manifest ${MANIFEST} -kernel ${KERNEL_BIN} -config ${CONFIG} -core-unit ${CORE_UNIT}
Restart=always
RestartSec=3
# 需要调用 systemctl 重启内核单元，因此保留对 systemd 的访问
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${NODE_DIR} /run
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
NGG_API_UNIT
chmod 644 "/etc/systemd/system/${API_UNIT}"
ok 9

`

const startTpl = `# ---------------------------------------------------------------- 10 内核单元
step 10 "注册并启动内核单元 ${CORE_UNIT}"
cat > "/etc/systemd/system/${CORE_UNIT}" <<NGG_CORE_UNIT
[Unit]
Description=NGG core ${NODE_ID} (${MODE} :${PORT})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${KERNEL_BIN} run -config ${CONFIG}
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
NGG_CORE_UNIT
chmod 644 "/etc/systemd/system/${CORE_UNIT}"
systemctl daemon-reload || die 10 "systemctl daemon-reload 失败"

wait_active() {
  unit="$1"; i=0
  while [ "$i" -lt 20 ]; do
    [ "$(systemctl is-active "$unit" 2>/dev/null || true)" = "active" ] && return 0
    i=$((i + 1))
    sleep 1
  done
  return 1
}

systemctl enable "$API_UNIT" >/dev/null 2>&1 || note "systemctl enable ${API_UNIT} 失败，重启后 API 不会自动拉起"
systemctl restart "$API_UNIT" >/dev/null 2>&1 || true
if ! wait_active "$API_UNIT"; then
  journalctl -u "$API_UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
  die 10 "节点 API 未进入 active（当前：$(systemctl is-active "$API_UNIT" 2>/dev/null || true)）"
fi
log "节点 API 已启动：$(systemctl show -p ActiveEnterTimestamp --value "$API_UNIT" 2>/dev/null)"

systemctl enable "$CORE_UNIT" >/dev/null 2>&1 || note "systemctl enable ${CORE_UNIT} 失败，重启后内核不会自动拉起"
systemctl restart "$CORE_UNIT" >/dev/null 2>&1 || true
if ! wait_active "$CORE_UNIT"; then
  journalctl -u "$CORE_UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
  die 10 "内核单元未进入 active（当前：$(systemctl is-active "$CORE_UNIT" 2>/dev/null || true)）"
fi
log "内核单元已启动：$(systemctl show -p ActiveEnterTimestamp --value "$CORE_UNIT" 2>/dev/null)"
ok 10

`

const firewallTpl = `# ---------------------------------------------------------------- 11 放通
step 11 "放通 TCP ${PORT}（数据）与 TCP ${NODE_PORT}（管理）"
open_port() {
  p="$1"
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi '^Status: active'; then
    ufw allow "${p}/tcp" >/dev/null 2>&1 && note "ufw 已放通 ${p}/tcp" || note "ufw 放通 ${p}/tcp 失败，请手动执行"
  fi
  if command -v firewall-cmd >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="${p}/tcp" >/dev/null 2>&1 || true
    firewall-cmd --reload >/dev/null 2>&1 || true
  fi
  if command -v iptables >/dev/null 2>&1; then
    if iptables -C INPUT -p tcp --dport "${p}" -j ACCEPT 2>/dev/null; then
      note "iptables 已有 ${p}/tcp 规则"
    elif iptables -I INPUT -p tcp --dport "${p}" -j ACCEPT 2>/dev/null; then
      note "iptables 已放通 ${p}/tcp（规则不持久化，重启后失效）"
    fi
  fi
}
open_port "$PORT"
open_port "$NODE_PORT"
note "云服务器安全组需另行放通 TCP ${PORT}；管理端口 ${NODE_PORT} 建议只对面板 IP 开放"
ok 11

`

const nodeCtlTpl = `# ---------------------------------------------------------------- 12 管理命令
step 12 "安装管理命令 ${NODE_CTL}"
cat > "$NODE_CTL" <<'` + cliDelimiter + `'
#!/bin/sh
# nggctl — NGG 节点管理命令（由安装脚本生成，重复安装会覆盖）
# 用法：nggctl {status|params|api|logs [n]|core-logs [n]|restart|uninstall}
# 节点状态由面板连入读取；本命令只用于登录机器时自查。
set -u
MANIFEST={{NODE_MANIFEST}}
CONFIG={{NODE_CONFIG}}
CORE_UNIT={{CORE_UNIT}}
API_UNIT={{API_UNIT}}
NODE_BIN={{NODE_BIN}}
CERT={{NODE_CERT}}
KEY={{NODE_KEY}}
CA={{NODE_CA}}

die() { printf '%s\n' "$*" >&2; exit 1; }
[ -r "$MANIFEST" ] || die "找不到 $MANIFEST：这台机器还没有安装 NGG 节点"

gets() { sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$MANIFEST" | head -1; }
getn() { sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$MANIFEST" | head -1; }
clean() { printf '%s' "${1:-}" | tr -d '"\\' | tr '\n' ' '; }
sha() { sha256sum "$CONFIG" 2>/dev/null | cut -d' ' -f1; }
state() { systemctl is-active "$1" 2>/dev/null || true; }
health() {
  p="$(getn node_port)"
  curl -sS --max-time 10 --cert "$CERT" --key "$KEY" --cacert "$CA" \
    "https://127.0.0.1:${p}/node/xray/healthcheck" 2>/dev/null || true
}

case "${1:-}" in
  status)
    printf '节点 ID   : %s\n' "$(clean "$(gets node_id)")"
    printf '名称      : %s\n' "$(clean "$(gets host_name)")"
    printf '模式      : %s（%s）\n' "$(clean "$(gets mode)")" "$(clean "$(gets protocol)")"
    printf '地址      : %s\n' "$(clean "$(gets address)")"
    printf '数据端口  : %s（客户端）\n' "$(getn port)"
    printf '管理端口  : %s（面板）\n' "$(getn node_port)"
    printf '内核单元  : %s = %s\n' "$CORE_UNIT" "$(state "$CORE_UNIT")"
    printf 'API 单元  : %s = %s\n' "$API_UNIT" "$(state "$API_UNIT")"
    printf '配置指纹  : %s\n' "$(sha)"
    printf '内核版本  : %s\n' "$(/usr/local/bin/xray version 2>/dev/null | head -1 || /usr/local/bin/ngg-xray version 2>/dev/null | head -1 || echo unknown)"
    printf 'API 健康  : %s\n' "$(health)"
    ;;
  params)
    ADDR="$(gets address)"; PORT="$(getn port)"; MODE="$(gets mode)"
    NAME="$(gets host_name | tr -cd 'A-Za-z0-9._-')"; [ -n "$NAME" ] || NAME="NGG"
    case "$MODE" in
      reality)
        printf '协议     : VLESS + REALITY\n地址     : %s\n端口     : %s\nUUID     : %s\n公钥 pbk : %s\nShortID  : %s\nSNI      : %s（fingerprint=chrome）\n链接     : vless://%s@%s:%s?encryption=none&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&type=tcp#%s\n' \
          "$ADDR" "$PORT" "$(gets uuid)" "$(gets reality_public)" "$(gets short_id)" "$(gets sni)" \
          "$(gets uuid)" "$ADDR" "$PORT" "$(gets sni)" "$(gets reality_public)" "$(gets short_id)" "$NAME"
        ;;
      trojan-tls)
        printf '协议     : Trojan + TLS（自签证书，需 allowInsecure）\n地址     : %s\n端口     : %s\n密码     : %s\nSNI      : %s\n' \
          "$ADDR" "$PORT" "$(gets password)" "$(gets sni)"
        ;;
      *)
        printf '协议     : VLESS + TLS（自签证书，需 allowInsecure）\n地址     : %s\n端口     : %s\nUUID     : %s\nSNI      : %s\n' \
          "$ADDR" "$PORT" "$(gets uuid)" "$(gets sni)"
        ;;
    esac
    ;;
  api)     health; printf '\n' ;;
  logs)    journalctl -u "$CORE_UNIT" -n "${2:-80}" --no-pager 2>/dev/null || die "读取日志失败" ;;
  core-logs) journalctl -u "$CORE_UNIT" -n "${2:-80}" --no-pager 2>/dev/null || die "读取日志失败" ;;
  restart) systemctl restart "$CORE_UNIT" || die "重启失败"; sleep 1
           printf 'core=%s api=%s\n' "$(state "$CORE_UNIT")" "$(state "$API_UNIT")" ;;
  uninstall)
    systemctl disable --now "$API_UNIT" >/dev/null 2>&1 || true
    systemctl disable --now "$CORE_UNIT" >/dev/null 2>&1 || true
    rm -f "/etc/systemd/system/${API_UNIT}" "/etc/systemd/system/${CORE_UNIT}" "$NODE_BIN" "{{NODE_CTL}}"
    systemctl daemon-reload >/dev/null 2>&1 || true
    printf '已停止并移除 %s 与 %s；配置与证书保留在 %s（确认无误后可手动删除）\n' "$API_UNIT" "$CORE_UNIT" "{{NODE_DIR}}"
    ;;
  *)
    printf '用法：nggctl {status|params|api|logs [n]|core-logs [n]|restart|uninstall}\n'
    exit 2
    ;;
esac
` + cliDelimiter + `
chmod 755 "$NODE_CTL" || die 12 "无法安装管理命令"
"$NODE_CTL" params >/dev/null 2>&1 || die 12 "管理命令无法执行（params 自检失败）"
ok 12

`

const selfTestTpl = `# ---------------------------------------------------------------- 13 自检
step 13 "自检：mTLS 调节点 API / 端口监听 / 单元状态"
sleep 1
LISTEN=0
if port_busy "$PORT"; then LISTEN=1; fi
if [ "$LISTEN" -ne 1 ]; then
  journalctl -u "$CORE_UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
  die 13 "数据端口 ${PORT} 没有处于监听状态"
fi
API_LISTEN=0
if port_busy "$NODE_PORT"; then API_LISTEN=1; fi
if [ "$API_LISTEN" -ne 1 ]; then
  journalctl -u "$API_UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
  die 13 "管理端口 ${NODE_PORT} 没有处于监听状态"
fi

CORE_STATE="$(systemctl is-active "$CORE_UNIT" 2>/dev/null || true)"
[ "$CORE_STATE" = "active" ] || die 13 "内核单元状态为 ${CORE_STATE:-unknown}"
API_STATE="$(systemctl is-active "$API_UNIT" 2>/dev/null || true)"
[ "$API_STATE" = "active" ] || die 13 "节点 API 单元状态为 ${API_STATE:-unknown}"

# 正向自检：用节点自己的证书当客户端（API 只接受 CN=ngg-panel 或本节点 ID）
HEALTH="$(curl -sS --max-time 10 --cert "$CERT" --key "$KEY" --cacert "$CA" \
  "https://127.0.0.1:${NODE_PORT}/node/xray/healthcheck" 2>/dev/null || true)"
case "$HEALTH" in
  *'"isAlive":true'*) note "节点 API 双向认证自检通过：$(printf '%s' "$HEALTH" | head -c 160)" ;;
  *) journalctl -u "$API_UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
     die 13 "节点 API 自检失败：健康检查没有返回 isAlive=true" ;;
esac

# 反向自检：不带客户端证书必须被拒绝，否则等于管理端口对公网裸奔
if curl -fsS --max-time 5 --cacert "$CA" "https://127.0.0.1:${NODE_PORT}/node/xray/healthcheck" >/dev/null 2>&1; then
  die 13 "节点 API 没有强制双向认证：不带客户端证书也能访问"
fi
note "反向校验通过：无客户端证书的连接被拒绝"
ok 13

CONFIG_SHA="$(sha256sum "$CONFIG" | awk '{print $1}')"
printf '\n------------------------------------------------------------\n'
printf '节点已就绪（%s）\n' "$NODE_ID"
printf '  数据端口 : %s（客户端连接）\n' "$PORT"
printf '  管理端口 : %s（面板以 mTLS 连入，请只对面板 IP 开放）\n' "$NODE_PORT"
printf '  单元     : %s（内核）/ %s（API）\n' "$CORE_UNIT" "$API_UNIT"
printf '  内核     : %s\n' "$KERNEL_VERSION"
printf '  配置指纹 : %s\n' "$CONFIG_SHA"
printf '\n客户端连接参数：\n'
"$NODE_CTL" params || true
printf '%s\n' '------------------------------------------------------------'
printf '管理命令：%s {status|params|api|logs|restart|uninstall}\n' "$NODE_CTL"
printf '目录配置：%s（配置 0600，清单 0600，节点私钥 0600）\n' "{{NODE_DIR}}"
printf '下一步  ：回到面板 → 节点管理 → 该节点的部署窗口，点「连接节点并下发配置」\n'

printf 'NGG_NODE_READY node=%s port=%s node_port=%s mode=%s listen=%s api=%s config_sha=%s\n' \
  "$NODE_ID" "$PORT" "$NODE_PORT" "$MODE" "$LISTEN" "$API_STATE" "$CONFIG_SHA"
`
