// Package nodedeploy turns a registered host into a RUNNING node, following
// Remnawave's panel ↔ node split (github.com/remnawave) as closely as the
// stack allows.
//
//	Remnawave                          NGG
//	---------------------------------  ----------------------------------------
//	node = a container running xray    node = ngg-node (control API) + a
//	plus the node API                  separate ngg-core.service running xray
//	panel → node over mutual TLS on    panel → node over mutual TLS on
//	NODE_PORT (2222)                   NODE_PORT (2222), certificates issued by
//	                                   the panel's own CA (internal/nodepki)
//	node identity = certificate        node identity = certificate CN = node id,
//	                                   pinned by the panel on every dial
//	panel pushes the config with       same: POST /node/xray/start carries the
//	POST /node/xray/start              rendered kernel config in the body
//	panel reads stats with             same: GET /node/stats/get-system-stats
//	GET /node/stats/get-system-stats
//
// What is deliberately NOT ported: Docker. Upstream ships the node as an
// image; this project deploys a static binary and two systemd units so a node
// needs no container runtime and cannot disturb whatever else runs on the box.
//
// A deploy is therefore a real bash script the operator runs on the node once.
// It installs the units, writes the certificates and starts the API; from then
// on the panel does everything else over the network. The panel never uses
// SSH, never runs a remote command and never needs the node's shell password —
// the only inbound port it touches is NODE_PORT.
//
// The script contains the node's own secrets (REALITY private key, client
// UUID, node certificate and key) because the operator needs them; it never
// contains a panel credential.
package nodedeploy

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Node deployment modes. These strings are the wire contract with the console
// (web/src/api/ngg.ts · NodeDeployMode).
const (
	// ModeReality is VLESS + REALITY on the node port: no certificate, no
	// allowInsecure on the client, and the traffic looks like the SNI site.
	ModeReality = "reality"
	// ModeVLESS is VLESS + a self-signed TLS certificate.
	ModeVLESS = "vless-tls"
	// ModeTrojan is Trojan + a self-signed TLS certificate.
	ModeTrojan = "trojan-tls"
	// ModeProfile writes a bound 协议配置 (xray-core only) verbatim and lets
	// the profile decide everything, including the port.
	ModeProfile = "profile"
)

// Defaults.
const (
	// DefaultPort is the node data port. 3372 is what the console pre-fills and
	// what the TCP probe dials; 443 is usually already taken by a web server on
	// a node that also hosts a panel (see the notes on port conflicts).
	DefaultPort = 3372
	// DefaultRealitySNI is a TLS 1.3 + X25519 site used as the REALITY mask.
	DefaultRealitySNI = "www.microsoft.com"
	// DefaultSNIForTLS is used for the self-signed certificate subject when the
	// host is a bare IP (an IP cannot be a TLS server name).
	DefaultSNIForTLS = "www.bing.com"

	// DefaultNodePort is the node API port (Remnawave's NODE_PORT default).
	// It is the ONE port the panel dials into.
	DefaultNodePort = 2222

	// NodeDir is the node's own directory on the server. It is deliberately NOT
	// /usr/local/etc/xray: a machine may already run another panel's Xray, and
	// the deploy must not touch it.
	NodeDir = "/etc/ngg"
	// NodeBin is the node API daemon (this project's rw-node equivalent).
	NodeBin = "/usr/local/bin/ngg-node"
	// NodeService is the API unit; CoreService is the kernel unit. Splitting
	// them is what lets the panel restart the kernel without dropping the API.
	NodeService = "ngg-node.service"
	CoreService = "ngg-core.service"
	// NodeCtlBin is the operator's on-box helper.
	NodeCtlBin = "/usr/local/bin/nggctl"

	// PKI paths on the node.
	NodePKIDir  = NodeDir + "/node"
	NodeCertPEM = NodePKIDir + "/node.crt"
	NodeKeyPEM  = NodePKIDir + "/node.key"
	NodeCAPEM   = NodePKIDir + "/ca.crt"

	// NodeConfigPath is the kernel config the core unit runs.
	NodeConfigPath = NodeDir + "/xray/config.json"
	// NodeManifestPath is the node identity / kernel path / port binding.
	NodeManifestPath = NodeDir + "/node.json"
)

