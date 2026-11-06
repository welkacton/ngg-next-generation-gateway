// Package gateway owns the declarative WAN / LAN / firewall configuration of
// the NGG gateway console and a real read-out of the host's network state.
//
// The configuration here is DECLARATIVE: NGG validates and persists the
// desired state in data/gateway-config.json (mode 0600, atomic replace). It
// does not re-address the host's NICs, start a DHCP server or program
// nftables — that would need root and a per-platform backend that does not
// exist yet, so the API never pretends a save has been applied.
//
// Everything Status() reports is read from the live host with the standard
// library (net.Interfaces, /proc/net/route); nothing is synthesised. Fields
// that cannot be read on the current platform are returned empty rather than
// guessed.
package gateway

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Supported enum values. Kept as maps so validation and normalisation share
// one definition.
var (
	wanModes        = map[string]bool{"dhcp": true, "static": true, "pppoe": true}
	ruleActions     = map[string]bool{"accept": true, "drop": true, "reject": true}
	ruleProtocols   = map[string]bool{"tcp": true, "udp": true, "any": true}
	forwardProtos   = map[string]bool{"tcp": true, "udp": true, "both": true}
	inboundDefaults = map[string]bool{"accept": true, "drop": true}
)

// WANConfig is the uplink. Password is write-only on the API surface: GET
// returns PasswordSet instead of the secret, and a PUT with an empty password
// keeps the stored one.
type WANConfig struct {
	Mode        string   `json:"mode"`
	Interface   string   `json:"interface"`
	Address     string   `json:"address"`
	Gateway     string   `json:"gateway"`
	MTU         int      `json:"mtu"`
	DNS         []string `json:"dns"`
	IPv6        bool     `json:"ipv6"`
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	PasswordSet bool     `json:"password_set"`
}

// LANConfig is the downlink / DHCP scope.
type LANConfig struct {
	Interface    string `json:"interface"`
	Address      string `json:"address"`
	Netmask      string `json:"netmask"`
	DHCPEnabled  bool   `json:"dhcp_enabled"`
	DHCPStart    string `json:"dhcp_start"`
	DHCPEnd      string `json:"dhcp_end"`
	LeaseMinutes int    `json:"lease_minutes"`
	Domain       string `json:"domain"`
}

// FirewallRule is one inbound rule.
type FirewallRule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	Port     string `json:"port"`
	Enabled  bool   `json:"enabled"`
}

// PortForward is one DNAT entry.
type PortForward struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	ExtPort  int    `json:"ext_port"`
	IntAddr  string `json:"int_addr"`
	IntPort  int    `json:"int_port"`
	Enabled  bool   `json:"enabled"`
}

// FirewallConfig is the declarative firewall policy.
type FirewallConfig struct {
	DefaultInbound string         `json:"default_inbound"`
	Rules          []FirewallRule `json:"rules"`
	PortForwards   []PortForward  `json:"port_forwards"`
	UPnP           bool           `json:"upnp"`
	Conntrack      bool           `json:"conntrack"`
}

// Config is the whole gateway document.
type Config struct {
	WAN      WANConfig      `json:"wan"`
	LAN      LANConfig      `json:"lan"`
	Firewall FirewallConfig `json:"firewall"`
}

// Default returns the configuration a fresh installation starts from.
func Default() Config {
	return Config{
		WAN: WANConfig{
			Mode: "dhcp",
			MTU:  1500,
			IPv6: true,
			DNS:  []string{"223.5.5.5", "119.29.29.29"},
		},
		LAN: LANConfig{
			Address:      "192.168.10.1",
			Netmask:      "255.255.255.0",
			DHCPEnabled:  true,
			DHCPStart:    "192.168.10.100",
			DHCPEnd:      "192.168.10.200",
			LeaseMinutes: 1440,
			Domain:       "lan",
		},
		Firewall: FirewallConfig{
			DefaultInbound: "drop",
			Rules:          []FirewallRule{},
			PortForwards:   []PortForward{},
			UPnP:           false,
			Conntrack:      true,
		},
	}
}

// Public returns a copy safe to hand to the UI: the WAN password is replaced
// by its presence flag so the secret never leaves the process.
func (c Config) Public() Config {
	out := clone(c)
	out.WAN.PasswordSet = c.WAN.Password != ""
	out.WAN.Password = ""
	return out
}

func clone(c Config) Config {
	out := c
	out.WAN.DNS = append([]string{}, c.WAN.DNS...)
	out.Firewall.Rules = append([]FirewallRule{}, c.Firewall.Rules...)
	out.Firewall.PortForwards = append([]PortForward{}, c.Firewall.PortForwards...)
	return out
}

