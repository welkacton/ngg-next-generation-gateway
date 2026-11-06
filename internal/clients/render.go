package clients

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// placeholderPeerKey is a well-formed but obviously empty X25519 key (32 zero
// bytes). It is used where a WireGuard config needs the *server* public key and
// NGG has no server-side WireGuard listener to derive it from. Every config
// that carries it also carries a comment saying it must be replaced — a random
// key would look real and fail silently.
var placeholderPeerKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

// WireGuardAddress is the client tunnel address handed to every device. It is a
// documentation-range / private address that does not collide with the default
// LAN subnets NGG ships with.
const WireGuardAddress = "10.66.0.2/32"

// Render produces one client config. The returned filename is safe for a
// download; the content is valid JSON / YAML / INI for its format.
func Render(c Client, a AccessConfig, format string) (filename, content string, err error) {
	a = normalizeAccess(a)
	protocol := strings.ToLower(strings.TrimSpace(c.Protocol))
	if protocol == "" {
		protocol = a.Protocol
	}
	if a.PublicHost == "" {
		// Nothing is guessed here on purpose: a fabricated domain produces a
		// config that only fails once it reaches the device.
		return "", "", ErrNoPublicHost
	}
	if err := validateProtocol(protocol); err != nil {
		return "", "", err
	}
	switch format {
	case FormatSocks:
		if protocol == ProtocolWireGuard {
			return "", "", invalid("format", "socks5 配置仅适用于 socks5/http 设备，当前设备为 wireguard")
		}
		return socksFilename(c), renderSocks(c, a, protocol), nil
	case FormatSingBox:
		s, err := renderSingBox(c, a, protocol)
		if err != nil {
			return "", "", err
		}
		return baseFilename(c) + ".json", s, nil
	case FormatMihomo:
		s, err := renderMihomo(c, a, protocol)
		if err != nil {
			return "", "", err
		}
		return baseFilename(c) + ".yaml", s, nil
	case FormatWireGuard:
		if protocol != ProtocolWireGuard {
			return "", "", invalid("format", "wireguard 配置仅适用于 wireguard 协议设备，当前设备为 %s", protocol)
		}
		s, err := renderWireGuard(c, a)
		if err != nil {
			return "", "", err
		}
		return baseFilename(c) + ".conf", s, nil
	default:
		return "", "", fmt.Errorf("%w: %q", ErrUnknownFormat, format)
	}
}

// renderSocks is the human-copyable form: the URI plus the four fields the
// system SOCKS dialog asks for.
func renderSocks(c Client, a AccessConfig, protocol string) string {
	scheme := "socks5"
	if protocol == ProtocolHTTP {
		scheme = "http"
	}
	u := url.URL{
		Scheme: scheme,
		User:   url.UserPassword(c.ID, c.Credential),
		Host:   fmt.Sprintf("%s:%d", a.PublicHost, a.PublicPort),
	}
	var b strings.Builder
	b.WriteString(u.String())
	b.WriteString("\n")
	fmt.Fprintf(&b, "host: %s\n", a.PublicHost)
	fmt.Fprintf(&b, "port: %d\n", a.PublicPort)
	fmt.Fprintf(&b, "username: %s\n", c.ID)
	fmt.Fprintf(&b, "password: %s\n", c.Credential)
	return b.String()
}

type singBoxTLS struct {
	Enabled    bool   `json:"enabled"`
	ServerName string `json:"server_name,omitempty"`
}

type singBoxOutbound struct {
	Type          string      `json:"type"`
	Tag           string      `json:"tag"`
	Server        string      `json:"server,omitempty"`
	ServerPort    int         `json:"server_port,omitempty"`
	Username      string      `json:"username,omitempty"`
	Password      string      `json:"password,omitempty"`
	Version       string      `json:"version,omitempty"`
	LocalAddress  []string    `json:"local_address,omitempty"`
	PrivateKey    string      `json:"private_key,omitempty"`
	PeerPublicKey string      `json:"peer_public_key,omitempty"`
	MTU           int         `json:"mtu,omitempty"`
	TLS           *singBoxTLS `json:"tls,omitempty"`
}

type singBoxDoc struct {
	Log       map[string]any    `json:"log"`
	Outbounds []singBoxOutbound `json:"outbounds"`
	Route     map[string]any    `json:"route"`
	// PublicKey and Note are NGG annotations on the wireguard profile: JSON has
	// no comment syntax, so the client's public key travels in a real field.
	PublicKey string `json:"_ngg_public_key,omitempty"`
	Note      string `json:"_ngg_note,omitempty"`
}

