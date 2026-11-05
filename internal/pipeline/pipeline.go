// Package pipeline turns the Config Engine pages into ONE runnable proxy
// chain.
//
// The order is fixed and explicit, because "which page feeds which" was the
// main source of confusion:
//
//	协议配置  →  入口节点  →  规则配置  →  DNS配置  →  出口节点  →  用户管理
//
// The first two describe how traffic gets IN, the middle two decide where it
// goes, the last two describe who may use it and where it leaves. Render()
// produces a real mihomo configuration from those six sources; the API layer
// applies it through the existing config manager (native `mihomo -t` check,
// backup, hot replace) and then dials through the resulting SOCKS port to prove
// the chain actually works.
package pipeline

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/rulesets"
)

// Inbound is the listener NGG opens for clients (browser / phone / LAN).
type Inbound struct {
	HostID   string `json:"host_id"`   // optional: first-hop VPS in front of the listener
	Listen   string `json:"listen"`    // 127.0.0.1 or 0.0.0.0
	Port     int    `json:"port"`      // SOCKS5/HTTP mixed port
	AllowLAN bool   `json:"allow_lan"` // expose to the LAN (requires auth or trust)
}

// Outbound selects the exit VPS.
type Outbound struct {
	HostIDs []string `json:"host_ids"`
	Mode    string   `json:"mode"` // select | url-test | fallback
	// DirectFallback appends DIRECT to the group so a dead VPS still lets the
	// operator reach the console instead of black-holing everything.
	DirectFallback bool `json:"direct_fallback"`
}

