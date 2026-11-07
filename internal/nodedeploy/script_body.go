package nodedeploy

// The kernel-specific middle (step 5), the shared config/validate/start steps,
// the tail (firewall, probe installer, management CLI, self-test).

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
TEST_OUT="$("$KERNEL_BIN" run -test -config "$CONFIG" 2>&1)" || {
  printf '%s\n' "$TEST_OUT" | tail -8
  die 7 "配置校验未通过（见上方内核输出）"
}
log "校验通过：$(printf '%s' "$TEST_OUT" | tail -1)"
ok 7

`

const startTpl = `# ---------------------------------------------------------------- 8 启动
step 8 "注册并启动 systemd 单元 ${UNIT}"
cat > "/etc/systemd/system/${UNIT}" <<NGG_UNIT
[Unit]
Description=NGG Node ${NODE_ID} (${MODE} :${PORT})
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
NGG_UNIT
chmod 644 "/etc/systemd/system/${UNIT}"
systemctl daemon-reload || die 8 "systemctl daemon-reload 失败"
systemctl enable "$UNIT" >/dev/null 2>&1 || note "systemctl enable 失败，重启后节点不会自动拉起"
systemctl restart "$UNIT" >/dev/null 2>&1 || true
i=0
while [ "$i" -lt 15 ]; do
  [ "$(systemctl is-active "$UNIT" 2>/dev/null || true)" = "active" ] && break
  i=$((i + 1))
  sleep 1
done
STATE="$(systemctl is-active "$UNIT" 2>/dev/null || true)"
if [ "$STATE" != "active" ]; then
  log "最近日志："
  journalctl -u "$UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
  die 8 "单元未进入 active 状态（当前：${STATE:-unknown}）"
fi
log "单元已启动：$(systemctl show -p ActiveEnterTimestamp --value "$UNIT" 2>/dev/null)"
ok 8

`

const firewallTpl = `# ---------------------------------------------------------------- 9 放通
step 9 "放通 TCP ${PORT}"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi '^Status: active'; then
  if ufw allow "${PORT}/tcp" >/dev/null 2>&1; then
    note "ufw 已放通 ${PORT}/tcp"
  else
    note "ufw 放通失败，请手动执行 ufw allow ${PORT}/tcp"
  fi
else
  note "ufw 未启用或未安装，跳过 ufw"
fi
if command -v firewall-cmd >/dev/null 2>&1; then
  firewall-cmd --permanent --add-port="${PORT}/tcp" >/dev/null 2>&1 || true
  firewall-cmd --reload >/dev/null 2>&1 || true
fi
if command -v iptables >/dev/null 2>&1; then
  if iptables -C INPUT -p tcp --dport "${PORT}" -j ACCEPT 2>/dev/null; then
    note "iptables 规则已存在"
  elif iptables -I INPUT -p tcp --dport "${PORT}" -j ACCEPT 2>/dev/null; then
    note "iptables 已放通（规则不持久化，重启后失效）"
  else
    note "iptables 放通失败"
  fi
fi
note "云服务器安全组需另行放通 TCP ${PORT}（脚本无法操作云控制台）"
ok 9

`

// probeInstallTpl installs the outbound reporter. It is the step that replaces
// the old SSH-based status channel: the panel never connects in.
//
// Two heredocs are quoted ('…') so the token and the shell body are written
// byte-for-byte; only the systemd unit is expanded, because its ExecStart must
// point at the real install path.
const probeInstallTpl = `# ---------------------------------------------------------------- 10 探针
step 10 "安装上报探针（每 {{PROBE_INTERVAL}} 秒主动上报，无需 SSH）"
PROBE_STATE="off"
if [ -z "$PANEL_URL" ]; then
  # 明确「不上报」：停掉这台机器上可能存在的旧探针，避免它继续往旧地址推数据。
  if [ -f "/etc/systemd/system/${PROBE_UNIT}" ]; then
    systemctl disable --now "$PROBE_UNIT" >/dev/null 2>&1 || true
    note "已停止旧的 ${PROBE_UNIT}：方案里没有面板上报地址"
  fi
  note "未配置面板上报地址：探针不启动，节点信息不会回传（在控制台填好上报地址后重新部署即可）"
else
  install -d -m 0750 "{{NODE_DIR}}"
  umask 077
  cat > "$PROBE_CONF" <<'` + probeConfDelimiter + `'
{{AGENT_CONF}}
` + probeConfDelimiter + `
  chmod 600 "$PROBE_CONF" || die 10 "无法设置探针配置权限"
  umask 022
  cat > "$PROBE_BIN" <<'` + probeDelimiter + `'
{{PROBE}}
` + probeDelimiter + `
  chmod 755 "$PROBE_BIN" || die 10 "无法安装探针"
  sh -n "$PROBE_BIN" || die 10 "探针脚本语法自检失败"
  cat > "/etc/systemd/system/${PROBE_UNIT}" <<NGG_PROBE_UNIT