// normalize trims and lower-cases enum-ish fields and fills the defaults that
// are unambiguous to fill (a zero MTU or lease time is never intentional).
// Empty-but-explicit values (an empty DNS list, conntrack=false) are kept.
func normalize(c Config) Config {
	d := Default()

	c.WAN.Mode = strings.ToLower(strings.TrimSpace(c.WAN.Mode))
	if c.WAN.Mode == "" {
		c.WAN.Mode = d.WAN.Mode
	}
	c.WAN.Interface = strings.TrimSpace(c.WAN.Interface)
	c.WAN.Address = strings.TrimSpace(c.WAN.Address)
	c.WAN.Gateway = strings.TrimSpace(c.WAN.Gateway)
	c.WAN.Username = strings.TrimSpace(c.WAN.Username)
	if c.WAN.MTU == 0 {
		c.WAN.MTU = d.WAN.MTU
	}
	if c.WAN.DNS == nil {
		c.WAN.DNS = append([]string{}, d.WAN.DNS...)
	}
	dns := make([]string, 0, len(c.WAN.DNS))
	seen := map[string]bool{}
	for _, s := range c.WAN.DNS {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		dns = append(dns, s)
	}
	c.WAN.DNS = dns
	c.WAN.PasswordSet = c.WAN.Password != ""

	c.LAN.Interface = strings.TrimSpace(c.LAN.Interface)
	c.LAN.Address = strings.TrimSpace(c.LAN.Address)
	c.LAN.Netmask = strings.TrimSpace(c.LAN.Netmask)
	c.LAN.DHCPStart = strings.TrimSpace(c.LAN.DHCPStart)
	c.LAN.DHCPEnd = strings.TrimSpace(c.LAN.DHCPEnd)
	c.LAN.Domain = strings.TrimSpace(c.LAN.Domain)
	if c.LAN.Address == "" {
		c.LAN.Address = d.LAN.Address
	}
	if c.LAN.Netmask == "" {
		c.LAN.Netmask = d.LAN.Netmask
	}
	if c.LAN.LeaseMinutes == 0 {
		c.LAN.LeaseMinutes = d.LAN.LeaseMinutes
	}
	if c.LAN.Domain == "" {
		c.LAN.Domain = d.LAN.Domain
	}
	if c.LAN.DHCPEnabled && c.LAN.DHCPStart == "" && c.LAN.DHCPEnd == "" {
		c.LAN.DHCPStart = d.LAN.DHCPStart
		c.LAN.DHCPEnd = d.LAN.DHCPEnd
	}

	c.Firewall.DefaultInbound = strings.ToLower(strings.TrimSpace(c.Firewall.DefaultInbound))
	if c.Firewall.DefaultInbound == "" {
		c.Firewall.DefaultInbound = d.Firewall.DefaultInbound
	}
	rules := make([]FirewallRule, 0, len(c.Firewall.Rules))
	for _, r := range c.Firewall.Rules {
		r.ID = strings.TrimSpace(r.ID)
		r.Name = strings.TrimSpace(r.Name)
		r.Action = strings.ToLower(strings.TrimSpace(r.Action))
		r.Protocol = strings.ToLower(strings.TrimSpace(r.Protocol))
		r.Src = strings.TrimSpace(r.Src)
		r.Dst = strings.TrimSpace(r.Dst)
		r.Port = strings.TrimSpace(r.Port)
		if r.Protocol == "" {
			r.Protocol = "any"
		}
		if r.Action == "" {
			r.Action = "accept"
		}
		rules = append(rules, r)
	}
	c.Firewall.Rules = rules
	forwards := make([]PortForward, 0, len(c.Firewall.PortForwards))
	for _, f := range c.Firewall.PortForwards {
		f.ID = strings.TrimSpace(f.ID)
		f.Name = strings.TrimSpace(f.Name)
		f.Protocol = strings.ToLower(strings.TrimSpace(f.Protocol))
		f.IntAddr = strings.TrimSpace(f.IntAddr)
		if f.Protocol == "" {
			f.Protocol = "tcp"
		}
		forwards = append(forwards, f)
	}
	c.Firewall.PortForwards = forwards
	return c
}

// ValidationError marks a payload that must be rejected with 422 and must not
// be persisted. The API layer distinguishes it from I/O failures this way.
type ValidationError struct {
	Field  string
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return "网关配置不合法: " + e.Detail
	}
	return "网关配置不合法: " + e.Field + " " + e.Detail
}

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Detail: fmt.Sprintf(format, args...)}
}