// Config is the single active chain.
type Config struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kernel     string    `json:"kernel"`
	Protocol   string    `json:"protocol"`
	Inbound    Inbound   `json:"inbound"`
	Outbound   Outbound  `json:"outbound"`
	RuleSetIDs []string  `json:"rule_set_ids"`
	DNSEnabled bool      `json:"dns_enabled"`
	UserIDs    []string  `json:"user_ids"`
	Enabled    bool      `json:"enabled"`
	TestTarget string    `json:"test_target"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Default returns the chain a fresh installation starts from.
func Default() Config {
	return Config{
		ID:         "default",
		Name:       "默认链路",
		Kernel:     "mihomo",
		Protocol:   "mixed",
		Inbound:    Inbound{Listen: "127.0.0.1", Port: 7891, AllowLAN: false},
		Outbound:   Outbound{Mode: "select", DirectFallback: true},
		Enabled:    true,
		TestTarget: "www.gstatic.com:80",
	}
}

// Store persists the chain to data/pipeline.json.
type Store struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "pipeline.json"), cfg: Default()}
}

func (s *Store) Path() string { return s.path }

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("pipeline.json 解析失败: %w", err)
	}
	s.cfg = normalize(c)
	return nil
}

func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalize(s.cfg)
}

func (s *Store) Set(c Config) (Config, error) {
	c = normalize(c)
	if err := checkConfig(c); err != nil {
		return Config{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.UpdatedAt = time.Now().UTC()
	s.cfg = c
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return Config{}, err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return Config{}, err
	}
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		return Config{}, err
	}
	return c, nil
}

func normalize(c Config) Config {
	d := Default()
	if c.ID == "" {
		c.ID = d.ID
	}
	if c.Name == "" {
		c.Name = d.Name
	}
	if c.Kernel == "" {
		c.Kernel = d.Kernel
	}
	if c.Protocol == "" {
		c.Protocol = d.Protocol
	}
	if c.Inbound.Listen == "" {
		c.Inbound.Listen = d.Inbound.Listen
	}
	if c.Inbound.Port == 0 {
		c.Inbound.Port = d.Inbound.Port
	}
	if c.Outbound.Mode == "" {
		c.Outbound.Mode = d.Outbound.Mode
	}
	if c.TestTarget == "" {
		c.TestTarget = d.TestTarget
	}
	c.Outbound.HostIDs = dedupe(c.Outbound.HostIDs)
	c.RuleSetIDs = dedupe(c.RuleSetIDs)
	c.UserIDs = dedupe(c.UserIDs)
	return c
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

var validModes = map[string]bool{"select": true, "url-test": true, "fallback": true}

func checkConfig(c Config) error {
	if c.Inbound.Port < 1 || c.Inbound.Port > 65535 {
		return fmt.Errorf("入口端口必须在 1-65535 之间")
	}
	switch c.Inbound.Listen {
	case "127.0.0.1", "0.0.0.0", "::1", "::":
	default:
		if net.ParseIP(c.Inbound.Listen) == nil {
			return fmt.Errorf("入口监听地址 %q 不是合法 IP", c.Inbound.Listen)
		}
	}
	if !validModes[c.Outbound.Mode] {
		return fmt.Errorf("出口策略 %q 不受支持（select / url-test / fallback）", c.Outbound.Mode)
	}
	if c.Inbound.AllowLAN && c.Inbound.Listen == "127.0.0.1" {
		c.Inbound.Listen = "0.0.0.0"
	}
	return nil
}

// Inputs is a snapshot of the other Config Engine pages, assembled by the API
// layer so this package stays free of engine dependencies (and testable).
type Inputs struct {
	Hosts       []hosts.Host
	RuleSets    []rulesets.RuleSet
	DNSUpstream []string
	DNSEnabled  bool
	ActiveUsers int
	KernelReady bool
	KernelState string
	// PortFree reports whether the requested local port can be bound.
	PortFree func(addr string) bool
}

// Step is one stage of the chain with a real verdict.
type Step struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Order   int    `json:"order"`
	Status  string `json:"status"` // ok | warn | fail | skip
	Value   string `json:"value"`
	Detail  string `json:"detail"`
	Page    string `json:"page"` // router name of the owning page
	FixHint string `json:"fix_hint"`
}

func step(key, title string, order int, page, value, detail string) Step {
	return Step{Key: key, Title: title, Order: order, Status: "ok", Value: value, Detail: detail, Page: page}
}

// Validate runs the six gates in chain order and returns them in that order.
func Validate(c Config, in Inputs) []Step {
	steps := make([]Step, 0, 7)

	// 1) 协议
	s1 := step("protocol", "协议", 1, "protocols", c.Protocol, "客户端以 SOCKS5 / HTTP 混合端口接入，由 mihomo 承载")
	if c.Kernel != "mihomo" {
		s1.Status = "warn"
		s1.Detail = fmt.Sprintf("当前只对 mihomo 做完整渲染与原生校验，%s 暂未接入链路编排", c.Kernel)
	}
	steps = append(steps, s1)

	// 2) 入口
	s2 := step("inbound", "入口", 2, "inbound", fmt.Sprintf("%s:%d", c.Inbound.Listen, c.Inbound.Port), "本机监听地址，供客户端接入")
	if c.Inbound.HostID != "" {
		h, ok := findHost(in.Hosts, c.Inbound.HostID)
		switch {
		case !ok:
			s2.Status = "fail"
			s2.Detail = "入口节点不存在，可能已被删除"
			s2.FixHint = "在「入口节点」重新选择，或清空入口节点改为本机直入"
		case !h.Enabled:
			s2.Status = "fail"
			s2.Detail = "入口节点已禁用"
			s2.FixHint = "在「入口节点」启用该主机"
		default:
			s2.Detail = fmt.Sprintf("流量先经过入口节点 %s（%s:%d），再进入出口", h.Name, h.Address, svcPort(h))
			if !probeHost(h, 2*time.Second) {
				s2.Status = "warn"
				s2.Detail += "；但该节点当前 TCP 不可达，链路会在第一跳失败"
				s2.FixHint = "检查入口节点的地址、端口与防火墙"
			}
		}
	}
	if in.PortFree != nil && !in.PortFree(net.JoinHostPort(c.Inbound.Listen, strconv.Itoa(c.Inbound.Port))) {
		s2.Status = "fail"
		s2.Detail = fmt.Sprintf("端口 %d 已被占用", c.Inbound.Port)
		s2.FixHint = "换一个入口端口，或先停止占用该端口的程序"
	}
	steps = append(steps, s2)

	// 3) 规则
	enabled := enabledRuleSets(in.RuleSets)
	total := 0
	for _, rs := range enabled {
		total += len(rs.Rules)
	}
	s3 := step("rules", "规则", 3, "rules", fmt.Sprintf("%d 个规则集 / %d 条规则", len(enabled), total), "按顺序匹配，未命中走默认出口")
	if len(enabled) == 0 {
		s3.Status = "warn"
		s3.Detail = "没有启用的规则集，所有流量都会走默认出口（等价于全局代理）"
		s3.FixHint = "在「规则配置」启用或新建规则集，例如国内直连 + 其余走出口"
	} else if total == 0 {
		s3.Status = "warn"
		s3.Detail = "规则集已启用但规则数为 0，请先刷新规则源"
		s3.FixHint = "在「规则配置」点击刷新，确认规则解析成功"
	}
	if len(c.RuleSetIDs) > 0 {
		missing := 0
		for _, id := range c.RuleSetIDs {
			if !hasRuleSet(in.RuleSets, id) {
				missing++
			}
		}
		if missing > 0 {
			s3.Status = "fail"
			s3.Detail = fmt.Sprintf("%d 个被引用的规则集已不存在", missing)
			s3.FixHint = "在「规则配置」重新勾选规则集"
		}
	}
	steps = append(steps, s3)

	// 4) DNS
	s4 := step("dns", "DNS", 4, "dns", strings.Join(in.DNSUpstream, " / "), "域名解析由 NGG 统一 DNS 服务与出口内核共同完成")
	if len(in.DNSUpstream) == 0 {
		s4.Value = "未配置上游"
		s4.Status = "warn"
		s4.Detail = "没有可用的 DNS 上游，域名规则可能失效"
		s4.FixHint = "在「DNS配置」填入至少一个上游，例如 223.5.5.5:53"
	} else if !in.DNSEnabled {
		s4.Status = "warn"
		s4.Detail = "NGG DNS 服务未启用，域名将由系统解析器处理"
	}
	steps = append(steps, s4)

	// 5) 出口
	s5 := step("outbound", "出口", 5, "outbound", "未选择", "最终出站节点（VPS）")
	outHosts := outboundHosts(c, in.Hosts)
	switch {
	case len(outHosts) == 0:
		s5.Status = "warn"
		s5.Value = "直连"
		s5.Detail = "没有可用的出口节点，链路将直接使用本机网络出去"
		s5.FixHint = "在「出口节点」添加一台 VPS 并设为出站"
	default:
		names := make([]string, 0, len(outHosts))
		offline := 0
		for _, h := range outHosts {
			names = append(names, h.Name)
			if !probeHost(h, 2*time.Second) {
				offline++
			}
		}
		s5.Value = strings.Join(names, " / ")
		s5.Detail = fmt.Sprintf("%d 个出口节点，策略 %s", len(outHosts), c.Outbound.Mode)
		if offline > 0 {
			s5.Status = "warn"
			s5.Detail += fmt.Sprintf("；其中 %d 个当前 TCP 不可达", offline)
			s5.FixHint = "确认 VPS 在线、端口开放，或在出口节点重新探测"
		}
		for _, h := range outHosts {
			if p, err := proxyFor(h); err != nil {
				s5.Status = "warn"
				s5.Detail += "；" + h.Name + ": " + err.Error()
			} else {
				_ = p
			}
		}
	}
	steps = append(steps, s5)

	// 6) 用户
	s6 := step("users", "用户", 6, "users", fmt.Sprintf("%d 个可用用户", in.ActiveUsers), "用户目录只做准入与配额记账，不参与协议握手")
	if in.ActiveUsers == 0 {
		s6.Status = "warn"
		s6.Detail = "还没有可用的用户，链路本身可用，但没有任何账号被计入配额"
		s6.FixHint = "在「用户管理」添加用户；不需要配额时可忽略"
	}
	steps = append(steps, s6)

	// 7) 内核（执行者，不是链路的一环，但必须就绪）
	s7 := step("kernel", "内核", 7, "cores", c.Kernel, "负责实际协议握手与出站")
	if !in.KernelReady {
		s7.Status = "fail"
		s7.Detail = fmt.Sprintf("%s 未安装，链路无法启动", c.Kernel)
		s7.FixHint = "在「内核管理」安装 mihomo"
	} else {
		s7.Detail = fmt.Sprintf("%s 已安装，当前状态 %s", c.Kernel, in.KernelState)
	}
	steps = append(steps, s7)

	return steps
}

// HasBlockingFailure reports whether any step forbids applying the chain.
func HasBlockingFailure(steps []Step) (Step, bool) {
	for _, s := range steps {
		if s.Status == "fail" {
			return s, true
		}
	}
	return Step{}, false
}

func findHost(list []hosts.Host, id string) (hosts.Host, bool) {
	for _, h := range list {
		if h.ID == id {
			return h, true
		}
	}
	return hosts.Host{}, false
}

func hasRuleSet(list []rulesets.RuleSet, id string) bool {
	for _, rs := range list {
		if rs.ID == id {
			return true
		}
	}
	return false
}

func enabledRuleSets(list []rulesets.RuleSet) []rulesets.RuleSet {
	out := make([]rulesets.RuleSet, 0, len(list))
	for _, rs := range list {
		if rs.Enabled && rs.RuleCount > 0 {
			out = append(out, rs)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func outboundHosts(c Config, list []hosts.Host) []hosts.Host {
	out := make([]hosts.Host, 0, len(c.Outbound.HostIDs))
	for _, id := range c.Outbound.HostIDs {
		if h, ok := findHost(list, id); ok && h.Enabled {
			out = append(out, h)
		}
	}
	return out
}

func svcPort(h hosts.Host) int {
	if h.SvcPort > 0 {
		return h.SvcPort
	}
	return h.NodePort
}

func probeHost(h hosts.Host, timeout time.Duration) bool {
	addr := net.JoinHostPort(h.Address, strconv.Itoa(svcPort(h)))
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// ---------------------------------------------------------------- rendering ---

const groupName = "NGG-出站"

type mihomoDoc struct {
	MixedPort   int              `yaml:"mixed-port"`
	AllowLan    bool             `yaml:"allow-lan"`
	BindAddress string           `yaml:"bind-address,omitempty"`
	Mode        string           `yaml:"mode"`
	LogLevel    string           `yaml:"log-level"`
	IPv6        bool             `yaml:"ipv6"`
	DNS         *mihomoDNS       `yaml:"dns,omitempty"`
	Proxies     []map[string]any `yaml:"proxies"`
	ProxyGroups []map[string]any `yaml:"proxy-groups"`
	Rules       []string         `yaml:"rules"`
}

type mihomoDNS struct {
	Enable         bool     `yaml:"enable"`
	IPv6           bool     `yaml:"ipv6"`
	EnhancedMode   string   `yaml:"enhanced-mode"`
	Nameserver     []string `yaml:"nameserver"`
	FallbackFilter struct {
		GeoIP bool `yaml:"geoip"`
	} `yaml:"fallback-filter,omitempty"`
}

// Render produces a complete mihomo configuration for the chain.
func Render(c Config, in Inputs) (string, []string, error) {
	warnings := []string{}
	proxies := []map[string]any{}
	names := []string{}

	entry, hasEntry := hosts.Host{}, false
	if c.Inbound.HostID != "" {
		entry, hasEntry = findHost(in.Hosts, c.Inbound.HostID)
		if !hasEntry {
			warnings = append(warnings, "入口节点不存在，已忽略第一跳")
		}
	}
	if hasEntry {
		p, err := proxyFor(entry)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("入口节点 %s 无法渲染：%v", entry.Name, err))
			hasEntry = false
		} else {
			p["name"] = "entry-" + entry.Internal
			proxies = append(proxies, p)
			names = append(names, p["name"].(string))
		}
	}

	outHosts := outboundHosts(c, in.Hosts)
	for _, h := range outHosts {
		p, err := proxyFor(h)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("出口节点 %s 已跳过：%v", h.Name, err))
			continue
		}
		name := "out-" + h.Internal
		if h.Internal == "" {
			name = "out-" + h.ID
		}
		p["name"] = name
		if hasEntry {
			// Chain: local -> entry VPS -> this outbound -> target.
			p["dialer-proxy"] = "entry-" + entry.Internal
		}
		proxies = append(proxies, p)
		names = append(names, name)
	}

	groupMembers := append([]string{}, names...)
	// DIRECT may only be a manual option: in url-test / fallback it would win
	// on latency and silently leak traffic around the operator's VPS.
	if c.Outbound.DirectFallback && c.Outbound.Mode == "select" {
		groupMembers = append(groupMembers, "DIRECT")
	}
	if len(groupMembers) == 0 {
		groupMembers = []string{"DIRECT"}
	}

	group := map[string]any{
		"name":    groupName,
		"type":    c.Outbound.Mode,
		"proxies": groupMembers,
	}
	if c.Outbound.Mode == "url-test" || c.Outbound.Mode == "fallback" {
		group["url"] = "http://www.gstatic.com/generate_204"
		group["interval"] = 300
	}

	rules := buildRules(c, in, warnings)

	doc := mihomoDoc{
		MixedPort:   c.Inbound.Port,
		AllowLan:    c.Inbound.AllowLAN || c.Inbound.Listen == "0.0.0.0" || c.Inbound.Listen == "::",
		Mode:        "rule",
		LogLevel:    "info",
		IPv6:        false,
		Proxies:     proxies,
		ProxyGroups: []map[string]any{group},
		Rules:       rules,
	}
	if c.Inbound.Listen != "" && c.Inbound.Listen != "0.0.0.0" && c.Inbound.Listen != "::" {
		doc.BindAddress = c.Inbound.Listen
	}
	if c.DNSEnabled && len(in.DNSUpstream) > 0 {
		d := &mihomoDNS{
			Enable:       true,
			IPv6:         false,
			EnhancedMode: "fake-ip",
			Nameserver:   normalizeUpstreams(in.DNSUpstream),
		}
		d.FallbackFilter.GeoIP = true
		doc.DNS = d
	}

	var buf strings.Builder
	buf.WriteString("# 由 NGG 链路编排生成 —— 请勿手工编辑，修改请在「链路编排」保存后重新应用\n")
	buf.WriteString(fmt.Sprintf("# 链路：本机:%d", c.Inbound.Port))
	if hasEntry {
		buf.WriteString(" → 入口 " + entry.Name)
	}
	if len(outHosts) > 0 {
		buf.WriteString(" → 出口 " + strings.Join(hostNames(outHosts), "/"))
	} else {
		buf.WriteString(" → 直连")
	}
	buf.WriteString("\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", warnings, err
	}
	_ = enc.Close()
	return buf.String(), warnings, nil
}

func hostNames(list []hosts.Host) []string {
	out := make([]string, 0, len(list))
	for _, h := range list {
		out = append(out, h.Name)
	}
	return out
}

func normalizeUpstreams(up []string) []string {
	out := make([]string, 0, len(up))
	for _, u := range up {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		// mihomo accepts plain ip:port. Drop protocol prefixes users may paste.
		u = strings.TrimPrefix(u, "udp://")
		u = strings.TrimPrefix(u, "tcp://")
		if !strings.Contains(u, ":") {
			u += ":53"
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		out = []string{"223.5.5.5:53", "1.1.1.1:53"}
	}
	return out
}

func buildRules(c Config, in Inputs, warnings []string) []string {
	selected := in.RuleSets
	if len(c.RuleSetIDs) > 0 {
		picked := make([]rulesets.RuleSet, 0, len(c.RuleSetIDs))
		for _, id := range c.RuleSetIDs {
			for _, rs := range in.RuleSets {
				if rs.ID == id && rs.Enabled {
					picked = append(picked, rs)
					break
				}
			}
		}
		selected = picked
	} else {
		selected = enabledRuleSets(in.RuleSets)
	}

	const maxRules = 20000
	rules := make([]string, 0, 256)
	truncated := false
	for _, rs := range selected {
		for _, r := range rs.Rules {
			if len(rules) >= maxRules {
				truncated = true
				break
			}
			typ := strings.ToUpper(strings.TrimSpace(r.Type))
			val := strings.TrimSpace(r.Value)
			if typ == "" || val == "" {
				continue
			}
			if strings.EqualFold(typ, "MATCH") || strings.EqualFold(typ, "FINAL") {
				continue
			}
			target := strings.ToUpper(strings.TrimSpace(r.Action))
			switch target {
			case "", "PROXY", "NGG", "SELECT":
				target = groupName
			case "DIRECT":
				target = "DIRECT"
			case "REJECT", "BLOCK":
				target = "REJECT"
			default:
				target = groupName
			}
			rules = append(rules, fmt.Sprintf("%s,%s,%s", typ, val, target))
		}
		if truncated {
			break
		}
	}
	if truncated {
		warnings = append(warnings, fmt.Sprintf("规则总数超过 %d 条，已截断（其余规则未写入内核配置）", maxRules))
	}
	rules = append(rules, "MATCH,"+groupName)
	return rules
}

// --------------------------------------------------------------- share links ---

// proxyFor builds a mihomo proxy entry from a host. A full share link in
// proxy_url wins (it carries REALITY / obfs parameters that the flat fields
// cannot express); otherwise the flat fields are used.
func proxyFor(h hosts.Host) (map[string]any, error) {
	if link := strings.TrimSpace(h.ProxyURL); link != "" {
		if p, err := ParseShare(link); err == nil {
			return p, nil
		}
	}
	server := strings.TrimSpace(h.Address)
	if server == "" {
		return nil, errors.New("地址为空")
	}
	proto := normalizeProtocol(h.Protocol)
	if proto == "" {
		return nil, fmt.Errorf("协议 %q 无法被 mihomo 直接渲染", h.Protocol)
	}
	port := svcPort(h)
	if port <= 0 {
		return nil, errors.New("端口为空")
	}
	p := map[string]any{
		"name":   h.ID,
		"type":   proto,
		"server": server,
		"port":   port,
		"udp":    true,
	}
	secret := strings.TrimSpace(h.SecretKey)
	switch proto {
	case "vless", "vmess":
		if secret == "" {
			return nil, errors.New("缺少 UUID（Secret Key），可在「出口节点」填写或改用分享链接")
		}
		p["uuid"] = secret
		if proto == "vmess" {
			p["alterId"] = 0
			p["cipher"] = "auto"
		} else {
			p["network"] = "tcp"
			p["tls"] = true
			p["servername"] = server
			p["client-fingerprint"] = "chrome"
		}
	case "trojan":
		if secret == "" {
			return nil, errors.New("缺少密码（Secret Key）")
		}
		p["password"] = secret
		p["sni"] = server
	case "hysteria2", "hy2":
		p["type"] = "hysteria2"
		if secret == "" {
			return nil, errors.New("缺少密码（Secret Key）")
		}
		p["password"] = secret
		p["sni"] = server
	case "ss":
		if secret == "" {
			return nil, errors.New("缺少密码（Secret Key）")
		}
		p["cipher"] = "aes-128-gcm"
		p["password"] = secret
	case "socks5", "http":
		if secret != "" {
			parts := strings.SplitN(secret, ":", 2)
			p["username"] = parts[0]
			if len(parts) == 2 {
				p["password"] = parts[1]
			}
		}
	default:
		return nil, fmt.Errorf("协议 %q 暂不支持", h.Protocol)
	}
	return p, nil
}

func normalizeProtocol(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, "_", "-")
	switch {
	case v == "":
		return ""
	case strings.HasPrefix(v, "vless"):
		return "vless"
	case strings.HasPrefix(v, "vmess"):
		return "vmess"
	case strings.HasPrefix(v, "trojan"):
		return "trojan"
	case strings.HasPrefix(v, "hysteria2"), strings.HasPrefix(v, "hy2"):
		return "hysteria2"
	case strings.HasPrefix(v, "ss"), strings.HasPrefix(v, "shadowsocks"):
		return "ss"
	case strings.HasPrefix(v, "socks"):
		return "socks5"
	case strings.HasPrefix(v, "http"):
		return "http"
	}
	return ""
}

// ParseShare converts a standard share link into a mihomo proxy map. It covers
// the schemes Remnawave-style panels hand out; unknown schemes are rejected so
// the operator sees an explicit warning instead of a silently broken node.
func ParseShare(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	q := u.Query()
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	p := map[string]any{"name": name, "udp": true}

	switch scheme {
	case "vless":
		if u.User == nil || u.User.Username() == "" {
			return nil, errors.New("vless 链接缺少 UUID")
		}
		p["type"] = "vless"
		p["server"] = u.Hostname()
		p["port"] = portOf(u)
		p["uuid"] = u.User.Username()
		p["network"] = def(q.Get("type"), "tcp")
		security := strings.ToLower(q.Get("security"))
		if security != "" && security != "none" {
			p["tls"] = true
			if sni := q.Get("sni"); sni != "" {
				p["servername"] = sni
			} else if host := q.Get("host"); host != "" {
				p["servername"] = host
			}
			if fp := q.Get("fp"); fp != "" {
				p["client-fingerprint"] = fp
			}
			if security == "reality" {
				ro := map[string]any{}
				if pbk := q.Get("pbk"); pbk != "" {
					ro["public-key"] = pbk
				} else {
					return nil, errors.New("REALITY 链接缺少 public-key（pbk）")
				}
				if sid := q.Get("sid"); sid != "" {
					ro["short-id"] = sid
				}
				p["reality-opts"] = ro
			}
		}
		if flow := q.Get("flow"); flow != "" {
			p["flow"] = flow
		}
		if ws := q.Get("path"); ws != "" && strings.EqualFold(q.Get("type"), "ws") {
			p["ws-opts"] = map[string]any{"path": ws, "headers": map[string]string{"Host": def(q.Get("host"), u.Hostname())}}
		}
	case "vmess":
		// vmess://base64(json)
		raw := strings.TrimPrefix(link, "vmess://")
		if i := strings.Index(raw, "#"); i >= 0 {
			raw = raw[:i]
		}
		b, err := b64(raw)
		if err != nil {
			return nil, fmt.Errorf("vmess 链接无法解码: %w", err)
		}
		var v struct {
			PS   string `json:"ps"`
			Add  string `json:"add"`
			Port any    `json:"port"`
			ID   string `json:"id"`
			Net  string `json:"net"`
			TLS  string `json:"tls"`
			SNI  string `json:"sni"`
			Host string `json:"host"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("vmess JSON 解析失败: %w", err)
		}
		p["type"] = "vmess"
		p["server"] = v.Add
		p["port"] = toInt(v.Port)
		p["uuid"] = v.ID
		p["alterId"] = 0
		p["cipher"] = "auto"
		if v.PS != "" {
			p["name"] = v.PS
		}
		if strings.EqualFold(v.Net, "ws") {
			p["network"] = "ws"
			p["ws-opts"] = map[string]any{"path": def(v.Path, "/"), "headers": map[string]string{"Host": def(v.Host, v.Add)}}
		}
		if strings.EqualFold(v.TLS, "tls") {
			p["tls"] = true
			if v.SNI != "" {
				p["servername"] = v.SNI
			}
		}
	case "trojan":
		if u.User == nil {
			return nil, errors.New("trojan 链接缺少密码")
		}
		p["type"] = "trojan"
		p["server"] = u.Hostname()
		p["port"] = portOf(u)
		p["password"] = u.User.Username()
		if sni := q.Get("sni"); sni != "" {
			p["sni"] = sni
		}
		if strings.EqualFold(q.Get("allowInsecure"), "1") || strings.EqualFold(q.Get("insecure"), "1") {
			p["skip-cert-verify"] = true
		}
	case "hysteria2", "hy2":
		if u.User == nil {
			return nil, errors.New("hysteria2 链接缺少密码")
		}
		p["type"] = "hysteria2"
		p["server"] = u.Hostname()
		p["port"] = portOf(u)
		p["password"] = u.User.Username()
		if sni := q.Get("sni"); sni != "" {
			p["sni"] = sni
		}
		if strings.EqualFold(q.Get("insecure"), "1") {
			p["skip-cert-verify"] = true
		}
	case "ss":
		raw := strings.TrimPrefix(link, "ss://")
		if i := strings.Index(raw, "#"); i >= 0 {
			raw = raw[:i]
		}
		// Two shapes: base64(method:pass)@host:port  and  base64(method:pass@host:port)
		var userinfo, hostport string
		if at := strings.LastIndex(raw, "@"); at >= 0 {
			userinfo, hostport = raw[:at], raw[at+1:]
			if dec, err := b64(userinfo); err == nil {
				userinfo = string(dec)
			}
		} else {
			dec, err := b64(raw)
			if err != nil {
				return nil, fmt.Errorf("ss 链接无法解码: %w", err)
			}
			s := string(dec)
			at := strings.LastIndex(s, "@")
			if at < 0 {
				return nil, errors.New("ss 链接格式不正确")
			}
			userinfo, hostport = s[:at], s[at+1:]
		}
		parts := strings.SplitN(userinfo, ":", 2)
		if len(parts) != 2 {
			return nil, errors.New("ss 链接缺少 method:password")
		}
		hp := strings.SplitN(hostport, "?", 2)[0]
		host, portStr, err := net.SplitHostPort(hp)
		if err != nil {
			return nil, fmt.Errorf("ss 地址解析失败: %w", err)
		}
		p["type"] = "ss"
		p["server"] = host
		p["port"] = toInt(portStr)
		p["cipher"] = parts[0]
		p["password"] = parts[1]
	default:
		return nil, fmt.Errorf("不支持的分享链接协议 %q", scheme)
	}

	if p["server"] == nil || p["server"] == "" {
		return nil, errors.New("分享链接缺少服务器地址")
	}
	if toInt(p["port"]) <= 0 {
		return nil, errors.New("分享链接缺少端口")
	}
	return p, nil
}

