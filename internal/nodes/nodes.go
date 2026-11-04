// Package nodes implements the Config Engine's inbound/outbound node registry.
// A node declares WHICH encrypted protocol a kernel should expose or dial and
// with which parameters; the actual handshake/tunnel is executed by the kernel
// (NGG never reimplements a protocol stack). Node definitions are persisted
// and can be exported / re-imported as JSON for full JSON editing in the UI.
package nodes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ngg-project/ngg/internal/protocols"
	"gopkg.in/yaml.v3"
)

type Kind string

const (
	KindInbound  Kind = "inbound"
	KindOutbound Kind = "outbound"
)

// Node is one configurable protocol endpoint. Params carries protocol
// specific fields (credentials, cipher, TLS, obfs, ...) as free-form JSON so
// new protocols need no schema change.
type Node struct {
	ID          string         `json:"id" yaml:"id"`
	Name        string         `json:"name" yaml:"name"`
	Kind        Kind           `json:"kind" yaml:"kind"`
	Protocol    string         `json:"protocol" yaml:"protocol"`
	Address     string         `json:"address" yaml:"address"`
	Port        int            `json:"port" yaml:"port"`
	Enabled     bool           `json:"enabled" yaml:"enabled"`
	CoreID      string         `json:"core_id" yaml:"core_id"`
	Params      map[string]any `json:"params" yaml:"params"`
	Tags        []string       `json:"tags" yaml:"tags"`
	Description string         `json:"description" yaml:"description"`
}

type ProtocolInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Kinds       []Kind `json:"kinds"`
	DefaultPort int    `json:"default_port"`
	Encrypted   bool   `json:"encrypted"`
	PQCapable   bool   `json:"pq_capable"`
	Description string `json:"description"`
}

// Catalog is the protocol list offered by the node editor. Encrypted proxy
// protocols and traditional VPN protocols are both first-class citizens.
func Catalog() []ProtocolInfo {
	p := func(id, name, cat string, port int, enc, pq bool, desc string, kinds ...Kind) ProtocolInfo {
		return ProtocolInfo{ID: id, Name: name, Category: cat, Kinds: kinds, DefaultPort: port,
			Encrypted: enc, PQCapable: pq, Description: desc}
	}
	both := []Kind{KindInbound, KindOutbound}
	in := []Kind{KindInbound}
	out := []Kind{KindOutbound}
	return []ProtocolInfo{
		p("shadowsocks", "Shadowsocks", "encrypted-proxy", 8388, true, false, "AEAD 加密代理", both...),
		p("shadowsocksR", "ShadowsocksR", "encrypted-proxy", 8388, true, false, "SSR 协议", both...),
		p("vmess", "VMess", "encrypted-proxy", 443, true, false, "VMess / WS / gRPC", both...),
		p("vless-reality", "VLESS + REALITY", "encrypted-proxy", 443, true, false, "VLESS / XTLS-Vision / REALITY", both...),
		p("trojan", "Trojan", "encrypted-proxy", 443, true, false, "Trojan / TLS", both...),
		p("hysteria2", "Hysteria2", "encrypted-proxy", 443, true, false, "QUIC 加密代理", both...),
		p("tuic", "TUIC", "encrypted-proxy", 443, true, false, "QUIC 加密代理", both...),
		p("anytls", "AnyTLS", "encrypted-proxy", 443, true, false, "AnyTLS 加密代理", both...),
		p("mieru", "Mieru", "encrypted-proxy", 2999, true, false, "Mieru 混淆代理", both...),
		p("snell", "Snell", "encrypted-proxy", 443, true, false, "Snell 加密代理", both...),
		p("naive", "NaiveProxy", "encrypted-proxy", 443, true, false, "基于 Chromium 网络栈", both...),
		p("socks5", "SOCKS5", "local-inbound", 1080, false, false, "本地 SOCKS5 入口 / 出口", both...),
		p("http", "HTTP / HTTP CONNECT", "local-inbound", 8080, false, false, "本地 HTTP 代理", both...),
		p("mixed", "Mixed (SOCKS+HTTP)", "local-inbound", 7890, false, false, "混合本地入口", in...),
		p("wireguard", "WireGuard", "vpn", 51820, true, false, "NoiseIK / Curve25519 VPN", both...),
		p("openvpn", "OpenVPN", "vpn", 1194, true, false, "SSL VPN（外部 helper 执行）", out...),
		p("ipsec-ikev2", "IPsec / IKEv2", "vpn", 500, true, false, "企业级 VPN（外部 helper 执行）", out...),
		p("l2tp-ipsec", "L2TP / IPsec", "vpn", 1701, true, false, "传统路由器 VPN", out...),
		p("pptp", "PPTP", "vpn", 1723, false, false, "遗留协议，仅兼容用途", out...),
		p("sstp", "SSTP", "vpn", 443, true, false, "基于 HTTPS 的 VPN", out...),
		p("openconnect", "OpenConnect / AnyConnect", "vpn", 443, true, false, "Cisco AnyConnect 兼容", out...),
		p("ssh", "SSH Tunnel", "vpn", 22, true, false, "SSH 动态端口转发", out...),
		p("mlkem-hybrid", "ML-KEM-768 混合后量子", "post-quantum", 18443, true, true, "X25519 + ML-KEM-768 + AES-256-GCM 控制信道", both...),
		p("direct", "DIRECT", "route", 0, false, false, "NGG 直接出站", out...),
		p("reject", "REJECT", "route", 0, false, false, "阻断连接", out...),
	}
}

