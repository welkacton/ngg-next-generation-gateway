// Package mihomo implements the NGG Core Adapter for MetaCubeX/mihomo
// (Clash.Meta). It contains NO protocol implementation and NO process logic —
// only the contract that lets Core Manager drive the native binary.
package mihomo

import (
	"fmt"
	"path/filepath"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ core.CoreAdapter = (*Adapter)(nil)

func (a *Adapter) Descriptor() contracts.CoreDescriptor {
	return contracts.CoreDescriptor{
		ID:          contracts.CoreMihomo,
		DisplayName: "Mihomo (Clash.Meta)",
		Description: "Clash.Meta 分支内核，规则分流与多协议代理执行引擎。在 NGG 中以 Worker Mode 运行。",
		Homepage:    "https://github.com/MetaCubeX/mihomo",
		Repository:  "MetaCubeX/mihomo",
		Builtin:     true,
		Source:      "builtin",
		Capability: contracts.Capability{
			Protocols: []string{
				"shadowsocks", "shadowsocksR", "vmess", "vless", "trojan", "socks5", "http",
				"hysteria", "hysteria2", "tuic", "wireguard", "snell", "anytls", "mieru",
				"ssh", "direct", "reject",
			},
			InboundTypes: []string{"mixed", "socks", "http", "redir", "tproxy"},
			Features:     []string{"rule", "proxy-group", "clash-api", "tun-capable(disabled-in-worker-mode)"},
			WorkerMode:   true,
			// Stage 2: the Protocol Dispatcher will dial these local endpoints.
			LocalTCPEndpoint: "mixed-port",
			NativeStream:     false,
			NativeDatagram:   false,
		},
	}
}

func (a *Adapter) SupportedPlatforms() []contracts.Platform { return core.StandardPlatforms() }

func (a *Adapter) BinaryName(goos string) string {
	if goos == "windows" {
		return "mihomo.exe"
	}
	return "mihomo"
}

func (a *Adapter) VersionArgs() []string { return []string{"-v"} }

func (a *Adapter) ParseVersion(raw string) (string, error) {
	return core.ExtractVersionAny(raw, `Meta(?: v)?([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-]*)`)
}

func (a *Adapter) ConfigFileName() string { return "config.yaml" }

// ValidateCandidates: Mihomo's -t flag tests the config in -d directory and
// exits. -f is supported on newer builds; keep both real invocations.
func (a *Adapter) ValidateCandidates(configPath string) [][]string {
	return [][]string{
		{"-t", "-f", configPath},
		{"-t", "-d", filepath.Dir(configPath)},
	}
}

func (a *Adapter) ValidateArgs(configPath string) []string {
	return a.ValidateCandidates(configPath)[0]
}

func (a *Adapter) RunArgs(configPath string) []string {
	// Mihomo resolves config.yaml from its -d directory; that is the most
	// portable form across all upstream versions.
	return []string{"-d", filepath.Dir(configPath)}
}

func (a *Adapter) Environment(_ string) []string { return nil }

func (a *Adapter) Release() core.ReleaseSpec {
	return core.ReleaseSpec{
		Repository: "MetaCubeX/mihomo",
		AssetTemplates: map[string]string{
			"windows": "mihomo-windows-{arch}-{vversion}.zip",
			"linux":   "mihomo-linux-{arch}-{vversion}.gz",
			"darwin":  "mihomo-darwin-{arch}-{vversion}.gz",
			"android": "mihomo-android-{arch}-{vversion}.gz",
		},
		Archive:         "auto",
		BinaryInArchive: "mihomo*",
		ArchMap:         map[string]string{"amd64": "amd64", "arm64": "arm64", "386": "386", "arm": "armv7"},
		ArchMapByOS: map[string]map[string]string{
			"android": {"amd64": "amd64", "arm64": "arm64-v8", "386": "386", "arm": "armv7"},
		},
		VersionPrefix:   "v",
		DigestFormat:    "api",
		VersionInURLTag: true,
	}
}

func (a *Adapter) AssetNames(p contracts.Platform, version string) (string, string, error) {
	if version == "" {
		return "", "", fmt.Errorf("mihomo: version is required to resolve a release asset")
	}
	return core.DefaultAssetNames(a.Release(), p, version)
}

func (a *Adapter) ControlAPI(p core.RuntimeParams) core.ControlAPI {
	if p.APIPort <= 0 {
		return core.ControlAPI{Type: "none"}
	}
	return core.ControlAPI{
		Type:   "clash",
		URL:    fmt.Sprintf("http://127.0.0.1:%d", p.APIPort),
		Secret: p.Secret,
	}
}

// DefaultConfig is a deliberately minimal, valid Clash config in Worker Mode:
// no TUN, no DNS hijack, no global routing. NGG (Stage 2/3) owns all of that.
func (a *Adapter) DefaultConfig(p core.RuntimeParams) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	cfg := fmt.Sprintf(`# NGG generated config - Mihomo in Worker Mode.
# NGG owns the system TUN, global routing, DNS and rule engine (Stage 2/3).
# Do not enable tun/dns hijack here; that would fight NGG for global traffic.
mixed-port: %d
allow-lan: false
bind-address: 127.0.0.1
mode: direct
log-level: info
ipv6: false
external-controller: 127.0.0.1:%d
secret: "%s"
tun:
  enable: false
dns:
  enable: false
proxies: []
proxy-groups: []
rules:
  - MATCH,DIRECT
`, p.MixedPort, p.APIPort, p.Secret)
	return []byte(cfg), nil
}

// LocalProxy exposes the isolated mixed inbound to the Stage 2 Core Bridge.
func (a *Adapter) LocalProxy(p core.RuntimeParams) core.ProxyEndpoint {
	return core.ProxyEndpoint{
		Kind:     core.ProxyMixed,
		Address:  fmt.Sprintf("127.0.0.1:%d", p.MixedPort),
		Networks: []string{"tcp", "udp"},
	}
}

var _ core.LocalProxyProvider = (*Adapter)(nil)
