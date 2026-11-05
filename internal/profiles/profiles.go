// Package profiles implements the editable "配置文件" (Profile) cards shown
// in 协议配置. A profile is one complete kernel configuration split into
// readable, independently editable sections: 协议内容 / 出站 / 入站 / 路由规则
// / DNS / 其他. Each section uses the target kernel's real, official syntax
// and carries inline comments explaining what each line means.
package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/protocols"
)

// Section is one editable block of a profile.
type Section struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Hint    string `json:"hint"`
	DocURL  string `json:"doc_url"`
	Content string `json:"content"`
}

// Profile is one configuration file card.
type Profile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kernel      string    `json:"kernel"` // mihomo | sing-box | xray-core
	Format      string    `json:"format"` // yaml | json
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	Default     bool      `json:"default"`
	Sections    []Section `json:"sections"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

// SectionKeys is the fixed display order. Keeping it server-side stops the UI
// from inventing sections and makes save/validate deterministic.
// Order is user-visible: 协议 → 入站 → 出站 → 路由规则 → DNS → 其他.
var SectionKeys = []string{"protocols", "inbounds", "outbounds", "routes", "dns", "extra"}

var sectionMeta = map[string]struct{ Label, Hint, Doc string }{
	"protocols": {"协议内容", "代理节点 / 出站协议条目；每个内核的字段名不同，切换内核会整段换成对应官方语法。", ""},
	"outbounds": {"出站", "选择哪个代理出口、负载均衡与直连/阻断策略。", ""},
	"inbounds":  {"入站", "本地监听入口（HTTP / SOCKS5 / mixed / TUN 等），决定流量从哪里进来。", ""},
	"routes":    {"路由规则", "按域名、IP、端口、进程决定走哪个出站；从上到下匹配，最后一条通常是兜底。", ""},
	"dns":       {"DNS", "DNS 服务器、分流与 Fake-IP 等设置。", ""},
	"extra":     {"其他", "日志级别、全局模式、实验性选项等顶层设置。", ""},
}

var kernelInfo = map[string]struct {
	Label, Format, Doc string
}{
	"mihomo":    {"Mihomo / Clash.Meta", "yaml", protocols.DocMihomo},
	"sing-box":  {"sing-box", "json", protocols.DocSingbox},
	"xray-core": {"Xray-core", "json", protocols.DocXray},
}

// KernelOptions drives the "新建配置文件" selector.
func KernelOptions() []map[string]string {
	out := make([]map[string]string, 0, len(kernelInfo))
	for _, k := range []string{"mihomo", "sing-box", "xray-core"} {
		v := kernelInfo[k]
		out = append(out, map[string]string{"kernel": k, "label": v.Label, "format": v.Format, "doc_url": v.Doc})
	}
	return out
}

// SectionDefs describes the six blocks for a kernel, used by the UI labels.
func SectionDefs(kernel string) []Section {
	doc := ""
	if v, ok := kernelInfo[kernel]; ok {
		doc = v.Doc
	}
	out := make([]Section, 0, len(SectionKeys))
	for _, k := range SectionKeys {
		m := sectionMeta[k]
		out = append(out, Section{Key: k, Label: m.Label, Hint: m.Hint, DocURL: doc})
	}
	return out
}

func slugID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = regexp.MustCompile(`[^a-z0-9\x{4e00}-\x{9fff}]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "profile"
	}
	return s + "-" + time.Now().UTC().Format("20060102150405")
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// ------------------------------------------------------------- templates ---

// Template returns a real, parseable starter configuration for a kernel.
// Comments explain every line; validation strips them before parsing.
func Template(kernel, name string) Profile {
	info, ok := kernelInfo[kernel]
	if !ok {
		kernel = "mihomo"
		info = kernelInfo[kernel]
	}
	p := Profile{
		ID: slugID(name), Name: name, Kernel: kernel, Format: info.Format,
		Description: kernel + " 默认配置文件", Enabled: true, Default: false,
		CreatedAt: now(), UpdatedAt: now(),
	}
	p.Sections = templateSections(kernel)
	return p
}

func templateSections(kernel string) []Section {
	var bodies map[string]string
	switch kernel {
	case "sing-box":
		bodies = singBoxTemplate()
	case "xray-core":
		bodies = xrayTemplate()
	default:
		bodies = mihomoTemplate()
	}
	out := make([]Section, 0, len(SectionKeys))
	for _, k := range SectionKeys {
		m := sectionMeta[k]
		doc := kernelInfo[kernel].Doc
		out = append(out, Section{
			Key: k, Label: m.Label, Hint: m.Hint, DocURL: doc,
			Content: bodies[k],
		})
	}
	return out
}

