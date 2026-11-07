package nodedeploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ngg-project/ngg/internal/version"
)

// Bash template rendering.
//
// The script is assembled from one preamble (identity + helpers), one kernel
// body per mode, a fixed middle (config / validate / install the node service /
// start / firewall), one operator CLI and a fixed tail (self-test).
// Placeholders are substituted with strings.NewReplacer so bash's own $, %,
// backticks and printf format strings survive untouched.
//
// Machine-readable protocol on stdout — the operator sees it, and `nggctl`
// parses the same lines when re-running parts of the flow:
//
//	NGG_STEP <n> <title>     a step started
//	NGG_OK <n>               a step finished
//	NGG_FAIL <n> <reason>    the run aborted here (exit code 1)
//	NGG_NOTE <text>          something worth knowing but not a failure
//	NGG_NODE_READY k=v ...   the node is live; last line of a successful run

// configDelimiter terminates the kernel-config heredoc. Rendered configs never
// contain it (it is checked before rendering).
const configDelimiter = "NGG_XRAY_CONFIG"

// manifestDelimiter terminates the node-manifest heredoc.
const manifestDelimiter = "NGG_NODE_MANIFEST"

// CLI heredoc delimiter.
const cliDelimiter = "NGG_NODE_CTL"

// PEM heredoc delimiters.
const (
	certDelimiter = "NGG_NODE_CERT"
	keyDelimiter  = "NGG_NODE_KEY"
	caDelimiter   = "NGG_CA_CERT"
)

// kernelBinPlaceholder is replaced on the node itself (step 3) because the
// kernel path is discovered at runtime: the machine may already have
// /usr/local/bin/xray from another panel, in which case NGG reuses it.
const kernelBinPlaceholder = "@KERNEL_BIN@"