// SSMethod is accepted for completeness when a shadowsocks profile is bound;
// the node service itself only runs xray-core.
const SSMethod = "aes-256-gcm"

var (
	// ErrInvalidSpec marks requests the operator has to fix (422, not 500).
	ErrInvalidSpec = errors.New("部署参数不合法")
	// ErrProfileKernel marks a bound profile whose kernel the node service
	// cannot run.
	ErrProfileKernel = errors.New("该配置档案的内核无法在线部署")
	// ErrNotFound means no deployment has been generated for the host.
	ErrNotFound = errors.New("该节点尚未生成部署方案")
)

var hostRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

// Spec is everything the script needs. It is persisted with the deployment
// record so the script can be re-rendered after a restart without re-minting
// credentials (rotating them would break every configured client).
type Spec struct {
	HostID   string `json:"host_id"`
	HostName string `json:"host_name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Mode     string `json:"mode"`
	SNI      string `json:"sni"`

	// Node-side secrets.
	UUID           string `json:"uuid,omitempty"`
	RealityPrivate string `json:"reality_private,omitempty"`
	RealityPublic  string `json:"reality_public,omitempty"`
	ShortID        string `json:"short_id,omitempty"`
	Password       string `json:"password,omitempty"`

	// NodePort is the API port the panel dials (NODE_PORT). It is separate
	// from Port, which carries user traffic.
	NodePort int `json:"node_port"`

	// TLS material for the panel ↔ node channel. Minted by the panel CA on
	// every deploy and embedded in the script, so they are deliberately NOT
	// serialised into the record: the script already holds them, and a second
	// copy of a private key is a second thing to leak.
	NodeCertPEM []byte `json:"-"`
	NodeKeyPEM  []byte `json:"-"`
	CAPEM       []byte `json:"-"`

	// Where the node downloads its API daemon from. Filled by the API layer
	// with the release URL for this version; the per-architecture digests let
	// the script verify what it downloaded before running it as root.
	NodeBinBase        string `json:"node_bin_base,omitempty"`
	NodeBinSHA256AMD64 string `json:"node_bin_sha256_amd64,omitempty"`
	NodeBinSHA256ARM64 string `json:"node_bin_sha256_arm64,omitempty"`

	// Bound 协议配置 profile (mode=profile).
	ProfileID   string `json:"profile_id,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
	Kernel      string `json:"kernel,omitempty"`
	// ProfileContent is never rendered to the frontend; the API strips it.
	ProfileContent string `json:"profile_content,omitempty"`

	GeneratedAt time.Time `json:"generated_at"`
}

// Protocol is the label used by the console / subscription layer.
func (s Spec) Protocol() string {
	switch s.Mode {
	case ModeReality, ModeVLESS, ModeProfile:
		if s.Mode == ModeProfile {
			return "xray"
		}
		return "vless"
	case ModeTrojan:
		return "trojan"
	}
	return s.Mode
}

// Label is the human-facing protocol name stored in the host registry, so the
// node card keeps the distinction between REALITY and a certificate-based TLS
// inbound after a deployment.
func (s Spec) Label() string {
	switch s.Mode {
	case ModeReality:
		return "vless-reality"
	case ModeVLESS:
		return "vless-tls"
	case ModeTrojan:
		return "trojan-tls"
	case ModeProfile:
		if s.ProfileName != "" {
			return "xray-core"
		}
	}
	return s.Protocol()
}

// TLS reports whether clients need TLS settings (and allowInsecure for the
// self-signed modes).
func (s Spec) TLS() bool { return s.Mode != ModeReality }

// PublicSpec strips the material a client must not receive (the REALITY
// private key and the agent token) before the record goes to the browser.
func (s Spec) PublicSpec() Spec {
	out := s
	out.RealityPrivate = ""
	out.Password = ""
	if out.Mode == ModeProfile {
		out.ProfileContent = ""
	}
	return out
}

// ValidMode reports whether m is a deployable mode.
func ValidMode(m string) bool {
	switch m {
	case ModeReality, ModeVLESS, ModeTrojan, ModeProfile:
		return true
	}
	return false
}