[Unit]
Description=NGG node probe (outbound report, no inbound port)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${PROBE_BIN}
Restart=always
RestartSec=5
Nice=10
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/run

[Install]
WantedBy=multi-user.target
NGG_PROBE_UNIT
  chmod 644 "/etc/systemd/system/${PROBE_UNIT}"
  systemctl daemon-reload || die 10 "systemctl daemon-reload 失败"
  systemctl enable "$PROBE_UNIT" >/dev/null 2>&1 || note "systemctl enable ${PROBE_UNIT} 失败，重启后探针不会自动拉起"
  systemctl restart "$PROBE_UNIT" >/dev/null 2>&1 || true
  i=0
  while [ "$i" -lt 15 ]; do
    [ "$(systemctl is-active "$PROBE_UNIT" 2>/dev/null || true)" = "active" ] && break
    i=$((i + 1))
    sleep 1
  done
  PSTATE="$(systemctl is-active "$PROBE_UNIT" 2>/dev/null || true)"
  if [ "$PSTATE" != "active" ]; then
    journalctl -u "$PROBE_UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
    die 10 "探针单元未进入 active（当前：${PSTATE:-unknown}）"
  fi
  if curl -fsS -m 6 "${PANEL_URL%/}/v1/agent/ping" >/dev/null 2>&1; then
    note "面板上报端点可达：${PANEL_URL}（探针已开始上报）"
    PROBE_STATE="up"
  else
    note "当前连不上面板上报端点 ${PANEL_URL}：探针保持运行并按 1s→60s 退避重试（检查面板端口 / 安全组 / URL）"
    PROBE_STATE="down"
  fi
fi
ok 10

`

const nodeCLITpl = `# ---------------------------------------------------------------- 11 CLI
step 11 "安装管理命令 {{NODE_BIN}}"
cat > "{{NODE_BIN}}" <<'` + cliDelimiter + `'
#!/bin/sh
# ngg-node — NGG 节点管理命令（由 NGG 部署脚本安装，重复部署会覆盖）
# 用法：ngg-node {params|status|probe|logs [n]|restart|uninstall}
# 节点状态由探针主动上报到面板，本命令只用于登录机器时自查。
set -u
MANIFEST={{NODE_MANIFEST}}
CONFIG={{NODE_CONFIG}}
UNIT={{NODE_SERVICE}}
PROBE={{PROBE_BIN}}
PROBE_UNIT={{PROBE_UNIT}}

die() { printf '%s\n' "$*" >&2; exit 1; }
[ -r "$MANIFEST" ] || die "找不到 $MANIFEST：这台机器还没有部署 NGG 节点"