// manifest is the node's identity on disk: who it is, which kernel runs it,
// which port the panel dials and where the certificates live. It never
// contains a private key — those live next to it, 0600.
type manifest struct {
	NodeID      string `json:"node_id"`
	HostName    string `json:"host_name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	NodePort    int    `json:"node_port"`
	Mode        string `json:"mode"`
	Protocol    string `json:"protocol"`
	SNI         string `json:"sni"`
	UUID        string `json:"uuid,omitempty"`
	RealityPub  string `json:"reality_public,omitempty"`
	ShortID     string `json:"short_id,omitempty"`
	Password    string `json:"password,omitempty"`
	ConfigPath  string `json:"config_path"`
	KernelBin   string `json:"kernel_bin"`
	CoreService string `json:"core_service"`
	APIService  string `json:"api_service"`
	CertPath    string `json:"cert_path"`
	KeyPath     string `json:"key_path"`
	CAPath      string `json:"ca_path"`
	GeneratedAt string `json:"generated_at"`
	NGGVersion  string `json:"ngg_version"`
}

// Script renders the deployment script for s. s must have gone through Prepare
// and must carry the certificate material the panel minted for this node.
func Script(s Spec) (string, error) {
	cfg, err := ConfigJSON(s)
	if err != nil {
		return "", err
	}
	if strings.Contains(cfg, configDelimiter) {
		return "", fmt.Errorf("内核配置包含保留标记 %s，请改名后重试", configDelimiter)
	}
	if err := checkScriptValues(s, cfg); err != nil {
		return "", err
	}

	mf := manifest{
		NodeID:      s.HostID,
		HostName:    s.HostName,
		Address:     s.Address,
		Port:        s.Port,
		NodePort:    s.NodePort,
		Mode:        s.Mode,
		Protocol:    s.Protocol(),
		SNI:         s.SNI,
		UUID:        s.UUID,
		RealityPub:  s.RealityPublic,
		ShortID:     s.ShortID,
		Password:    s.Password,
		ConfigPath:  NodeConfigPath,
		KernelBin:   kernelBinPlaceholder,
		CoreService: CoreService,
		APIService:  NodeService,
		CertPath:    NodeCertPEM,
		KeyPath:     NodeKeyPEM,
		CAPath:      NodeCAPEM,
		GeneratedAt: s.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"),
		NGGVersion:  version.Release,
	}
	mfRaw, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return "", err
	}

	body, err := modeBody(s)
	if err != nil {
		return "", err
	}

	out := fill(preambleTpl, s)
	out += fill(body, s)
	out += fill(configTpl, s)
	out += fill(installServiceTpl, s)
	out += fill(startTpl, s)
	out += fill(firewallTpl, s)
	out += fill(nodeCtlTpl, s)
	out += fill(selfTestTpl, s)

	out = strings.NewReplacer(
		"{{CONFIG}}", cfg,
		"{{MANIFEST}}", string(mfRaw),
		"{{NODE_CERT_PEM}}", string(s.NodeCertPEM),
		"{{NODE_KEY_PEM}}", string(s.NodeKeyPEM),
		"{{CA_PEM}}", string(s.CAPEM),
	).Replace(out)

	if !strings.Contains(out, "NGG_NODE_READY node=%s") {
		return "", errors.New("脚本缺少就绪标记")
	}
	if strings.Contains(out, "{{") {
		return "", errors.New("脚本仍有未替换的占位符")
	}
	return out, nil
}

// hostNameRE keeps a display name from breaking out of a double-quoted shell
// assignment. The name is operator input (it can be Chinese), so this allows
// anything except the characters that are special inside "…".
var hostNameRE = regexp.MustCompile(`^[^"'$` + "`" + `\\\r\n]*$`)

// addressRE is deliberately strict: the address ends up in URLs and in
// certificate SANs, so only hostname / IP characters are allowed.
var addressRE = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

var sha256RE = regexp.MustCompile(`^[a-f0-9]{64}$`)

// checkScriptValues rejects anything that could corrupt the generated shell or
// smuggle a second command into it.
func checkScriptValues(s Spec, cfg string) error {
	if !hostNameRE.MatchString(s.HostName) {
		return fmt.Errorf("%w: 节点名称含引号、反引号或换行，无法安全写入脚本", ErrInvalidSpec)
	}
	if !addressRE.MatchString(s.Address) {
		return fmt.Errorf("%w: 节点地址只允许字母、数字、点、冒号、下划线与连字符", ErrInvalidSpec)
	}
	if !addressRE.MatchString(s.SNI) {
		return fmt.Errorf("%w: SNI 只允许字母、数字、点、连字符", ErrInvalidSpec)
	}
	if !strings.HasPrefix(s.NodeBinBase, "http://") && !strings.HasPrefix(s.NodeBinBase, "https://") {
		return fmt.Errorf("%w: 节点二进制地址需以 http:// 或 https:// 开头", ErrInvalidSpec)
	}
	for _, d := range []struct{ name, val string }{
		{"amd64 摘要", s.NodeBinSHA256AMD64},
		{"arm64 摘要", s.NodeBinSHA256ARM64},
	} {
		if d.val != "" && !sha256RE.MatchString(d.val) {
			return fmt.Errorf("%w: %s 不是 64 位十六进制", ErrInvalidSpec, d.name)
		}
	}
	if len(s.NodeCertPEM) == 0 || len(s.NodeKeyPEM) == 0 || len(s.CAPEM) == 0 {
		return fmt.Errorf("%w: 缺少节点证书材料，无法生成脚本", ErrInvalidSpec)
	}
	for _, d := range []struct {
		name, body, delim string
	}{
		{"节点证书", string(s.NodeCertPEM), certDelimiter},
		{"节点私钥", string(s.NodeKeyPEM), keyDelimiter},
		{"面板 CA", string(s.CAPEM), caDelimiter},
		{"内核配置", cfg, configDelimiter},
	} {
		if strings.Contains(d.body, d.delim) {
			return fmt.Errorf("%w: %s 包含保留标记 %s", ErrInvalidSpec, d.name, d.delim)
		}
	}
	return nil
}

// fill substitutes the scalar placeholders.
func fill(tpl string, s Spec) string {
	return strings.NewReplacer(
		"{{HOST_ID}}", s.HostID,
		"{{HOST_NAME}}", s.HostName,
		"{{ADDRESS}}", s.Address,
		"{{PORT}}", itoa(s.Port),
		"{{NODE_PORT}}", itoa(s.NodePort),
		"{{MODE}}", s.Mode,
		"{{PROTOCOL}}", s.Protocol(),
		"{{SNI}}", s.SNI,
		"{{UUID}}", s.UUID,
		"{{REALITY_PRIVATE}}", s.RealityPrivate,
		"{{REALITY_PUBLIC}}", s.RealityPublic,
		"{{SHORT_ID}}", s.ShortID,
		"{{PASSWORD}}", s.Password,
		"{{PROFILE_NAME}}", s.ProfileName,
		"{{NODE_DIR}}", NodeDir,
		"{{NODE_CONFIG}}", NodeConfigPath,
		"{{NODE_MANIFEST}}", NodeManifestPath,
		"{{NODE_BIN}}", NodeBin,
		"{{NODE_CTL}}", NodeCtlBin,
		"{{API_UNIT}}", NodeService,
		"{{CORE_UNIT}}", CoreService,
		"{{PKI_DIR}}", NodePKIDir,
		"{{NODE_CERT}}", NodeCertPEM,
		"{{NODE_KEY}}", NodeKeyPEM,
		"{{NODE_CA}}", NodeCAPEM,
		"{{NODE_BIN_BASE}}", s.NodeBinBase,
		"{{SHA_AMD64}}", s.NodeBinSHA256AMD64,
		"{{SHA_ARM64}}", s.NodeBinSHA256ARM64,
		"{{KERNEL_BIN_PLACEHOLDER}}", kernelBinPlaceholder,
		"{{NGG_VERSION}}", version.Release,
		"{{GENERATED_AT}}", s.GeneratedAt.Format("2006-01-02 15:04:05 UTC"),
	).Replace(tpl)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// modeBody picks the kernel-specific middle: REALITY verification, TLS
// certificate generation, or nothing for a bound profile.
func modeBody(s Spec) (string, error) {
	switch s.Mode {
	case ModeReality:
		return realityBodyTpl, nil
	case ModeVLESS, ModeTrojan:
		return tlsBodyTpl, nil
	case ModeProfile:
		return profileBodyTpl, nil
	}
	return "", fmt.Errorf("%w: 模式 %q 没有部署步骤", ErrInvalidSpec, s.Mode)
}

const preambleTpl = `#!/usr/bin/env bash
# ============================================================================
# NGG 节点安装脚本 · 面板下发 / 节点执行（双向 TLS，节点不需要 SSH）
# ----------------------------------------------------------------------------
# 节点 ID   : {{HOST_ID}}
# 节点名称  : {{HOST_NAME}}
# 模式      : {{MODE}}（{{PROTOCOL}}）
# 数据端口  : {{ADDRESS}}:{{PORT}}（客户端连这个）
# 管理端口  : {{NODE_PORT}}（面板连这个，只对面板开放）
# 生成时间  : {{GENERATED_AT}}
# 生成版本  : NGG {{NGG_VERSION}}
#
# 架构（与 Remnawave 的 panel ↔ node 一致）
#   面板 → 节点：双向 TLS 调 https://{{ADDRESS}}:{{NODE_PORT}}/node/*
#                 下发配置 POST /node/xray/start、读取指标 GET /node/stats/get-system-stats
#   节点 → 面板：从不主动连接。节点不需要能访问面板，也不需要 SSH 密码。
#   节点身份   ：证书 CN = {{HOST_ID}}，由面板自己的 CA 签发；面板每次拨号都会校验
#
# 这个脚本会做什么
#   1  预检：root / systemd / 架构 / 端口占用 / 磁盘
#   2  安装依赖（只装缺少的包，已装则跳过 apt）
#   3  准备 Xray 内核（已有可用内核直接复用，否则只下载二进制，不动任何 service）
#   4  写入节点清单 {{NODE_MANIFEST}}（节点身份 + 端口 + 单元名，0600）
#   5  生成传输层凭据（REALITY 校验密钥对 / TLS 自签证书）
#   6  写入内核配置 {{NODE_CONFIG}}（0600）
#   7  用内核自带的校验器验证配置（失败即中止，不留半启动状态）
#   8  安装节点控制服务 {{NODE_BIN}}（下载 + SHA256 校验，校验不过不执行）
#   9  写入节点证书并启动 {{API_UNIT}}（面板拨号的管理接口，双向认证）
#  10  注册并启动 {{CORE_UNIT}}（内核，与 API 分离，面板可单独重启它）
#  11  放通 {{PORT}}（数据）与 {{NODE_PORT}}（管理）
#  12  安装管理命令 {{NODE_CTL}}
#  13  自检：本机用 mTLS 调一次 /node/xray/healthcheck，并确认无证书会被拒绝
#
# 这个脚本不会做什么
#   - 不修改 /usr/local/etc/xray 与其它面板（Remnawave / Marzban）的配置和单元
#   - 不改 sshd，不改防火墙默认策略，不安装 docker
#   - 不往面板推任何数据（没有探针，没有上报）
#
# 幂等：可重复执行，会用同一套凭据覆盖 {{NODE_DIR}} 下的配置并重启单元。
# 卸载：{{NODE_CTL}} uninstall
#
# 警告：脚本包含该节点自己的凭据（UUID / REALITY 私钥 / 节点证书私钥），
#       请勿公开或提交到版本库。重新生成方案会签发新的节点证书。
# ============================================================================
set -uo pipefail

NODE_ID="{{HOST_ID}}"
NODE_NAME="{{HOST_NAME}}"
ADDRESS="{{ADDRESS}}"
PORT="{{PORT}}"
NODE_PORT="{{NODE_PORT}}"
MODE="{{MODE}}"
SNI="{{SNI}}"
CONFIG="{{NODE_CONFIG}}"
MANIFEST="{{NODE_MANIFEST}}"
CORE_UNIT="{{CORE_UNIT}}"
API_UNIT="{{API_UNIT}}"
NODE_BIN="{{NODE_BIN}}"
NODE_CTL="{{NODE_CTL}}"
PKI_DIR="{{PKI_DIR}}"
CERT="{{NODE_CERT}}"
KEY="{{NODE_KEY}}"
CA="{{NODE_CA}}"
NODE_BIN_BASE="{{NODE_BIN_BASE}}"
SHA_AMD64="{{SHA_AMD64}}"
SHA_ARM64="{{SHA_ARM64}}"
STEP=""

log()  { printf '[NGG] %s\n' "$*"; }
note() { printf 'NGG_NOTE %s\n' "$*"; }
step() { STEP="$1"; printf '\nNGG_STEP %s %s\n' "$1" "$2"; }
ok()   { printf 'NGG_OK %s\n' "$1"; }
die()  { printf 'NGG_FAIL %s %s\n' "$1" "$2"; printf '[NGG] 安装中止：%s\n' "$2" >&2; exit 1; }

log "NGG 节点安装开始：$NODE_ID（$MODE，数据 :$PORT / 管理 :$NODE_PORT）"

# ---------------------------------------------------------------- 1 预检
step 1 "预检：root / systemd / 架构 / 端口 / 磁盘"
[ "$(id -u)" -eq 0 ] || die 1 "需要 root 权限，请用 sudo -i 后重试"
command -v systemctl >/dev/null 2>&1 || die 1 "未找到 systemd（本脚本面向 Ubuntu / Debian）"
case "$(uname -m)" in
  x86_64|amd64)  NGG_ARCH=amd64 ;;
  aarch64|arm64) NGG_ARCH=arm64 ;;
  *) die 1 "不支持的架构：$(uname -m)" ;;
