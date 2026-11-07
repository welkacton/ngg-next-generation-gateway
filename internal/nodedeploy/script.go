package nodedeploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ngg-project/ngg/internal/agent"
	"github.com/ngg-project/ngg/internal/version"
)

// Bash template rendering.
//
// The script is assembled from one preamble (identity + helpers), one kernel
// body per mode, a fixed middle (config / validate / start / firewall), one
// probe installer, one management CLI and a fixed tail (self-test).
// Placeholders are substituted with strings.NewReplacer so bash's own $, %,
// backticks and printf format strings survive untouched.
//
// Machine-readable protocol on stdout — the panel parses exactly these lines:
//
//	NGG_STEP <n> <title>     a step started
//	NGG_OK <n>               a step finished
//	NGG_FAIL <n> <reason>    the run aborted here (exit code 1)
//	NGG_NOTE <text>          something the operator should know but that is
//	                         not a failure (e.g. "port was already in use by
//	                         the NGG unit itself")
//	NGG_NODE_READY k=v ...   the node is live; last line of a successful run

// configDelimiter terminates the kernel-config heredoc. Rendered configs never
// contain it (it is checked before rendering).
const configDelimiter = "NGG_XRAY_CONFIG"

// manifestDelimiter terminates the node-manifest heredoc.
const manifestDelimiter = "NGG_NODE_MANIFEST"

// cliDelimiter terminates the ngg-node CLI heredoc.
const cliDelimiter = "NGG_NODE_CLI"

// kernelBinPlaceholder is replaced on the node itself (step 4) because the
// kernel path is discovered at runtime: the machine may already have
// /usr/local/bin/xray from another panel, in which case NGG reuses it.
const kernelBinPlaceholder = "@KERNEL_BIN@"

