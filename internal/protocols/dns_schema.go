package protocols

import "github.com/ngg-project/ngg/internal/rulesets"

// KernelDnsSchema has exactly the same shape as rulesets.KernelRuleSetSchema:
// the DNS editor reuses the same field renderer and the same RuleSetField
// definition. A type alias (rather than a copy) keeps one single source of
// truth for the field structure across both the rule-set and DNS schemas.
type KernelDnsSchema = rulesets.KernelRuleSetSchema

var dnsSchemas = []KernelDnsSchema{
	{
		Kernel: "mihomo", Label: "Mihomo / Clash.Meta", Format: "yaml",
		Syntax:      "dns: {enable, enhanced-mode, fake-ip-range, nameserver, fallback, nameserver-policy}",
		DocURL:      "https://wiki.metacubex.one/config/dns/",
		DocName:     "Mihomo 官方 · DNS",
		Description: "Mihomo 的 dns: 段用 enhanced-mode 选择 fake-ip / redir-host / normal，nameserver 与 fallback 是上游 DNS 列表，nameserver-policy 按域名指定上游。",
		Fields: []rulesets.RuleSetField{
			{Key: "enable", Label: "启用 DNS", Type: "bool", Default: "true", Description: "dns.enable；是否启用 Mihomo 内置 DNS 模块", Required: false},
			{Key: "enhanced-mode", Label: "增强模式", Type: "select", Options: []string{"fake-ip", "redir-host", "normal"}, Default: "fake-ip", Description: "dns.enhanced-mode；fake-ip 返回保留地址段，redir-host 返回真实 IP", Required: false},
			{Key: "fake-ip-range", Label: "Fake-IP 段", Type: "text", Default: "198.18.0.1/16", Description: "dns.fake-ip-range；enhanced-mode=fake-ip 时分配的保留地址段", Required: false},
			{Key: "nameserver", Label: "主上游 DNS", Type: "list", Default: "", Description: "dns.nameserver；主 DNS 服务器列表（可带 #策略 后缀）", Required: false},
			{Key: "fallback", Label: "备用上游 DNS", Type: "list", Default: "", Description: "dns.fallback；当主上游结果被判定污染时使用的备用 DNS 列表", Required: false},
			{Key: "nameserver-policy", Label: "域名分流策略", Type: "text", Default: "", Description: "dns.nameserver-policy；按域名或 geosite 指定使用哪个上游（YAML 映射）", Required: false},
		},
		Example: `dns:
  enable: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  nameserver:
    - 223.5.5.5
    - 119.29.29.29
  fallback:
    - 8.8.8.8
  nameserver-policy:
    "geosite:cn": "223.5.5.5"`,
	},
	{
		Kernel: "sing-box", Label: "sing-box", Format: "json",
		Syntax:      "dns: {servers[].tag/address/detour, strategy, disable_cache, independent_cache}",
		DocURL:      "https://sing-box.sagernet.org/configuration/dns/",
		DocName:     "sing-box 官方 · DNS",
		Description: "sing-box 的 dns.servers 是带 tag / address / detour 的对象数组；strategy 控制 A/AAAA 解析偏好，disable_cache 与 independent_cache 控制缓存。",
		Fields: []rulesets.RuleSetField{
			{Key: "tag", Label: "服务器标签", Type: "text", Default: "local", Description: "dns.servers[].tag；路由规则引用该 DNS 服务器的标识", Required: false},
			{Key: "address", Label: "服务器地址", Type: "text", Default: "223.5.5.5", Description: "dns.servers[].address；上游 DNS 地址（可带 udp:// / tls:// 等协议前缀）", Required: true},
			{Key: "detour", Label: "解析出站", Type: "text", Default: "direct", Description: "dns.servers[].detour；查询该 DNS 时使用的出站 tag", Required: false},
			{Key: "strategy", Label: "解析策略", Type: "select", Options: []string{"prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only"}, Default: "prefer_ipv4", Description: "dns.strategy；A / AAAA 记录的偏好与限制", Required: false},
			{Key: "disable_cache", Label: "禁用缓存", Type: "bool", Default: "false", Description: "dns.disable_cache；为 true 时不缓存 DNS 结果", Required: false},
			{Key: "independent_cache", Label: "独立缓存", Type: "bool", Default: "false", Description: "dns.independent_cache；每个 DNS 服务器使用独立缓存", Required: false},
		},
		Example: `{
  "dns": {
    "servers": [
      { "tag": "local", "address": "223.5.5.5", "detour": "direct" }
    ],
    "strategy": "prefer_ipv4",
    "disable_cache": false,
    "independent_cache": false
  }
}`,
	},
	{
		Kernel: "xray-core", Label: "Xray-core", Format: "json",
		Syntax:      "dns: {servers, hosts, tag, queryStrategy, disableCache}",
		DocURL:      "https://xtls.github.io/config/dns/",
		DocName:     "Xray 官方 · DNS",
		Description: "Xray 的 dns.servers 是上游列表（字符串或对象），hosts 是静态域名映射，queryStrategy 控制 IPv4/IPv6 查询，tag 用于把 DNS 流量导向内置入站。",
		Fields: []rulesets.RuleSetField{
			{Key: "servers", Label: "上游 DNS", Type: "list", Default: "", Description: "dns.servers；上游 DNS 服务器列表（字符串或带 domains/expectIPs 的对象）", Required: true},
			{Key: "hosts", Label: "静态Hosts", Type: "text", Default: "", Description: "dns.hosts；域名到 IP 的静态映射（JSON 对象）", Required: false},
			{Key: "tag", Label: "DNS 入站标签", Type: "text", Default: "", Description: "dns.tag；配合 routing 把 DNS 查询导入内置 dns 出站", Required: false},
			{Key: "queryStrategy", Label: "查询策略", Type: "select", Options: []string{"UseIP", "UseIPv4", "UseIPv6"}, Default: "UseIP", Description: "dns.queryStrategy；允许的解析记录类型", Required: false},
			{Key: "disableCache", Label: "禁用缓存", Type: "bool", Default: "false", Description: "dns.disableCache；为 true 时禁用 DNS 缓存", Required: false},
		},
		Example: `{
  "dns": {
    "servers": ["223.5.5.5", "8.8.8.8"],
    "hosts": { "example.com": "1.2.3.4" },
    "tag": "dns_inbound",
    "queryStrategy": "UseIP",
    "disableCache": false
  }
}`,
	},
}

// DnsSchemas returns the three official DNS schemas in a stable order.
func DnsSchemas() []KernelDnsSchema {
	out := make([]KernelDnsSchema, len(dnsSchemas))
	copy(out, dnsSchemas)
	return out
}