func FindProtocol(id string) (ProtocolInfo, bool) {
	for _, p := range Catalog() {
		if p.ID == id {
			return p, true
		}
	}
	return ProtocolInfo{}, false
}

func protocolSupports(info ProtocolInfo, kind Kind) bool {
	for _, k := range info.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

type Registry struct {
	mu    sync.RWMutex
	nodes map[string]Node
	path  string
}

func NewRegistry(path string) *Registry {
	return &Registry{nodes: map[string]Node{}, path: path}
}

// Load reads the persisted node list. A missing file is a normal first boot.
func (r *Registry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []Node
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("parse %s: %w", r.path, err)
	}
	m := map[string]Node{}
	for _, n := range list {
		if err := validate(n); err != nil {
			return fmt.Errorf("invalid stored node %q: %w", n.ID, err)
		}
		m[n.ID] = n
	}
	r.nodes = m
	return nil
}

func (r *Registry) persistLocked() error {
	list := r.sortedLocked()
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o600)
}

func (r *Registry) sortedLocked() []Node {
	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func validate(n Node) error {
	if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.Name) == "" {
		return fmt.Errorf("id and name are required")
	}
	if n.Kind != KindInbound && n.Kind != KindOutbound {
		return fmt.Errorf("kind must be inbound or outbound")
	}
	info, ok := FindProtocol(n.Protocol)
	if !ok {
		return fmt.Errorf("protocol %q is not in the NGG protocol catalog", n.Protocol)
	}
	if !protocolSupports(info, n.Kind) {
		return fmt.Errorf("protocol %q does not support %s nodes", n.Protocol, n.Kind)
	}
	if n.Port < 0 || n.Port > 65535 {
		return fmt.Errorf("port must be 0..65535")
	}
	if info.DefaultPort > 0 && n.Port == 0 {
		return nil // default applied by the caller
	}
	if n.Params == nil {
		return nil
	}
	if _, err := json.Marshal(n.Params); err != nil {
		return fmt.Errorf("params must be JSON-serializable: %w", err)
	}
	return nil
}

func normalize(n *Node) {
	info, _ := FindProtocol(n.Protocol)
	if n.Port == 0 && info.DefaultPort > 0 {
		n.Port = info.DefaultPort
	}
	if n.Params == nil {
		n.Params = map[string]any{}
	}
	if n.Tags == nil {
		n.Tags = []string{}
	}
	n.Address = strings.TrimSpace(n.Address)
}

