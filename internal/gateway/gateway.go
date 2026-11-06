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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Supported enum values. Kept as maps so validation and normalisation share
// one definition.
var (
	wanModes        = map[string]bool{"dhcp": true, "static": true, "pppoe": true}
	wanRoutes       = map[string]bool{"local": true, "proxy": true}
	dnsModes        = map[string]bool{"forward": true, "recursive": true}
	ruleActions     = map[string]bool{"accept": true, "drop": true, "reject": true}
	ruleProtocols   = map[string]bool{"tcp": true, "udp": true, "any": true}
	forwardProtos   = map[string]bool{"tcp": true, "udp": true, "both": true}
	inboundDefaults = map[string]bool{"accept": true, "drop": true}
)

// wanIDPattern keeps the port ids stable: the chassis has six fixed ports, so
// an id is a slot name, not free text (the display name is).
var wanIDPattern = regexp.MustCompile(`^wan[1-6]$`)

// WANCount is the fixed number of WAN ports on the gateway.
const WANCount = 6

// DefaultWANDNS is what every fresh WAN starts with: two domestic resolvers.
var DefaultWANDNS = []string{"223.5.5.5", "119.29.29.29"}

// WANConfig is one uplink. Password is write-only on the API surface: GET
// returns PasswordSet instead of the secret, and a PUT with an empty password
// keeps the stored one. Each port keeps its own secret.
//
// Route is declarative only: "proxy" records the operator's intent to hand
// this uplink to the NGG policy engine, nothing in this package programs a
// policy route (see warningsFor).
type WANConfig struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
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
	Route       string   `json:"route"`
	Weight      int      `json:"weight"`
	Priority    int      `json:"priority"`
}