func mihomoTemplate() map[string]string {
	return map[string]string{
		"protocols": `# 协议内容 / proxies
# 每个代理节点写一项；下方的 proxy-groups 与 rules 用 name 引用它。
# type 必须是 Mihomo 支持的协议名，例如 ss / vmess / vless / trojan /
# hysteria2 / tuic / wireguard / anytls / mieru / snell / ssh。
# 官方文档：https://wiki.metacubex.one/config/proxies/
proxies:
  - name: example-vmess
    type: vmess
    server: 127.0.0.1
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    alterId: 0
    cipher: auto
    tls: true
    udp: true`,
		"outbounds": `# 出站 / proxy-groups
# select 手动选择，url-test 自动选延迟最低，fallback 故障转移，
# load-balance 负载均衡；DIRECT 与 REJECT 是 Mihomo 内置策略。
proxy-groups:
  - name: PROXY
    type: select
    proxies:
      - example-vmess
      - DIRECT`,
		"inbounds": `# 入站 / listeners
# 本地监听入口。mixed 同时提供 HTTP 与 SOCKS5；也可用 port /
# socks-port / redir-port / tun 等传统顶层字段。
listeners:
  - name: mixed-in
    type: mixed
    listen: 127.0.0.1
    port: 7890`,
		"routes": `# 路由规则 / rules
# 从上到下逐条匹配，最后一条 MATCH 是兜底。
# 常见类型：DOMAIN / DOMAIN-SUFFIX / IP-CIDR / GEOIP / PROCESS-NAME / MATCH
rules:
  - DOMAIN-SUFFIX,example.com,PROXY
  - GEOIP,CN,DIRECT
  - MATCH,PROXY`,
		"dns": `# DNS
# enable: 开启 Mihomo 内置 DNS；enhanced-mode: fake-ip 可减少 DNS 泄露。
dns:
  enable: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  nameserver:
    - 223.5.5.5
    - 119.29.29.29`,
		"extra": `# 其他 / 全局设置
# mode: rule(规则) / global(全局) / direct(直连)
mode: rule
log-level: info
allow-lan: false`,
	}
}

func singBoxTemplate() map[string]string {
	return map[string]string{
		"protocols": `// 协议内容 / outbounds
// 每个出站对象必须有 type 与 tag；路由规则用 tag 引用。
// 字段用下划线风格：server_port / uuid / password。
// 官方文档：https://sing-box.sagernet.org/configuration/outbound/
"outbounds": [
  {
    "type": "vmess",
    "tag": "example-vmess",
    "server": "127.0.0.1",
    "server_port": 443,
    "uuid": "00000000-0000-0000-0000-000000000000",
    "security": "auto",
    "tls": { "enabled": true, "server_name": "example.com" }
  }
]`,
		"outbounds": `// 出站 / 选择器与直连
// 保存时会与上一段的 outbounds 合并为同一个数组。
// urltest 自动测速，selector 手动选择，direct 直连，block 阻断。
"outbounds": [
  { "type": "selector", "tag": "PROXY", "outbounds": ["example-vmess", "DIRECT"] },
  { "type": "direct", "tag": "DIRECT" },
  { "type": "block", "tag": "REJECT" }
]`,
		"inbounds": `// 入站 / inbounds
// mixed 同时提供 HTTP 与 SOCKS5；tun 需要虚拟网卡与 CAP_NET_ADMIN。
"inbounds": [
  { "type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 7890 }
]`,
		"routes": `// 路由规则 / route.rules
// 从上到下匹配；outbound 填上面定义的 tag。
// 官方文档：https://sing-box.sagernet.org/configuration/route/
"route": {
  "rules": [
    { "domain_suffix": ["example.com"], "outbound": "PROXY" },
    { "geoip": ["cn"], "outbound": "DIRECT" }
  ],
  "default_outbound": "PROXY",
  "auto_detect_interface": true
}`,
		"dns": `// DNS / dns
// servers 里可写 UDP / DoH / DoT；strategy 控制 IPv4/IPv6 优先级。
"dns": {
  "servers": [
    { "tag": "local", "address": "223.5.5.5", "detour": "DIRECT" }
  ],
  "strategy": "prefer_ipv4"
}`,
		"extra": `// 其他 / 日志与全局
"log": { "level": "info", "timestamp": true }`,
	}
}