func renderSingBox(c Client, a AccessConfig, protocol string) (string, error) {
	out := singBoxOutbound{Tag: "ngg", Server: a.PublicHost, ServerPort: a.PublicPort}
	doc := singBoxDoc{
		Log: map[string]any{"level": "info", "timestamp": true},
		Outbounds: []singBoxOutbound{
			out,
			{Type: "direct", Tag: "direct"},
		},
		Route: map[string]any{"final": "ngg"},
	}
	switch protocol {
	case ProtocolWireGuard:
		priv, pub, err := newKeyPair()
		if err != nil {
			return "", err
		}
		doc.Outbounds[0].Type = "wireguard"
		doc.Outbounds[0].LocalAddress = []string{WireGuardAddress}
		doc.Outbounds[0].PrivateKey = priv
		doc.Outbounds[0].PeerPublicKey = placeholderPeerKey
		doc.Outbounds[0].MTU = 1408
		doc.PublicKey = pub
		doc.Note = "peer_public_key 为占位值，必须替换为服务端 WireGuard 公钥；NGG 当前不内置 WireGuard 服务端监听"
	case ProtocolHTTP:
		doc.Outbounds[0].Type = "http"
		doc.Outbounds[0].Username = c.ID
		doc.Outbounds[0].Password = c.Credential
		if a.TLS {
			doc.Outbounds[0].TLS = &singBoxTLS{Enabled: true, ServerName: a.PublicHost}
		}
	default:
		doc.Outbounds[0].Type = "socks"
		doc.Outbounds[0].Username = c.ID
		doc.Outbounds[0].Password = c.Credential
		doc.Outbounds[0].Version = "5"
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成 sing-box 配置失败: %w", err)
	}
	return string(b) + "\n", nil
}

type mihomoProxy struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	// Server/Port/IP/keys are shared between the socks5/http and wireguard shapes.
	Server     string `yaml:"server"`
	Port       int    `yaml:"port"`
	Username   string `yaml:"username,omitempty"`
	Password   string `yaml:"password,omitempty"`
	UDP        bool   `yaml:"udp,omitempty"`
	IP         string `yaml:"ip,omitempty"`
	PrivateKey string `yaml:"private-key,omitempty"`
	PublicKey  string `yaml:"public-key,omitempty"`
	MTU        int    `yaml:"mtu,omitempty"`
	TLS        bool   `yaml:"tls,omitempty"`
	SNI        string `yaml:"servername,omitempty"`
	SkipCert   bool   `yaml:"skip-cert-verify,omitempty"`
}

type mihomoGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

type mihomoDoc struct {
	Proxies     []mihomoProxy `yaml:"proxies"`
	ProxyGroups []mihomoGroup `yaml:"proxy-groups"`
	Rules       []string      `yaml:"rules"`
}

func renderMihomo(c Client, a AccessConfig, protocol string) (string, error) {
	// The proxy name must differ from the group name: mihomo rejects a group
	// that contains a proxy with the same name.
	name := "NGG-" + baseFilename(c)
	p := mihomoProxy{Name: name, Server: a.PublicHost, Port: a.PublicPort}
	switch protocol {
	case ProtocolWireGuard:
		priv, _, err := newKeyPair()
		if err != nil {
			return "", err
		}
		p.Type = "wireguard"
		p.IP = strings.TrimSuffix(WireGuardAddress, "/32")
		p.PrivateKey = priv
		p.PublicKey = placeholderPeerKey
		p.MTU = 1408
		p.UDP = true
	case ProtocolHTTP:
		p.Type = "http"
		p.Username = c.ID
		p.Password = c.Credential
		if a.TLS {
			p.TLS = true
			p.SNI = a.PublicHost
		}
	default:
		p.Type = "socks5"
		p.Username = c.ID
		p.Password = c.Credential
		p.UDP = true
	}
	doc := mihomoDoc{
		Proxies: []mihomoProxy{p},
		ProxyGroups: []mihomoGroup{
			{Name: "NGG", Type: "select", Proxies: []string{name}},
		},
		Rules: []string{"MATCH,NGG"},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	// mihomo/Clash configs are conventionally 2-space indented.
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", fmt.Errorf("生成 mihomo 配置失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("生成 mihomo 配置失败: %w", err)
	}
	header := "# NGG 客户端配置（mihomo / Clash.Meta）\n"
	if protocol == ProtocolWireGuard {
		header += "# public-key 为占位值，必须替换为服务端 WireGuard 公钥\n"
	}
	return header + buf.String(), nil
}

func renderWireGuard(c Client, a AccessConfig) (string, error) {
	priv, pub, err := newKeyPair()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# NGG 客户端配置 —— WireGuard\n")
	fmt.Fprintf(&b, "# 本设备公钥（需添加到服务端 peer）: %s\n", pub)
	b.WriteString("# 注意：NGG 目前没有内置 WireGuard 服务端监听，[Peer] PublicKey 必须由服务端提供并替换下方占位值，否则无法连通。\n")
	b.WriteString("\n[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", priv)
	fmt.Fprintf(&b, "Address = %s\n", WireGuardAddress)
	b.WriteString("DNS = 1.1.1.1\n")
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", placeholderPeerKey)
	fmt.Fprintf(&b, "Endpoint = %s:%d\n", a.PublicHost, a.PublicPort)
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	b.WriteString("PersistentKeepalive = 25\n")
	return b.String(), nil
}

// newKeyPair returns a WireGuard-compatible X25519 keypair in standard base64:
// the private half is clamped exactly like wg(8) does, so the public half
// matches what the server will derive.
func newKeyPair() (privB64, pubB64 string, err error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return "", "", fmt.Errorf("生成 WireGuard 密钥失败: %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	key, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("生成 WireGuard 密钥失败: %w", err)
	}
	pub := key.PublicKey().Bytes()
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), nil
}

// baseFilename turns a display name into a download-safe file stem. Anything
// that is not a letter, digit, dash, underscore or CJK character becomes "-".
func baseFilename(c Client) string {
	src := strings.TrimSpace(c.Name)
	if src == "" {
		src = c.ID
	}
	var b strings.Builder
	for _, r := range src {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r > 127:
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = c.ID
	}
	return out
}

func socksFilename(c Client) string { return baseFilename(c) + ".txt" }
