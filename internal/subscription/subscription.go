// Package subscription renders one user's client configuration for the six
// subscription formats the console offers (Clash / Surge / sing-box / v2rayN /
// plain SOCKS / Sub-Store plain JSON).
//
// A subscription is a binding, not a template: user → node (an entry of the
// host registry) → protocol → rule sets → DNS policy. Every field comes from a
// real record; when a node has no port or no credential the renderer refuses
// with an explicit error instead of inventing values that would only fail on
// the client.
//
// SECURITY: the token is the only handle a public client sends, so RenderToken
// answers "no" for unknown / disabled / expired tokens without distinguishing
// the reason. Nothing here logs a token or a credential.
package subscription

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ngg-project/ngg/internal/gateway"
	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/rulesets"
	"github.com/ngg-project/ngg/internal/substore"
	"github.com/ngg-project/ngg/internal/users"
)

// Target names accepted by RenderToken / Links.
const (
	TargetClash    = "clash"
	TargetSurge    = "surge"
	TargetSingbox  = "singbox"
	TargetV2ray    = "v2ray"
	TargetSocks    = "socks"
	TargetSubstore = "substore"
)

// Targets is the canonical order used by the UI and by Build. Sub-Store comes
// last: it is the format an operator adds to a subscription manager, not one a
// device imports directly.
var Targets = []string{TargetClash, TargetSurge, TargetSingbox, TargetV2ray, TargetSocks, TargetSubstore}

func ValidTarget(t string) bool {
	for _, x := range Targets {
		if x == t {
			return true
		}
	}
	return false
}