func xrayTemplate() map[string]string {
	return map[string]string{
		"protocols": `// 协议内容 / outbounds
// 每个对象有 protocol 与 tag；settings.vnext[].users[] 放节点用户。
// 官方文档：https://xtls.github.io/config/outbounds/
"outbounds": [
  {
    "tag": "example-vmess",
    "protocol": "vmess",
    "settings": {
      "vnext": [{
        "address": "127.0.0.1",
        "port": 443,
        "users": [{ "id": "00000000-0000-0000-0000-000000000000", "alterId": 0 }]
      }]
    },
    "streamSettings": { "network": "tcp", "security": "tls" }
  }
]`,
		"outbounds": `// 出站 / 直连与阻断
// 保存时会与上一段的 outbounds 合并为同一个数组。
// freedom 直连，blackhole 阻断；路由规则用 tag 引用。
"outbounds": [
  { "tag": "DIRECT", "protocol": "freedom" },
  { "tag": "REJECT", "protocol": "blackhole" }
]`,
		"inbounds": `// 入站 / inbounds
// 本地监听入口；dokodemo-door 用于透明代理/tproxy。
"inbounds": [
  { "tag": "socks-in", "protocol": "socks", "listen": "127.0.0.1", "port": 1080,
    "settings": { "auth": "noauth", "udp": true } }
]`,
		"routes": `// 路由规则 / routing.rules
// 从上到下匹配；outboundTag 填上面定义的 tag。
// 官方文档：https://xtls.github.io/config/routing/
"routing": {
  "domainStrategy": "IPIfNonMatch",
  "rules": [
    { "type": "field", "domain": ["geosite:cn"], "outboundTag": "DIRECT" },
    { "type": "field", "ip": ["geoip:cn"], "outboundTag": "DIRECT" }
  ]
}`,
		"dns": `// DNS / dns
// servers 可写普通 UDP 或 DoH 地址；Xray 会按路由规则分流查询。
"dns": {
  "servers": ["223.5.5.5", "119.29.29.29"]
}`,
		"extra": `// 其他 / 日志
"log": { "loglevel": "info", "access": "", "error": "" }`,
	}
}

// ------------------------------------------------------------- rendering ---

// mergeJSONSections merges top-level keys across sections. When the same key
// appears in two sections and both values are arrays (e.g. sing-box
// outbounds), they are concatenated, which is the real kernel semantics.
func mergeJSONSections(bodies []string) (string, error) {
	merged := map[string]json.RawMessage{}
	order := []string{}
	for _, b := range bodies {
		clean := strings.TrimSpace(string(protocols.StripComments([]byte(b), "json")))
		clean = strings.TrimSuffix(clean, ",")
		if clean == "" {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte("{"+clean+"}"), &obj); err != nil {
			return "", fmt.Errorf("段落不是合法的 JSON 键值片段: %w", err)
		}
		for k, v := range obj {
			if prev, ok := merged[k]; ok {
				var a, c []json.RawMessage
				if json.Unmarshal(prev, &a) == nil && json.Unmarshal(v, &c) == nil {
					a = append(a, c...)
					if b, err := json.Marshal(a); err == nil {
						merged[k] = b
						continue
					}
				}
			} else {
				order = append(order, k)
			}
			merged[k] = v
		}
	}
	// json.Marshal on a map sorts keys, which is fine for a config file, but
	// keep the append order for readability by building the text manually.
	var out []string
	for _, k := range order {
		key, _ := json.Marshal(k)
		var pretty any
		if json.Unmarshal(merged[k], &pretty) == nil {
			if b, err := json.MarshalIndent(pretty, "  ", "  "); err == nil {
				out = append(out, "  "+string(key)+": "+string(b))
				continue
			}
		}
		out = append(out, "  "+string(key)+": "+string(merged[k]))
	}
	return "{\n" + strings.Join(out, ",\n") + "\n}", nil
}

