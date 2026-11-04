package nodes

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Native configuration support: an operator can paste a COMPLETE kernel
// config (Xray-core / sing-box JSON, Mihomo YAML) and NGG will parse its
// inbounds / outbounds / routing into editable nodes, without inventing any
// protocol behaviour — the config is still executed by the kernel.

type RoutingBinding struct {
	InboundTag  string `json:"inbound_tag"`
	OutboundTag string `json:"outbound_tag"`
	RuleType    string `json:"rule_type,omitempty"`
	// Matchers keeps domain / ip conditions such as Remnawave's
	// "ext:geo-zapret.dat:zapret" extended geosite/geoip selectors.
	Matchers []string `json:"matchers,omitempty"`
}

type ParseResult struct {
	Kernel   string           `json:"kernel"`
	Nodes    []Node           `json:"nodes"`
	Routing  []RoutingBinding `json:"routing"`
	Detected map[string]int   `json:"detected"`
	Warnings []string         `json:"warnings"`
}

// CleanNativeText tolerates the rich-text wrappers used when pasting long
// configs (@long-text:"..." and <long_text_quote>...</long_text_quote>).
func CleanNativeText(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<long_text_quote>"); i >= 0 {
		s = s[i+len("<long_text_quote>"):]
		if j := strings.LastIndex(s, "</long_text_quote>"); j >= 0 {
			s = s[:j]
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@long-text:") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "@long-text:"))
		if strings.HasPrefix(s, "\"") {
			if err := json.Unmarshal([]byte(s), &s); err == nil {
				return strings.TrimSpace(s)
			}
		}
	}
	return s
}

var protocolAlias = map[string]string{
	"vless": "vless-reality", "vmess": "vmess", "trojan": "trojan",
	"shadowsocks": "shadowsocks", "ss": "shadowsocks",
	"socks": "socks5", "socks5": "socks5", "http": "http", "mixed": "mixed",
	"freedom": "direct", "direct": "direct", "blackhole": "reject", "block": "reject",
	"wireguard": "wireguard", "hysteria2": "hysteria2", "hysteria": "hysteria2",
	"tuic": "tuic", "anytls": "anytls", "mieru": "mieru", "ssh": "ssh",
	"openvpn": "openvpn", "httpupgrade": "vless-reality",
}

func mapProtocol(raw string, params map[string]any) string {
	lower := strings.ToLower(raw)
	// VLESS with an ML-KEM hybrid "decryption" is the post-quantum variant.
	if lower == "vless" {
		if settings, ok := params["settings"].(map[string]any); ok {
			if dec, _ := settings["decryption"].(string); strings.HasPrefix(strings.ToLower(dec), "mlkem768x25519plus") {
				return "mlkem-hybrid"
			}
		}
		if dec, _ := params["decryption"].(string); strings.HasPrefix(strings.ToLower(dec), "mlkem768x25519plus") {
			return "mlkem-hybrid"
		}
	}
	if v, ok := protocolAlias[lower]; ok {
		return v
	}
	return lower
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func asList(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		var i int
		_, _ = fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}

func uniqueID(base string, seen map[string]bool, fallback string) string {
	id := strings.TrimSpace(base)
	if id == "" {
		id = fallback
	}
	if seen[id] {
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s-%d", id, i)
			if !seen[candidate] {
				id = candidate
				break
			}
		}
	}
	seen[id] = true
	return id
}

func nodeFromEntry(kind Kind, raw map[string]any, kernel string, seen map[string]bool, idx int, warnings *[]string) (Node, bool) {
	tag := asString(raw["tag"])
	if tag == "" {
		tag = asString(raw["name"])
	}
	protocolRaw := asString(raw["protocol"])
	if protocolRaw == "" {
		protocolRaw = asString(raw["type"])
	}
	if protocolRaw == "" {
		*warnings = append(*warnings, fmt.Sprintf("%s #%d has no protocol/type and was skipped", kind, idx+1))
		return Node{}, false
	}
	params := map[string]any{}
	for _, k := range []string{"settings", "streamSettings", "sniffing", "tlsSettings", "decryption", "password", "uuid", "cipher", "method", "server", "server_port", "alterId", "flow", "security", "network", "users", "servers", "certificates"} {
		if v, ok := raw[k]; ok {
			params[k] = v
		}
	}
	// Xray puts protocol params under settings; keep them verbatim.
	protocol := mapProtocol(protocolRaw, params)
	if _, ok := FindProtocol(protocol); !ok {
		*warnings = append(*warnings, fmt.Sprintf("protocol %q (tag %s) is not in the NGG catalog; skipped", protocolRaw, tag))
		return Node{}, false
	}
	info, _ := FindProtocol(protocol)
	if !protocolSupports(info, kind) {
		*warnings = append(*warnings, fmt.Sprintf("protocol %q (tag %s) is not supported for %s nodes; skipped", protocolRaw, tag, kind))
		return Node{}, false
	}
	port := asInt(raw["port"])
	if port == 0 {
		port = asInt(raw["listen_port"])
	}
	if port == 0 {
		port = asInt(raw["server_port"])
	}
	if port == 0 {
		port = info.DefaultPort
	}
	address := asString(raw["listen"])
	if kind == KindOutbound {
		if s := asString(raw["server"]); s != "" {
			address = s
		} else {
			address = asString(raw["address"])
		}
	}
	if address == "" && kind == KindInbound {
		address = "0.0.0.0"
	}
	id := uniqueID(tag, seen, fmt.Sprintf("%s-%d", kind, idx+1))
	return Node{
		ID: id, Name: tag, Kind: kind, Protocol: protocol,
		Address: address, Port: port, Enabled: true,
		CoreID: kernel, Params: params, Tags: []string{"from-native"},
		Description: fmt.Sprintf("由 %s 原生配置解析（原协议 %s）", kernel, protocolRaw),
	}, true
}

