package rulesets

// KernelRuleSetSchema and RuleSetField describe the OFFICIAL rule-set syntax
// of each of the three kernels. Field keys below are the keys used by that
// kernel's own configuration format; nothing is invented. The UI renders the
// fields as a form and shows the matching official example verbatim.

// RuleSetField is one visual-editor field of an official kernel syntax.
type RuleSetField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text | number | bool | select | list
	Options     []string `json:"options,omitempty"`
	Default     string   `json:"default"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
}

// KernelRuleSetSchema is the full visual schema of one kernel's rule-set
// syntax. It is also the shared shape used by the DNS schema (see protocols).
type KernelRuleSetSchema struct {
	Kernel      string         `json:"kernel"`
	Label       string         `json:"label"`
	Format      string         `json:"format"` // yaml | json
	Syntax      string         `json:"syntax"`
	DocURL      string         `json:"doc_url"`
	DocName     string         `json:"doc_name"`
	Description string         `json:"description"`
	Fields      []RuleSetField `json:"fields"`
	Example     string         `json:"example"`
}

var ruleSetSchemas = []KernelRuleSetSchema{
	{
		Kernel: "mihomo", Label: "Mihomo / Clash.Meta", Format: "yaml",
		Syntax:      "rule-providers: {<tag>: {type, behavior, format, url, path, interval}} + rules: RULE-SET,<tag>,<policy>",
		DocURL:      "https://wiki.metacubex.one/config/rule-providers/",
		DocName:     "Mihomo 官方 · Rule Providers",
		Description: "Mihomo 在 rule-providers 下用 tag 定义一个远程/本地规则集，再由 rules 里的 RULE-SET,<tag>,<policy> 引用。behavior 决定载荷类型（domain / ipcidr / classical），format 支持 yaml / text / mrs。",
		Fields: []RuleSetField{
			{Key: "type", Label: "来源类型", Type: "select", Options: []string{"http", "file", "inline"}, Default: "http", Description: "rule-providers.<tag>.type；http 为远程订阅，file 读本地文件，inline 直接内联 payload", Required: true},
			{Key: "behavior", Label: "行为类型", Type: "select", Options: []string{"domain", "ipcidr", "classical"}, Default: "domain", Description: "rule-providers.<tag>.behavior；声明载荷是域名、CIDR 还是经典规则行", Required: true},
			{Key: "format", Label: "载荷格式", Type: "select", Options: []string{"yaml", "text", "mrs"}, Default: "yaml", Description: "rule-providers.<tag>.format；http 类型常用 yaml/text，mrs 为 Mihomo 二进制规则集", Required: true},
			{Key: "url", Label: "订阅地址", Type: "text", Default: "", Description: "rule-providers.<tag>.url；type=http 时的远程规则集下载地址", Required: false},
			{Key: "path", Label: "本地路径", Type: "text", Default: "", Description: "rule-providers.<tag>.path；下载缓存或 type=file 时的本地文件路径", Required: false},
			{Key: "interval", Label: "更新间隔(秒)", Type: "number", Default: "86400", Description: "rule-providers.<tag>.interval；远程规则集自动更新周期", Required: false},
			{Key: "RULE-SET", Label: "规则引用", Type: "text", Default: "RULE-SET,tag,PROXY", Description: "rules[] 中的 RULE-SET,<tag>,<policy> 行，把上面的 tag 接到具体策略", Required: false},
		},
		Example: `rule-providers:
  apple:
    type: http
    behavior: domain
    format: yaml
    url: "https://cdn.jsdelivr.net/gh/blackmatrix7/ios_rule_script@master/rule/Clash/Apple/Apple.yaml"
    path: ./ruleset/apple.yaml
    interval: 86400
