package gateway

import (
	"fmt"
	"net"
	"strings"
	"unicode/utf8"
)

// 网关总控（虚拟网卡 + 智能分流）的声明式模型。
//
// This is still declarative configuration: NGG records the operator's intent
// and reports what the host really looks like (see datapath.go). It never
// claims the data path is taken over unless Apply really ran and the default
// route really goes through the adapter.

// Split modes and defaults. Kept as maps so normalisation and validation share
// one definition.
var (
	splitModes    = map[string]bool{"smart": true, "global-proxy": true, "direct": true}
	splitDefaults = map[string]bool{"local": true, "proxy": true}
	uplinkModes   = map[string]bool{"gateway-wan": true, "pipeline": true}
)

// SplitPolicy is the "smart split" policy: which domains / CIDRs go through
// the proxy and which stay local. Mode decides which lists are used at all.
type SplitPolicy struct {
	Mode         string   `json:"mode"`
	Default      string   `json:"default"`
	ProxyDomains []string `json:"proxy_domains"`
	LocalDomains []string `json:"local_domains"`
	ProxyCIDRs   []string `json:"proxy_cidrs"`
	LocalCIDRs   []string `json:"local_cidrs"`
}

// UplinkPolicy selects where the captured traffic leaves: a physical WAN port
// or a pipeline (link orchestration) chain.
type UplinkPolicy struct {
	Mode       string `json:"mode"`
	WANID      string `json:"wan_id"`
	PipelineID string `json:"pipeline_id"`
}

// Master is the v2.7 gateway master switch (contract "GatewayMaster").
type Master struct {
	Enabled       bool         `json:"enabled"`
	AdapterName   string       `json:"adapter_name"`
	AdapterMetric int          `json:"adapter_metric"`
	CaptureAll    bool         `json:"capture_all"`
	AutoStart     bool         `json:"auto_start"`
	BypassPrivate bool         `json:"bypass_private"`
	BypassCIDRs   []string     `json:"bypass_cidrs"`
	Split         SplitPolicy  `json:"split"`
	Uplink        UplinkPolicy `json:"uplink"`
}

// Split entry kinds used when handing the policy to the rule engine.
const (
	SplitDomain = "domain"
	SplitCIDR   = "cidr"
)

// Split actions, matching the split list a value came from.
const (
	SplitProxy  = "proxy"
	SplitDirect = "direct"
)

// SplitEntry is one flattened split decision: a domain suffix or a CIDR that
// must be routed one way.
type SplitEntry struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Action string `json:"action"`
}

// DefaultMaster returns the factory master switch described in the contract.
func DefaultMaster() Master {
	return Master{
		Enabled:       false,
		AdapterName:   "NGG",
		AdapterMetric: 0,
		CaptureAll:    true,
		AutoStart:     false,
		BypassPrivate: true,
		BypassCIDRs:   []string{},
		Split: SplitPolicy{
			Mode:         "smart",
			Default:      "local",
			ProxyDomains: []string{},
			LocalDomains: []string{},
			ProxyCIDRs:   []string{},
			LocalCIDRs:   []string{},
		},
		Uplink: UplinkPolicy{Mode: "gateway-wan", WANID: "wan1"},
	}
}