// Prepare validates the request and fills in every default. Credentials already
// present in s are kept, so re-rendering a script never rotates a live node's
// secrets behind the operator's back.
func Prepare(s Spec) (Spec, error) {
	s.HostID = strings.TrimSpace(s.HostID)
	s.Address = strings.TrimSpace(s.Address)
	if s.HostID == "" || s.Address == "" {
		return Spec{}, fmt.Errorf("%w: 缺少节点 ID 或地址", ErrInvalidSpec)
	}
	s.Mode = strings.ToLower(strings.TrimSpace(s.Mode))
	if s.Mode == "" {
		s.Mode = ModeReality
	}
	if !ValidMode(s.Mode) {
		return Spec{}, fmt.Errorf("%w: 未知模式 %q", ErrInvalidSpec, s.Mode)
	}
	if s.Mode == ModeProfile {
		if strings.TrimSpace(s.ProfileContent) == "" {
			return Spec{}, fmt.Errorf("%w: 档案内容为空", ErrInvalidSpec)
		}
		if k := strings.ToLower(strings.TrimSpace(s.Kernel)); k != "xray-core" {
			return Spec{}, fmt.Errorf("%w: 档案内核为 %q，在线部署只支持 xray-core（mihomo / sing-box 请用脚本方式部署）", ErrProfileKernel, s.Kernel)
		}
	}

	if s.Port == 0 {
		s.Port = DefaultPort
	}
	if s.Port < 1 || s.Port > 65535 {
		return Spec{}, fmt.Errorf("%w: 端口需在 1..65535，当前 %d", ErrInvalidSpec, s.Port)
	}

	s.SNI = strings.ToLower(strings.TrimSpace(s.SNI))
	if s.SNI == "" {
		if s.Mode == ModeReality {
			s.SNI = DefaultRealitySNI
		} else {
			if net.ParseIP(s.Address) == nil && strings.Contains(s.Address, ".") {
				s.SNI = s.Address
			} else {
				s.SNI = DefaultSNIForTLS
			}
		}
	}
	if !hostRE.MatchString(s.SNI) {
		return Spec{}, fmt.Errorf("%w: SNI 需为域名（不含协议前缀与空格）", ErrInvalidSpec)
	}

	if s.NodePort == 0 {
		s.NodePort = DefaultNodePort
	}
	if s.NodePort < 1 || s.NodePort > 65535 {
		return Spec{}, fmt.Errorf("%w: 节点管理端口需在 1..65535，当前 %d", ErrInvalidSpec, s.NodePort)
	}
	if s.NodePort == s.Port {
		return Spec{}, fmt.Errorf("%w: 管理端口与数据端口不能相同（%d），面板会连不上节点", ErrInvalidSpec, s.NodePort)
	}
	if strings.TrimSpace(s.NodeBinBase) == "" {
		s.NodeBinBase = DefaultNodeBinBase()
	}

	var err error
	switch s.Mode {
	case ModeReality:
		if s.UUID == "" {
			if s.UUID, err = newUUIDv4(); err != nil {
				return Spec{}, err
			}
		}
		if s.RealityPrivate == "" || s.RealityPublic == "" {
			priv, pub, kerr := realityKeypair()
			if kerr != nil {
				return Spec{}, kerr
			}
			s.RealityPrivate, s.RealityPublic = priv, pub
		}
		if s.ShortID == "" {
			if s.ShortID, err = randomHex(4); err != nil {
				return Spec{}, err
			}
		}
	case ModeVLESS:
		if s.UUID == "" {
			if s.UUID, err = newUUIDv4(); err != nil {
				return Spec{}, err
			}
		}
	case ModeTrojan:
		if s.Password == "" {
			b, rerr := randBytes(24)
			if rerr != nil {
				return Spec{}, rerr
			}
			s.Password = base64.RawURLEncoding.EncodeToString(b)
		}
	}

	if _, err := ConfigJSON(s); err != nil {
		return Spec{}, err
	}
	if s.GeneratedAt.IsZero() {
		s.GeneratedAt = time.Now().UTC().Truncate(time.Second)
	}
	return s, nil
}

/* ------------------------------------------------------------------ *
 * 内核配置
 * ------------------------------------------------------------------ */

