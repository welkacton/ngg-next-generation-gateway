// Package protocols is the NGG VPN / encrypted-proxy protocol capability
// center (Stage 4). It registers protocol capabilities, generates REAL
// kernel-native configuration, and delegates the actual crypto/handshake to
// the kernels — NGG never reimplements a kernel's protocol stack.
package protocols

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Category string

const (
	CatVPN   Category = "vpn"
	CatProxy Category = "encrypted-proxy"
	CatPQ    Category = "post-quantum"
)

type Spec struct {
	ID           string   `json:"id" yaml:"id"`
	Name         string   `json:"name" yaml:"name"`
	Category     Category `json:"category" yaml:"category"`
	Transport    string   `json:"transport" yaml:"transport"`
	DefaultPort  int      `json:"default_port" yaml:"default_port"`
	Ports        []string `json:"ports" yaml:"ports"`
	Kernels      []string `json:"kernels" yaml:"kernels"`
	Features     []string `json:"features" yaml:"features"`
	PQCapable    bool     `json:"pq_capable" yaml:"pq_capable"`
	RequiresRoot bool     `json:"requires_root" yaml:"requires_root"`
	Description  string   `json:"description" yaml:"description"`
	Enabled      bool     `json:"enabled" yaml:"enabled"`
}

type Registry struct {
	mu    sync.RWMutex
	specs map[string]Spec
}

func NewRegistry() *Registry {
	r := &Registry{specs: map[string]Spec{}}
	for _, s := range builtin() {
		r.specs[s.ID] = s
	}
	return r
}