func def(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func portOf(u *url.URL) int {
	if p := u.Port(); p != "" {
		return toInt(p)
	}
	return 0
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	}
	return 0
}

func b64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// ------------------------------------------------------------------- testing ---

// SocksDial performs a REAL SOCKS5 CONNECT through addr and returns how long
// the tunnel took to establish. It is the evidence the UI shows after 应用.
func SocksDial(addr, target string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return 0, fmt.Errorf("连接入口 %s 失败: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return 0, err
	}
	resp := make([]byte, 2)
	if _, err := conn.Read(resp); err != nil {
		return 0, fmt.Errorf("SOCKS5 握手失败: %w", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		return 0, fmt.Errorf("SOCKS5 服务返回错误: %v", resp)
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return 0, fmt.Errorf("测试目标 %q 需要 host:port: %w", target, err)
	}
	port, _ := strconv.Atoi(portStr)
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("测试目标端口不合法: %q", portStr)
	}

	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		req = append(req, 0x01)
		req = append(req, ip.To4()...)
	} else if ip != nil {
		req = append(req, 0x04)
		req = append(req, ip.To16()...)
	} else {
		if len(host) > 255 {
			return 0, errors.New("目标域名过长")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}

	head := make([]byte, 4)
	if _, err := conn.Read(head); err != nil {
		return 0, fmt.Errorf("等待 CONNECT 应答失败: %w", err)
	}
	if head[1] != 0x00 {
		return 0, fmt.Errorf("出口拒绝连接（SOCKS5 应答码 0x%02x）", head[1])
	}
	switch head[3] {
	case 0x01:
		_, _ = conn.Read(make([]byte, 4+2))
	case 0x04:
		_, _ = conn.Read(make([]byte, 16+2))
	case 0x03:
		l := make([]byte, 1)
		if _, err := conn.Read(l); err == nil {
			_, _ = conn.Read(make([]byte, int(l[0])+2))
		}
	}
	return time.Since(start), nil
}