// privateCIDRs are routed to the blackhole inbound: a public node must not be
// usable as a bridge into the server's own network. They are written out
// literally instead of "geoip:private" so the node needs no geoip.dat.
var privateCIDRs = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "100.64.0.0/10",
	"::1/128", "fc00::/7", "fe80::/10",
}

type realitySettings struct {
	Show        bool     `json:"show"`
	Dest        string   `json:"dest"`
	Xver        int      `json:"xver"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIDs    []string `json:"shortIds"`
}

type certPair struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

type tlsSettings struct {
	Certificates []certPair `json:"certificates"`
}

type streamSettings struct {
	Network         string           `json:"network"`
	Security        string           `json:"security"`
	RealitySettings *realitySettings `json:"realitySettings,omitempty"`
	TLSSettings     *tlsSettings     `json:"tlsSettings,omitempty"`
}

type sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	RouteOnly    bool     `json:"routeOnly"`
}

type inbound struct {
	Tag            string         `json:"tag"`
	Listen         string         `json:"listen"`
	Port           int            `json:"port"`
	Protocol       string         `json:"protocol"`
	Settings       map[string]any `json:"settings"`
	StreamSettings streamSettings `json:"streamSettings"`
	Sniffing       sniffing       `json:"sniffing"`
}

type outbound struct {
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
}

type rule struct {
	Type        string   `json:"type"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

type routing struct {
	DomainStrategy string `json:"domainStrategy"`
	Rules          []rule `json:"rules"`
}

type xrayConfig struct {
	Log       map[string]string `json:"log"`
	Inbounds  []inbound         `json:"inbounds"`
	Outbounds []outbound        `json:"outbounds"`
	Routing   routing           `json:"routing"`
}

// ConfigJSON renders the kernel config. For mode=profile it is the bound
// profile content, verbatim: the console promised "原样写入", and the kernel's
// own checker is what validates it.
func ConfigJSON(s Spec) (string, error) {
	if s.Mode == ModeProfile {
		content := strings.TrimRight(s.ProfileContent, "\n")
		if strings.TrimSpace(content) == "" {
			return "", fmt.Errorf("%w: 档案内容为空", ErrInvalidSpec)
		}
		if !json.Valid([]byte(content)) {
			return "", fmt.Errorf("%w: 档案不是合法 JSON，xray-core 无法加载", ErrInvalidSpec)
		}
		return content, nil
	}

	in := inbound{
		Tag:      "ngg-in",
		Listen:   "0.0.0.0",
		Port:     s.Port,
		Sniffing: sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}, RouteOnly: true},
	}
	switch s.Mode {
	case ModeReality:
		in.Protocol = "vless"
		in.Settings = map[string]any{
			"clients":    []map[string]any{{"id": s.UUID, "email": s.HostID}},
			"decryption": "none",
		}
		in.StreamSettings = streamSettings{
			Network:  "tcp",
			Security: "reality",
			RealitySettings: &realitySettings{
				Show:        false,
				Dest:        net.JoinHostPort(s.SNI, "443"),
				Xver:        0,
				ServerNames: []string{s.SNI},
				PrivateKey:  s.RealityPrivate,
				ShortIDs:    []string{s.ShortID},
			},
		}
	case ModeVLESS:
		in.Protocol = "vless"
		in.Settings = map[string]any{
			"clients":    []map[string]any{{"id": s.UUID, "email": s.HostID}},
			"decryption": "none",
		}
		in.StreamSettings = streamSettings{
			Network:     "tcp",
			Security:    "tls",
			TLSSettings: &tlsSettings{Certificates: []certPair{{CertificateFile: NodeDir + "/tls/cert.pem", KeyFile: NodeDir + "/tls/key.pem"}}},
		}
	case ModeTrojan:
		in.Protocol = "trojan"
		in.Settings = map[string]any{
			"clients": []map[string]any{{"password": s.Password, "email": s.HostID}},
		}
		in.StreamSettings = streamSettings{
			Network:     "tcp",
			Security:    "tls",
			TLSSettings: &tlsSettings{Certificates: []certPair{{CertificateFile: NodeDir + "/tls/cert.pem", KeyFile: NodeDir + "/tls/key.pem"}}},
		}
	default:
		return "", fmt.Errorf("%w: 模式 %q 没有内核配置模板", ErrInvalidSpec, s.Mode)
	}

	cfg := xrayConfig{
		Log:       map[string]string{"loglevel": "warning"},
		Inbounds:  []inbound{in},
		Outbounds: []outbound{{Protocol: "freedom", Tag: "direct"}, {Protocol: "blackhole", Tag: "block"}},
		Routing: routing{
			DomainStrategy: "AsIs",
			Rules:          []rule{{Type: "field", IP: privateCIDRs, OutboundTag: "block"}},
		},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ShareURI builds the SIP002 / vless / trojan URI for quick client import.
func (s Spec) ShareURI() string {
	host := s.Address
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(s.Port))
	tag := "#" + urlSafe(s.HostName)
	switch s.Mode {
	case ModeReality:
		q := "encryption=none&security=reality&sni=" + s.SNI + "&fp=chrome&pbk=" + s.RealityPublic +
			"&sid=" + s.ShortID + "&type=tcp"
		return "vless://" + s.UUID + "@" + addr + "?" + q + tag
	case ModeVLESS:
		q := "encryption=none&security=tls&sni=" + s.SNI + "&allowInsecure=1&type=tcp"
		return "vless://" + s.UUID + "@" + addr + "?" + q + tag
	case ModeTrojan:
		q := "security=tls&sni=" + s.SNI + "&allowInsecure=1&type=tcp"
		return "trojan://" + s.Password + "@" + addr + "?" + q + tag
	}
	return ""
}