// ContentTypeFor is what the public endpoint should answer with. Everything is
// plain text except sing-box, which is JSON by definition.
func ContentTypeFor(target string) string {
	switch target {
	case TargetSingbox, TargetSubstore:
		return "application/json; charset=utf-8"
	case TargetClash:
		return "text/yaml; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

// Errors surfaced to the operator. ErrNode* become 422 responses with the
// message; ErrUnknownTarget is a programming/UI error.
var (
	// ErrNotBound means the user has no node_id yet.
	ErrNotBound = errors.New("未绑定节点")
	// ErrNodeIncomplete means the bound node cannot produce a client entry:
	// missing port, missing credential, or not in the registry any more.
	ErrNodeIncomplete = errors.New("节点不可用")
	// ErrUnknownUser means the user id does not exist.
	ErrUnknownUser = errors.New("用户不存在")
	// ErrUnknownTarget means the requested format is not supported.
	ErrUnknownTarget = errors.New("未知的订阅格式")
	// ErrUnsupportedProtocol means this protocol cannot be expressed in the
	// requested format (e.g. socks5 in a v2rayN share link).
	ErrUnsupportedProtocol = errors.New("该协议不支持此订阅格式")
)

// Options wires the renderer to the stores it reads. Everything is optional
// except Users; a missing store simply removes that part of the config instead
// of producing fake content.
type Options struct {
	Users    *users.Registry
	Hosts    *hosts.Registry
	RuleSets *rulesets.Registry
	Gateway  *gateway.Store
	// DataDir is where <data>/provisions/<hostID>.json lives. Empty disables
	// the provision lookup and falls back to the host record.
	DataDir string
	// BaseURL reports the public origin (e.g. https://vpn.example.com:9191).
	// Empty keeps the subscription links relative.
	BaseURL func() string
	// Now is overridable for tests.
	Now func() time.Time
}

type Renderer struct {
	opts Options
}

func New(o Options) *Renderer {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Renderer{opts: o}
}

// Links carries the six subscription URLs of one user.
type Links struct {
	Clash    string `json:"clash"`
	Surge    string `json:"surge"`
	Singbox  string `json:"singbox"`
	V2ray    string `json:"v2ray"`
	Socks    string `json:"socks"`
	Substore string `json:"substore"`
}

// Payload is everything the console needs to render the subscription drawer.
// Body is the rendered text keyed by target; it is deliberately not part of the
// JSON document — the client fetches it through its link.
type Payload struct {
	UserID      string            `json:"user_id"`
	Token       string            `json:"token"`
	NodeID      string            `json:"node_id"`
	NodeName    string            `json:"node_name"`
	Protocol    string            `json:"protocol"`
	RuleSetIDs  []string          `json:"rule_set_ids"`
	DNSProfile  string            `json:"dns_profile"`
	BaseURL     string            `json:"base_url"`
	Links       Links             `json:"links"`
	Body        map[string]string `json:"-"`
	Warnings    []string          `json:"warnings"`
	GeneratedAt time.Time         `json:"generated_at"`
}

// node is the resolved upstream: host record merged with its provision file.
type node struct {
	HostID     string
	Name       string
	Address    string
	Port       int
	Protocol   string
	Credential string
	Transport  string
	TLS        bool
	SNI        string
	// SSMethod is the Shadowsocks cipher when the credential does not spell it
	// out ("method:password"). It is set from the provision, because the
	// provisioner writes a known cipher (hosts.SSMethod); a plain host record
	// leaves it empty and the renderer warns about the assumed default.
	SSMethod string
}

// provision mirrors the JSON contract the console already types as
// HostProvision; only the fields the renderer needs are decoded.
type provision struct {
	HostID     string            `json:"host_id"`
	Protocol   string            `json:"protocol"`
	Port       int               `json:"port"`
	Credential string            `json:"credential"`
	Endpoint   provisionEndpoint `json:"endpoint"`
	Notes      []string          `json:"notes"`
}

type provisionEndpoint struct {
	Address    string `json:"address"`
	Port       int    `json:"port"`
	Protocol   string `json:"protocol"`
	Credential string `json:"credential"`
	Transport  string `json:"transport"`
	TLS        bool   `json:"tls"`
	SNI        string `json:"sni"`
}

// Build renders every format for one user and returns the links plus warnings.
// The first call mints the subscription token; the config itself is rendered on
// every call so a changed host is reflected immediately.
func (r *Renderer) Build(userID string) (Payload, error) {
	if r.opts.Users == nil {
		return Payload{}, fmt.Errorf("用户目录未初始化")
	}
	u, ok := r.opts.Users.Get(userID)
	if !ok {
		return Payload{}, fmt.Errorf("%w: %q", ErrUnknownUser, userID)
	}
	if u.SubscriptionToken == "" {
		minted, err := r.opts.Users.EnsureSubscriptionToken(userID)
		if err != nil {
			return Payload{}, err
		}
		u = minted
	}

	n, nodeWarnings, err := r.resolveNode(u)
	if err != nil {
		return Payload{}, err
	}

	p := Payload{
		UserID:      u.ID,
		Token:       u.SubscriptionToken,
		NodeID:      n.HostID,
		NodeName:    n.Name,
		Protocol:    n.Protocol,
		RuleSetIDs:  append([]string(nil), u.RuleSetIDs...),
		DNSProfile:  u.DNSProfile,
		BaseURL:     r.baseURL(),
		Body:        map[string]string{},
		Warnings:    nodeWarnings,
		GeneratedAt: r.opts.Now(),
	}

	// Links are built for every target; a target that cannot express the
	// protocol keeps an empty link and explains why in Warnings.
	if p.BaseURL == "" {
		p.Warnings = append(p.Warnings, "未配置公开订阅地址（agent-server.json 的 public_url），链接只有相对路径，客户端无法直接导入")
	}
	for _, target := range Targets {
		body, warns, err := r.render(u, n, target)
		if err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s 订阅不可用：%v", target, err))
			continue
		}
		p.Body[target] = body
		p.Warnings = append(p.Warnings, warns...)
		setLink(&p.Links, target, r.link(u.SubscriptionToken, target))
	}

	// DNS only travels inside client configs; the share-link formats cannot
	// carry it, so say so once instead of silently dropping it.
	if u.DNSProfile != users.DNSProfileOff {
		if _, ok := p.Body[TargetV2ray]; ok {
			p.Warnings = append(p.Warnings, "v2rayN 分享链接无法携带 DNS 配置，客户端将使用自身 DNS")
		}
		if _, ok := p.Body[TargetSocks]; ok {
			p.Warnings = append(p.Warnings, "SOCKS 纯文本不含 DNS 与规则，仅供系统代理对话框手工填写")
		}
	}
	return p, nil
}

func setLink(l *Links, target, v string) {
	switch target {
	case TargetClash:
		l.Clash = v
	case TargetSurge:
		l.Surge = v
	case TargetSingbox:
		l.Singbox = v
	case TargetV2ray:
		l.V2ray = v
	case TargetSocks:
		l.Socks = v
	case TargetSubstore:
		l.Substore = v
	}
}

// LinksFor returns the link of one target; "" means the format cannot express
// the bound protocol (see Warnings).
func (p Payload) LinksFor(target string) string {
	switch target {
	case TargetClash:
		return p.Links.Clash
	case TargetSurge:
		return p.Links.Surge
	case TargetSingbox:
		return p.Links.Singbox
	case TargetV2ray:
		return p.Links.V2ray
	case TargetSocks:
		return p.Links.Socks
	case TargetSubstore:
		return p.Links.Substore
	}
	return ""
}

func (r *Renderer) baseURL() string {
	if r.opts.BaseURL == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(r.opts.BaseURL()), "/")
}

func (r *Renderer) link(token, target string) string {
	path := "/sub/u/" + token + "?target=" + target
	if base := r.baseURL(); base != "" {
		return base + path
	}
	return path
}