func parseRouting(doc map[string]any, kernel string) []RoutingBinding {
	var out []RoutingBinding
	var rules []any
	if r, ok := doc["routing"].(map[string]any); ok {
		rules = asList(r["rules"])
	}
	if r, ok := doc["route"].(map[string]any); ok {
		rules = append(rules, asList(r["rules"])...)
	}
	for _, item := range rules {
		m := asMap(item)
		inbound := ""
		if tags := asList(m["inboundTag"]); len(tags) > 0 {
			inbound = asString(tags[0])
		}
		if inbound == "" {
			inbound = asString(m["inboundTag"])
		}
		if inbound == "" {
			inbound = asString(m["inbound"])
		}
		outbound := ""
		if tags := asList(m["outboundTag"]); len(tags) > 0 {
			outbound = asString(tags[0])
		}
		if outbound == "" {
			outbound = asString(m["outboundTag"])
		}
		if outbound == "" {
			outbound = asString(m["outbound"])
		}
		var matchers []string
		for _, key := range []string{"domain", "ip"} {
			for _, v := range asList(m[key]) {
				if str := asString(v); str != "" {
					matchers = append(matchers, key+"="+str)
				}
			}
		}
		if inbound == "" && outbound == "" && len(matchers) == 0 {
			continue
		}
		out = append(out, RoutingBinding{
			InboundTag: inbound, OutboundTag: outbound,
			RuleType: asString(m["type"]), Matchers: matchers,
		})
	}
	return out
}

// ParseNative parses a complete kernel config into NGG nodes + routing view.
func ParseNative(kernel, content string) (*ParseResult, error) {
	text := CleanNativeText(content)
	if text == "" {
		return nil, fmt.Errorf("config is empty")
	}
	res := &ParseResult{
		Kernel: kernel, Nodes: []Node{}, Routing: []RoutingBinding{},
		Detected: map[string]int{}, Warnings: []string{},
	}
	var doc map[string]any
	switch kernel {
	case "xray-core", "sing-box":
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			return nil, fmt.Errorf("invalid %s JSON: %w", kernel, err)
		}
	case "mihomo":
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			return nil, fmt.Errorf("invalid Mihomo YAML: %w", err)
		}
	default:
		return nil, fmt.Errorf("kernel %q has no native config parser", kernel)
	}
	seen := map[string]bool{}
	inboundKey, outboundKey := "inbounds", "outbounds"
	if kernel == "mihomo" {
		inboundKey, outboundKey = "listeners", "proxies"
	}
	for i, item := range asList(doc[inboundKey]) {
		if n, ok := nodeFromEntry(KindInbound, asMap(item), kernel, seen, i, &res.Warnings); ok {
			res.Nodes = append(res.Nodes, n)
		}
	}
	for i, item := range asList(doc[outboundKey]) {
		if n, ok := nodeFromEntry(KindOutbound, asMap(item), kernel, seen, i, &res.Warnings); ok {
			res.Nodes = append(res.Nodes, n)
		}
	}
	res.Routing = parseRouting(doc, kernel)
	res.Detected["inbounds"] = len(asList(doc[inboundKey]))
	res.Detected["outbounds"] = len(asList(doc[outboundKey]))
	res.Detected["nodes"] = len(res.Nodes)
	res.Detected["routing_rules"] = len(res.Routing)
	if len(res.Nodes) == 0 {
		res.Warnings = append(res.Warnings, "no inbounds/outbounds could be mapped to NGG nodes")
	}
	return res, nil
}