// normalizeMaster trims the text fields and fills the defaults that carry no
// risk of overriding an explicit choice: an empty adapter name or enum is
// always a side effect of a missing value, never an intentional one. The
// booleans are left untouched — with an explicit "master" object in the
// document, false is a real answer.
func normalizeMaster(m Master, d Master) Master {
	m.AdapterName = strings.TrimSpace(m.AdapterName)
	if m.AdapterName == "" {
		m.AdapterName = d.AdapterName
	}

	if m.BypassCIDRs == nil {
		m.BypassCIDRs = []string{}
	}
	m.BypassCIDRs = dedupeStrings(m.BypassCIDRs)

	m.Split.Mode = strings.ToLower(strings.TrimSpace(m.Split.Mode))
	if m.Split.Mode == "" {
		m.Split.Mode = d.Split.Mode
	}
	m.Split.Default = strings.ToLower(strings.TrimSpace(m.Split.Default))
	if m.Split.Default == "" {
		m.Split.Default = d.Split.Default
	}
	m.Split.ProxyDomains = normalizeDomains(m.Split.ProxyDomains)
	m.Split.LocalDomains = normalizeDomains(m.Split.LocalDomains)
	if m.Split.ProxyCIDRs == nil {
		m.Split.ProxyCIDRs = []string{}
	}
	if m.Split.LocalCIDRs == nil {
		m.Split.LocalCIDRs = []string{}
	}
	m.Split.ProxyCIDRs = dedupeStrings(m.Split.ProxyCIDRs)
	m.Split.LocalCIDRs = dedupeStrings(m.Split.LocalCIDRs)

	m.Uplink.Mode = strings.ToLower(strings.TrimSpace(m.Uplink.Mode))
	if m.Uplink.Mode == "" {
		m.Uplink.Mode = d.Uplink.Mode
	}
	m.Uplink.WANID = strings.ToLower(strings.TrimSpace(m.Uplink.WANID))
	if m.Uplink.WANID == "" && m.Uplink.Mode == "gateway-wan" {
		m.Uplink.WANID = d.Uplink.WANID
	}
	m.Uplink.PipelineID = strings.ToLower(strings.TrimSpace(m.Uplink.PipelineID))
	return m
}

// validateMaster enforces the contract rules for the master switch. adapter
// names are also used to build shell commands, so the characters that could
// ever change the meaning of a command line are rejected here — networking
// stacks legitimately use spaces and non-ASCII names, so those stay allowed.
func validateMaster(m Master) error {
	if err := validateAdapterName(m.AdapterName); err != nil {
		return err
	}
	if m.AdapterMetric < 0 || m.AdapterMetric > 9999 {
		return invalid("master.adapter_metric", "必须在 0-9999 之间，收到 %d", m.AdapterMetric)
	}
	if !splitModes[m.Split.Mode] {
		return invalid("master.split.mode", "只能是 smart / global-proxy / direct，收到 %q", m.Split.Mode)
	}
	if !splitDefaults[m.Split.Default] {
		return invalid("master.split.default", "只能是 local / proxy，收到 %q", m.Split.Default)
	}
	for _, list := range []struct {
		field string
		items []string
	}{
		{"master.split.proxy_domains", m.Split.ProxyDomains},
		{"master.split.local_domains", m.Split.LocalDomains},
	} {
		for _, d := range list.items {
			if err := checkDomainSpec(d); err != nil {
				return invalid(list.field, "%v", err)
			}
		}
	}
	for _, list := range []struct {
		field string
		items []string
	}{
		{"master.split.proxy_cidrs", m.Split.ProxyCIDRs},
		{"master.split.local_cidrs", m.Split.LocalCIDRs},
		{"master.bypass_cidrs", m.BypassCIDRs},
	} {
		for _, c := range list.items {
			if _, _, err := net.ParseCIDR(c); err != nil {
				return invalid(list.field, "%q 不是合法 CIDR", c)
			}
		}
	}
	if !uplinkModes[m.Uplink.Mode] {
		return invalid("master.uplink.mode", "只能是 gateway-wan / pipeline，收到 %q", m.Uplink.Mode)
	}
	if m.Uplink.WANID != "" && !wanIDPattern.MatchString(m.Uplink.WANID) {
		return invalid("master.uplink.wan_id", "只能是 wan1..wan6，收到 %q", m.Uplink.WANID)
	}
	if err := checkPipelineID("master.uplink.pipeline_id", m.Uplink.PipelineID); err != nil {
		return err
	}
	return nil
}