// validate enforces the documented contract. Callers pass an already
// normalised config so enum case never causes a false rejection.
func validate(c Config) error {
	if !wanModes[c.WAN.Mode] {
		return invalid("wan.mode", "只能是 dhcp / static / pppoe，收到 %q", c.WAN.Mode)
	}
	if c.WAN.MTU < 576 || c.WAN.MTU > 9000 {
		return invalid("wan.mtu", "必须在 576-9000 之间，收到 %d", c.WAN.MTU)
	}
	if c.WAN.Address != "" && !isIPv4(c.WAN.Address) {
		return invalid("wan.address", "%q 不是合法 IPv4 地址", c.WAN.Address)
	}
	if c.WAN.Gateway != "" && !isIPv4(c.WAN.Gateway) {
		return invalid("wan.gateway", "%q 不是合法 IPv4 地址", c.WAN.Gateway)
	}
	for _, d := range c.WAN.DNS {
		if net.ParseIP(d) == nil {
			return invalid("wan.dns", "%q 不是合法 IP 地址", d)
		}
	}
	switch c.WAN.Mode {
	case "static":
		if c.WAN.Address == "" {
			return invalid("wan.address", "静态模式下必填")
		}
		if c.WAN.Gateway == "" {
			return invalid("wan.gateway", "静态模式下必填")
		}
	case "pppoe":
		if c.WAN.Username == "" {
			return invalid("wan.username", "PPPoE 模式下必填")
		}
		if c.WAN.Password == "" {
			return invalid("wan.password", "PPPoE 模式下必填（已保存的密码留空即沿用）")
		}
	}
	if c.WAN.Interface != "" && utf8.RuneCountInString(c.WAN.Interface) > 64 {
		return invalid("wan.interface", "长度不能超过 64 个字符")
	}

	if err := validateLAN(c.LAN); err != nil {
		return err
	}

	if !inboundDefaults[c.Firewall.DefaultInbound] {
		return invalid("firewall.default_inbound", "只能是 accept / drop，收到 %q", c.Firewall.DefaultInbound)
	}
	seenRule := map[string]bool{}
	for i, r := range c.Firewall.Rules {
		field := fmt.Sprintf("firewall.rules[%d]", i)
		if r.ID == "" {
			return invalid(field+".id", "不能为空")
		}
		if seenRule[r.ID] {
			return invalid(field+".id", "重复的规则 id %q", r.ID)
		}
		seenRule[r.ID] = true
		if r.Name == "" {
			return invalid(field+".name", "不能为空")
		}
		if utf8.RuneCountInString(r.Name) > 64 {
			return invalid(field+".name", "长度不能超过 64 个字符")
		}
		if !ruleActions[r.Action] {
			return invalid(field+".action", "只能是 accept / drop / reject，收到 %q", r.Action)
		}
		if !ruleProtocols[r.Protocol] {
			return invalid(field+".protocol", "只能是 tcp / udp / any，收到 %q", r.Protocol)
		}
		if err := checkHostSpec(r.Src); err != nil {
			return invalid(field+".src", "%v", err)
		}
		if err := checkHostSpec(r.Dst); err != nil {
			return invalid(field+".dst", "%v", err)
		}
		if err := checkPortSpec(r.Port); err != nil {
			return invalid(field+".port", "%v", err)
		}
	}
	seenFwd := map[string]bool{}
	for i, f := range c.Firewall.PortForwards {
		field := fmt.Sprintf("firewall.port_forwards[%d]", i)
		if f.ID == "" {
			return invalid(field+".id", "不能为空")
		}
		if seenFwd[f.ID] {
			return invalid(field+".id", "重复的转发 id %q", f.ID)
		}
		seenFwd[f.ID] = true
		if f.Name == "" {
			return invalid(field+".name", "不能为空")
		}
		if utf8.RuneCountInString(f.Name) > 64 {
			return invalid(field+".name", "长度不能超过 64 个字符")
		}
		if !forwardProtos[f.Protocol] {
			return invalid(field+".protocol", "只能是 tcp / udp / both，收到 %q", f.Protocol)
		}
		if f.ExtPort < 1 || f.ExtPort > 65535 {
			return invalid(field+".ext_port", "必须在 1-65535 之间，收到 %d", f.ExtPort)
		}
		if f.IntPort < 1 || f.IntPort > 65535 {
			return invalid(field+".int_port", "必须在 1-65535 之间，收到 %d", f.IntPort)
		}
		if !isIPv4(f.IntAddr) {
			return invalid(field+".int_addr", "%q 不是合法 IPv4 地址", f.IntAddr)
		}
	}
	return nil
}