func (r *Registry) List(kind Kind) []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Node
	for _, n := range r.nodes {
		if kind == "" || n.Kind == kind {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Get(id string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	return n, ok
}

func (r *Registry) Add(n Node) (Node, error) {
	normalize(&n)
	if err := validate(n); err != nil {
		return Node{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.nodes[n.ID]; exists {
		return Node{}, fmt.Errorf("node %q already exists", n.ID)
	}
	r.nodes[n.ID] = n
	if err := r.persistLocked(); err != nil {
		delete(r.nodes, n.ID)
		return Node{}, fmt.Errorf("persist node: %w", err)
	}
	return n, nil
}

func (r *Registry) Update(id string, n Node) (Node, error) {
	n.ID = id // ID is immutable while editing
	normalize(&n)
	if err := validate(n); err != nil {
		return Node{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, exists := r.nodes[id]
	if !exists {
		return Node{}, fmt.Errorf("node %q not found", id)
	}
	r.nodes[id] = n
	if err := r.persistLocked(); err != nil {
		r.nodes[id] = old
		return Node{}, fmt.Errorf("persist node: %w", err)
	}
	return n, nil
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, exists := r.nodes[id]
	if !exists {
		return fmt.Errorf("node %q not found", id)
	}
	delete(r.nodes, id)
	if err := r.persistLocked(); err != nil {
		r.nodes[id] = old
		return fmt.Errorf("persist node removal: %w", err)
	}
	return nil
}

// Replace performs an all-or-nothing import, matching the rule engine's
// Fail-Closed semantics: every node must validate before anything changes.
func (r *Registry) Replace(list []Node) (int, error) {
	m := map[string]Node{}
	for i := range list {
		n := list[i]
		normalize(&n)
		if err := validate(n); err != nil {
			return 0, fmt.Errorf("node #%d (%s): %w", i+1, n.ID, err)
		}
		if _, dup := m[n.ID]; dup {
			return 0, fmt.Errorf("duplicate node id %q", n.ID)
		}
		m[n.ID] = n
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.nodes
	r.nodes = m
	if err := r.persistLocked(); err != nil {
		r.nodes = old
		return 0, fmt.Errorf("persist imported nodes: %w", err)
	}
	return len(m), nil
}

func (r *Registry) Protocols(kind Kind) []ProtocolInfo {
	var out []ProtocolInfo
	for _, p := range Catalog() {
		if kind == "" || protocolSupports(p, kind) {
			out = append(out, p)
		}
	}
	return out
}

func (r *Registry) Path() string { return r.path }

// GenerateKernelConfig produces a REAL kernel-native configuration fragment
// for a node, then validates it with the format-aware parser. This is the
// bridge between "configure a protocol in the UI" and what a kernel consumes.
func GenerateKernelConfig(n Node, kernel string) ([]byte, string, error) {
	if kernel == "" {
		return nil, "", fmt.Errorf("kernel is required")
	}
	params := n.Params
	if params == nil {
		params = map[string]any{}
	}
	switch kernel {
	case "mihomo":
		// Mihomo / Clash YAML
		key := "proxies"
		if n.Kind == KindInbound {
			key = "listeners"
		}
		entry := map[string]any{"name": n.ID, "type": n.Protocol, "port": n.Port}
		if n.Kind == KindInbound {
			entry["listen"] = "0.0.0.0"
		} else {
			entry["server"] = n.Address
		}
		for k, v := range params {
			entry[k] = v
		}
		doc := map[string]any{key: []any{entry}}
		b, err := yaml.Marshal(doc)
		if err != nil {
			return nil, "", err
		}
		return b, "yaml", protocols.ValidateConfig(b, "yaml")
	case "xray-core", "sing-box":
		key := "outbounds"
		entry := map[string]any{"tag": n.ID, "protocol": n.Protocol}
		if kernel == "sing-box" {
			entry = map[string]any{"tag": n.ID, "type": n.Protocol}
		}
		if n.Kind == KindInbound {
			key = "inbounds"
			entry["listen"] = "0.0.0.0"
			entry["port"] = n.Port
		} else {
			entry["server"] = n.Address
			entry["server_port"] = n.Port
		}
		settings := map[string]any{}
		for k, v := range params {
			settings[k] = v
		}
		entry["settings"] = settings
		b, err := json.MarshalIndent(map[string]any{key: []any{entry}}, "", "  ")
		if err != nil {
			return nil, "", err
		}
		return b, "json", protocols.ValidateConfig(b, "json")
	default:
		return nil, "", fmt.Errorf("kernel %q has no config generator", kernel)
	}
}

// Merge imports parsed native-config nodes without deleting existing ones.
// Everything is validated first; a failure leaves the registry untouched.
func (r *Registry) Merge(list []Node) (int, error) {
	validated := make([]Node, 0, len(list))
	for i := range list {
		n := list[i]
		normalize(&n)
		if err := validate(n); err != nil {
			return 0, fmt.Errorf("node #%d (%s): %w", i+1, n.ID, err)
		}
		validated = append(validated, n)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.nodes
	next := map[string]Node{}
	for k, v := range old {
		next[k] = v
	}
	for _, n := range validated {
		next[n.ID] = n
	}
	r.nodes = next
	if err := r.persistLocked(); err != nil {
		r.nodes = old
		return 0, fmt.Errorf("persist merged nodes: %w", err)
	}
	return len(validated), nil
}