gets() { sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$MANIFEST" | head -1; }
getn() { sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$MANIFEST" | head -1; }
clean() { printf '%s' "${1:-}" | tr -d '"\\' | tr '\n' ' '; }
sha() { sha256sum "$CONFIG" 2>/dev/null | cut -d' ' -f1; }
kver() { (/usr/local/bin/xray version 2>/dev/null || /usr/local/bin/ngg-xray version 2>/dev/null || echo unknown) | head -1 | tr -d '"'; }
listen() {
  p="$(getn port)"
  if ss -lntH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${p}$"; then echo 1; else echo 0; fi
}
traffic() {
  awk '
    { line=$0; sub(/^[ \t]+/, "", line); if (index(line, ":") == 0) next
      split(line, p, ":"); if (p[1] == "lo") next
      n = split(p[2], f, /[ \t]+/); if (n < 10) next
      rx += f[2]; tx += f[10] }
    END { printf "%d %d", rx + 0, tx + 0 }' /proc/net/dev 2>/dev/null
}

case "${1:-}" in
  status)
    set -- $(traffic); RX="${1:-0}"; TX="${2:-0}"
    MT="$(awk '/^MemTotal:/{print $2 * 1024}' /proc/meminfo 2>/dev/null)"
    MA="$(awk '/^MemAvailable:/{print $2 * 1024}' /proc/meminfo 2>/dev/null)"
    printf '{"node_id":"%s","mode":"%s","protocol":"%s","address":"%s","port":%s,"sni":"%s","active":"%s","since":"%s","listen":%s,"config_sha256":"%s","kernel_version":"%s","probe_active":"%s","panel_url":"%s","uptime_sec":%s,"rx_bytes":%s,"tx_bytes":%s,"collected_at":"%s"}\n' \
      "$(clean "$(gets node_id)")" "$(clean "$(gets mode)")" "$(clean "$(gets protocol)")" \
      "$(clean "$(gets address)")" "$(getn port)" "$(clean "$(gets sni)")" \
      "$(systemctl is-active "$UNIT" 2>/dev/null || true)" \
      "$(systemctl show -p ActiveEnterTimestamp --value "$UNIT" 2>/dev/null || true)" \
      "$(listen)" "$(sha)" "$(clean "$(kver)")" \
      "$(systemctl is-active "$PROBE_UNIT" 2>/dev/null || true)" \
      "$(clean "$(gets panel_url)")" \
      "$(cut -d. -f1 /proc/uptime 2>/dev/null || echo 0)" "$RX" "$TX" \
      "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
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
  probe)
    printf '面板地址 : %s\n探针单元 : %s（%s）\n配置文件 : {{PROBE_CONF}}\n\n最近上报日志：\n' \
      "$(clean "$(gets panel_url)")" "$PROBE_UNIT" "$(systemctl is-active "$PROBE_UNIT" 2>/dev/null || true)"
    journalctl -u "$PROBE_UNIT" -n "${2:-20}" --no-pager 2>/dev/null | tail -20 || true
    ;;
  logs)    journalctl -u "$UNIT" -n "${2:-80}" --no-pager 2>/dev/null || die "读取日志失败" ;;
  restart) systemctl restart "$UNIT" || die "重启失败"; sleep 1; printf 'unit=%s\n' "$(systemctl is-active "$UNIT" 2>/dev/null || true)" ;;
  uninstall)
    systemctl disable --now "$PROBE_UNIT" >/dev/null 2>&1 || true
    rm -f "/etc/systemd/system/${PROBE_UNIT}"
    systemctl disable --now "$UNIT" >/dev/null 2>&1 || true
    rm -f "/etc/systemd/system/$UNIT" "{{PROBE_BIN}}"
    systemctl daemon-reload >/dev/null 2>&1 || true
    printf '已停止并移除 %s 与 %s；配置保留在 %s（确认无误后可手动删除）\n' "$UNIT" "$PROBE_UNIT" "{{NODE_DIR}}"
    ;;
  *)
    printf '用法：ngg-node {params|status|probe [n]|logs [n]|restart|uninstall}\n'
    exit 2
    ;;
esac
` + cliDelimiter + `
chmod 755 "{{NODE_BIN}}" || die 11 "无法安装管理命令"
"{{NODE_BIN}}" params >/dev/null 2>&1 || die 11 "管理命令无法执行（params 自检失败）"
ok 11

`

const selfTestTpl = `# ---------------------------------------------------------------- 12 自检
step 12 "自检：监听 / 单元 / 上报"
sleep 1
LISTEN=0
if ss -lntH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${PORT}$"; then LISTEN=1; fi
if [ "$LISTEN" -ne 1 ]; then
  journalctl -u "$UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
  die 12 "端口 ${PORT} 没有处于监听状态"
fi
CONFIG_SHA="$(sha256sum "$CONFIG" | awk '{print $1}')"
STATE="$(systemctl is-active "$UNIT" 2>/dev/null || true)"
if [ "$STATE" != "active" ]; then
  die 12 "单元状态为 ${STATE:-unknown}"
fi
case "$PROBE_STATE" in
  up)   note "探针上报中：${PROBE_UNIT} 已连上面板，节点状态会持续回传" ;;
  down) note "探针在运行但当前连不上面板：节点本身可用，网络恢复后自动续报" ;;
  *)    note "未部署探针（未配置面板上报地址）：节点可用，但面板看不到实时状态" ;;
esac
log "单元 ${STATE} · 监听 ${ADDRESS}:${PORT} · 配置指纹 ${CONFIG_SHA} · 探针 ${PROBE_STATE}"
ok 12

printf '\n------------------------------------------------------------\n'
printf '节点已就绪（%s），客户端连接参数：\n' "$NODE_ID"
"{{NODE_BIN}}" params || true
printf '%s\n' '------------------------------------------------------------'
printf '上报状态：%s · 面板：%s\n' "$PROBE_STATE" "${PANEL_URL:-(未配置)}"
printf '管理命令：{{NODE_BIN}} {params|status|probe|logs|restart|uninstall}\n'
printf '配置目录：{{NODE_DIR}}（配置 0600，清单 0600，探针配置 0600）\n'

printf 'NGG_NODE_READY node=%s port=%s mode=%s listen=%s config_sha=%s probe=%s\n' \
  "$NODE_ID" "$PORT" "$MODE" "$LISTEN" "$CONFIG_SHA" "$PROBE_STATE"
`