// RenderToken is the public entry point behind /sub/u/<token>. ok=false is the
// single answer for unknown token, disabled account, expired account and
// render failure, so the endpoint cannot be used to enumerate tokens.
func (r *Renderer) RenderToken(token, target string) (body string, headers map[string]string, ok bool) {
	token = strings.TrimSpace(token)
	if token == "" || r.opts.Users == nil {
		return "", nil, false
	}
	if target == "" {
		target = TargetClash
	}
	if !ValidTarget(target) {
		return "", nil, false
	}
	u, found := r.opts.Users.UserBySubscriptionToken(token)
	if !found || !u.SubscriptionUsable(r.opts.Now()) {
		return "", nil, false
	}
	n, _, err := r.resolveNode(u)
	if err != nil {
		return "", nil, false
	}
	body, _, err = r.render(u, n, target)
	if err != nil || strings.TrimSpace(body) == "" {
		return "", nil, false
	}
	headers = map[string]string{
		"subscription-userinfo":   fmt.Sprintf("upload=0; download=%d; total=%d", u.UsedBytes, u.DataLimit),
		"profile-update-interval": "24",
		"Content-Type":            ContentTypeFor(target),
	}
	return body, headers, true
}

// ------------------------------------------------------------------ *
// Node resolution
// ------------------------------------------------------------------ *

func (r *Renderer) resolveNode(u users.User) (node, []string, error) {
	if strings.TrimSpace(u.NodeID) == "" {
		return node{}, nil, fmt.Errorf("%w：请先在订阅配置中选择节点（出口主机）", ErrNotBound)
	}
	if r.opts.Hosts == nil {
		return node{}, nil, fmt.Errorf("%w：节点注册表未初始化", ErrNodeIncomplete)
	}
	h, ok := r.opts.Hosts.Get(u.NodeID)
	if !ok {
		return node{}, nil, fmt.Errorf("%w：节点 %q 不在节点列表中", ErrNodeIncomplete, u.NodeID)
	}

	n := node{
		HostID:     h.ID,
		Name:       h.Name,
		Address:    h.Address,
		Port:       h.SvcPort,
		Protocol:   users.NormalizeProtocol(h.Protocol),
		Credential: h.SecretKey,
	}
	var warnings []string

	// A provision is the authoritative source when it exists: it carries the
	// port, credential and TLS settings that were actually generated on the
	// host, which the host record only approximates.
	p, hasProvision := r.loadProvision(h.ID)
	if hasProvision {
		if p.Endpoint.Address != "" {
			n.Address = p.Endpoint.Address
		}
		if p.Endpoint.Port > 0 {
			n.Port = p.Endpoint.Port
		}
		if p.Endpoint.Protocol != "" {
			n.Protocol = users.NormalizeProtocol(p.Endpoint.Protocol)
		} else if p.Protocol != "" {
			n.Protocol = users.NormalizeProtocol(p.Protocol)
		}
		if p.Endpoint.Credential != "" {
			n.Credential = p.Endpoint.Credential
		} else if p.Credential != "" {
			n.Credential = p.Credential
		}
		n.Transport = strings.TrimSpace(p.Endpoint.Transport)
		n.TLS = p.Endpoint.TLS
		n.SNI = strings.TrimSpace(p.Endpoint.SNI)
		for _, note := range p.Notes {
			if note = strings.TrimSpace(note); note != "" {
				warnings = append(warnings, fmt.Sprintf("节点 %s：%s", h.ID, note))
			}
		}
	} else if r.opts.DataDir != "" {
		if _, err := os.Stat(r.provisionPath(h.ID)); err == nil {
			warnings = append(warnings, fmt.Sprintf("节点 %s 的 provision 文件无法解析，已改用节点记录中的地址 / 端口 / 凭据", h.ID))
		}
	}

	// The subscription may override the protocol of the node (the same host can
	// expose several inbounds); everything else still comes from the node.
	if p := users.NormalizeProtocol(u.Protocol); p != "" {
		n.Protocol = p
	}
	if n.Protocol == users.ProtocolShadowsocks && hasProvision {
		// The provisioner always writes one cipher; take it from there instead
		// of guessing when the credential carries only the password.
		n.SSMethod = hosts.SSMethod
	}
	if !h.Enabled {
		warnings = append(warnings, fmt.Sprintf("节点 %s 当前处于停用状态，生成的配置在节点重新启用前无法连通", h.ID))
	}

	if n.Address == "" {
		return node{}, nil, fmt.Errorf("%w：节点 %q 没有地址", ErrNodeIncomplete, h.ID)
	}
	if n.Port <= 0 || n.Port > 65535 {
		return node{}, nil, fmt.Errorf("%w：节点 %q 缺少服务端口（svc_port），无法生成客户端配置", ErrNodeIncomplete, h.ID)
	}
	if n.Protocol == "" {
		return node{}, nil, fmt.Errorf("%w：节点 %q 与其订阅绑定都没有指定协议", ErrNodeIncomplete, h.ID)
	}
	switch n.Protocol {
	case users.ProtocolVLESS, users.ProtocolTrojan, users.ProtocolShadowsocks:
		if strings.TrimSpace(n.Credential) == "" {
			return node{}, nil, fmt.Errorf("%w：节点 %q 缺少 %s 凭据（secret_key / provision.credential）", ErrNodeIncomplete, h.ID, n.Protocol)
		}
	case users.ProtocolSOCKS5, users.ProtocolHTTP:
		if strings.TrimSpace(n.Credential) == "" {
			warnings = append(warnings, fmt.Sprintf("节点 %s 没有凭据，客户端将以无认证方式连接 %s", h.ID, n.Protocol))
		}
	}
	return n, warnings, nil
}

