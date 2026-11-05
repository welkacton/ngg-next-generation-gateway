// Package profiles — reverse direction of Render.
//
// Render turns the six editable sections into one complete kernel config.
// Split does the opposite: it takes a hand-edited complete config (the text
// shown in "完整配置") and redistributes its top-level keys back into the six
// sections, so editing the full file automatically updates the section editor.
//
// Keeping this logic on the server (instead of a second, slightly different
// implementation in TypeScript) means Render/Split share one grammar map and
// one round-trip contract that Go tests can enforce.
package profiles

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ngg-project/ngg/internal/protocols"
)

// SplitResult is applied directly onto the editing Profile by the frontend.
type SplitResult struct {
	Sections []Section `json:"sections"`
	Format   string    `json:"format"`
	Kernel   string    `json:"kernel"`
	Message  string    `json:"message"`
	Warnings []string  `json:"warnings,omitempty"`
	Changed  []string  `json:"changed"`
}

// block is one top-level key of the complete config, with the original text
// (including the comment lines that preceded it) kept intact for YAML.
type block struct {
	key          string
	text         string
	forceSection string // used when one key must be split across two sections
}

// keySection maps each kernel's native top-level keys onto our six sections.
// Known global keys are listed explicitly as "extra" so only truly unfamiliar
// keys produce a warning; unknown keys are never dropped.
var keySection = map[string]map[string]string{
	"mihomo": {
		"proxies": "protocols", "proxy-providers": "protocols",
		"listeners": "inbounds", "port": "inbounds", "socks-port": "inbounds",
		"mixed-port": "inbounds", "redir-port": "inbounds", "tproxy-port": "inbounds",
		"tun": "inbounds", "bind-address": "inbounds",
		"authentication": "inbounds", "external-controller": "inbounds",
		"external-controller-tls": "inbounds", "external-controller-cors": "inbounds",
		"external-ui": "inbounds", "secret": "inbounds", "interface-name": "inbounds",
		"proxy-groups": "outbounds",
		"rules":        "routes", "rule-providers": "routes", "sub-rule": "routes",
		"dns": "dns", "hosts": "dns", "fake-ip-range": "dns", "fake-ip-filter": "dns",
		"mode": "extra", "log-level": "extra", "allow-lan": "extra", "ipv6": "extra",
		"unified-delay": "extra", "tcp-concurrent": "extra", "find-process-mode": "extra",
		"global-client-fingerprint": "extra", "geodata-mode": "extra", "geodata-loader": "extra",
		"geox-url": "extra", "profile": "extra", "experimental": "extra",
		"sniffer": "extra", "ebpf": "extra", "iptables": "extra", "routing-mark": "extra",
		"auto-redir": "extra", "tls": "extra", "general": "extra", "ntp": "extra",
	},
	"sing-box": {
		"inbounds": "inbounds", "route": "routes", "dns": "dns",
		"outbounds": "protocols", // special-cased below
		"log":       "extra", "experimental": "extra", "ntp": "extra", "certificate": "extra",
		"endpoints": "extra", "services": "extra",
	},
	"xray-core": {
		"inbounds": "inbounds", "routing": "routes", "dns": "dns",
		"outbounds": "protocols", // special-cased below
		"log":       "extra", "api": "extra", "stats": "extra", "policy": "extra",
		"reverse": "extra", "transport": "extra", "fakedns": "extra",
		"observatory": "extra", "burstObservatory": "extra", "metrics": "extra",
		"browserForwarder": "extra",
	},
}

var yamlTopKeyRE = regexp.MustCompile(`^(?:'([^']+)'|"([^"]+)"|([A-Za-z0-9_.-]+))\s*:`)

func yamlTopKey(line string) string {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return ""
	}
	m := yamlTopKeyRE.FindStringSubmatch(line)
	if len(m) == 0 {
		return ""
	}
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// policyTypes are the outbounds entries that belong to 「出站」 rather than
// 「协议内容」. Both kernels identify them by a different field name.
var policyTypes = map[string]map[string]bool{
	"sing-box":  {"selector": true, "urltest": true, "direct": true, "block": true, "fallback": true, "dns": true, "loopback": true},
	"xray-core": {"freedom": true, "blackhole": true, "dns": true, "loopback": true},
}