// DNSConfig is the enterprise router resolver policy. Like the rest of the
// document it is declarative: NGG stores the desired state, it does not run a
// forwarder or a recursive resolver yet.
type DNSConfig struct {
	Mode         string            `json:"mode"`
	Servers      []string          `json:"servers"`
	Fallback     []string          `json:"fallback"`
	DoH          string            `json:"doh"`
	CacheEnabled bool              `json:"cache_enabled"`
	CacheSize    int               `json:"cache_size"`
	CacheTTL     int               `json:"cache_ttl"`
	LocalDomain  string            `json:"local_domain"`
	Hosts        map[string]string `json:"hosts"`
	Blocklist    []string          `json:"blocklist"`
	DNSSEC       bool              `json:"dnssec"`
	LogQueries   bool              `json:"log_queries"`
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
//
// WAN is the v2.6 compatibility field and always equals WANs[0] after
// normalisation; WANs is the canonical list and always holds exactly
// WANCount entries. A client that only sends "wan" (the pre-multi-WAN shape)
// is migrated onto WANs[0] with the other five ports at their defaults.
type Config struct {
	WAN      WANConfig      `json:"wan"`
	WANs     []WANConfig    `json:"wans"`
	LAN      LANConfig      `json:"lan"`
	DNS      DNSConfig      `json:"dns"`
	Firewall FirewallConfig `json:"firewall"`
}

// DefaultWAN returns the factory settings of the idx-th port (0-based).
func DefaultWAN(idx int) WANConfig {
	if idx < 0 || idx >= WANCount {
		idx = 0
	}
	return WANConfig{
		ID:       fmt.Sprintf("wan%d", idx+1),
		Name:     fmt.Sprintf("WAN %d", idx+1),
		Enabled:  idx == 0,
		Mode:     "dhcp",
		MTU:      1500,
		DNS:      append([]string{}, DefaultWANDNS...),
		IPv6:     true,
		Route:    "local",
		Weight:   1,
		Priority: idx + 1,
	}
}

// DefaultDNS returns the factory resolver policy.
func DefaultDNS() DNSConfig {
	return DNSConfig{
		Mode:         "forward",
		Servers:      append([]string{}, DefaultWANDNS...),
		Fallback:     []string{"8.8.8.8"},
		DoH:          "",
		CacheEnabled: true,
		CacheSize:    10000,
		CacheTTL:     3600,
		LocalDomain:  "lan",
		Hosts:        map[string]string{},
		Blocklist:    []string{},
		DNSSEC:       false,
		LogQueries:   false,
	}
}

// Default returns the configuration a fresh installation starts from.
func Default() Config {
	wans := make([]WANConfig, 0, WANCount)
	for i := 0; i < WANCount; i++ {
		wans = append(wans, DefaultWAN(i))
	}
	return Config{
		WAN:  wans[0],
		WANs: wans,
		LAN: LANConfig{
			Address:      "192.168.10.1",
			Netmask:      "255.255.255.0",
			DHCPEnabled:  true,
			DHCPStart:    "192.168.10.100",
			DHCPEnd:      "192.168.10.200",
			LeaseMinutes: 1440,
			Domain:       "lan",
		},
		DNS: DefaultDNS(),
		Firewall: FirewallConfig{
			DefaultInbound: "drop",
			Rules:          []FirewallRule{},
			PortForwards:   []PortForward{},
			UPnP:           false,
			Conntrack:      true,
		},
	}
}

// Public returns a copy safe to hand to the UI: every WAN password is replaced
// by its presence flag so no secret leaves the process.
func (c Config) Public() Config {
	out := clone(c)
	for i := range out.WANs {
		out.WANs[i].PasswordSet = out.WANs[i].Password != ""
		out.WANs[i].Password = ""
	}
	if len(out.WANs) > 0 {
		out.WAN = out.WANs[0]
	} else {
		out.WAN.PasswordSet = out.WAN.Password != ""
		out.WAN.Password = ""
	}
	return out
}

func clone(c Config) Config {
	out := c
	out.WAN.DNS = append([]string{}, c.WAN.DNS...)
	out.WANs = make([]WANConfig, len(c.WANs))
	for i, w := range c.WANs {
		w.DNS = append([]string{}, w.DNS...)
		out.WANs[i] = w
	}
	out.DNS.Servers = append([]string{}, c.DNS.Servers...)
	out.DNS.Fallback = append([]string{}, c.DNS.Fallback...)
	out.DNS.Blocklist = append([]string{}, c.DNS.Blocklist...)
	out.DNS.Hosts = make(map[string]string, len(c.DNS.Hosts))
	for k, v := range c.DNS.Hosts {
		out.DNS.Hosts[k] = v
	}
	out.Firewall.Rules = append([]FirewallRule{}, c.Firewall.Rules...)
	out.Firewall.PortForwards = append([]PortForward{}, c.Firewall.PortForwards...)
	return out
}

// normalize trims and lower-cases enum-ish fields and fills the defaults that
// are unambiguous to fill (a zero MTU or lease time is never intentional).
// Empty-but-explicit values (an empty DNS list, conntrack=false) are kept.
//
// It also performs the multi-WAN migration: a document without "wans" (the
// single-WAN era shape, or a request that only carries the compat "wan" field)
// is promoted to six ports with WANs[0] taken from "wan", and a document with
// fewer than six ports is padded from the defaults. The result always has
// exactly WANCount ports and WAN == WANs[0].
func normalize(c Config) Config {
	d := Default()

	wans := c.WANs
	if len(wans) == 0 {
		legacy := c.WAN
		// A pre-multi-WAN document has no "enabled" flag at all: its single
		// uplink was in use by definition, so the migrated port1 is up.
		legacy.Enabled = true
		wans = []WANConfig{legacy}
	}
	normalized := make([]WANConfig, 0, WANCount)
	for i := 0; i < WANCount; i++ {
		src := d.WANs[i]
		if i < len(wans) {
			src = wans[i]
		}
		normalized = append(normalized, normalizeWAN(src, i))
	}
	c.WANs = normalized
	c.WAN = normalized[0]

	c.DNS = normalizeDNS(c.DNS)

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

// normalizeWAN trims one port and fills the per-port defaults that are safe to
// fill. idx is the port position, which also supplies the fallback id/name and
// the default priority.
func normalizeWAN(w WANConfig, idx int) WANConfig {
	d := DefaultWAN(idx)

	w.ID = strings.ToLower(strings.TrimSpace(w.ID))
	if w.ID == "" {
		w.ID = d.ID
	}
	w.Name = strings.TrimSpace(w.Name)
	if w.Name == "" {
		w.Name = d.Name
	}
	w.Mode = strings.ToLower(strings.TrimSpace(w.Mode))
	if w.Mode == "" {
		w.Mode = d.Mode
	}
	w.Route = strings.ToLower(strings.TrimSpace(w.Route))
	if w.Route == "" {
		w.Route = d.Route
	}
	w.Interface = strings.TrimSpace(w.Interface)
	w.Address = strings.TrimSpace(w.Address)
	w.Gateway = strings.TrimSpace(w.Gateway)
	w.Username = strings.TrimSpace(w.Username)
	if w.MTU == 0 {
		w.MTU = d.MTU
	}
	if w.DNS == nil {
		w.DNS = append([]string{}, d.DNS...)
	}
	w.DNS = dedupeStrings(w.DNS)
	if w.Weight == 0 {
		w.Weight = d.Weight
	}
	if w.Priority == 0 {
		w.Priority = d.Priority
	}
	w.PasswordSet = w.Password != ""
	return w
}

// normalizeDNS trims the resolver policy and fills its documented defaults.
func normalizeDNS(n DNSConfig) DNSConfig {
	d := DefaultDNS()

	n.Mode = strings.ToLower(strings.TrimSpace(n.Mode))
	if n.Mode == "" {
		n.Mode = d.Mode
	}
	if n.Servers == nil {
		n.Servers = append([]string{}, d.Servers...)
	}
	n.Servers = dedupeStrings(n.Servers)
	if n.Fallback == nil {
		n.Fallback = append([]string{}, d.Fallback...)
	}
	n.Fallback = dedupeStrings(n.Fallback)
	n.DoH = strings.TrimSpace(n.DoH)
	if n.CacheSize == 0 {
		n.CacheSize = d.CacheSize
	}
	if n.CacheTTL == 0 {
		n.CacheTTL = d.CacheTTL
	}
	n.LocalDomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n.LocalDomain)), ".")
	if n.LocalDomain == "" {
		n.LocalDomain = d.LocalDomain
	}
	hosts := make(map[string]string, len(n.Hosts))
	for k, v := range n.Hosts {
		k = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(k)), ".")
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		hosts[k] = v
	}
	n.Hosts = hosts
	if n.Blocklist == nil {
		n.Blocklist = []string{}
	}
	blocked := make([]string, 0, len(n.Blocklist))
	for _, s := range dedupeStrings(n.Blocklist) {
		blocked = append(blocked, strings.TrimSuffix(strings.ToLower(s), "."))
	}
	n.Blocklist = blocked
	return n
}