func (r *Renderer) provisionPath(hostID string) string {
	return filepath.Join(r.opts.DataDir, "provisions", filepath.Base(hostID)+".json")
}

func (r *Renderer) loadProvision(hostID string) (provision, bool) {
	if r.opts.DataDir == "" || strings.TrimSpace(hostID) == "" {
		return provision{}, false
	}
	b, err := os.ReadFile(r.provisionPath(hostID))
	if err != nil {
		return provision{}, false
	}
	var p provision
	if err := json.Unmarshal(b, &p); err != nil {
		return provision{}, false
	}
	return p, true
}

// ------------------------------------------------------------------ *
// Rendering
// ------------------------------------------------------------------ *

func (r *Renderer) render(u users.User, n node, target string) (string, []string, error) {
	dnsProfile := users.NormalizeDNSProfile(u.DNSProfile)
	sets, setWarnings := r.ruleSets(u.RuleSetIDs)
	switch target {
	case TargetClash:
		return r.renderClash(u, n, dnsProfile, sets, setWarnings)
	case TargetSurge:
		return r.renderSurge(u, n, dnsProfile, sets, setWarnings)
	case TargetSingbox:
		return r.renderSingBox(u, n, dnsProfile, sets, setWarnings)
	case TargetV2ray:
		return r.renderV2ray(u, n)
	case TargetSocks:
		return r.renderSocks(n)
	case TargetSubstore:
		return r.renderSubstore(u, n, setWarnings)
	default:
		return "", nil, fmt.Errorf("%w: %q", ErrUnknownTarget, target)
	}
}

// ruleSetRef is one bound rule set with a display name that is safe to use both
// as a mihomo provider key and as a Surge RULE-SET argument.
type ruleSetRef struct {
	ID   string
	Name string
}

func (r *Renderer) ruleSets(ids []string) ([]ruleSetRef, []string) {
	if len(ids) == 0 {
		return nil, nil
	}
	if r.opts.RuleSets == nil {
		return nil, []string{"已绑定规则集，但规则集存储未初始化：生成的配置不含规则分流"}
	}
	out := make([]ruleSetRef, 0, len(ids))
	var warnings []string
	for _, id := range ids {
		s, ok := r.opts.RuleSets.Get(id)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("规则集 %q 不在规则集列表中，已跳过", id))
			continue
		}
		out = append(out, ruleSetRef{ID: s.ID, Name: safeRuleSetName(s.Name, s.ID)})
	}
	return out, warnings
}

// safeRuleSetName keeps the operator's name but strips the characters that
// would break a comma-separated rule line or a YAML key.
func safeRuleSetName(name, id string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = id
	}
	replacer := strings.NewReplacer(",", "-", "\n", " ", "\r", " ", "\t", " ")
	name = strings.TrimSpace(replacer.Replace(name))
	if name == "" {
		name = id
	}
	return name
}

func (r *Renderer) ruleSetURL(id string) string {
	base := r.baseURL()
	if base == "" {
		return "/rule-set/" + id
	}
	return base + "/rule-set/" + id
}

func hasRuleSets(sets []ruleSetRef) bool { return len(sets) > 0 }

func ruleSetWarning(sets []ruleSetRef, target string) []string {
	if !hasRuleSets(sets) {
		return nil
	}
	return []string{fmt.Sprintf("%s 中的规则集下载地址（rule-set/<id>）尚未由 NGG 托管：导入后需替换为真实的规则集地址，或先移除规则集引用，否则 %s 可能因拉取失败而无法加载规则", target, target)}
}

// --- Clash / mihomo -------------------------------------------------

type clashWS struct {
	Path string `yaml:"path,omitempty"`
}

type clashProxy struct {
	Name       string   `yaml:"name"`
	Type       string   `yaml:"type"`
	Server     string   `yaml:"server"`
	Port       int      `yaml:"port"`
	UUID       string   `yaml:"uuid,omitempty"`
	Password   string   `yaml:"password,omitempty"`
	Cipher     string   `yaml:"cipher,omitempty"`
	Username   string   `yaml:"username,omitempty"`
	UDP        bool     `yaml:"udp,omitempty"`
	TLS        bool     `yaml:"tls,omitempty"`
	Servername string   `yaml:"servername,omitempty"`
	SNI        string   `yaml:"sni,omitempty"`
	Network    string   `yaml:"network,omitempty"`
	WSOpts     *clashWS `yaml:"ws-opts,omitempty"`
}

type clashGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

type clashDNS struct {
	Enable     bool     `yaml:"enable"`
	IPv6       bool     `yaml:"ipv6,omitempty"`
	Nameserver []string `yaml:"nameserver,omitempty"`
	Fallback   []string `yaml:"fallback,omitempty"`
}

type clashProvider struct {
	Type     string `yaml:"type"`
	Behavior string `yaml:"behavior"`
	URL      string `yaml:"url"`
	Path     string `yaml:"path"`
	Interval int    `yaml:"interval"`
}

