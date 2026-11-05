package protocols

import (
	"fmt"
	"regexp"
	"strings"
)

// The three kernels have genuinely different configuration grammars. This
// file is the single source of truth for the fields the visual editor exposes,
// what each field means, and where the official documentation lives. The
// generator (outboundConfig in protocols.go) maps the SAME visual fields onto
// each kernel's real syntax. Nothing here invents a protocol: every field name
// below is the field name used by that kernel's own config format.

type Field struct {
	Key         string   `json:"key"`   // native key in THIS kernel's syntax
	Label       string   `json:"label"` // short Chinese name
	Type        string   `json:"type"`  // text | number | bool | select
	Options     []string `json:"options,omitempty"`
	Default     string   `json:"default"`
	Description string   `json:"description"` // what this field actually does
	Required    bool     `json:"required"`
}

type KernelSchema struct {
	Kernel      string  `json:"kernel"`
	Label       string  `json:"label"`
	Syntax      string  `json:"syntax"`
	Format      string  `json:"format"`
	DocURL      string  `json:"doc_url"`
	DocName     string  `json:"doc_name"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
}

const (
	DocMihomo  = "https://wiki.metacubex.one/config/proxies/"
	DocSingbox = "https://sing-box.sagernet.org/configuration/outbound/"
	DocXray    = "https://xtls.github.io/config/outbounds/"
)

var kernelSchemas = map[string]KernelSchema{
	"mihomo": {
		Kernel: "mihomo", Label: "Mihomo / Clash.Meta", Syntax: "YAML · proxies: 数组", Format: "yaml",
		DocURL: DocMihomo, DocName: "Mihomo 官方 · 代理配置 proxies",
		Description: "Mihomo 把每个代理写成 proxies: 数组里的一项；规则（rules:）通过 name 引用它。字段名用连字符风格，布尔值直接写 true/false。",
		Fields: []Field{
			{Key: "name", Label: "代理名称", Type: "text", Default: "", Description: "proxies[].name，该条代理在配置里的唯一标识；rules 规则用这个名字引用它", Required: true},
			{Key: "type", Label: "协议类型", Type: "text", Default: "", Description: "proxies[].type，如 vmess / vless / trojan / ss / hysteria2 / tuic / wireguard；必须与所选协议匹配", Required: true},
			{Key: "server", Label: "服务器地址", Type: "text", Default: "127.0.0.1", Description: "proxies[].server，节点域名或 IP", Required: true},
			{Key: "port", Label: "服务器端口", Type: "number", Default: "443", Description: "proxies[].port，节点监听端口", Required: true},
			{Key: "cipher", Label: "加密方式", Type: "text", Default: "auto", Description: "Shadowsocks 的 AEAD 算法（如 aes-128-gcm）或 VMess 的 auto/aes-128-gcm；不同协议含义不同"},
			{Key: "password", Label: "密码", Type: "text", Default: "", Description: "Shadowsocks / Trojan / Hysteria2 / TUIC 的认证密码"},
			{Key: "uuid", Label: "用户 UUID", Type: "text", Default: "", Description: "VMess / VLESS 的用户 ID，必须与服务端一致"},
			{Key: "alterId", Label: "额外 ID 数", Type: "number", Default: "0", Description: "VMess 的 alterId；AEAD 模式下通常为 0"},
			{Key: "network", Label: "传输层", Type: "select", Options: []string{"tcp", "ws", "grpc", "h2", "http", "quic"}, Default: "tcp", Description: "底层传输方式；选 ws/grpc 时还要配 path / headers 等 opts"},
			{Key: "tls", Label: "启用 TLS", Type: "bool", Default: "false", Description: "是否为该代理启用 TLS；Trojan / VLESS+TLS 必须开启"},
			{Key: "sni", Label: "TLS SNI", Type: "text", Default: "", Description: "TLS 握手时发送的 servername；REALITY / 伪装场景常需与服务端一致"},
			{Key: "skip-cert-verify", Label: "跳过证书校验", Type: "bool", Default: "false", Description: "仅测试环境使用；生产环境请保持 false"},
			{Key: "udp", Label: "启用 UDP", Type: "bool", Default: "true", Description: "是否允许该代理承载 UDP 流量"},
		},
	},
	"sing-box": {
		Kernel: "sing-box", Label: "sing-box", Syntax: "JSON · outbound 对象", Format: "json",
		DocURL: DocSingbox, DocName: "sing-box 官方 · Outbound 配置",
		Description: "sing-box 用 JSON 的 outbounds 数组，每个元素是一个带 type/tag 的对象；TLS 与传输层是嵌套的 tls / transport 对象。",
		Fields: []Field{
			{Key: "type", Label: "出站类型", Type: "text", Default: "", Description: "outbound.type，如 vmess / vless / trojan / shadowsocks / hysteria2 / tuic / wireguard", Required: true},
			{Key: "tag", Label: "出站标签", Type: "text", Default: "", Description: "outbound.tag，路由规则用这个 tag 引用该出站"},
			{Key: "server", Label: "服务器地址", Type: "text", Default: "127.0.0.1", Description: "outbound.server，节点域名或 IP", Required: true},
			{Key: "server_port", Label: "服务器端口", Type: "number", Default: "443", Description: "outbound.server_port（注意 sing-box 用下划线命名，不是 port）", Required: true},
			{Key: "uuid", Label: "用户 UUID", Type: "text", Default: "", Description: "VMess / VLESS / TUIC 的用户 ID"},
			{Key: "password", Label: "密码", Type: "text", Default: "", Description: "Shadowsocks / Trojan / Hysteria2 / TUIC 的认证密码"},
			{Key: "method", Label: "加密方法", Type: "text", Default: "aes-128-gcm", Description: "Shadowsocks 的 method（sing-box 用 method，Mihomo 用 cipher）"},
			{Key: "tls", Label: "TLS 对象", Type: "bool", Default: "false", Description: "生成嵌套的 tls: { enabled, server_name }；Trojan / VLESS 通常必须开启"},
			{Key: "server_name", Label: "TLS server_name", Type: "text", Default: "", Description: "tls.server_name，即 TLS SNI / 证书域名"},
			{Key: "transport", Label: "传输层类型", Type: "select", Options: []string{"ws", "grpc", "http", "httpupgrade", "quic"}, Default: "ws", Description: "生成嵌套的 transport.type；不同内核叫法不同（Xray 叫 streamSettings.network）"},
			{Key: "path", Label: "传输路径", Type: "text", Default: "", Description: "transport.path，ws / httpupgrade 的路径"},
			{Key: "udp_over_tcp", Label: "UDP over TCP", Type: "bool", Default: "false", Description: "是否把 UDP 封装进 TCP；仅部分协议支持"},
		},
	},
	"xray-core": {
		Kernel: "xray-core", Label: "Xray-core", Syntax: "JSON · outbounds / settings.vnext / streamSettings", Format: "json",
		DocURL: DocXray, DocName: "Xray 官方 · 出站配置 Outbounds",
		Description: "Xray 用 outbounds 数组；协议参数放在 settings.vnext[].users[]，传输层单独放在 streamSettings（不是 settings 里）。",
		Fields: []Field{
			{Key: "protocol", Label: "协议名", Type: "text", Default: "", Description: "outbounds[].protocol，如 vmess / vless / trojan / shadowsocks / wireguard", Required: true},
			{Key: "tag", Label: "出站标签", Type: "text", Default: "", Description: "outbounds[].tag，路由规则引用的标识"},
			{Key: "address", Label: "服务器地址", Type: "text", Default: "127.0.0.1", Description: "settings.vnext[].address（Xray 用 address，不是 server）", Required: true},
			{Key: "port", Label: "服务器端口", Type: "number", Default: "443", Description: "settings.vnext[].port", Required: true},
			{Key: "id", Label: "用户 UUID", Type: "text", Default: "", Description: "settings.vnext[].users[].id，VMess / VLESS 用户 ID"},
			{Key: "alterId", Label: "额外 ID 数", Type: "number", Default: "0", Description: "users[].alterId；VMess AEAD 下一般为 0"},
			{Key: "security", Label: "用户加密", Type: "select", Options: []string{"auto", "aes-128-gcm", "chacha20-poly1305", "none"}, Default: "auto", Description: "users[].security，VMess 的内建加密方式"},
			{Key: "flow", Label: "流控模式", Type: "text", Default: "", Description: "users[].flow，VLESS 的 XTLS 流控（如 xtls-rprx-vision）"},
			{Key: "network", Label: "传输网络", Type: "select", Options: []string{"tcp", "ws", "grpc", "h2", "quic", "httpupgrade"}, Default: "tcp", Description: "streamSettings.network；Xray 把传输层放在 streamSettings"},
			{Key: "stream_security", Label: "传输层安全", Type: "select", Options: []string{"none", "tls", "reality"}, Default: "none", Description: "streamSettings.security；REALITY 还需 realitySettings"},
			{Key: "serverName", Label: "TLS serverName", Type: "text", Default: "", Description: "streamSettings.tlsSettings.serverName（注意大驼峰命名）"},
			{Key: "path", Label: "WS 路径", Type: "text", Default: "", Description: "streamSettings.wsSettings.path"},
			{Key: "Host", Label: "WS Host 头", Type: "text", Default: "", Description: "streamSettings.wsSettings.headers.Host"},
		},
	},
}

// Schemas returns the three kernel schemas in a stable order.
func Schemas() []KernelSchema {
	return []KernelSchema{
		kernelSchemas["mihomo"], kernelSchemas["sing-box"], kernelSchemas["xray-core"],
	}
}

// SchemaFor returns the schema for a kernel, defaulting to sing-box for
// unknown values so the editor always has real field definitions.
func SchemaFor(kernel string) KernelSchema {
	if s, ok := kernelSchemas[kernel]; ok {
		return s
	}
	return kernelSchemas["sing-box"]
}

// DocFor returns the official documentation URL for a kernel.
func DocFor(kernel string) string {
	return SchemaFor(kernel).DocURL
}

var (
	jsonKeyRE = regexp.MustCompile(`^\s*"([A-Za-z0-9_.-]+)"\s*:`)
	yamlKeyRE = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*:`)
	confKeyRE = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*=`)
)

func lineKey(line, format string) string {
	var re *regexp.Regexp
	switch format {
	case "json":
		re = jsonKeyRE
	case "conf", "text", "raw":
		re = confKeyRE
	default:
		re = yamlKeyRE
	}
	if m := re.FindStringSubmatch(line); len(m) > 1 {
		return m[1]
	}
	return ""
}

func commentPrefix(format string) string {
	if format == "json" {
		return "//"
	}
	return "#"
}

// Annotate returns a copy of the generated config with a header explaining
// the kernel's syntax and an inline comment above every recognised field.
// Plain JSON does not allow comments, so this is a JSONC-like readable view;
// StripComments removes it again before validation or persistence.
func Annotate(content []byte, format, kernel string) []byte {
	sch := SchemaFor(kernel)
	prefix := commentPrefix(format)
	byKey := map[string]Field{}
	for _, f := range sch.Fields {
		byKey[f.Key] = f
	}
	// Nested objects produced by the generator also deserve an explanation.
	for _, extra := range []struct{ key, label, desc string }{
		{"proxies", "代理数组", "Mihomo 的代理列表，每一项是一个出站代理"},
		{"outbounds", "出站数组", "Xray 的出站列表，每一项是一个出站配置"},
		{"settings", "协议参数", "Xray 里放协议自身参数（vnext/users 等）"},
		{"vnext", "节点列表", "Xray 里服务端节点地址、端口与用户列表"},
		{"users", "用户列表", "该节点允许的用户（id / security / flow）"},
		{"streamSettings", "传输层设置", "Xray 的底层传输与 TLS/REALITY 设置，独立于 settings"},
		{"tls", "TLS 设置", "TLS 开关与 server_name（SNI）"},
		{"transport", "传输层设置", "sing-box 的 ws/grpc 等传输层参数"},
		{"enabled", "TLS 开关", "tls.enabled，是否启用 TLS"},
		{"headers", "请求头", "WebSocket / HTTP 传输层的自定义请求头"},
	} {
		if _, ok := byKey[extra.key]; !ok {
			byKey[extra.key] = Field{Key: extra.key, Label: extra.label, Description: extra.desc}
		}
	}

	out := []string{
		fmt.Sprintf("%s ── %s · %s ──", prefix, sch.Label, sch.Syntax),
		fmt.Sprintf("%s 官方文档：%s", prefix, sch.DocURL),
		fmt.Sprintf("%s %s", prefix, sch.Description),
		fmt.Sprintf("%s 每个字段上方的注释说明该参数的含义；「校验 / 保存」时会自动去掉这些注释行。", prefix),
		"",
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		key := lineKey(line, format)
		if f, ok := byKey[key]; ok && !seen[key] {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			out = append(out, fmt.Sprintf("%s%s %s：%s", indent, prefix, f.Label, f.Description))
			seen[key] = true
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// StripComments removes whole-line // and # comments. It only strips lines
// whose first non-space characters are the comment marker, so URLs such as
// https:// inside string values are never damaged.
func StripComments(content []byte, format string) []byte {
	lines := strings.Split(string(content), "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
			continue
		}
		kept = append(kept, l)
	}
	return []byte(strings.Join(kept, "\n"))
}