func outboundTypeField(kernel string) string {
	if kernel == "xray-core" {
		return "protocol"
	}
	return "type"
}

func isJSONKernel(kernel string) bool {
	return kernel == "sing-box" || kernel == "xray-core"
}

// Split parses a complete config and returns fresh section contents. It never
// writes to disk and never mutates p.
func Split(p Profile, content string) (SplitResult, error) {
	if _, ok := kernelInfo[p.Kernel]; !ok {
		return SplitResult{}, fmt.Errorf("未知内核 %q（可用 mihomo / sing-box / xray-core）", p.Kernel)
	}
	format := p.Format
	if format == "" {
		format = kernelInfo[p.Kernel].Format
	}
	if format != "yaml" && format != "json" {
		return SplitResult{}, fmt.Errorf("不支持的配置格式 %q", format)
	}
	if strings.TrimSpace(content) == "" {
		return SplitResult{}, fmt.Errorf("完整配置不能为空")
	}
	if err := protocols.ValidateConfig([]byte(content), format); err != nil {
		return SplitResult{}, fmt.Errorf("完整配置语法错误: %w", err)
	}

	var blocks []block
	var warnings []string
	var err error
	if format == "json" {
		blocks, warnings, err = splitJSONBlocks(content, p.Kernel)
	} else {
		blocks, warnings = splitYAMLBlocks(content)
	}
	if err != nil {
		return SplitResult{}, err
	}

	// Bucket the top-level keys. Unknown keys go to 「其他」 with a warning so
	// the user can see that nothing was thrown away.
	buckets := map[string][]string{}
	unknown := map[string]bool{}
	sep := "\n\n"
	if format == "json" {
		sep = ",\n"
	}
	for _, b := range blocks {
		sec := b.forceSection
		if sec == "" {
			var known bool
			sec, known = keySection[p.Kernel][b.key]
			if !known {
				unknown[b.key] = true
				sec = "extra"
			}
		}
		buckets[sec] = append(buckets[sec], b.text)
	}
	if len(unknown) > 0 {
		keys := make([]string, 0, len(unknown))
		for k := range unknown {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		warnings = append(warnings, "未识别的顶层键已归入「其他」："+strings.Join(keys, ", "))
	}

	sections := SectionDefs(p.Kernel)
	for i := range sections {
		sections[i].Content = strings.Join(buckets[sections[i].Key], sep)
	}

	// Report which sections actually changed, so the UI can highlight them.
	old := map[string]string{}
	for _, s := range p.Sections {
		old[s.Key] = s.Content
	}
	changed := make([]string, 0, len(sections))
	for _, s := range sections {
		if old[s.Key] != s.Content {
			changed = append(changed, s.Key)
		}
	}

	label := p.Kernel
	if info, ok := kernelInfo[p.Kernel]; ok {
		label = info.Label
	}
	msg := fmt.Sprintf("已按 %s 官方语法拆分到 %d 个分区，分段配置已同步", label, len(sections))
	return SplitResult{
		Sections: sections, Format: format, Kernel: p.Kernel,
		Message: msg, Warnings: warnings, Changed: changed,
	}, nil
}

// ------------------------------------------------------------------ YAML ---

// splitYAMLBlocks keeps the original text: every top-level key starts a new
// block, and all following indented/comment lines belong to it. Leading
// comments travel with the key they describe, so a Render(Split(x)) round
// trip keeps them instead of silently losing documentation.
func splitYAMLBlocks(content string) ([]block, []string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var blocks []block
	var pending []string // lines before the first top-level key
	var cur *block
	var curLines []string
	seen := map[string]bool{}
	var warnings []string

	flush := func() {
		if cur == nil {
			return
		}
		cur.text = strings.TrimRight(strings.Join(curLines, "\n"), "\n")
		blocks = append(blocks, *cur)
		cur, curLines = nil, nil
	}

	for _, line := range lines {
		key := yamlTopKey(line)
		if key != "" {
			flush()
			if seen[key] {
				warnings = append(warnings, "顶层键 "+key+" 出现多次，已按原文分别保留")
			}
			seen[key] = true
			cur = &block{key: key}
			curLines = append([]string{}, pending...)
			pending = nil
			curLines = append(curLines, line)
			continue
		}
		if cur != nil {
			curLines = append(curLines, line)
		} else {
			pending = append(pending, line)
		}
	}
	flush()
	if len(pending) > 0 {
		tail := strings.TrimRight(strings.Join(pending, "\n"), "\n")
		if len(blocks) > 0 && tail != "" {
			blocks[len(blocks)-1].text += "\n" + tail
		} else if tail != "" {
			// A comment-only document still has to land somewhere.
			blocks = append(blocks, block{key: "extra", text: tail, forceSection: "extra"})
		}
	}
	return blocks, warnings
}

// ------------------------------------------------------------------ JSON ---

func jsonBlock(key string, raw json.RawMessage) block {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return block{key: key, text: "  " + strconv.Quote(key) + ": " + strings.TrimSpace(string(raw))}
	}
	b, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		return block{key: key, text: "  " + strconv.Quote(key) + ": " + strings.TrimSpace(string(raw))}
	}
	return block{key: key, text: "  " + strconv.Quote(key) + ": " + string(b)}
}