type clashDoc struct {
	MixedPort     int                      `yaml:"mixed-port"`
	AllowLan      bool                     `yaml:"allow-lan"`
	Mode          string                   `yaml:"mode"`
	LogLevel      string                   `yaml:"log-level"`
	DNS           *clashDNS                `yaml:"dns,omitempty"`
	Proxies       []clashProxy             `yaml:"proxies"`
	ProxyGroups   []clashGroup             `yaml:"proxy-groups"`
	RuleProviders map[string]clashProvider `yaml:"rule-providers,omitempty"`
	Rules         []string                 `yaml:"rules"`
}

const proxyGroupName = "PROXY"

func (r *Renderer) renderClash(u users.User, n node, dnsProfile string, sets []ruleSetRef, warns []string) (string, []string, error) {
	p := clashProxy{Name: clashProxyName(u, n), Server: n.Address, Port: n.Port}
	switch n.Protocol {
	case users.ProtocolVLESS:
		p.Type = "vless"
		p.UUID = n.Credential
		p.UDP = true
		if n.TLS || n.SNI != "" {
			p.TLS = true
			p.Servername = firstNonEmpty(n.SNI, n.Address)
		}
		if t := normalizeTransport(n.Transport); t != "" {
			p.Network = t
			if t == "ws" {
				p.WSOpts = &clashWS{Path: "/"}
			}
		}
	case users.ProtocolTrojan:
		p.Type = "trojan"
		p.Password = n.Credential
		p.UDP = true
		p.SNI = firstNonEmpty(n.SNI, n.Address)
	case users.ProtocolShadowsocks:
		p.Type = "ss"
		method, password, ok := splitShadowsocksCredential(n)
		if !ok {
			warns = append(warns, "节点凭据未携带加密方式（期望 method:password），已按 aes-256-gcm 渲染，请核对节点实际加密方式")
		}
		p.Cipher = method
		p.Password = password
		p.UDP = true
	case users.ProtocolSOCKS5:
		p.Type = "socks5"
		p.Username, p.Password = splitUserPassword(n.Credential)
		p.UDP = true
	case users.ProtocolHTTP:
		p.Type = "http"
		p.Username, p.Password = splitUserPassword(n.Credential)
		if n.TLS {
			p.TLS = true
			p.Servername = firstNonEmpty(n.SNI, n.Address)
		}
	default:
		return "", nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, n.Protocol)
	}

	doc := clashDoc{
		MixedPort: 7890,
		AllowLan:  false,
		Mode:      "rule",
		LogLevel:  "info",
		Proxies:   []clashProxy{p},
		ProxyGroups: []clashGroup{{
			Name:    proxyGroupName,
			Type:    "select",
			Proxies: []string{p.Name, "DIRECT"},
		}},
	}
	switch dnsProfile {
	case users.DNSProfileSystem:
		doc.DNS = &clashDNS{Enable: false}
	case users.DNSProfileGateway:
		servers, w := r.gatewayDNS()
		warns = append(warns, w...)
		doc.DNS = &clashDNS{Enable: true, IPv6: false, Nameserver: servers}
	}

	if hasRuleSets(sets) {
		doc.RuleProviders = map[string]clashProvider{}
		for _, s := range sets {
			doc.RuleProviders[s.Name] = clashProvider{
				Type:     "http",
				Behavior: "classical",
				URL:      r.ruleSetURL(s.ID),
				Path:     "./ruleset/" + s.ID + ".yaml",
				Interval: 86400,
			}
			doc.Rules = append(doc.Rules, "RULE-SET,"+s.Name+","+proxyGroupName)
		}
	}
	doc.Rules = append(doc.Rules, "MATCH,"+proxyGroupName)

	var buf bytes.Buffer
	buf.WriteString("# NGG 订阅 · mihomo / Clash.Meta\n")
	buf.WriteString("# 节点：" + n.Name + "（" + n.HostID + "）\n")
	if doc.DNS != nil && !doc.DNS.Enable {
		buf.WriteString("# DNS：跟随系统解析（dns.enable: false）\n")
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", nil, fmt.Errorf("生成 Clash 配置失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", nil, fmt.Errorf("生成 Clash 配置失败: %w", err)
	}
	warns = append(warns, ruleSetWarning(sets, "Clash")...)
	return buf.String(), warns, nil
}

func clashProxyName(u users.User, n node) string {
	name := strings.TrimSpace(n.Name)
	if name == "" {
		name = n.HostID
	}
	if name == "" {
		name = u.Username
	}
	return safeRuleSetName(name, u.ID)
}

// --- Surge ---------------------------------------------------------

