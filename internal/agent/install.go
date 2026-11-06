package agent

import (
	"fmt"
	"strings"

	"github.com/ngg-project/ngg/internal/version"
)

// releaseDownloadBase is the GitHub release asset prefix used by every
// generated installer. The version comes from internal/version.Release so the
// scripts can never point at a different build than the running daemon.
const releaseDownloadBase = "https://github.com/welkacton/ngg-next-generation-gateway/releases/download"

// TokenPlaceholder is emitted when a script is generated without a plaintext
// token (the token is only known at create / rotate time and is never stored).
// An un-replaced placeholder makes the script fail to authenticate instead of
// silently shipping a working-looking but useless config.
const TokenPlaceholder = "REPLACE_WITH_AGENT_TOKEN"

// ServerPlaceholder is used when the operator has not configured public_url
// and the listen address is not routable (e.g. 0.0.0.0).
const ServerPlaceholder = "http://REPLACE_WITH_NGG_SERVER:9191"

// InstallParams are the variables injected into installers and agent.yaml.
type InstallParams struct {
	Server      string
	AgentID     string
	Token       string
	IntervalSec int
}

func (p InstallParams) interval() int {
	if p.IntervalSec <= 0 {
		return DefaultIntervalSec
	}
	return p.IntervalSec
}

func (p InstallParams) token() string {
	t := strings.TrimSpace(p.Token)
	if t == "" {
		return TokenPlaceholder
	}
	return t
}

func (p InstallParams) server() string {
	s := strings.TrimRight(strings.TrimSpace(p.Server), "/")
	if s == "" {
		return ServerPlaceholder
	}
	return s
}

func (p InstallParams) agentID() string {
	id := strings.TrimSpace(p.AgentID)
	if id == "" {
		return "REPLACE_WITH_AGENT_ID"
	}
	return id
}

// DownloadBase is the release asset directory for the running version.
func DownloadBase() string {
	return fmt.Sprintf("%s/v%s/", releaseDownloadBase, version.Release)
}

// ConfigYAML renders agent.yaml exactly as the plugin expects to read it.
func ConfigYAML(p InstallParams) string {
	var b strings.Builder
	b.WriteString("# NGG 远程代理节点配置（agent.yaml）\n")
	b.WriteString("# token 等同于该节点的密钥：请用 0600 / NTFS ACL 保护本文件，不要提交到任何仓库。\n")
	b.WriteString(fmt.Sprintf("server: %s\n", p.server()))
	b.WriteString(fmt.Sprintf("agent_id: %s\n", p.agentID()))
	b.WriteString(fmt.Sprintf("token: %s\n", p.token()))
	b.WriteString(fmt.Sprintf("interval_sec: %d\n", p.interval()))
	b.WriteString("insecure_skip_verify: false\n")
	return b.String()
}

// Scripts returns the three installer scripts keyed by os name.
func Scripts(p InstallParams) map[string]string {
	return map[string]string{
		"windows": windowsScript(p),
		"linux":   linuxScript(p),
		"docker":  dockerScript(p),
	}
}

// Script returns one installer. Only the three documented targets exist.
func Script(osName string, p InstallParams) (string, error) {
	switch strings.ToLower(strings.TrimSpace(osName)) {
	case "windows":
		return windowsScript(p), nil
	case "linux":
		return linuxScript(p), nil
	case "docker":
		return dockerScript(p), nil
	default:
		return "", fmt.Errorf("%w: os 必须是 windows / linux / docker", ErrInvalid)
	}
}