rules:
  - RULE-SET,apple,PROXY`,
	},
	{
		Kernel: "sing-box", Label: "sing-box", Format: "json",
		Syntax:      "route.rule_set[]: {type, tag, format, url, download_detour, update_interval} + route.rules[].rule_set",
		DocURL:      "https://sing-box.sagernet.org/configuration/route/rule_set/",
		DocName:     "sing-box 官方 · Rule Set",
		Description: "sing-box 把规则集定义在 route.rule_set 数组里，每项有 tag 与 format（source/binary）；route.rules[] 通过 rule_set 字段按 tag 引用。远程规则集用 .srs 二进制格式，由 download_detour 指定下载出站。",
		Fields: []RuleSetField{
			{Key: "type", Label: "来源类型", Type: "select", Options: []string{"remote", "local", "inline"}, Default: "remote", Description: "route.rule_set[].type；remote 为远程下载，local 读本地文件，inline 内联", Required: true},
			{Key: "tag", Label: "规则集标签", Type: "text", Default: "", Description: "route.rule_set[].tag；route.rules[].rule_set 用这个 tag 引用", Required: true},
			{Key: "format", Label: "载荷格式", Type: "select", Options: []string{"source", "binary"}, Default: "binary", Description: "route.rule_set[].format；远程 .srs 用 binary，JSON 源用 source", Required: true},
			{Key: "url", Label: "下载地址", Type: "text", Default: "", Description: "route.rule_set[].url；type=remote 时的 .srs 规则集地址", Required: false},
			{Key: "download_detour", Label: "下载出站", Type: "text", Default: "direct", Description: "route.rule_set[].download_detour；拉取规则集所使用的出站 tag", Required: false},
			{Key: "update_interval", Label: "更新间隔", Type: "text", Default: "1d", Description: "route.rule_set[].update_interval；远程规则集更新周期（Go duration 字符串）", Required: false},
			{Key: "rule_set", Label: "规则引用", Type: "text", Default: "", Description: "route.rules[].rule_set；引用上面的 tag（可为字符串或数组）", Required: false},
		},
		Example: `{
  "route": {
    "rule_set": [
      {
        "type": "remote",
        "tag": "geosite-cn",
        "format": "binary",
        "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs",
        "download_detour": "direct"
      }
    ],
    "rules": [
      { "rule_set": "geosite-cn", "outbound": "direct" }
    ]
  }
}`,
	},
	{
		Kernel: "xray-core", Label: "Xray-core", Format: "json",
		Syntax:      "routing.rules[]: {domain: [...], ip: [...]} + geosite:/geoip: 内置规则集",
		DocURL:      "https://xtls.github.io/config/routing/",
		DocName:     "Xray 官方 · Routing",
		Description: "Xray 没有独立的 rule_set 对象，而是把域名/IP 匹配直接写在 routing.rules[].domain / .ip 数组里，并用 geosite: 与 geoip: 前缀引用内置的域名/IP 规则集，outboundTag 指定命中的出站。",
		Fields: []RuleSetField{
			{Key: "domain", Label: "域名匹配", Type: "list", Default: "", Description: "routing.rules[].domain；支持明文、regexp:、geosite: 前缀与 ext: 文件", Required: false},
			{Key: "ip", Label: "IP 匹配", Type: "list", Default: "", Description: "routing.rules[].ip；支持 CIDR、geoip: 前缀与 ext: 文件", Required: false},
			{Key: "geosite", Label: "内置域名集", Type: "text", Default: "geosite:cn", Description: "domain 数组里的 geosite:<tag> 项，引用 Xray 内置域名分类", Required: false},
			{Key: "geoip", Label: "内置 IP 集", Type: "text", Default: "geoip:cn", Description: "ip 数组里的 geoip:<tag> 项，引用 Xray 内置 IP 分类", Required: false},
			{Key: "outboundTag", Label: "命中出站", Type: "text", Default: "direct", Description: "routing.rules[].outboundTag；规则命中时使用的出站 tag", Required: false},
		},
		Example: `{
  "routing": {
    "rules": [
      {
        "type": "field",
        "domain": ["geosite:cn"],
        "ip": ["geoip:cn"],
        "outboundTag": "direct"
      }
    ]
  }
}`,
	},
}

// SyntaxSchemas returns the three official rule-set schemas in a stable order.
func SyntaxSchemas() []KernelRuleSetSchema {
	out := make([]KernelRuleSetSchema, len(ruleSetSchemas))
	copy(out, ruleSetSchemas)
	return out
}