// Render produces the complete, parseable kernel configuration text for a
// profile. It is what "查看完整配置" shows and what gets exported.
func Render(p Profile) (content, format string, err error) {
	format = p.Format
	if format == "" {
		format = kernelInfo[p.Kernel].Format
	}
	bodies := orderedBodies(p)
	if format == "yaml" {
		var chunks []string
		for _, s := range p.Sections {
			if strings.TrimSpace(s.Content) == "" {
				continue
			}
			chunks = append(chunks, strings.TrimRight(s.Content, "\n"))
		}
		return strings.Join(chunks, "\n\n") + "\n", format, nil
	}
	merged, err := mergeJSONSections(bodies)
	if err != nil {
		return "", format, err
	}
	return merged, format, nil
}

func orderedBodies(p Profile) []string {
	byKey := map[string]string{}
	for _, s := range p.Sections {
		byKey[s.Key] = s.Content
	}
	out := make([]string, 0, len(SectionKeys))
	for _, k := range SectionKeys {
		if v, ok := byKey[k]; ok {
			out = append(out, v)
		}
	}
	return out
}

// normalizeSections keeps the user-visible order stable even for profiles
// saved by an older release (or edited through the raw API). Unknown sections
// are appended at the end rather than silently dropped.
func normalizeSections(p *Profile) {
	byKey := map[string]Section{}
	for _, s := range p.Sections {
		byKey[s.Key] = s
	}
	out := make([]Section, 0, len(byKey))
	for _, k := range SectionKeys {
		if s, ok := byKey[k]; ok {
			out = append(out, s)
		} else if m, ok := sectionMeta[k]; ok {
			out = append(out, Section{Key: k, Label: m.Label, Hint: m.Hint})
		}
	}
	for _, s := range p.Sections {
		known := false
		for _, k := range SectionKeys {
			if s.Key == k {
				known = true
				break
			}
		}
		if !known {
			out = append(out, s)
		}
	}
	p.Sections = out
}