// splitOutbounds divides the single native "outbounds" array into the two
// sections our editor exposes (nodes vs selector/direct/block). Render merges
// them back into one array, which is exactly how both kernels work.
func splitOutbounds(key string, raw json.RawMessage, kernel string) ([]block, []string) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return []block{jsonBlock(key, raw)}, []string{"outbounds 不是数组，已整体保留在「协议内容」"}
	}
	var nodes, policies []json.RawMessage
	field := outboundTypeField(kernel)
	for _, it := range items {
		var obj map[string]any
		typ := ""
		if json.Unmarshal(it, &obj) == nil {
			typ, _ = obj[field].(string)
		}
		if policyTypes[kernel][strings.ToLower(typ)] {
			policies = append(policies, it)
		} else {
			nodes = append(nodes, it)
		}
	}
	var out []block
	var warnings []string
	if len(nodes) > 0 {
		if b, err := json.Marshal(nodes); err == nil {
			out = append(out, block{key: key, text: jsonBlock(key, b).text, forceSection: "protocols"})
		}
	}
	if len(policies) > 0 {
		if b, err := json.Marshal(policies); err == nil {
			out = append(out, block{key: key, text: jsonBlock(key, b).text, forceSection: "outbounds"})
		}
		warnings = append(warnings, "outbounds 已按「协议内容 / 出站」重新分组；数组顺序可能变化，但 tag 引用不受影响")
	}
	if len(out) == 0 { // an explicitly empty outbounds array must survive
		out = append(out, block{key: key, text: jsonBlock(key, raw).text, forceSection: "protocols"})
	}
	return out, warnings
}

// splitJSONBlocks uses json.Decoder.Token so the original key order survives
// (a plain map would sort keys and make the round trip look different).
func splitJSONBlocks(content, kernel string) ([]block, []string, error) {
	clean := strings.TrimSpace(string(protocols.StripComments([]byte(content), "json")))
	dec := json.NewDecoder(strings.NewReader(clean))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("完整配置语法错误: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("完整配置必须是一个 JSON 对象（顶层 { ... }）")
	}
	var blocks []block
	var warnings []string
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("完整配置语法错误: %w", err)
		}
		key, ok := kt.(string)
		if !ok {
			return nil, nil, fmt.Errorf("完整配置的顶层键必须是字符串")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, fmt.Errorf("键 %q 的值解析失败: %w", key, err)
		}
		if seen[key] {
			warnings = append(warnings, "顶层键 "+key+" 出现多次，后者会覆盖前者")
		}
		seen[key] = true
		if key == "outbounds" && isJSONKernel(kernel) {
			parts, w := splitOutbounds(key, raw, kernel)
			blocks = append(blocks, parts...)
			warnings = append(warnings, w...)
			continue
		}
		blocks = append(blocks, jsonBlock(key, raw))
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, nil, fmt.Errorf("完整配置语法错误: %w", err)
	}
	if len(blocks) == 0 {
		return nil, nil, fmt.Errorf("完整配置里没有任何顶层键")
	}
	return blocks, warnings, nil
}
