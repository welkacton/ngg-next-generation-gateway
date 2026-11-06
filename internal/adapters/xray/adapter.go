// Package xray implements the NGG Core Adapter for XTLS/Xray-core.
package xray

import (
	"encoding/json"
	"fmt"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ core.CoreAdapter = (*Adapter)(nil)
var _ core.ValidateCandidateProvider = (*Adapter)(nil)

func (a *Adapter) Descriptor() contracts.CoreDescriptor {
	return contracts.CoreDescriptor{
		ID:          contracts.CoreXray,
		DisplayName: "Xray-core",
		Description: "XTLS/Xray-core，XTLS / VLESS / REALITY 等协议执行引擎。在 NGG 中以 Worker Mode 运行。",
		Homepage:    "https://github.com/XTLS/Xray-core",
		Repository:  "XTLS/Xray-core",
		Builtin:     true,
		Source:      "builtin",
		Capability: contracts.Capability{
			Protocols: []string{
				"vless", "vmess", "trojan", "shadowsocks", "socks", "http",
				"wireguard", "hysteria2", "tuic", "freedom", "blackhole", "dns",
			},
			InboundTypes:     []string{"socks", "http", "dokodemo-door", "tproxy(disabled-in-worker-mode)"},
			Features:         []string{"xtls-rprx-vision", "reality", "routing", "stats-api"},
			WorkerMode:       true,
			LocalTCPEndpoint: "socks-port",
			NativeStream:     false,
			NativeDatagram:   false,
		},
	}
}

func (a *Adapter) SupportedPlatforms() []contracts.Platform { return core.StandardPlatforms() }

func (a *Adapter) BinaryName(goos string) string {
	if goos == "windows" {
		return "xray.exe"
	}
	return "xray"
}

func (a *Adapter) VersionArgs() []string { return []string{"version"} }

func (a *Adapter) ParseVersion(raw string) (string, error) {
	return core.ExtractVersionAny(raw, `Xray\s+([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-]*)`)
}

func (a *Adapter) ConfigFileName() string { return "config.json" }

// Xray has used several config-check dialects across major versions. Every
// candidate below is a REAL native invocation; none of them starts the proxy
// service. ConfigManager accepts the first that exits 0.
func (a *Adapter) ValidateCandidates(configPath string) [][]string {
	return [][]string{
		{"-test", "-config", configPath},
		{"-test", "-c", configPath},
		{"test", "-c", configPath},
		{"run", "-test", "-c", configPath},
	}
}

func (a *Adapter) ValidateArgs(configPath string) []string {
	return a.ValidateCandidates(configPath)[0]
}

func (a *Adapter) RunArgs(configPath string) []string {
	return []string{"run", "-c", configPath}
}

func (a *Adapter) Environment(_ string) []string { return nil }

func (a *Adapter) Release() core.ReleaseSpec {
	return core.ReleaseSpec{
		Repository: "XTLS/Xray-core",
		AssetTemplates: map[string]string{
			"windows": "Xray-windows-{arch}.zip",
			"linux":   "Xray-linux-{arch}.zip",
			"darwin":  "Xray-macos-{arch}.zip",
			"android": "Xray-android-{arch}.zip",
		},
		DigestTemplate:  "{asset}.dgst",
		Archive:         "zip",
		BinaryInArchive: "xray*",
		ArchMap: map[string]string{
			"amd64": "64", "386": "32", "arm64": "arm64-v8a", "arm": "arm32-v7a",
		},
		DigestFormat:    "dgst+api",
		VersionInURLTag: true,
	}
}

func (a *Adapter) AssetNames(p contracts.Platform, version string) (string, string, error) {
	return core.DefaultAssetNames(a.Release(), p, version)
}

func (a *Adapter) ControlAPI(p core.RuntimeParams) core.ControlAPI {
	// Xray's stats/gRPC API is not enabled in the Worker Mode baseline config.
	return core.ControlAPI{Type: "none"}
}

// DefaultConfig is a valid Xray baseline: two isolated local inbounds and a
// freedom DIRECT outbound. No TUN, no transparent proxy, no global route.
func (a *Adapter) DefaultConfig(p core.RuntimeParams) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag": "ngg-socks", "listen": "127.0.0.1", "port": p.SocksPort,
				"protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": true},
				"sniffing": map[string]any{"enabled": false},
			},
			map[string]any{
				"tag": "ngg-http", "listen": "127.0.0.1", "port": p.HTTPPort,
				"protocol": "http", "settings": map[string]any{"allowTransparent": false},
			},
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules": []any{
				map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "direct"},
			},
		},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("xray: marshal default config: %w", err)
	}
	return append(b, '\n'), nil
}

// LocalProxy exposes Xray's isolated SOCKS inbound (UDP enabled in the
// Worker Mode baseline config) to the Stage 2 Core Bridge.
func (a *Adapter) LocalProxy(p core.RuntimeParams) core.ProxyEndpoint {
	return core.ProxyEndpoint{
		Kind:     core.ProxySOCKS5,
		Address:  fmt.Sprintf("127.0.0.1:%d", p.SocksPort),
		Networks: []string{"tcp", "udp"},
	}
}

var _ core.LocalProxyProvider = (*Adapter)(nil)