// Validate checks every section's syntax and the assembled config. It never
// writes anything to disk.
func Validate(p Profile) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("配置文件名称不能为空")
	}
	if _, ok := kernelInfo[p.Kernel]; !ok {
		return fmt.Errorf("未知内核 %q（可用 mihomo / sing-box / xray-core）", p.Kernel)
	}
	format := kernelInfo[p.Kernel].Format
	for _, s := range p.Sections {
		if strings.TrimSpace(s.Content) == "" {
			continue
		}
		if format == "json" {
			clean := strings.TrimSpace(string(protocols.StripComments([]byte(s.Content), "json")))
			if err := protocols.ValidateConfig([]byte("{"+strings.TrimSuffix(clean, ",")+"}"), "json"); err != nil {
				return fmt.Errorf("%s 段语法错误: %w", s.Label, err)
			}
		} else if err := protocols.ValidateConfig([]byte(s.Content), "yaml"); err != nil {
			return fmt.Errorf("%s 段语法错误: %w", s.Label, err)
		}
	}
	content, f, err := Render(p)
	if err != nil {
		return err
	}
	if err := protocols.ValidateConfig([]byte(content), f); err != nil {
		return fmt.Errorf("合并后的完整配置语法错误: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------- store ---

type diskState struct {
	Profiles []Profile `json:"profiles"`
	Active   string    `json:"active"`
}

// Store persists profiles to one JSON file with atomic writes.
type Store struct {
	mu   sync.RWMutex
	path string
	data diskState
}

func NewStore(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "profiles.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(s.data.Profiles) == 0 {
		def := Template("mihomo", "Default-Profile")
		def.Default = true
		s.data.Profiles = []Profile{def}
		s.data.Active = def.ID
		if err := s.persist(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns profiles sorted by name, plus the active id.
func (s *Store) List() ([]Profile, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Profile(nil), s.data.Profiles...)
	for i := range out {
		normalizeSections(&out[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, s.data.Active
}

func (s *Store) Get(id string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.data.Profiles {
		if p.ID == id {
			normalizeSections(&p)
			return p, true
		}
	}
	return Profile{}, false
}

// Create validates and stores a new profile. Name must be unique.
func (s *Store) Create(p Profile) (Profile, error) {
	if err := Validate(p); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.data.Profiles {
		if strings.EqualFold(e.Name, p.Name) {
			return Profile{}, fmt.Errorf("已存在同名配置文件 %q", p.Name)
		}
	}
	if p.ID == "" {
		p.ID = slugID(p.Name)
	}
	for _, e := range s.data.Profiles {
		if e.ID == p.ID {
			return Profile{}, fmt.Errorf("配置文件 ID 冲突: %s", p.ID)
		}
	}
	p.CreatedAt, p.UpdatedAt = now(), now()
	before := append([]Profile(nil), s.data.Profiles...)
	active := s.data.Active
	if p.Default {
		for i := range s.data.Profiles {
			s.data.Profiles[i].Default = false
		}
		s.data.Active = p.ID
	}
	s.data.Profiles = append(s.data.Profiles, p)
	if err := s.persist(); err != nil {
		s.data.Profiles, s.data.Active = before, active
		return Profile{}, fmt.Errorf("保存配置文件失败: %w", err)
	}
	return p, nil
}

// Update replaces an existing profile by id. Kernel/format changes rebuild the
// section set only when the caller did not supply sections explicitly.
func (s *Store) Update(id string, p Profile) (Profile, error) {
	if err := Validate(p); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, e := range s.data.Profiles {
		if e.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Profile{}, fmt.Errorf("配置文件 %s 不存在", id)
	}
	for i, e := range s.data.Profiles {
		if i != idx && strings.EqualFold(e.Name, p.Name) {
			return Profile{}, fmt.Errorf("已存在同名配置文件 %q", p.Name)
		}
	}
	old := s.data.Profiles[idx]
	p.ID = id
	p.CreatedAt = old.CreatedAt
	p.UpdatedAt = now()
	p.Format = kernelInfo[p.Kernel].Format
	before := append([]Profile(nil), s.data.Profiles...)
	active := s.data.Active
	if p.Default {
		for i := range s.data.Profiles {
			s.data.Profiles[i].Default = false
		}
		s.data.Active = id
	}
	s.data.Profiles[idx] = p
	if err := s.persist(); err != nil {
		s.data.Profiles, s.data.Active = before, active
		return Profile{}, fmt.Errorf("保存配置文件失败: %w", err)
	}
	return p, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.data.Profiles {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("配置文件 %s 不存在", id)
	}
	if s.data.Profiles[idx].Default && len(s.data.Profiles) > 1 {
		return fmt.Errorf("不能删除默认配置文件；请先把另一个配置文件设为默认")
	}
	before := append([]Profile(nil), s.data.Profiles...)
	active := s.data.Active
	s.data.Profiles = append(s.data.Profiles[:idx], s.data.Profiles[idx+1:]...)
	if active == id && len(s.data.Profiles) > 0 {
		s.data.Active = s.data.Profiles[0].ID
		s.data.Profiles[0].Default = true
	}
	if err := s.persist(); err != nil {
		s.data.Profiles, s.data.Active = before, active
		return fmt.Errorf("删除失败: %w", err)
	}
	return nil
}

// Duplicate copies a profile under a new name.
func (s *Store) Duplicate(id, name string) (Profile, error) {
	src, ok := s.Get(id)
	if !ok {
		return Profile{}, fmt.Errorf("配置文件 %s 不存在", id)
	}
	cp := src
	cp.ID, cp.Name, cp.Default, cp.Enabled = "", name, false, false
	cp.Description = src.Description + " (副本)"
	return s.Create(cp)
}

// SetEnabled flips one profile, a selection, or all profiles. Profiles of
// different kernels are independent: enabling several at once is valid, each
// stays bound to its own kernel. A failed persist rolls every change back.
func (s *Store) SetEnabled(ids []string, all bool, enabled bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	before := append([]Profile(nil), s.data.Profiles...)
	changed := 0
	for i := range s.data.Profiles {
		if !all && !selected[s.data.Profiles[i].ID] {
			continue
		}
		if s.data.Profiles[i].Enabled != enabled {
			s.data.Profiles[i].Enabled = enabled
			s.data.Profiles[i].UpdatedAt = now()
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := s.persist(); err != nil {
		s.data.Profiles = before
		return 0, fmt.Errorf("保存启用状态失败: %w", err)
	}
	return changed, nil
}

// SetActive marks one profile as the default/active configuration.
func (s *Store) SetActive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	before := append([]Profile(nil), s.data.Profiles...)
	active := s.data.Active
	for i := range s.data.Profiles {
		s.data.Profiles[i].Default = s.data.Profiles[i].ID == id
		if s.data.Profiles[i].ID == id {
			found = true
		}
	}
	if !found {
		s.data.Profiles = before
		return fmt.Errorf("配置文件 %s 不存在", id)
	}
	s.data.Active = id
	if err := s.persist(); err != nil {
		s.data.Profiles, s.data.Active = before, active
		return fmt.Errorf("切换默认配置失败: %w", err)
	}
	return nil
}