// ClientParams is the human-readable connection block printed at the end of a
// deploy and shown in the console.
func (s Spec) ClientParams() []string {
	out := []string{
		"协议 : " + strings.ToUpper(s.Protocol()),
		"地址 : " + s.Address,
		"端口 : " + strconv.Itoa(s.Port),
	}
	switch s.Mode {
	case ModeReality:
		out = append(out,
			"UUID : "+s.UUID,
			"公钥 : "+s.RealityPublic+"（pbk）",
			"ShortID : "+s.ShortID,
			"SNI : "+s.SNI+" / fingerprint=chrome / security=reality",
			"说明 : REALITY 不需要证书，客户端无需 allowInsecure",
		)
	case ModeVLESS:
		out = append(out, "UUID : "+s.UUID, "SNI : "+s.SNI, "说明 : 自签证书，客户端需开启 allowInsecure")
	case ModeTrojan:
		out = append(out, "密码 : "+s.Password, "SNI : "+s.SNI, "说明 : 自签证书，客户端需开启 allowInsecure")
	case ModeProfile:
		out = append(out, "配置 : "+s.ProfileName+"（内容由档案决定，端口 "+strconv.Itoa(s.Port)+" 仅用于放通与自检）")
	}
	if uri := s.ShareURI(); uri != "" {
		out = append(out, "链接 : "+uri)
	}
	return out
}

/* ------------------------------------------------------------------ *
 * 凭据
 * ------------------------------------------------------------------ */

func randBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func randomHex(n int) (string, error) {
	b, err := randBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newUUIDv4 returns a canonical RFC 4122 version-4 UUID, the shape Xray's
// VLESS inbound requires.
func newUUIDv4() (string, error) {
	b, err := randBytes(16)
	if err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// realityKeypair mints an X25519 keypair in the encoding `xray x25519` uses
// (base64.RawURLEncoding of both scalars). The deploy script re-derives the
// public key on the node with `xray x25519 -i` and aborts on a mismatch, so a
// future encoding change surfaces as a failed step instead of a node that
// silently rejects every client.
func realityKeypair() (priv string, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(k.Bytes()),
		base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// urlSafe keeps a display name inside a URI fragment. Non-ASCII runs (a Chinese
// display name, a bullet separator) collapse into a single dash and the result
// is trimmed, so "主 VPS · 10.0.0.1" becomes "VPS-10-0-0-1" instead of a row of
// dashes.
func urlSafe(s string) string {
	if s == "" {
		return "NGG"
	}
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		case r == '.' || r == '-' || r == ' ':
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "NGG"
	}
	return out
}