func validateLAN(l LANConfig) error {
	ip := net.ParseIP(l.Address)
	if ip == nil || ip.To4() == nil {
		return invalid("lan.address", "%q 不是合法 IPv4 地址", l.Address)
	}
	lanIP := ip.To4()
	mask, ok := parseNetmask(l.Netmask)
	if !ok {
		return invalid("lan.netmask", "%q 不是合法的连续子网掩码", l.Netmask)
	}
	if l.Interface != "" && utf8.RuneCountInString(l.Interface) > 64 {
		return invalid("lan.interface", "长度不能超过 64 个字符")
	}
	if l.LeaseMinutes < 1 || l.LeaseMinutes > 10080 {
		return invalid("lan.lease_minutes", "必须在 1-10080 之间（最长 7 天），收到 %d", l.LeaseMinutes)
	}
	if !l.DHCPEnabled {
		return nil
	}
	start := net.ParseIP(l.DHCPStart)
	end := net.ParseIP(l.DHCPEnd)
	if start == nil || start.To4() == nil {
		return invalid("lan.dhcp_start", "%q 不是合法 IPv4 地址", l.DHCPStart)
	}
	if end == nil || end.To4() == nil {
		return invalid("lan.dhcp_end", "%q 不是合法 IPv4 地址", l.DHCPEnd)
	}
	s4, e4 := start.To4(), end.To4()
	netAddr := lanIP.Mask(mask)
	if !s4.Mask(mask).Equal(netAddr) {
		return invalid("lan.dhcp_start", "必须与 LAN 地址 %s/%s 在同一网段", l.Address, l.Netmask)
	}
	if !e4.Mask(mask).Equal(netAddr) {
		return invalid("lan.dhcp_end", "必须与 LAN 地址 %s/%s 在同一网段", l.Address, l.Netmask)
	}
	if bytesCompare(s4, e4) >= 0 {
		return invalid("lan.dhcp_start", "必须小于 dhcp_end（%s >= %s）", l.DHCPStart, l.DHCPEnd)
	}
	if s4.Equal(netAddr) {
		return invalid("lan.dhcp_start", "不能是网段网络地址 %s", netAddr.String())
	}
	broadcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		broadcast[i] = netAddr[i] | ^mask[i]
	}
	if e4.Equal(broadcast) {
		return invalid("lan.dhcp_end", "不能是广播地址 %s", broadcast.String())
	}
	return nil
}

func bytesCompare(a, b net.IP) int {
	for i := 0; i < 4; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func isIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() != nil
}

// parseNetmask accepts dotted-quad masks and only contiguous ones.
func parseNetmask(s string) (net.IPMask, bool) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return nil, false
	}
	m := net.IPMask(ip.To4())
	ones, bits := m.Size()
	if bits != 32 || ones < 0 {
		return nil, false
	}
	return m, true
}

// checkHostSpec accepts an empty match-all, "any"/"*", an IP or a CIDR.
func checkHostSpec(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" || strings.EqualFold(s, "any") {
		return nil
	}
	if net.ParseIP(s) != nil {
		return nil
	}
	if _, _, err := net.ParseCIDR(s); err == nil {
		return nil
	}
	return fmt.Errorf("%q 既不是 IP 也不是 CIDR", s)
}

// checkPortSpec accepts empty/any/"*", a single port, a range a-b, or a
// comma separated list of those. Every port must be 1..65535.
func checkPortSpec(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" || strings.EqualFold(s, "any") {
		return nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("端口列表中包含空项")
		}
		if i := strings.Index(part, "-"); i > 0 {
			lo, err1 := strconv.Atoi(strings.TrimSpace(part[:i]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(part[i+1:]))
			if err1 != nil || err2 != nil {
				return fmt.Errorf("端口范围 %q 无法解析", part)
			}
			if lo < 1 || hi > 65535 || lo >= hi {
				return fmt.Errorf("端口范围 %q 必须在 1-65535 内且起始小于结束", part)
			}
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return fmt.Errorf("端口 %q 无法解析", part)
		}
		if n < 1 || n > 65535 {
			return fmt.Errorf("端口 %d 必须在 1-65535 之间", n)
		}
	}
	return nil
}

// diffPaths lists the leaf paths that changed between two configs, e.g.
// "wan.address" or "firewall.rules[0].enabled". It is derived by flattening
// the JSON encoding of both documents, so a new field is never forgotten.
func diffPaths(old, now Config) []string {
	a := flatten(old)
	b := flatten(now)
	seen := map[string]bool{}
	paths := make([]string, 0, 8)
	for k, v := range b {
		seen[k] = true
		if av, ok := a[k]; !ok || av != v {
			paths = append(paths, k)
		}
	}
	for k := range a {
		if !seen[k] {
			paths = append(paths, k)
		}
	}
	sort.Strings(paths)
	return paths
}

func flatten(c Config) map[string]string {
	out := map[string]string{}
	raw, err := json.Marshal(c)
	if err != nil {
		return out
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out
	}
	flattenValue("", doc, out)
	return out
}

func flattenValue(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flattenValue(p, val, out)
		}
	case []any:
		if len(t) == 0 {
			out[prefix] = "[]"
			return
		}
		for i, val := range t {
			flattenValue(fmt.Sprintf("%s[%d]", prefix, i), val, out)
		}
	default:
		if t == nil {
			out[prefix] = ""
			return
		}
		out[prefix] = fmt.Sprint(reflect.ValueOf(t).Interface())
	}
}