esac

# 端口被占用时区分「是不是 NGG 自己」：自己的单元允许覆盖重启，别人的直接中止。
port_busy() { ss -lntH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]$1$"; }
if port_busy "$PORT"; then
  if systemctl is-active --quiet "$CORE_UNIT" 2>/dev/null; then
    note "数据端口 ${PORT} 当前由 ${CORE_UNIT} 占用，将重启该单元"
  else
    OWNER="$(ss -lntpH 2>/dev/null | awk -v p=":${PORT}" '$4 ~ p"$" {print $6; exit}')"
    die 1 "数据端口 ${PORT} 已被其它进程占用${OWNER:+（$OWNER）}，请换端口或先停止该服务"
  fi
fi
if port_busy "$NODE_PORT"; then
  if systemctl is-active --quiet "$API_UNIT" 2>/dev/null; then
    note "管理端口 ${NODE_PORT} 当前由 ${API_UNIT} 占用，将重启该单元"
  else
    OWNER="$(ss -lntpH 2>/dev/null | awk -v p=":${NODE_PORT}" '$4 ~ p"$" {print $6; exit}')"
    die 1 "管理端口 ${NODE_PORT} 已被其它进程占用${OWNER:+（$OWNER）}，请换管理端口"
  fi
fi

DISK_KB="$(df -Pk / | awk 'NR==2 {print $4}')"
[ "${DISK_KB:-0}" -ge 204800 ] || die 1 "根分区剩余空间不足 200 MiB（当前 ${DISK_KB:-0} KiB）"
[ "$(uname -s)" = "Linux" ] || die 1 "仅支持 Linux"
ok 1