// manifest is the node's identity on disk: who it is, which kernel runs it,
// and which agent identity it reports under. It never contains the probe token
// — that lives only in NodeProbeConf (0600).
type manifest struct {
	NodeID        string `json:"node_id"`
	HostName      string `json:"host_name"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	Mode          string `json:"mode"`
	Protocol      string `json:"protocol"`
	SNI           string `json:"sni"`
	UUID          string `json:"uuid,omitempty"`
	RealityPublic string `json:"reality_public,omitempty"`
	ShortID       string `json:"short_id,omitempty"`
	Password      string `json:"password,omitempty"`
	ConfigPath    string `json:"config_path"`
	KernelBin     string `json:"kernel_bin"`
	Service       string `json:"service"`
	AgentID       string `json:"agent_id,omitempty"`
	PanelURL      string `json:"panel_url,omitempty"`
	ProbeUnit     string `json:"probe_unit,omitempty"`
	GeneratedAt   string `json:"generated_at"`
	NGGVersion    string `json:"ngg_version"`
}

// Script renders the deployment script for s. s must have gone through Prepare.
func Script(s Spec) (string, error) {
	cfg, err := ConfigJSON(s)
	if err != nil {
		return "", err
	}
	if strings.Contains(cfg, configDelimiter) {
		return "", fmt.Errorf("内核配置包含保留标记 %s，请改名后重试", configDelimiter)
	}
	if err := checkProbeValues(s); err != nil {
		return "", err
	}

	mf := manifest{
		NodeID:        s.HostID,
		HostName:      s.HostName,
		Address:       s.Address,
		Port:          s.Port,
		Mode:          s.Mode,
		Protocol:      s.Protocol(),
		SNI:           s.SNI,
		UUID:          s.UUID,
		RealityPublic: s.RealityPublic,
		ShortID:       s.ShortID,
		Password:      s.Password,
		ConfigPath:    NodeConfigPath,
		KernelBin:     kernelBinPlaceholder,
		Service:       NodeService,
		AgentID:       s.AgentID,
		PanelURL:      s.PanelURL,
		ProbeUnit:     NodeProbeUnit,
		GeneratedAt:   s.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"),
		NGGVersion:    version.Release,
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
	out += fill(startTpl, s)
	out += fill(firewallTpl, s)
	out += fill(probeInstallTpl, s)
	out += fill(nodeCLITpl, s)
	out += fill(selfTestTpl, s)

	out = strings.NewReplacer(
		"{{CONFIG}}", cfg,
		"{{MANIFEST}}", string(mfRaw),
		"{{AGENT_CONF}}", probeConf(s),
		"{{PROBE}}", fill(probeTpl, s),
	).Replace(out)

	if !strings.Contains(out, "NGG_NODE_READY node=%s") {
		return "", errors.New("脚本缺少就绪标记")
	}
	if strings.Contains(out, "{{") {
		return "", errors.New("脚本仍有未替换的占位符")
	}
	return out, nil
}

// probeConf renders NodeProbeConf. Values are single-quoted, so Script refuses
// anything that could break out of a shell assignment (see checkProbeValues).
func probeConf(s Spec) string {
	return fmt.Sprintf(`# NGG 节点探针配置（由 NGG 部署脚本生成，重新部署会覆盖）
# 这是该节点的密钥文件：0600，不要提交到任何仓库，也不要复制到其它机器。
PANEL_URL='%s'
AGENT_ID='%s'
AGENT_TOKEN='%s'
INTERVAL=%d
NODE_MANIFEST='%s'
`, s.PanelURL, s.AgentID, s.AgentToken, agent.DefaultIntervalSec, NodeManifestPath)
}

// checkProbeValues rejects values that would corrupt the generated shell
// assignments. Everything else about them is validated in Prepare.
func checkProbeValues(s Spec) error {
	for _, f := range []struct{ name, val string }{
		{"面板上报地址", s.PanelURL},
		{"探针 ID", s.AgentID},
		{"探针令牌", s.AgentToken},
	} {
		if strings.ContainsAny(f.val, "'\"\\ \t\r\n") {
			return fmt.Errorf("%w: %s 含引号或空白字符", ErrInvalidSpec, f.name)
		}
	}
	if s.PanelURL != "" && !strings.HasPrefix(s.PanelURL, "http://") && !strings.HasPrefix(s.PanelURL, "https://") {
		return fmt.Errorf("%w: 面板上报地址需以 http:// 或 https:// 开头", ErrInvalidSpec)
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
		"{{MODE}}", s.Mode,
		"{{PROTOCOL}}", s.Protocol(),
		"{{SNI}}", s.SNI,
		"{{UUID}}", s.UUID,
		"{{REALITY_PRIVATE}}", s.RealityPrivate,
		"{{REALITY_PUBLIC}}", s.RealityPublic,
		"{{SHORT_ID}}", s.ShortID,
		"{{PASSWORD}}", s.Password,
		"{{AGENT_ID}}", s.AgentID,
		"{{AGENT_TOKEN}}", s.AgentToken,
		"{{PANEL_URL}}", s.PanelURL,
		"{{PROFILE_NAME}}", s.ProfileName,
		"{{NODE_DIR}}", NodeDir,
		"{{NODE_CONFIG}}", NodeConfigPath,
		"{{NODE_MANIFEST}}", NodeManifestPath,
		"{{NODE_BIN}}", NodeBin,
		"{{NODE_SERVICE}}", NodeService,
		"{{PROBE_BIN}}", NodeProbeBin,
		"{{PROBE_CONF}}", NodeProbeConf,
		"{{PROBE_UNIT}}", NodeProbeUnit,
		"{{PROBE_INTERVAL}}", itoa(agent.DefaultIntervalSec),
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
# NGG 节点部署脚本 · 节点主动上报（面板不需要 SSH 进来）
# ----------------------------------------------------------------------------
# 节点 ID  : {{HOST_ID}}
# 节点名称 : {{HOST_NAME}}
# 模式     : {{MODE}}（{{PROTOCOL}}）
# 监听     : {{ADDRESS}}:{{PORT}}
# 上报到   : {{PANEL_URL}}
# 生成时间 : {{GENERATED_AT}}
# 生成版本 : NGG {{NGG_VERSION}}
#
# 这个脚本会做什么
#   1  预检：root / systemd / 架构 / 端口占用 / 磁盘
#   2  安装依赖（只装缺少的包，已装则跳过 apt）
#   3  准备 Xray 内核（已有可用内核直接复用，否则只下载二进制，不动任何 service）
#   4  写入节点清单 {{NODE_MANIFEST}}（节点身份 + 内核路径 + 探针编号，0600）
#   5  生成传输层凭据（REALITY 校验密钥对 / TLS 自签证书）
#   6  写入内核配置 {{NODE_CONFIG}}（0600）
#   7  用内核自带的校验器验证配置（失败即中止，不留半启动状态）
#   8  注册并启动独立单元 {{NODE_SERVICE}}（不重启、不改写别人的服务）
#   9  放通 {{PORT}} 端口（仅当 ufw 已启用时动 ufw；iptables 规则不持久化）
#  10  安装上报探针 {{PROBE_BIN}}（每 {{PROBE_INTERVAL}} 秒主动上报到面板，无需入站端口）
#  11  安装管理命令 {{NODE_BIN}}（params / status / logs / restart / uninstall）
#  12  自检：端口监听、单元状态、探针上报，并打印客户端参数
#
# 信息怎么回到面板（与哪吒探针同一模式）
#   节点 → 面板：POST {{PANEL_URL}}/v1/agent/report（带 X-NGG-Agent-Token）
#   面板 → 节点：从不主动连接，不需要 SSH，不需要开管理端口
#   面板断连时探针按 1s→60s 退避重试，恢复后自动续报。
#
# 这个脚本不会做什么
#   - 不修改 /usr/local/etc/xray 与其它面板（Remnawave / Marzban）的配置和单元
#   - 不改 sshd，不改防火墙默认策略，不安装 docker
#
# 幂等：可重复执行，会用同一套凭据覆盖 {{NODE_DIR}} 下的配置并重启单元。
# 卸载：{{NODE_BIN}} uninstall
#
# 警告：脚本包含该节点自己的凭据，请勿公开或提交到版本库。
# ============================================================================
set -uo pipefail

NODE_ID="{{HOST_ID}}"
NODE_NAME="{{HOST_NAME}}"
ADDRESS="{{ADDRESS}}"
PORT="{{PORT}}"
MODE="{{MODE}}"
SNI="{{SNI}}"
CONFIG="{{NODE_CONFIG}}"
UNIT="{{NODE_SERVICE}}"
PANEL_URL="{{PANEL_URL}}"
AGENT_ID="{{AGENT_ID}}"
PROBE_BIN="{{PROBE_BIN}}"
PROBE_CONF="{{PROBE_CONF}}"
PROBE_UNIT="{{PROBE_UNIT}}"
STEP=""

log()  { printf '[NGG] %s\n' "$*"; }
note() { printf 'NGG_NOTE %s\n' "$*"; }
step() { STEP="$1"; printf '\nNGG_STEP %s %s\n' "$1" "$2"; }
ok()   { printf 'NGG_OK %s\n' "$1"; }
die()  { printf 'NGG_FAIL %s %s\n' "$1" "$2"; printf '[NGG] 部署中止：%s\n' "$2" >&2; exit 1; }

log "NGG 节点部署开始：$NODE_ID（$MODE，$ADDRESS:$PORT）"

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
if ss -lntH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${PORT}$"; then
  if systemctl is-active --quiet "$UNIT" 2>/dev/null; then
    note "端口 ${PORT} 当前由 ${UNIT} 占用，将重启该单元"
  else
    OWNER="$(ss -lntpH 2>/dev/null | awk -v p=":${PORT}" '$4 ~ p"$" {print $6; exit}')"
    die 1 "端口 ${PORT} 已被其它进程占用${OWNER:+（$OWNER）}，请换端口或先停止该服务"
  fi
fi

DISK_KB="$(df -Pk / | awk 'NR==2 {print $4}')"
[ "${DISK_KB:-0}" -ge 204800 ] || die 1 "根分区剩余空间不足 200 MiB（当前 ${DISK_KB:-0} KiB）"
[ "$(uname -s)" = "Linux" ] || die 1 "仅支持 Linux"
ok 1

# ---------------------------------------------------------------- 2 依赖
step 2 "安装依赖（只装缺少的包）"
NEED=""
for pkg in curl ca-certificates openssl unzip; do
  case "$pkg" in
    curl)          command -v curl >/dev/null 2>&1 || NEED="$NEED curl" ;;
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
step 4 "写入节点清单 {{NODE_MANIFEST}}"
install -d -m 0750 "{{NODE_DIR}}" "{{NODE_DIR}}/xray" || die 4 "无法创建 {{NODE_DIR}}"
cat > "{{NODE_MANIFEST}}" <<'` + manifestDelimiter + `'
{{MANIFEST}}
` + manifestDelimiter + `
# 内核路径只有运行时才知道（可能复用机器上已有的 Xray）
sed -i "s|{{KERNEL_BIN_PLACEHOLDER}}|${KERNEL_BIN}|" "{{NODE_MANIFEST}}" || die 4 "无法写入内核路径"
chmod 600 "{{NODE_MANIFEST}}" || die 4 "无法设置清单权限"
if [ -n "$PANEL_URL" ]; then
  log "节点身份已写入，上报目标：$PANEL_URL"
else
  log "节点身份已写入（未配置面板上报地址）"
fi
ok 4

`