// dedupeStrings trims and de-duplicates case-insensitively, dropping empties
// and keeping the first spelling seen.
func dedupeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out
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
// normalised config so enum case never causes a false rejection, and a config
// whose WAN passwords have already been merged with the stored ones (that is
// what makes "leave the password empty to keep it" work for PPPoE).
func validate(c Config) error {
	if len(c.WANs) != WANCount {
		return invalid("wans", "必须恰好包含 %d 个 WAN 口，收到 %d", WANCount, len(c.WANs))
	}
	enabled := 0
	seenID := map[string]bool{}
	for i, w := range c.WANs {
		if err := validateWAN(w, i); err != nil {
			return err
		}
		if seenID[w.ID] {
			return invalid(fmt.Sprintf("wans[%d].id", i), "重复的 WAN id %q", w.ID)
		}
		seenID[w.ID] = true
		if w.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return invalid("wans", "至少需要启用一个 WAN 口")
	}

	if err := validateDNS(c.DNS); err != nil {
		return err
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

func validateWAN(w WANConfig, idx int) error {
	field := func(name string) string { return fmt.Sprintf("wans[%d].%s", idx, name) }

	if w.ID == "" {
		return invalid(field("id"), "不能为空")
	}
	if !wanIDPattern.MatchString(w.ID) {
		return invalid(field("id"), "只能是 wan1..wan6，收到 %q", w.ID)
	}
	if w.Name == "" {
		return invalid(field("name"), "不能为空")
	}
	if utf8.RuneCountInString(w.Name) > 64 {
		return invalid(field("name"), "长度不能超过 64 个字符")
	}
	if !wanModes[w.Mode] {
		return invalid(field("mode"), "只能是 dhcp / static / pppoe，收到 %q", w.Mode)
	}
	if !wanRoutes[w.Route] {
		return invalid(field("route"), "只能是 local / proxy，收到 %q", w.Route)
	}
	if w.MTU < 576 || w.MTU > 9000 {
		return invalid(field("mtu"), "必须在 576-9000 之间，收到 %d", w.MTU)
	}
	if w.Weight < 1 || w.Weight > 100 {
		return invalid(field("weight"), "必须在 1-100 之间，收到 %d", w.Weight)
	}
	if w.Priority < 1 || w.Priority > 10 {
		return invalid(field("priority"), "必须在 1-10 之间，收到 %d", w.Priority)
	}
	if w.Interface != "" && utf8.RuneCountInString(w.Interface) > 64 {
		return invalid(field("interface"), "长度不能超过 64 个字符")
	}
	if w.Address != "" && !isIPv4(w.Address) {
		return invalid(field("address"), "%q 不是合法 IPv4 地址", w.Address)
	}
	if w.Gateway != "" && !isIPv4(w.Gateway) {
		return invalid(field("gateway"), "%q 不是合法 IPv4 地址", w.Gateway)
	}
	for _, d := range w.DNS {
		if net.ParseIP(d) == nil {
			return invalid(field("dns"), "%q 不是合法 IP 地址", d)
		}
	}
	// A disabled port is not dialled, so its addressing requirements are not
	// enforced — the operator may fill the form in any order.
	if !w.Enabled {
		return nil
	}
	switch w.Mode {
	case "static":
		if w.Address == "" {
			return invalid(field("address"), "静态模式下必填")
		}
		if w.Gateway == "" {
			return invalid(field("gateway"), "静态模式下必填")
		}
	case "pppoe":
		if w.Username == "" {
			return invalid(field("username"), "PPPoE 模式下必填")
		}
		if w.Password == "" {
			return invalid(field("password"), "PPPoE 模式下必填（已保存的密码留空即沿用）")
		}
	}
	return nil
}

func validateDNS(n DNSConfig) error {
	if !dnsModes[n.Mode] {
		return invalid("dns.mode", "只能是 forward / recursive，收到 %q", n.Mode)
	}
	if len(n.Servers) == 0 {
		return invalid("dns.servers", "至少需要一条上游 DNS")
	}
	for _, s := range n.Servers {
		if net.ParseIP(s) == nil {
			return invalid("dns.servers", "%q 不是合法 IP 地址", s)
		}
	}
	for _, s := range n.Fallback {
		if net.ParseIP(s) == nil {
			return invalid("dns.fallback", "%q 不是合法 IP 地址", s)
		}
	}
	if n.DoH != "" && !strings.HasPrefix(strings.ToLower(n.DoH), "https://") {
		return invalid("dns.doh", "必须以 https:// 开头，收到 %q", n.DoH)
	}
	if n.CacheSize < 100 || n.CacheSize > 1000000 {
		return invalid("dns.cache_size", "必须在 100-1000000 之间，收到 %d", n.CacheSize)
	}
	if n.CacheTTL < 10 || n.CacheTTL > 86400 {
		return invalid("dns.cache_ttl", "必须在 10-86400 之间，收到 %d", n.CacheTTL)
	}
	if !isDomainName(n.LocalDomain) {
		return invalid("dns.local_domain", "%q 不是合法域名", n.LocalDomain)
	}
	for k, v := range n.Hosts {
		if !isDomainName(k) {
			return invalid("dns.hosts", "主机名 %q 不是合法域名", k)
		}
		if net.ParseIP(v) == nil {
			return invalid("dns.hosts", "主机 %q 的地址 %q 不是合法 IP", k, v)
		}
	}
	for _, b := range n.Blocklist {
		// Adblock-style lists use a "*.example.com" prefix; the suffix is the
		// domain that actually gets blocked.
		if !isDomainName(strings.TrimPrefix(b, "*.")) {
			return invalid("dns.blocklist", "%q 不是合法域名", b)
		}
	}
	return nil
}

// isDomainName accepts the RFC 1123 shape (labels of 1-63 characters of
// letters/digits/hyphen, no leading or trailing hyphen). Underscores are
// tolerated because SRV/TXT records in a hosts-style map legitimately use them.
func isDomainName(s string) bool {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return false
			}
		}
	}
	return true
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