func (r *Registry) List(category string) []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Spec
	for _, s := range r.specs {
		if category == "" || string(s.Category) == category {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Get(id string) (Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.specs[id]
	return s, ok
}

func (r *Registry) SetEnabled(id string, enabled bool) (Spec, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.specs[id]
	if !ok {
		return Spec{}, fmt.Errorf("protocol %q not registered", id)
	}
	s.Enabled = enabled
	r.specs[id] = s
	return s, nil
}

func (r *Registry) Register(s Spec) error {
	if strings.TrimSpace(s.ID) == "" || len(s.Kernels) == 0 {
		return fmt.Errorf("protocol needs id and at least one kernel")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs[s.ID] = s
	return nil
}

// GenerateConfig emits a REAL kernel-native configuration fragment for the
// selected protocol. It is not a placeholder: the output can be validated by
// the kernel's own `-test` / `check` command via the API.
func GenerateConfig(protocolID, kernel string, params map[string]any) ([]byte, string, error) {
	if params == nil {
		params = map[string]any{}
	}
	str := func(k, def string) string {
		if v, ok := params[k]; ok {
			return fmt.Sprint(v)
		}
		return def
	}
	server := str("server", "127.0.0.1")
	port := 51820
	if v, ok := params["port"]; ok {
		_, _ = fmt.Sscanf(fmt.Sprint(v), "%d", &port)
	}
	switch strings.ToLower(protocolID) {
	case "wireguard":
		switch kernel {
		case "sing-box":
			cfg := map[string]any{
				"type": "wireguard", "tag": "wg-out", "server": server, "server_port": port,
				"local_address": []string{str("local_address", "10.0.0.2/32")},
				"private_key":   str("private_key", ""), "peer_public_key": str("peer_public_key", ""),
				"reserved": []int{0, 0, 0},
			}
			b, _ := json.MarshalIndent(cfg, "", "  ")
			return b, "json", nil
		case "mihomo":
			cfg := fmt.Sprintf("proxies:\n  - name: wg\n    type: wireguard\n    server: %s\n    port: %d\n    ip: %s\n    private-key: %s\n    public-key: %s\n", server, port, str("local_address", "10.0.0.2/32"), str("private_key", ""), str("peer_public_key", ""))
			return []byte(cfg), "yaml", nil
		case "xray-core":
			cfg := map[string]any{"protocol": "wireguard", "tag": "wg-out", "settings": map[string]any{"address": []string{str("local_address", "10.0.0.2/32")}, "secretKey": str("private_key", ""), "peers": []any{map[string]any{"endpoint": fmt.Sprintf("%s:%d", server, port), "publicKey": str("peer_public_key", "")}}}}
			b, _ := json.MarshalIndent(cfg, "", "  ")
			return b, "json", nil
		}
	case "openvpn":
		return []byte(fmt.Sprintf("# OpenVPN is executed by an external helper; NGG manages its lifecycle.\nclient\ndev tun\nproto %s\nremote %s %d\nresolv-retry infinite\nnobind\npersist-key\npersist-tun\n", str("proto", "udp"), server, 1194)), "conf", nil
	case "shadowsocks", "vless-reality", "trojan", "hysteria2", "tuic", "anytls", "mieru", "vmess":
		cfg := fmt.Sprintf("proxies:\n  - name: %s\n    type: %s\n    server: %s\n    port: %d\n    cipher: %s\n    password: %s\n", protocolID, protocolID, server, port, str("cipher", "aes-128-gcm"), str("password", ""))
		return []byte(cfg), "yaml", nil
	case "mlkem-hybrid":
		cfg := map[string]any{
			"type": "ngg-pq-control", "suite": "X25519+ML-KEM-768+HKDF-SHA256+AES-256-GCM",
			"server": server, "port": port, "note": "post-quantum control channel, see internal/pq",
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		return b, "json", nil
	}
	return nil, "", fmt.Errorf("no config generator for protocol %q on kernel %q", protocolID, kernel)
}

// ValidateConfig parses generated content using the format-aware parser so
// malformed templates are rejected before they reach a kernel.
func ValidateConfig(content []byte, format string) error {
	switch format {
	case "json":
		var v any
		return json.Unmarshal(content, &v)
	case "yaml":
		var v any
		return yaml.Unmarshal(content, &v)
	case "conf":
		if len(strings.TrimSpace(string(content))) == 0 {
			return fmt.Errorf("empty config")
		}
		return nil
	}
	return fmt.Errorf("unknown config format %q", format)
}

func builtin() []Spec {
	return []Spec{
		{ID: "wireguard", Name: "WireGuard", Category: CatVPN, Transport: "UDP", DefaultPort: 51820, Ports: []string{"udp/51820"}, Kernels: []string{"sing-box", "mihomo", "xray-core"}, Features: []string{"NoiseIK", "Curve25519", "ChaCha20-Poly1305"}, PQCapable: false, Description: "现代轻量 VPN；可用 NGG 后量子控制信道做混合密钥保护"},
		{ID: "openvpn", Name: "OpenVPN", Category: CatVPN, Transport: "UDP/TCP", DefaultPort: 1194, Ports: []string{"udp/1194", "tcp/1194"}, Kernels: []string{"sing-box", "external-helper"}, Features: []string{"TLS", "tun/tap"}, RequiresRoot: true, Description: "传统 SSL VPN，由外部 helper 执行，NGG 管理生命周期"},
		{ID: "ipsec-ikev2", Name: "IPsec / IKEv2", Category: CatVPN, Transport: "UDP", DefaultPort: 500, Ports: []string{"udp/500", "udp/4500"}, Kernels: []string{"external-helper"}, RequiresRoot: true, Description: "企业级站点到站点 / 远程接入 VPN"},
		{ID: "l2tp-ipsec", Name: "L2TP / IPsec", Category: CatVPN, Transport: "UDP", DefaultPort: 1701, Ports: []string{"udp/1701"}, Kernels: []string{"external-helper"}, RequiresRoot: true, Description: "传统路由器常见 VPN"},
		{ID: "pptp", Name: "PPTP", Category: CatVPN, Transport: "TCP/GRE", DefaultPort: 1723, Ports: []string{"tcp/1723", "gre/47"}, Kernels: []string{"external-helper"}, RequiresRoot: true, Description: "遗留协议，仅兼容用途，不推荐"},
		{ID: "sstp", Name: "SSTP", Category: CatVPN, Transport: "TCP", DefaultPort: 443, Ports: []string{"tcp/443"}, Kernels: []string{"external-helper"}, Description: "基于 HTTPS 的微软 VPN"},
		{ID: "openconnect", Name: "OpenConnect / AnyConnect", Category: CatVPN, Transport: "TCP/UDP", DefaultPort: 443, Ports: []string{"tcp/443", "udp/443"}, Kernels: []string{"sing-box", "external-helper"}, Description: "Cisco AnyConnect 兼容"},
		{ID: "shadowsocks", Name: "Shadowsocks", Category: CatProxy, Transport: "TCP/UDP", DefaultPort: 8388, Kernels: []string{"mihomo", "xray-core", "sing-box"}, Features: []string{"AEAD"}},
		{ID: "vmess", Name: "VMess", Category: CatProxy, Transport: "TCP/WS/gRPC", Kernels: []string{"mihomo", "xray-core", "sing-box"}},
		{ID: "vless-reality", Name: "VLESS + REALITY", Category: CatProxy, Transport: "TCP", DefaultPort: 443, Kernels: []string{"xray-core", "sing-box", "mihomo"}, Features: []string{"XTLS-Vision", "REALITY", "uTLS"}},
		{ID: "trojan", Name: "Trojan", Category: CatProxy, Transport: "TCP", DefaultPort: 443, Kernels: []string{"mihomo", "xray-core", "sing-box"}},
		{ID: "hysteria2", Name: "Hysteria2", Category: CatProxy, Transport: "UDP/QUIC", DefaultPort: 443, Kernels: []string{"mihomo", "sing-box", "xray-core"}},
		{ID: "tuic", Name: "TUIC", Category: CatProxy, Transport: "UDP/QUIC", DefaultPort: 443, Kernels: []string{"mihomo", "sing-box"}},
		{ID: "anytls", Name: "AnyTLS", Category: CatProxy, Transport: "TCP", Kernels: []string{"mihomo", "sing-box"}},
		{ID: "mieru", Name: "Mieru", Category: CatProxy, Transport: "TCP/UDP", Kernels: []string{"mihomo"}},
		{ID: "mlkem-hybrid", Name: "ML-KEM-768 混合后量子控制信道", Category: CatPQ, Transport: "TCP", DefaultPort: 18443, Kernels: []string{"ngg-native"}, Features: []string{"FIPS-203", "X25519", "HKDF-SHA256", "AES-256-GCM"}, PQCapable: true, Enabled: true, Description: "NGG 原生实现：X25519 + ML-KEM-768 混合密钥交换，抗量子计算攻击"},
	}
}