func windowsScript(p InstallParams) string {
	url := DownloadBase() + "ngg-agent-windows-amd64.exe"
	return fmt.Sprintf(`# NGG 远程代理节点安装脚本（Windows）
# 需要：管理员权限的 PowerShell 5.1+；已发布的 release 必须包含 ngg-agent-windows-amd64.exe
# 执行前请把本文件保存为 .ps1，然后在「以管理员身份运行」的 PowerShell 中执行。
$ErrorActionPreference = "Stop"

$version = "%[1]s"
$url     = "%[2]s"
$dir     = "C:\Program Files\NGG\agent"
$exe     = Join-Path $dir "ngg-agent.exe"
$yaml    = Join-Path $dir "agent.yaml"

New-Item -ItemType Directory -Force -Path $dir | Out-Null
Write-Host "下载 agent $version ..."
Invoke-WebRequest -Uri $url -OutFile $exe -UseBasicParsing

$config = @'
%[3]s'@
Set-Content -Path $yaml -Value $config -Encoding UTF8 -NoNewline

# 只允许 SYSTEM / Administrators 读取配置，避免 token 被普通用户读走。
icacls $yaml /inheritance:r /grant:r "SYSTEM:(R)" "Administrators:(R)" | Out-Null

# 重复执行时先清理旧服务，避免 sc.exe create 报 1073。
if (Get-Service -Name NGGAgent -ErrorAction SilentlyContinue) {
    sc.exe stop NGGAgent | Out-Null
    sc.exe delete NGGAgent | Out-Null
    Start-Sleep -Seconds 2
}

# sc.exe 要求 "key= value" 形式（等号后必须有空格）。
# 路径含空格，因此 binPath 自带引号；单引号拼接避免转义歧义。
$binPath = '"' + $exe + '" -config "' + $yaml + '"'
sc.exe create NGGAgent binPath= $binPath start= auto DisplayName= "NGG Agent" | Out-Null
sc.exe description NGGAgent "NGG remote agent: reports real CPU/memory/disk/network metrics to the NGG control plane" | Out-Null
sc.exe start NGGAgent

Write-Host ""
Write-Host "安装完成。检查状态："
Write-Host "  sc.exe query NGGAgent"
Write-Host '  日志：Get-Content "C:\Program Files\NGG\agent\ngg-agent.log" -Tail 50'
`, version.Release, url, ConfigYAML(p))
}

func linuxScript(p InstallParams) string {
	url := DownloadBase() + "ngg-agent-linux-amd64"
	return fmt.Sprintf(`#!/usr/bin/env bash
# NGG 远程代理节点安装脚本（Linux, amd64）
# 需要：root；release 必须包含 ngg-agent-linux-amd64 资产。
# 其它架构（arm64 等）请自行下载对应资产后替换 /usr/local/bin/ngg-agent。
set -euo pipefail

VERSION="%[1]s"
URL="%[2]s"
BIN="/usr/local/bin/ngg-agent"
CONF_DIR="/etc/ngg"
CONF="${CONF_DIR}/agent.yaml"
UNIT="/etc/systemd/system/ngg-agent.service"

if [ "$(id -u)" -ne 0 ]; then
  echo "请用 root 执行（sudo bash $0）" >&2
  exit 1
fi

echo "下载 agent ${VERSION} ..."
curl -fsSL "${URL}" -o /tmp/ngg-agent.download
install -m 0755 /tmp/ngg-agent.download "${BIN}"
rm -f /tmp/ngg-agent.download

install -d -m 0700 "${CONF_DIR}"
cat > /tmp/ngg-agent.yaml <<'NGGCONF'
%[3]sNGGCONF
install -m 0600 /tmp/ngg-agent.yaml "${CONF}"
rm -f /tmp/ngg-agent.yaml

cat > "${UNIT}" <<'NGGUNIT'
[Unit]
Description=NGG remote agent (real metrics reporter)
Documentation=https://github.com/welkacton/ngg-next-generation-gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/ngg-agent -config /etc/ngg/agent.yaml
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
NGGUNIT

systemctl daemon-reload
systemctl enable --now ngg-agent
systemctl --no-pager --lines=5 status ngg-agent || true

echo ""
echo "安装完成。查看日志：journalctl -u ngg-agent -f"
`, version.Release, url, ConfigYAML(p))
}

func dockerScript(p InstallParams) string {
	image := "ghcr.io/welkacton/ngg-agent:" + version.Release
	return fmt.Sprintf(`# NGG 远程代理节点（Docker）
# 注意：ghcr.io/welkacton/ngg-agent 镜像默认并未发布，需要在本仓库自行构建：
#   docker build -f Dockerfile.agent -t %[1]s .
# 若已发布官方镜像，可把镜像名换回 ghcr.io/welkacton/ngg-agent:latest
set -e

docker rm -f ngg-agent >/dev/null 2>&1 || true

docker run -d --name ngg-agent --restart=always \
  -e NGG_SERVER="%[2]s" \
  -e NGG_AGENT_ID="%[3]s" \
  -e NGG_AGENT_TOKEN="%[4]s" \
  -e NGG_INTERVAL_SEC="%[5]d" \
  -v ngg-agent-data:/etc/ngg \
  %[1]s

echo ""
echo "查看日志：docker logs -f ngg-agent"
`, image, p.server(), p.agentID(), p.token(), p.interval())
}
