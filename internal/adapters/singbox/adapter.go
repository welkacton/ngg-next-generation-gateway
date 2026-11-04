// Package singbox implements the NGG Core Adapter for SagerNet/sing-box.
package singbox

import (
	"encoding/json"
	"fmt"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ core.CoreAdapter = (*Adapter)(nil)

func (a *Adapter) Descriptor() contracts.CoreDescriptor {
	return contracts.CoreDescriptor{
		ID:          contracts.CoreSingBox,
		DisplayName: "sing-box",
		Description: "通用代理平台内核，支持多协议与多平台。在 NGG 中以 Worker Mode 运行。",
		Homepage:    "https://github.com/SagerNet/sing-box",
		Repository:  "SagerNet/sing-box",
		Builtin:     true,
		Source:      "builtin",
		Capability: contracts.Capability{
			Protocols: []string{
				"shadowsocks", "vmess", "vless", "trojan", "hysteria", "hysteria2", "tuic",
				"wireguard", "socks", "http", "ssh", "shadowtls", "anytls", "naive", "direct", "block",
			},
			InboundTypes:     []string{"mixed", "socks", "http", "tun(disabled-in-worker-mode)", "redirect", "tproxy"},
			Features:         []string{"route", "clash-api", "dns", "rule-set", "platform-clients"},
			WorkerMode:       true,
			LocalTCPEndpoint: "mixed-listen_port",
			NativeStream:     false,
			NativeDatagram:   false,
		},
	}
}

func (a *Adapter) SupportedPlatforms() []contracts.Platform { return core.StandardPlatforms() }

func (a *Adapter) BinaryName(goos string) string {
	if goos == "windows" {
		return "sing-box.exe"
	}
	return "sing-box"
}

func (a *Adapter) VersionArgs() []string { return []string{"version"} }

func (a *Adapter) ParseVersion(raw string) (string, error) {
	return core.ExtractVersionAny(raw, `sing-box version ([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-]*)`)
}

func (a *Adapter) ConfigFileName() string { return "config.json" }

func (a *Adapter) ValidateArgs(configPath string) []string {
	return []string{"check", "-c", configPath}
}

func (a *Adapter) RunArgs(configPath string) []string {
	return []string{"run", "-c", configPath}
}

func (a *Adapter) Environment(_ string) []string { return nil }

func (a *Adapter) Release() core.ReleaseSpec {
	return core.ReleaseSpec{
		Repository: "SagerNet/sing-box",
		AssetTemplates: map[string]string{
			"windows": "sing-box-{version}-windows-{arch}.zip",
			"linux":   "sing-box-{version}-linux-{arch}.tar.gz",
			"darwin":  "sing-box-{version}-darwin-{arch}.tar.gz",
			"android": "sing-box-{version}-android-{arch}.tar.gz",
		},
		Archive:         "auto",
		BinaryInArchive: "sing-box*",
		ArchMap:         map[string]string{"amd64": "amd64", "arm64": "arm64", "386": "386", "arm": "armv7"},
		VersionPrefix:   "v",
		DigestFormat:    "api",
		VersionInURLTag: true,
	}
}

func (a *Adapter) AssetNames(p contracts.Platform, version string) (string, string, error) {
	if version == "" {
		return "", "", fmt.Errorf("sing-box: version is required to resolve a release asset")
	}
	return core.DefaultAssetNames(a.Release(), p, version)
}

func (a *Adapter) ControlAPI(p core.RuntimeParams) core.ControlAPI {
	return core.ControlAPI{Type: "none"}
}

// DefaultConfig is a valid sing-box 1.1x baseline. No TUN, no auto_detect
// interface, no DNS hijack — NGG owns global networking in later stages.
func (a *Adapter) DefaultConfig(p core.RuntimeParams) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"log": map[string]any{"level": "info", "timestamp": true},
		"inbounds": []any{
			map[string]any{
				"type": "mixed", "tag": "ngg-mixed",
				"listen": "127.0.0.1", "listen_port": p.MixedPort,
			},
		},
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "direct"},
			map[string]any{"type": "block", "tag": "block"},
		},
		"route": map[string]any{"final": "direct"},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sing-box: marshal default config: %w", err)
	}
	return append(b, '\n'), nil
}

// LocalProxy exposes sing-box's isolated mixed inbound to the Stage 2 bridge.
func (a *Adapter) LocalProxy(p core.RuntimeParams) core.ProxyEndpoint {
	return core.ProxyEndpoint{
		Kind:     core.ProxyMixed,
		Address:  fmt.Sprintf("127.0.0.1:%d", p.MixedPort),
		Networks: []string{"tcp", "udp"},
	}
}

var _ core.LocalProxyProvider = (*Adapter)(nil)