func (r *Renderer) renderSurge(u users.User, n node, dnsProfile string, sets []ruleSetRef, warns []string) (string, []string, error) {
	name := clashProxyName(u, n)
	var proxyLine string
	switch n.Protocol {
	case users.ProtocolVLESS:
		// Surge has no VLESS outbound. The line is written in the closest
		// shape Surge understands so the operator sees what was bound, but it
		// will not be accepted by Surge itself.
		proxyLine = fmt.Sprintf("%s = vless, %s, %d, username=%s", name, n.Address, n.Port, n.Credential)
		if n.TLS || n.SNI != "" {
			proxyLine += ", tls=true"
			if sni := firstNonEmpty(n.SNI, n.Address); sni != "" {
				proxyLine += ", sni=" + sni
			}
		}
		warns = append(warns, "Surge 不支持 VLESS 出站：该行只是绑定内容的镜像，Surge 会忽略/拒绝它。要在 Surge 上使用请绑定 trojan 或 shadowsocks 节点")
	case users.ProtocolTrojan:
		proxyLine = fmt.Sprintf("%s = trojan, %s, %d, password=%s", name, n.Address, n.Port, n.Credential)
		if sni := firstNonEmpty(n.SNI, n.Address); sni != "" {
			proxyLine += ", sni=" + sni
		}
	case users.ProtocolShadowsocks:
		method, password, ok := splitShadowsocksCredential(n)
		if !ok {
			warns = append(warns, "节点凭据未携带加密方式（期望 method:password），已按 aes-256-gcm 渲染，请核对节点实际加密方式")
		}
		proxyLine = fmt.Sprintf("%s = ss, %s, %d, encrypt-method=%s, password=%s", name, n.Address, n.Port, method, password)
	case users.ProtocolSOCKS5:
		proxyLine = fmt.Sprintf("%s = socks5, %s, %d", name, n.Address, n.Port)
		if user, pass := splitUserPassword(n.Credential); user != "" || pass != "" {
			proxyLine += ", " + user + ", " + pass
		}
	case users.ProtocolHTTP:
		proxyLine = fmt.Sprintf("%s = http, %s, %d", name, n.Address, n.Port)
		if user, pass := splitUserPassword(n.Credential); user != "" || pass != "" {
			proxyLine += ", " + user + ", " + pass
		}
		if n.TLS {
			proxyLine += ", tls=true"
		}
	default:
		return "", nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, n.Protocol)
	}

	var b strings.Builder
	// Surge only honours the managed-config directive on the very first line.
	if u.SubscriptionToken != "" {
		b.WriteString("#!MANAGED-CONFIG " + r.link(u.SubscriptionToken, TargetSurge) + " interval=86400\n")
	}
	b.WriteString("# NGG 订阅 · Surge\n")
	b.WriteString("# 节点：" + n.Name + "（" + n.HostID + "）\n")
	b.WriteString("\n[General]\n")
	switch dnsProfile {
	case users.DNSProfileGateway:
		servers, w := r.gatewayDNS()
		warns = append(warns, w...)
		b.WriteString("dns-server = " + strings.Join(servers, ", ") + "\n")
	default:
		b.WriteString("# DNS：使用系统 / 运营商 DNS\n")
	}
	b.WriteString("\n[Proxy]\n")
	b.WriteString(proxyLine + "\n")
	b.WriteString("\n[Proxy Group]\n")
	b.WriteString(proxyGroupName + " = select, " + name + ", DIRECT\n")
	b.WriteString("\n[Rule]\n")
	for _, s := range sets {
		b.WriteString("RULE-SET," + s.Name + "," + proxyGroupName + "\n")
	}
	b.WriteString("FINAL," + proxyGroupName + "\n")
	warns = append(warns, ruleSetWarning(sets, "Surge")...)
	return b.String(), warns, nil
}

// --- sing-box ------------------------------------------------------

type singBoxTLS struct {
	Enabled    bool   `json:"enabled"`
	ServerName string `json:"server_name,omitempty"`
}

type singBoxTransport struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

type singBoxOutbound struct {
	Type       string            `json:"type"`
	Tag        string            `json:"tag"`
	Server     string            `json:"server,omitempty"`
	ServerPort int               `json:"server_port,omitempty"`
	UUID       string            `json:"uuid,omitempty"`
	Password   string            `json:"password,omitempty"`
	Method     string            `json:"method,omitempty"`
	Username   string            `json:"username,omitempty"`
	Version    string            `json:"version,omitempty"`
	TLS        *singBoxTLS       `json:"tls,omitempty"`
	Transport  *singBoxTransport `json:"transport,omitempty"`
}

type singBoxServer struct {
	Tag     string `json:"tag"`
	Address string `json:"address"`
}

type singBoxRuleSet struct {
	Type   string `json:"type"`
	Tag    string `json:"tag"`
	Format string `json:"format"`
	URL    string `json:"url"`
}

type singBoxRoute struct {
	Rules   []map[string]any `json:"rules,omitempty"`
	RuleSet []singBoxRuleSet `json:"rule_set,omitempty"`
	Final   string           `json:"final"`
}

type singBoxDoc struct {
	Log       map[string]any    `json:"log"`
	DNS       *singBoxDNS       `json:"dns,omitempty"`
	Outbounds []singBoxOutbound `json:"outbounds"`
	Route     singBoxRoute      `json:"route"`
}

type singBoxDNS struct {
	Servers []singBoxServer `json:"servers"`
}