// validateAdapterName rejects the values that would make an interface command
// ambiguous (quotes, path separators, shell metacharacters) or meaningless
// (empty / over-long).
func validateAdapterName(name string) error {
	if name == "" {
		return invalid("master.adapter_name", "不能为空")
	}
	if utf8.RuneCountInString(name) > 64 {
		return invalid("master.adapter_name", "长度不能超过 64 个字符")
	}
	if strings.ContainsAny(name, "\"'`\\/|&<>;%$*?") {
		return invalid("master.adapter_name", "不能包含引号、路径分隔符或 shell 特殊字符：%q", name)
	}
	for _, r := range name {
		if r < 0x20 {
			return invalid("master.adapter_name", "不能包含控制字符")
		}
	}
	return nil
}

// checkDomainSpec accepts a domain or a "*.domain" wildcard. The wildcard is
// only meaningful as a prefix: anything else is a typo, not a pattern.
func checkDomainSpec(s string) error {
	if s == "" {
		return fmt.Errorf("不能为空")
	}
	if strings.Contains(s, "*") && !strings.HasPrefix(s, "*.") {
		return fmt.Errorf("%q 通配符只允许出现在最前面的 *. 前缀", s)
	}
	base := BareDomain(s)
	if !isDomainName(base) {
		return fmt.Errorf("%q 不是合法域名", s)
	}
	return nil
}

// checkPipelineID accepts an empty id (default chain) or a slug-shaped id.
// "pl-*" is the documented shape; the slug rule is deliberately wider so the
// built-in "default" pipeline can be referenced without a 422.
func checkPipelineID(field, id string) error {
	if id == "" {
		return nil
	}
	if len(id) > 64 {
		return invalid(field, "长度不能超过 64 个字符")
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return invalid(field, "只能由小写字母、数字、- _ . 组成，收到 %q", id)
		}
		if i == 0 && (r == '-' || r == '_' || r == '.') {
			return invalid(field, "不能以 %q 开头", string(r))
		}
	}
	return nil
}

// BareDomain strips the "*.", which DOMAIN-SUFFIX already covers.
func BareDomain(s string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))), "*.")
}

// normalizeDomains lower-cases, trims and de-duplicates a domain list.
func normalizeDomains(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		d := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ".")))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// SplitEntries flattens the split policy into the rule list that belongs in
// the rule engine. The mode decides which lists apply at all:
//   - smart:        both sides
//   - global-proxy: only the proxy lists (everything else is proxied too)
//   - direct:       only the local lists (everything else goes direct)
func (m Master) SplitEntries() []SplitEntry {
	entries := make([]SplitEntry, 0, len(m.Split.ProxyDomains)+len(m.Split.LocalDomains)+
		len(m.Split.ProxyCIDRs)+len(m.Split.LocalCIDRs))

	add := func(kind, action string, values []string) {
		for _, v := range values {
			if kind == SplitDomain {
				v = BareDomain(v)
			}
			if v == "" {
				continue
			}
			entries = append(entries, SplitEntry{Kind: kind, Value: v, Action: action})
		}
	}

	proxySide := func() {
		add(SplitDomain, SplitProxy, m.Split.ProxyDomains)
		add(SplitCIDR, SplitProxy, m.Split.ProxyCIDRs)
	}
	localSide := func() {
		add(SplitDomain, SplitDirect, m.Split.LocalDomains)
		add(SplitCIDR, SplitDirect, m.Split.LocalCIDRs)
	}

	switch m.Split.Mode {
	case "global-proxy":
		proxySide()
	case "direct":
		localSide()
	default: // smart
		proxySide()
		localSide()
	}
	return entries
}

// DefaultSplitAction maps the policy onto the rule engine's default action:
// the mode wins, and smart mode falls back to the explicit default field.
func (m Master) DefaultSplitAction() string {
	switch m.Split.Mode {
	case "global-proxy":
		return "proxy"
	case "direct":
		return "local"
	}
	return m.Split.Default
}
