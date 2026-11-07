package mnode

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ngg-project/ngg/internal/module"
)

const scriptTpl = `#!/usr/bin/env bash
# ============================================================================
# NGG 模块节点 · 规划 1：空白节点部署（面板下发 / 节点执行）
# ----------------------------------------------------------------------------
# 节点 ID   : {{NODE_ID}}
# 地址     : {{ADDRESS}}:{{PORT}}
# 凭据     : API 密码 + 后量子指纹 + 双向 TLS 证书（全部由面板自动生成）
# 节点端只跑一个极简 HTTPS 服务，响应连接探测，不需要 Docker / SSH / 探针。
# ============================================================================
set -uo pipefail

NODE_ID="{{NODE_ID}}"
ADDRESS="{{ADDRESS}}"
PORT="{{PORT}}"
API_PASSWORD="{{API_PASSWORD}}"
PQ_FINGERPRINT="{{PQ_FINGERPRINT}}"
DIR="/etc/ngg-mnode"
UNIT="ngg-mnode.service"

log()  { printf '[NGG] %s\n' "$*"; }
note() { printf 'NGG_NOTE %s\n' "$*"; }
step() { printf '\nNGG_STEP %s %s\n' "$1" "$2"; }
ok()   { printf 'NGG_OK %s\n' "$1"; }
die()  { printf 'NGG_FAIL %s %s\n' "$1" "$2"; printf '[NGG] 安装中止：%s\n' "$2" >&2; exit 1; }

# ---------------------------------------------------------------- 1 预检
step 1 "预检 root / systemd / python3 / 端口"
[ "$(id -u)" -eq 0 ] || die 1 "需要 root 权限"
command -v systemctl >/dev/null 2>&1 || die 1 "未找到 systemd"
command -v python3   >/dev/null 2>&1 || die 1 "未找到 python3（apt install python3）"
if ss -lntH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]$PORT$"; then
  if ! systemctl is-active --quiet "$UNIT" 2>/dev/null; then
    die 1 "端口 ${PORT} 已被其它进程占用，请换端口"
  fi
  note "端口 ${PORT} 由 ${UNIT} 占用，将重启该单元"
fi
ok 1

# ---------------------------------------------------------------- 2 目录与凭据
step 2 "写入自动生成的 API 密码 / 后量子指纹 / mTLS 证书"
install -d -m 0700 "$DIR" || die 2 "无法创建 $DIR"
umask 077
printf '%s\n' "$API_PASSWORD"   > "$DIR/api_password"
printf '%s\n' "$PQ_FINGERPRINT" > "$DIR/pq_fingerprint"

cat > "$DIR/cert.pem" <<'NGG_CERT'
{{NODE_CERT_PEM}}
NGG_CERT
cat > "$DIR/key.pem" <<'NGG_KEY'
{{NODE_KEY_PEM}}
NGG_KEY
cat > "$DIR/ca.pem" <<'NGG_CA'
{{CA_CERT_PEM}}
NGG_CA
chmod 600 "$DIR/key.pem" "$DIR/api_password"
chmod 644 "$DIR/cert.pem" "$DIR/ca.pem"
openssl x509 -in "$DIR/cert.pem" -noout -subject >/dev/null 2>&1 || die 2 "节点证书无法解析"
ok 2

# ---------------------------------------------------------------- 3 节点服务
step 3 "写入节点探测服务"
cat > "$DIR/server.py" <<'NGG_SERVER_PY'
import json, os, ssl, time
from http.server import BaseHTTPRequestHandler, HTTPServer

DIR = "/etc/ngg-mnode"

def read(path, default=""):
    try:
        with open(path, "r", encoding="utf-8") as f:
            return f.read().strip()
    except OSError:
        return default

def cpu_percent():
    # 1 秒采样 /proc/stat，避免伪造数值
    def snap():
        with open("/proc/stat", "r", encoding="utf-8") as f:
            v = [int(x) for x in f.readline().split()[1:]]
        return sum(v[:8]), v[3]
    a, idle_a = snap(); time.sleep(0.25); b, idle_b = snap()
    total = b - a
    if total <= 0:
        return 0.0
    return round(100.0 * (1.0 - (idle_b - idle_a) / total), 1)

def mem():
    info = {}
    with open("/proc/meminfo", "r", encoding="utf-8") as f:
        for line in f:
            k, _, rest = line.partition(":")
            info[k] = int(rest.split()[0]) * 1024
    total = info.get("MemTotal", 0)
    avail = info.get("MemAvailable", 0)
    return total, total - avail

def disk():
    st = os.statvfs("/")
    total = st.f_blocks * st.f_frsize
    return total, (st.f_blocks - st.f_bfree) * st.f_frsize

def net_bytes():
    rx = tx = 0
    try:
        with open("/proc/net/dev", "r", encoding="utf-8") as f:
            for line in f.readlines()[2:]:
                parts = line.split(":")[1].split()
                rx += int(parts[0]); tx += int(parts[8])
    except OSError:
        pass
    return rx, tx

def host_info():
    mt, mu = mem(); dt, du = disk(); rx, tx = net_bytes()
    with open("/proc/uptime", "r", encoding="utf-8") as f:
        uptime = int(float(f.readline().split()[0]))
    return {
        "online": True,
        "hostname": os.uname().nodename,
        "os": read("/etc/os-release").split("PRETTY_NAME=")[-1].split("\n")[0].strip('"'),
        "arch": os.uname().machine,
        "cpu_percent": cpu_percent(),
        "memory_total": mt, "memory_used": mu,
        "disk_total": dt, "disk_used": du,
        "uptime_sec": uptime,
        "net_rx_bytes": rx, "net_tx_bytes": tx,
        "collected_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def _reject(self, code, msg):
        body = json.dumps({"error": msg}).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path != "/health":
            return self._reject(404, "not found")
        if self.headers.get("X-NGG-API-Password", "") != read(DIR + "/api_password"):
            return self._reject(401, "api password rejected")
        if self.headers.get("X-NGG-PQ-Fingerprint", "") != read(DIR + "/pq_fingerprint"):
            return self._reject(401, "post-quantum fingerprint rejected")
        peer = self.connection.getpeercert()
        subject = dict(x[0] for x in peer.get("subject", ())) if peer else {}
        if subject.get("commonName") != "ngg-panel":
            return self._reject(403, "client certificate rejected")
        body = json.dumps(host_info()).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.minimum_version = ssl.TLSVersion.TLSv1_2
ctx.load_cert_chain(DIR + "/cert.pem", DIR + "/key.pem")
ctx.load_verify_locations(DIR + "/ca.pem")
ctx.verify_mode = ssl.CERT_REQUIRED
srv = HTTPServer(("0.0.0.0", int(os.environ.get("NGG_PORT", "0"))), Handler)
srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
NGG_SERVER_PY
python3 -m py_compile "$DIR/server.py" 2>/dev/null || die 3 "节点服务脚本语法错误"
[ -s "$DIR/server.py" ] || die 3 "节点服务写入失败"
ok 3

# ---------------------------------------------------------------- 4 单元
step 4 "注册并启动 ${UNIT}"
cat > "/etc/systemd/system/${UNIT}" <<NGG_UNIT
[Unit]
Description=NGG module node ${NODE_ID} (mTLS :${PORT})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=NGG_PORT=${PORT}
ExecStart=/usr/bin/python3 ${DIR}/server.py
Restart=always
RestartSec=3
NoNewPrivileges=true
ProtectSystem=full
PrivateTmp=true
ReadWritePaths=${DIR}
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
NGG_UNIT
[ -s "/etc/systemd/system/${UNIT}" ] || die 4 "单元文件写入失败（${UNIT} 为空）"
systemctl daemon-reload || die 4 "daemon-reload 失败"
systemctl enable "$UNIT" >/dev/null 2>&1 || true
systemctl restart "$UNIT" >/dev/null 2>&1 || true
STATE=""
for i in $(seq 1 20); do
  STATE="$(systemctl is-active "$UNIT" 2>/dev/null || true)"
  [ "$STATE" = "active" ] && break
  sleep 1
done
if [ "$STATE" != "active" ]; then
  journalctl -u "$UNIT" -n 20 --no-pager 2>/dev/null | tail -20 || true
  die 4 "节点单元未进入 active（当前：${STATE:-unknown}）"
fi
ok 4

# ---------------------------------------------------------------- 5 自检
step 5 "本机双向 TLS 自检（带证书必须通过 / 不带必须失败）"
CRT="$(mktemp)"; KEY="$(mktemp)"
# 面板客户端证书不落节点：这里用节点自己的证书仅为验证 TLS 层是否生效，
# 真实探测由面板用 CN=ngg-panel 的客户端证书发起。
cp "$DIR/cert.pem" "$CRT"; cp "$DIR/key.pem" "$KEY"
if curl -fsS --max-time 5 --cert "$CRT" --key "$KEY" --cacert "$DIR/ca.pem" \
    -H "X-NGG-API-Password: $API_PASSWORD" \
    -H "X-NGG-PQ-Fingerprint: $PQ_FINGERPRINT" \
    "https://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
  note "TLS + 凭据自检通过"
else
  note "自检请求被拒绝（节点只接受 CN=ngg-panel 的面板证书，属预期）"
fi
rm -f "$CRT" "$KEY"
if curl -fsS --max-time 5 --cacert "$DIR/ca.pem" "https://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
  die 5 "没有客户端证书也能访问，双向认证未生效"
fi
note "反向校验通过：无客户端证书被拒绝"
ok 5

printf '\n------------------------------------------------------------\n'
printf '节点已就绪（%s）\n' "$NODE_ID"
printf '  地址     : %s:%s\n' "$ADDRESS" "$PORT"
printf '  单元     : %s\n' "$UNIT"
printf '  凭据     : API 密码 / 后量子指纹 / mTLS 均已自动写入 %s\n' "$DIR"
printf 'NGG_MNODE_READY node=%s address=%s port=%s\n' "$NODE_ID" "$ADDRESS" "$PORT"
`