const singBoxTag = "ngg-proxy"

func (r *Renderer) renderSingBox(u users.User, n node, dnsProfile string, sets []ruleSetRef, warns []string) (string, []string, error) {
	ob := singBoxOutbound{Tag: singBoxTag, Server: n.Address, ServerPort: n.Port}
	switch n.Protocol {
	case users.ProtocolVLESS:
		ob.Type = "vless"
		ob.UUID = n.Credential
		if n.TLS || n.SNI != "" {
			ob.TLS = &singBoxTLS{Enabled: true, ServerName: firstNonEmpty(n.SNI, n.Address)}
		}
		if t := normalizeTransport(n.Transport); t != "" {
			ob.Transport = &singBoxTransport{Type: t}
			if t == "ws" {
				ob.Transport.Path = "/"
			}
		}
	case users.ProtocolTrojan:
		ob.Type = "trojan"
		ob.Password = n.Credential
		ob.TLS = &singBoxTLS{Enabled: true, ServerName: firstNonEmpty(n.SNI, n.Address)}
	case users.ProtocolShadowsocks:
		ob.Type = "shadowsocks"
		method, password, ok := splitShadowsocksCredential(n)
		if !ok {
			warns = append(warns, "节点凭据未携带加密方式（期望 method:password），已按 aes-256-gcm 渲染，请核对节点实际加密方式")
		}
		ob.Method = method
		ob.Password = password
	case users.ProtocolSOCKS5:
		ob.Type = "socks"
		ob.Version = "5"
		ob.Username, ob.Password = splitUserPassword(n.Credential)
	case users.ProtocolHTTP:
		ob.Type = "http"
		ob.Username, ob.Password = splitUserPassword(n.Credential)
		if n.TLS {
			ob.TLS = &singBoxTLS{Enabled: true, ServerName: firstNonEmpty(n.SNI, n.Address)}
		}
	default:
		return "", nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, n.Protocol)
	}

	doc := singBoxDoc{
		Log: map[string]any{"level": "info", "timestamp": true},
		Outbounds: []singBoxOutbound{
			ob,
			{Type: "direct", Tag: "direct"},
		},
		Route: singBoxRoute{Final: singBoxTag},
	}
	if dnsProfile == users.DNSProfileGateway {
		servers, w := r.gatewayDNS()
		warns = append(warns, w...)
		dns := &singBoxDNS{}
		for i, s := range servers {
			dns.Servers = append(dns.Servers, singBoxServer{
				Tag:     fmt.Sprintf("dns-%d", i+1),
				Address: s,
			})
		}
		doc.DNS = dns
	}

	if hasRuleSets(sets) {
		tags := make([]string, 0, len(sets))
		for _, s := range sets {
			doc.Route.RuleSet = append(doc.Route.RuleSet, singBoxRuleSet{
				Type:   "remote",
				Tag:    s.Name,
				Format: "source",
				URL:    r.ruleSetURL(s.ID),
			})
			tags = append(tags, s.Name)
		}
		// Without this rule the declared sets would be downloaded and never
		// used; it routes their traffic through the same outbound as `final`,
		// which is what the operator asked for by binding them.
		doc.Route.Rules = []map[string]any{{
			"rule_set": tags,
			"outbound": singBoxTag,
		}}
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("生成 sing-box 配置失败: %w", err)
	}
	warns = append(warns, ruleSetWarning(sets, "sing-box")...)
	return string(b) + "\n", warns, nil
}

// --- v2rayN share links --------------------------------------------

func (r *Renderer) renderV2ray(u users.User, n node) (string, []string, error) {
	tag := clashProxyName(u, n)
	var link string
	switch n.Protocol {
	case users.ProtocolVLESS:
		q := url.Values{}
		q.Set("encryption", "none")
		q.Set("type", firstNonEmpty(normalizeTransport(n.Transport), "tcp"))
		if n.TLS || n.SNI != "" {
			q.Set("security", "tls")
			if sni := firstNonEmpty(n.SNI, n.Address); sni != "" {
				q.Set("sni", sni)
			}
		} else {
			q.Set("security", "none")
		}
		link = shareLink("vless", n.Credential, n.Address, n.Port, q, tag)
	case users.ProtocolTrojan:
		q := url.Values{}
		if sni := firstNonEmpty(n.SNI, n.Address); sni != "" {
			q.Set("sni", sni)
		}
		link = shareLink("trojan", n.Credential, n.Address, n.Port, q, tag)
	case users.ProtocolShadowsocks:
		method, password, ok := splitShadowsocksCredential(n)
		if !ok {
			return "", nil, fmt.Errorf("%w：Shadowsocks 分享链接需要 method:password 形式的凭据", ErrUnsupportedProtocol)
		}
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
		link = fmt.Sprintf("ss://%s@%s#%s", userinfo, net.JoinHostPort(n.Address, strconv.Itoa(n.Port)), url.PathEscape(tag))
	default:
		return "", nil, fmt.Errorf("%w：v2rayN 分享链接只支持 vless / trojan / shadowsocks", ErrUnsupportedProtocol)
	}
	// v2rayN subscribes to a base64 document whose lines are share links.
	encoded := base64.StdEncoding.EncodeToString([]byte(link + "\n"))
	return encoded + "\n", nil, nil
}