# ---------------------------------------------------------------- 2 依赖
step 2 "安装依赖（只装缺少的包）"
NEED=""
for pkg in curl ca-certificates openssl unzip iproute2; do
  case "$pkg" in
    curl)          command -v curl >/dev/null 2>&1 || NEED="$NEED curl" ;;
    iproute2)      command -v ss >/dev/null 2>&1 || NEED="$NEED iproute2" ;;
    openssl)       command -v openssl >/dev/null 2>&1 || NEED="$NEED openssl" ;;
    unzip)         command -v unzip >/dev/null 2>&1 || NEED="$NEED unzip" ;;
    ca-certificates) [ -f /etc/ssl/certs/ca-certificates.crt ] || NEED="$NEED ca-certificates" ;;
  esac
done
if [ -n "$NEED" ]; then
  log "缺少：$NEED"
  export DEBIAN_FRONTEND=noninteractive
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -y >/dev/null 2>&1 || note "apt-get update 失败，继续尝试安装"
    # shellcheck disable=SC2086
    apt-get install -y --no-install-recommends $NEED >/dev/null 2>&1 || die 2 "依赖安装失败：$NEED"
  else
    die 2 "缺少 $NEED 且没有 apt-get，请手动安装后重试"
  fi
else
  log "依赖齐备，跳过 apt"