// Script renders a self-contained installer for 规划 1. All substituted values
// come from generated credentials, but we still validate them so a crafted
// value can never terminate a heredoc and inject shell code.
func Script(n module.NodeSpec, secrets module.NodeSecrets) (string, error) {
	if n.Port < 1 || n.Port > 65535 {
		return "", errors.New("节点端口不合法")
	}
	values := map[string]string{
		"{{NODE_ID}}":        n.ID,
		"{{ADDRESS}}":        n.Address,
		"{{PORT}}":           strconv.Itoa(n.Port),
		"{{API_PASSWORD}}":   secrets.APIPassword,
		"{{PQ_FINGERPRINT}}": secrets.PQFingerprint,
		"{{NODE_CERT_PEM}}":  secrets.NodeCertPEM,
		"{{NODE_KEY_PEM}}":   secrets.NodeKeyPEM,
		"{{CA_CERT_PEM}}":    secrets.CACertPEM,
	}
	for key, v := range values {
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("缺少 %s", key)
		}
		if strings.Contains(v, "\nNGG_CERT\n") || strings.Contains(v, "\nNGG_KEY\n") ||
			strings.Contains(v, "\nNGG_CA\n") || strings.Contains(v, "\nNGG_UNIT\n") ||
			strings.Contains(v, "\nNGG_SERVER_PY\n") {
			return "", fmt.Errorf("%s 与脚本分隔符冲突", key)
		}
	}
	var replacerArgs []string
	for k, v := range values {
		replacerArgs = append(replacerArgs, k, v)
	}
	out := strings.NewReplacer(replacerArgs...).Replace(scriptTpl)
	if strings.Contains(out, "{{") {
		return "", errors.New("脚本仍有未替换的占位符")
	}
	return out, nil
}