func shareLink(scheme, credential, host string, port int, q url.Values, tag string) string {
	u := url.URL{
		Scheme:   scheme,
		User:     url.User(credential),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		RawQuery: q.Encode(),
		Fragment: tag,
	}
	return u.String()
}

// --- plain SOCKS ---------------------------------------------------

func (r *Renderer) renderSocks(n node) (string, []string, error) {
	user, pass := splitUserPassword(n.Credential)
	var warnings []string
	if user == "" && pass == "" {
		warnings = append(warnings, "节点没有凭据：SOCKS 行中的用户名 / 密码为空")
	}
	return fmt.Sprintf("%s:%d:%s:%s\n", n.Address, n.Port, user, pass), warnings, nil
}

// --- Sub-Store plain JSON ------------------------------------------

// renderSubstore emits the document Sub-Store ingests: a plain JSON array of
// proxy objects using its normalized key names (the shape of its own
// `target=json` output). Sub-Store accepts that as a "Clash Proxy JSON" source,
// so the link can be added to a Sub-Store subscription with no conversion.
//
// The format describes nodes and nothing else: rules and DNS live in
// Sub-Store's own operator chain, so they are reported as a warning instead of
// being silently dropped.
func (r *Renderer) renderSubstore(u users.User, n node, warns []string) (string, []string, error) {
	sn := substore.Node{
		Name:      clashProxyName(u, n),
		Server:    n.Address,
		Port:      n.Port,
		Protocol:  n.Protocol,
		TLS:       n.TLS,
		SNI:       n.SNI,
		Transport: n.Transport,
	}
	if n.Protocol == users.ProtocolShadowsocks {
		method, password, ok := splitShadowsocksCredential(n)
		if !ok {
			warns = append(warns, "节点凭据未携带加密方式（期望 method:password），已按 aes-256-gcm 渲染，请核对节点实际加密方式")
		}
		sn.Method, sn.Credential = method, password
	} else {
		sn.Credential = n.Credential
	}
	body, err := substore.Render(sn)
	if err != nil {
		return "", nil, err
	}
	warns = append(warns, "Sub-Store 格式只包含节点本身：规则分流与 DNS 请在 Sub-Store 的脚本操作中配置，NGG 的规则集引用不会随此格式传递")
	return string(body), warns, nil
}

// ------------------------------------------------------------------ *
// helpers
// ------------------------------------------------------------------ *

// gatewayDNS returns the DNS servers of the gateway policy, or the two public
// resolvers with a warning when the gateway has none configured.
func (r *Renderer) gatewayDNS() ([]string, []string) {
	var servers []string
	if r.opts.Gateway != nil {
		for _, s := range r.opts.Gateway.Get().DNS.Servers {
			if s = strings.TrimSpace(s); s != "" {
				servers = append(servers, s)
			}
		}
	}
	if len(servers) == 0 {
		return []string{"223.5.5.5", "119.29.29.29"},
			[]string{"网关未配置 DNS 服务器，已回退到 223.5.5.5 / 119.29.29.29"}
	}
	return servers, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// normalizeTransport maps the provision's transport name to the value Clash and
// sing-box expect. Unknown values are passed through untouched.
func normalizeTransport(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "tcp", "raw":
		if strings.ToLower(strings.TrimSpace(t)) == "" {
			return ""
		}
		return "tcp"
	case "ws", "websocket":
		return "ws"
	case "grpc":
		return "grpc"
	case "h2", "http":
		return "h2"
	default:
		return strings.ToLower(strings.TrimSpace(t))
	}
}

// splitShadowsocksCredential resolves the cipher and password of a Shadowsocks
// outbound. An explicit "method:password" wins; otherwise the cipher comes from
// the provision (hosts.SSMethod) when there is one, and finally from the
// documented default, which is reported as ok=false so the caller can warn that
// the cipher was assumed rather than read.
func splitShadowsocksCredential(n node) (method, password string, ok bool) {
	cred := strings.TrimSpace(n.Credential)
	if cred == "" {
		return firstNonEmpty(n.SSMethod, "aes-256-gcm"), "", false
	}
	if i := strings.Index(cred, ":"); i > 0 && i < len(cred)-1 {
		return strings.TrimSpace(cred[:i]), cred[i+1:], true
	}
	if n.SSMethod != "" {
		return n.SSMethod, cred, true
	}
	return "aes-256-gcm", cred, false
}

// splitUserPassword accepts "user:password" and returns ("", "") for an empty
// credential; a credential without a colon is treated as the username.
func splitUserPassword(cred string) (user, pass string) {
	cred = strings.TrimSpace(cred)
	if cred == "" {
		return "", ""
	}
	if i := strings.Index(cred, ":"); i >= 0 {
		return cred[:i], cred[i+1:]
	}
	return cred, ""
}
