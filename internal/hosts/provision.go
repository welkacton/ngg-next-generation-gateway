package hosts

// VPS onboarding ("provisioning"): turn a registered host into a node that
// actually serves traffic.
//
// The panel cannot reach into a customer VPS by itself, so the unit of work is
// a REAL, executable bash script: install a kernel, write a minimal config with
// fresh credentials, open the port, start the service. The same payload is
// persisted under <data>/provisions/<hostID>.json so the operator can copy the
// script again after a restart.
//
// Honesty boundaries, which are also surfaced in Notes:
//   - the script installs a kernel only; it does NOT install the NGG agent, so
//     per-node CPU/traffic still needs the agent,
//   - the certificate is self-signed, so clients need allowInsecure,
//   - the script has never been executed against the operator's VPS by NGG.
//
// The script must never contain a panel credential: the only secret in it is
// the node's own UUID/password, which the operator needs to configure clients.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Supported provision protocols. These strings are the wire contract with the
// console (web/src/api/ngg.ts · ProvisionProtocol).
const (
	ProvProtoVLESS       = "vless"
	ProvProtoTrojan      = "trojan"
	ProvProtoShadowsocks = "shadowsocks"
)

// SSMethod is the Shadowsocks AEAD cipher written into the sing-box config.
const SSMethod = "aes-256-gcm"

// fallbackSNI is used when the host is a bare IP address: the TLS handshake
// needs a server name, and any real domain works for a self-signed cert.
const fallbackSNI = "www.bing.com"

var (
	// ErrUnknownHost means the host id is not in the registry.
	ErrUnknownHost = errors.New("节点不存在")
	// ErrProvisionNotFound means no script has been generated for this host.
	ErrProvisionNotFound = errors.New("该节点尚未生成部署脚本")
)

// ProvisionEndpoint is what a client needs to dial the node.
type ProvisionEndpoint struct {
	Address    string `json:"address"`
	Port       int    `json:"port"`
	Protocol   string `json:"protocol"`
	Credential string `json:"credential"`
	Transport  string `json:"transport"`
	TLS        bool   `json:"tls"`
	SNI        string `json:"sni"`
}

// HostProvision is the persisted result of Provision. Every field is real:
// the credential is generated here, the script is the one the operator runs.
type HostProvision struct {
	HostID      string            `json:"host_id"`
	Protocol    string            `json:"protocol"`
	Port        int               `json:"port"`
	Credential  string            `json:"credential"`
	Script      string            `json:"script"`
	Endpoint    ProvisionEndpoint `json:"endpoint"`
	Notes       []string          `json:"notes"`
	GeneratedAt time.Time         `json:"generated_at"`
}

// ValidProvisionProtocol reports whether p is a protocol NGG can generate a
// deployment script for.
func ValidProvisionProtocol(p string) bool {
	switch p {
	case ProvProtoVLESS, ProvProtoTrojan, ProvProtoShadowsocks:
		return true
	}
	return false
}

func defaultProvisionPort(protocol string) int {
	if protocol == ProvProtoShadowsocks {
		return 8388
	}
	return 443
}

// credentialFor mints a node-local secret. Shapes match what the kernels
// expect, so the script is usable without post-editing.
func credentialFor(protocol string) (string, error) {
	switch protocol {
	case ProvProtoVLESS:
		return newUUIDv4()
	case ProvProtoTrojan:
		// 24 random bytes → 32 URL-safe characters; no padding, no JSON
		// escaping needed.
		b, err := randBytes(24)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(b), nil
	case ProvProtoShadowsocks:
		// aes-256-gcm takes a pre-shared password string; 16 random bytes
		// base64-encoded is 24 characters and stays inside every client's
		// allowed length.
		b, err := randBytes(16)
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(b), nil
	default:
		return "", fmt.Errorf("不支持的协议 %q", protocol)
	}
}

func randBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// newUUIDv4 returns a canonical RFC 4122 version-4 UUID.
func newUUIDv4() (string, error) {
	b, err := randBytes(16)
	if err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// defaultSNIFor keeps the host's own domain as SNI when it has one; a bare IP
// cannot be a TLS server name, so a well-known domain is used instead.
func defaultSNIFor(address string) string {
	a := strings.TrimSpace(address)
	if a == "" || net.ParseIP(a) != nil || strings.Contains(a, ":") {
		return fallbackSNI
	}
	return a
}

// Provision generates credentials, the endpoint descriptor and the deployment
// script for one host, then persists them. port<=0 selects the protocol default.
func (r *Registry) Provision(hostID string, protocol string, port int, sni string) (HostProvision, error) {
	host, ok := r.Get(hostID)
	if !ok {
		return HostProvision{}, fmt.Errorf("%w: %s", ErrUnknownHost, hostID)
	}
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if !ValidProvisionProtocol(proto) {
		return HostProvision{}, fmt.Errorf("不支持的协议 %q：仅支持 vless / trojan / shadowsocks", protocol)
	}
	if port == 0 {
		port = defaultProvisionPort(proto)
	}
	if port < 1 || port > 65535 {
		return HostProvision{}, fmt.Errorf("端口需在 1..65535，当前 %d", port)
	}
	sni = strings.TrimSpace(sni)
	if sni == "" {
		sni = defaultSNIFor(host.Address)
	} else if !hostRE.MatchString(sni) {
		return HostProvision{}, fmt.Errorf("SNI 需为域名（不含协议前缀与空格）")
	}

	cred, err := credentialFor(proto)
	if err != nil {
		return HostProvision{}, fmt.Errorf("生成凭据失败: %w", err)
	}

	p := HostProvision{
		HostID:     host.ID,
		Protocol:   proto,
		Port:       port,
		Credential: cred,
		Endpoint: ProvisionEndpoint{
			Address:    host.Address,
			Port:       port,
			Protocol:   proto,
			Credential: cred,
			Transport:  "tcp",
			TLS:        proto != ProvProtoShadowsocks,
			SNI:        sni,
		},
		GeneratedAt: time.Now().UTC().Truncate(time.Second),
	}
	p.Notes = provisionNotes(p, host)

	script, err := buildScript(p)
	if err != nil {
		return HostProvision{}, fmt.Errorf("生成部署脚本失败: %w", err)
	}
	p.Script = script

	if err := r.saveProvision(p); err != nil {
		return HostProvision{}, fmt.Errorf("保存部署脚本失败: %w", err)
	}
	return p, nil
}

// provisionNotes documents the deployment facts and the parts the script
// deliberately does not do.
func provisionNotes(p HostProvision, host Host) []string {
	kernel := "Xray-core"
	if p.Protocol == ProvProtoShadowsocks {
		kernel = "sing-box"
	}
	notes := []string{
		fmt.Sprintf("脚本面向 Ubuntu 22.04 / 24.04 与 Debian 12，需要 root 权限；只安装 %s 并写入最小可用配置，不改动机器上其它服务。", kernel),
		fmt.Sprintf("本脚本内含该节点自己的连接凭据（%s 协议），请勿公开、勿提交到版本库；泄漏后重新生成即可换新凭据。", strings.ToUpper(p.Protocol)),
	}
	if p.Endpoint.TLS {
		notes = append(notes,
			fmt.Sprintf("证书由脚本在本机用 openssl 自签生成（CN=%s，有效期 3650 天），客户端必须开启 allowInsecure / skip-cert-verify；如需正式证书，请把 cert.pem / key.pem 换成真实证书后 systemctl restart xray。", p.Endpoint.SNI),
		)
	} else {
		notes = append(notes,
			fmt.Sprintf("Shadowsocks 使用 %s，无 TLS；密码是 16 字节随机值的 base64 编码，客户端需选择相同 method 并原样填写密码。", SSMethod),
		)
	}
	notes = append(notes,
		fmt.Sprintf("云服务器安全组与本机防火墙都要放通 TCP %d：脚本仅在 ufw 已启用时执行 ufw allow，iptables 规则不会持久化（重启后失效）。", p.Port),
		fmt.Sprintf("脚本不安装 NGG Agent：节点列表里的「在线」来自 NGG 对 %s 端口的真实 TCP 探测，不是协议握手验证；CPU / 流量等指标需另装 agent 才能采集。", net.JoinHostPort(host.Address, strconv.Itoa(host.NodePort))),
		"该脚本由 NGG 生成但未在目标 VPS 上执行过，实际内核版本与 systemd 状态请以脚本输出和 systemctl status 为准。",
	)
	if p.Protocol == ProvProtoVLESS {
		notes = append(notes,
			"当前传输为 tcp + TLS（自签证书）；如需 ws / grpc / REALITY，请改 config.json 的 streamSettings 后先跑 xray run -test 再重启服务。",
		)
	}
	return notes
}

/* ------------------------------------------------------------------ *
 * 持久化：<data>/provisions/<hostID>.json，0600 原子写
 * ------------------------------------------------------------------ */

func (r *Registry) provisionsDir() string {
	return filepath.Join(filepath.Dir(r.path), "provisions")
}

func (r *Registry) provisionPath(hostID string) (string, error) {
	// hostID comes from a URL path, so re-check it before touching the
	// filesystem: no separators, no traversal.
	if !idRE.MatchString(hostID) {
		return "", ErrProvisionNotFound
	}
	return filepath.Join(r.provisionsDir(), hostID+".json"), nil
}

func (r *Registry) saveProvision(p HostProvision) error {
	path, err := r.provisionPath(p.HostID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// Existing files keep their old mode across rename, so normalise it.
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// LoadProvision reads the script generated for a host. A missing file is
// ErrProvisionNotFound, not an error the caller has to decode.
func (r *Registry) LoadProvision(hostID string) (HostProvision, error) {
	path, err := r.provisionPath(hostID)
	if err != nil {
		return HostProvision{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HostProvision{}, ErrProvisionNotFound
		}
		return HostProvision{}, err
	}
	var p HostProvision
	if err := json.Unmarshal(b, &p); err != nil {
		return HostProvision{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(p.Script) == "" {
		return HostProvision{}, fmt.Errorf("%s: 已保存的记录缺少脚本内容", path)
	}
	return p, nil
}

/* ------------------------------------------------------------------ *
 * 脚本模板
 * ------------------------------------------------------------------ */

// fill substitutes the placeholders. strings.NewReplacer keeps the bash text
// verbatim: no %-formatting, so printf / awk inside the script stay intact.
func fill(tpl string, p HostProvision) string {
	tls := "关闭"
	if p.Endpoint.TLS {
		tls = "开启（自签证书）"
	}
	return strings.NewReplacer(
		"{{PROTOCOL}}", string(p.Protocol),
		"{{PORT}}", strconv.Itoa(p.Port),
		"{{CREDENTIAL}}", p.Credential,
		"{{SNI}}", p.Endpoint.SNI,
		"{{ADDRESS}}", p.Endpoint.Address,
		"{{TRANSPORT}}", p.Endpoint.Transport,
		"{{TLS}}", tls,
		"{{METHOD}}", SSMethod,
		"{{GENERATED_AT}}", p.GeneratedAt.Format(time.RFC3339),
		"{{HOST_ID}}", p.HostID,
	).Replace(tpl)
}

func buildScript(p HostProvision) (string, error) {
	var body string
	switch p.Protocol {
	case ProvProtoVLESS, ProvProtoTrojan:
		cfg := vlessConfigTpl
		if p.Protocol == ProvProtoTrojan {
			cfg = trojanConfigTpl
		}
		body = strings.Replace(xrayBodyTpl, "{{CONFIG}}", fill(cfg, p), 1)
	case ProvProtoShadowsocks:
		body = strings.Replace(singboxBodyTpl, "{{CONFIG}}", fill(singboxConfigTpl, p), 1)
	default:
		return "", fmt.Errorf("不支持的协议 %q", p.Protocol)
	}
	script := fill(shellPreambleTpl, p) + fill(body, p) + firewallTpl + fill(shellFooterTpl, p)
	if !strings.Contains(script, "NGG-NODE-READY protocol="+p.Protocol) {
		return "", errors.New("脚本缺少就绪标记")
	}
	return script, nil
}

const shellPreambleTpl = `#!/usr/bin/env bash
# NGG 一键部署脚本
# 节点 ID : {{HOST_ID}}
# 协议    : {{PROTOCOL}}
# 地址    : {{ADDRESS}}:{{PORT}}
# 生成时间: {{GENERATED_AT}}
#
# 用法 : 以 root 登录 VPS，粘贴到终端执行；或保存为 deploy.sh 后 chmod +x deploy.sh && ./deploy.sh
# 适用 : Ubuntu 22.04 / 24.04、Debian 12（amd64 / arm64）
# 警告 : 本脚本包含该节点自己的连接凭据，请勿公开或提交到版本库。
set -euo pipefail

PROTOCOL="{{PROTOCOL}}"
PORT="{{PORT}}"
SNI="{{SNI}}"
CREDENTIAL="{{CREDENTIAL}}"

log() { printf '[NGG] %s\n' "$*"; }

if [ "$(id -u)" -ne 0 ]; then
  echo "[NGG] 请以 root 身份运行（sudo -i 后重试）" >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
if command -v apt-get >/dev/null 2>&1; then
  log "安装基础依赖（curl / ca-certificates / openssl）"
  apt-get update -y
  apt-get install -y --no-install-recommends curl ca-certificates openssl
fi

log "节点协议 = ${PROTOCOL}，端口 = ${PORT}，SNI = ${SNI}"

`

const xrayBodyTpl = `log "安装 Xray-core（官方 install-release.sh）"
bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install

log "生成自签 TLS 证书（CN=${SNI}，有效期 3650 天）"
mkdir -p /usr/local/etc/xray
if [ ! -s /usr/local/etc/xray/cert.pem ] || [ ! -s /usr/local/etc/xray/key.pem ]; then
  openssl req -x509 -nodes -newkey rsa:2048 -days 3650 \
    -subj "/CN=${SNI}" \
    -keyout /usr/local/etc/xray/key.pem \
    -out /usr/local/etc/xray/cert.pem
fi
chmod 644 /usr/local/etc/xray/cert.pem
chmod 600 /usr/local/etc/xray/key.pem

log "写入 Xray 配置 /usr/local/etc/xray/config.json"
cat > /usr/local/etc/xray/config.json <<'XRAY_CONFIG'
{{CONFIG}}
XRAY_CONFIG
chmod 600 /usr/local/etc/xray/config.json

log "校验配置"
XRAY_BIN="$(command -v xray || echo /usr/local/bin/xray)"
"$XRAY_BIN" run -test -config /usr/local/etc/xray/config.json

log "启用并重启 Xray"
systemctl daemon-reload
systemctl enable xray
systemctl restart xray
`

const vlessConfigTpl = `{
  "log": { "loglevel": "warning" },
  "inbounds": [
    {
      "tag": "ngg-vless-in",
      "listen": "0.0.0.0",
      "port": {{PORT}},
      "protocol": "vless",
      "settings": {
        "clients": [
          { "id": "{{CREDENTIAL}}", "email": "{{HOST_ID}}" }
        ],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "{{TRANSPORT}}",
        "security": "tls",
        "tlsSettings": {
          "certificates": [
            {
              "certificateFile": "/usr/local/etc/xray/cert.pem",
              "keyFile": "/usr/local/etc/xray/key.pem"
            }
          ]
        }
      },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls"] }
    }
  ],
  "outbounds": [
    { "protocol": "freedom", "tag": "direct" }
  ]
}`

const trojanConfigTpl = `{
  "log": { "loglevel": "warning" },
  "inbounds": [
    {
      "tag": "ngg-trojan-in",
      "listen": "0.0.0.0",
      "port": {{PORT}},
      "protocol": "trojan",
      "settings": {
        "clients": [
          { "password": "{{CREDENTIAL}}", "email": "{{HOST_ID}}" }
        ]
      },
      "streamSettings": {
        "network": "{{TRANSPORT}}",
        "security": "tls",
        "tlsSettings": {
          "certificates": [
            {
              "certificateFile": "/usr/local/etc/xray/cert.pem",
              "keyFile": "/usr/local/etc/xray/key.pem"
            }
          ]
        }
      },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls"] }
    }
  ],
  "outbounds": [
    { "protocol": "freedom", "tag": "direct" }
  ]
}`

const singboxBodyTpl = `log "安装 sing-box（官方 install.sh）"
bash -c "$(curl -fsSL https://sing-box.app/install.sh)"

log "写入 sing-box 配置 /etc/sing-box/config.json"
mkdir -p /etc/sing-box
cat > /etc/sing-box/config.json <<'SINGBOX_CONFIG'
{{CONFIG}}
SINGBOX_CONFIG
chmod 600 /etc/sing-box/config.json

log "校验配置"
SINGBOX_BIN="$(command -v sing-box || echo /usr/bin/sing-box)"
"$SINGBOX_BIN" check -c /etc/sing-box/config.json

log "启用并重启 sing-box"
systemctl daemon-reload
systemctl enable sing-box
systemctl restart sing-box
`

const singboxConfigTpl = `{
  "log": { "level": "warn", "timestamp": true },
  "inbounds": [
    {
      "type": "shadowsocks",
      "tag": "ngg-ss-in",
      "listen": "0.0.0.0",
      "listen_port": {{PORT}},
      "method": "{{METHOD}}",
      "password": "{{CREDENTIAL}}"
    }
  ],
  "outbounds": [
    { "type": "direct", "tag": "direct" }
  ]
}`

const firewallTpl = `
log "放通 TCP ${PORT}"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi '^Status: active'; then
  ufw allow "${PORT}/tcp" || true
fi
if command -v firewall-cmd >/dev/null 2>&1; then
  firewall-cmd --permanent --add-port="${PORT}/tcp" || true
  firewall-cmd --reload || true
fi
if command -v iptables >/dev/null 2>&1; then
  iptables -C INPUT -p tcp --dport "${PORT}" -j ACCEPT 2>/dev/null || \
    iptables -I INPUT -p tcp --dport "${PORT}" -j ACCEPT || true
fi

log "检查端口监听"
sleep 2
if command -v ss >/dev/null 2>&1; then
  ss -lnt | grep -q ":${PORT}" || log "警告：ss 未看到 ${PORT} 处于监听状态，请执行 systemctl status 排查"
fi

`

const shellFooterTpl = `cat <<EOF

------------------------------------------------------------
节点已就绪，客户端连接参数：
  协议     : {{PROTOCOL}}
  地址     : {{ADDRESS}}
  端口     : {{PORT}}
  凭据     : {{CREDENTIAL}}
  传输     : {{TRANSPORT}}
  TLS      : {{TLS}}
  SNI      : {{SNI}}
------------------------------------------------------------
EOF

echo "NGG-NODE-READY protocol={{PROTOCOL}} port={{PORT}}"
`