fi
ok 2

# ---------------------------------------------------------------- 3 内核
step 3 "准备 Xray 内核"
KERNEL_BIN=""
if [ -x /usr/local/bin/xray ] && /usr/local/bin/xray version >/dev/null 2>&1; then
  KERNEL_BIN=/usr/local/bin/xray
  note "复用已安装的 Xray：$(/usr/local/bin/xray version | head -1)"
elif [ -x /usr/local/bin/ngg-xray ] && /usr/local/bin/ngg-xray version >/dev/null 2>&1; then
  KERNEL_BIN=/usr/local/bin/ngg-xray
  note "复用 NGG 自带的 Xray：$(/usr/local/bin/ngg-xray version | head -1)"
else
  case "$NGG_ARCH" in
    amd64) NGG_ZIP="Xray-linux-64.zip" ;;
    arm64) NGG_ZIP="Xray-linux-arm64-v8a.zip" ;;
  esac
  NGG_URL="https://github.com/XTLS/Xray-core/releases/latest/download/${NGG_ZIP}"
  log "下载内核：$NGG_URL"
  rm -rf /tmp/ngg-xray-pkg /tmp/ngg-xray.zip
  curl -fsSL --retry 3 --connect-timeout 10 -o /tmp/ngg-xray.zip "$NGG_URL" || die 3 "内核下载失败（网络不可达 GitHub）"
  unzip -oq /tmp/ngg-xray.zip -d /tmp/ngg-xray-pkg || die 3 "内核解压失败"
  install -m 0755 /tmp/ngg-xray-pkg/xray /usr/local/bin/ngg-xray || die 3 "内核安装失败"
  rm -rf /tmp/ngg-xray-pkg /tmp/ngg-xray.zip
  KERNEL_BIN=/usr/local/bin/ngg-xray
  note "已安装 $("$KERNEL_BIN" version | head -1)"
fi
[ -n "$KERNEL_BIN" ] || die 3 "没有可用的 Xray 内核"
KERNEL_VERSION="$("$KERNEL_BIN" version 2>/dev/null | head -1)"
ok 3

# ---------------------------------------------------------------- 4 清单
step 4 "写入节点清单 ${MANIFEST}"
install -d -m 0750 "{{NODE_DIR}}" "{{NODE_DIR}}/xray" || die 4 "无法创建 {{NODE_DIR}}"
cat > "$MANIFEST" <<'` + manifestDelimiter + `'
{{MANIFEST}}
` + manifestDelimiter + `
# 内核路径只有运行时才知道（可能复用机器上已有的 Xray）
sed -i "s|{{KERNEL_BIN_PLACEHOLDER}}|${KERNEL_BIN}|" "$MANIFEST" || die 4 "无法写入内核路径"
chmod 600 "$MANIFEST" || die 4 "无法设置清单权限"
log "节点身份：$NODE_ID · 管理端口 :$NODE_PORT · 面板通过 https://${ADDRESS}:${NODE_PORT} 连入"
ok 4

`
