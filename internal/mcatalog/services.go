package mcatalog

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/module"
)

/* ============================ 规划 5：DNS 分流 ============================ */

// domesticSuffixes covers the common mainland domains; combined with the CN
// IP CIDRs below it drives the "国内域名/IP → 国内上游，其余 → 加密国外上游"
// decision. It is deliberately a small, inspectable list rather than a huge
// GeoIP database download.
var domesticSuffixes = []string{
	".cn", ".com.cn", ".net.cn", ".org.cn", ".gov.cn", ".edu.cn", ".ac.cn",
	"baidu.com", "qq.com", "taobao.com", "tmall.com", "jd.com", "163.com",
	"126.com", "sina.com.cn", "sohu.com", "weibo.com", "bilibili.com",
	"aliyun.com", "tencent.com", "douyin.com", "xiaohongshu.com", "zhihu.com",
	"csdn.net", "huawei.com", "mi.com", "meituan.com", "didi.com", "bilivideo.com",
}

var domesticCIDRs = []string{
	"1.0.1.0/24", "1.0.2.0/23", "1.0.8.0/21", "1.0.32.0/19", "1.1.0.0/24",
	"1.2.0.0/23", "1.8.0.0/16", "1.10.0.0/15", "1.48.0.0/15", "14.0.0.0/11",
	"27.0.0.0/10", "36.0.0.0/12", "39.0.0.0/11", "42.0.0.0/10", "43.224.0.0/12",
	"45.64.0.0/14", "47.64.0.0/12", "49.64.0.0/13", "58.0.0.0/12", "59.32.0.0/12",
	"60.0.0.0/11", "61.0.0.0/10", "101.0.0.0/8", "103.0.0.0/10", "106.0.0.0/10",
	"110.0.0.0/9", "112.0.0.0/10", "113.0.0.0/9", "114.0.0.0/10", "115.0.0.0/9",
	"116.0.0.0/9", "117.0.0.0/8", "118.0.0.0/9", "119.0.0.0/8", "120.0.0.0/9",
	"121.0.0.0/9", "122.0.0.0/8", "123.0.0.0/9", "124.0.0.0/9", "125.0.0.0/9",
	"171.0.0.0/9", "175.0.0.0/9", "180.64.0.0/10", "182.0.0.0/8", "183.0.0.0/9",
	"202.0.0.0/9", "210.0.0.0/9", "211.64.0.0/10", "218.0.0.0/8", "219.64.0.0/10",
	"220.160.0.0/11", "221.0.0.0/9", "222.0.0.0/8", "223.0.0.0/9",
}

// DNSChoice is the outcome of one resolution decision, kept explicit so the
// console can show WHY an upstream was picked instead of a black box.
type DNSChoice struct {
	Region    string `json:"region"` // domestic | foreign
	Upstream  string `json:"upstream"`
	Reason    string `json:"reason"`
	Encrypted bool   `json:"encrypted"`
	LeakSafe  bool   `json:"leak_safe"`
}

func isDomesticDomain(domain string) bool {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	for _, s := range domesticSuffixes {
		if d == s || strings.HasSuffix(d, s) || strings.HasSuffix(d, "."+strings.TrimPrefix(s, ".")) {
			return true
		}
	}
	return false
}

func isDomesticIP(ip string) bool {
	addr := net.ParseIP(strings.TrimSpace(ip))
	if addr == nil {
		return false
	}
	for _, cidr := range domesticCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil && n.Contains(addr) {
			return true
		}
	}
	return false
}

// ResolveDNS is 规划 5: pick the upstream from the domain / resolved IP.
// With leak protection on, a domestic name NEVER goes to a foreign upstream.
func ResolveDNS(p DNSPolicy, domain, resolvedIP string) DNSChoice {
	domestic := p.Domestic
	foreign := p.Foreign
	if len(domestic) == 0 {
		domestic = []string{"https://dns.alidns.com/dns-query"}
	}
	if len(foreign) == 0 {
		foreign = []string{"https://cloudflare-dns.com/dns-query"}
	}
	byDomain := isDomesticDomain(domain)
	byIP := resolvedIP != "" && isDomesticIP(resolvedIP)
	// Extra CIDRs configured by the operator take priority over the built-in set.
	for _, cidr := range p.DomesticIPCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		ip := net.ParseIP(strings.TrimSpace(resolvedIP))
		if err == nil && ip != nil && n.Contains(ip) {
			byIP = true
		}
	}

	region := "foreign"
	reason := "域名与 IP 均不属于国内，走加密国外上游"
	upstream := foreign[0]
	if p.Strategy == "all-domestic" {
		region, upstream, reason = "domestic", domestic[0], "策略指定全部走国内上游"
	} else if p.Strategy == "all-foreign" {
		region, upstream, reason = "foreign", foreign[0], "策略指定全部走国外上游"
	} else if byDomain || byIP {
		region, upstream = "domestic", domestic[0]
		if byDomain && byIP {
			reason = "国内域名 + 国内 IP，走国内上游"
		} else if byDomain {
			reason = "国内域名，走国内上游"
		} else {
			reason = "国内 IP，走国内上游"
		}
	}

	encrypted := p.Encrypt != "" && p.Encrypt != "none" && p.Encrypt != module.DNSEncryptNone
	leakSafe := !p.LeakProtection || region == "domestic" || encrypted
	if p.LeakProtection && region == "foreign" && !encrypted {
		return DNSChoice{
			Region: region, Upstream: "", Encrypted: false, LeakSafe: false,
			Reason: "防泄密已开启但国外上游未加密：拒绝明文解析，请在 DNS 里启用 DoH / DoT / DoQ",
		}
	}
	return DNSChoice{
		Region: region, Upstream: upstream, Reason: reason,
		Encrypted: encrypted, LeakSafe: leakSafe,
	}
}

/* ============================ 规划 9：链接转换 ============================ */

// ClientTarget maps a client app to the subscription renderer target.
// The renderer lives in internal/subscription and is left untouched.
var ClientTarget = map[string]string{
	"clash":        "clash",
	"clash-meta":   "clash",
	"stash":        "clash",
	"shadowrocket": "surge",
	"surge":        "surge",
	"sing-box":     "singbox",
	"v2rayng":      "v2ray",
	"v2rayn":       "v2ray",
	"socks":        "socks",
	"sub-store":    "substore",
}

// Clients is the canonical display order.
var Clients = func() []string {
	out := make([]string, 0, len(ClientTarget))
	for k := range ClientTarget {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}()

// deepLink turns a subscription URL into a client-recognisable import link.
func deepLink(client, target, subURL string) string {
	escaped := url.QueryEscape(subURL)
	switch client {
	case "clash", "clash-meta", "stash":
		return "clash://install-config?url=" + escaped
	case "sing-box":
		return "sing-box://import-remote-profile?url=" + escaped
	case "surge":
		return "surge:///install-config?url=" + escaped
	case "shadowrocket":
		return "shadowrocket://add/sub://" + url.QueryEscape(strings.TrimPrefix(subURL, "https://"))
	case "v2rayng", "v2rayn":
		return subURL // v2ray clients import the subscription URL directly
	default:
		return subURL
	}
}

// BuildLinks is 规划 9: derive every client-specific link from ONE token.
func BuildLinks(user User, baseURL string, clients []string) []Link {
	if len(clients) == 0 {
		clients = Clients
	}
	base := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "/api/v1/sub"
	}
	out := make([]Link, 0, len(clients))
	now := time.Now().UTC()
	for _, client := range clients {
		target, ok := ClientTarget[client]
		if !ok {
			continue
		}
		sub := fmt.Sprintf("%s/%s?target=%s", base, user.Token, target)
		out = append(out, Link{
			ID: newID("link"), UserID: user.ID, Client: client, Target: target,
			URL: deepLink(client, target, sub), CreatedAt: now,
		})
	}
	return out
}

/* ============================ 规划 7：模块打包 ============================ */

// Selection identifies which catalog entries go into a new module.
type Selection struct {
	Name             string              `json:"name"`
	Note             string              `json:"note"`
	KernelIDs        []string            `json:"kernel_ids"`
	ProtocolGroupIDs []string            `json:"protocol_group_ids"`
	RuleIDs          []string            `json:"rule_ids"`
	RuleSetIDs       []string            `json:"rule_set_ids"`
	DNSID            string              `json:"dns_id"`
	PolicyGroupIDs   []string            `json:"policy_group_ids"`
	NodeIDs          []string            `json:"node_ids"`
	Master           module.MasterConfig `json:"master"`
	Nodes            []module.NodeSpec   `json:"-"`
}

func pick[T any](list []T, id func(T) string, ids []string, label string) ([]T, error) {
	want := map[string]bool{}
	for _, x := range ids {
		want[x] = true
	}
	out := make([]T, 0, len(ids))
	for _, item := range list {
		if want[id(item)] {
			out = append(out, item)
			delete(want, id(item))
		}
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for k := range want {
			missing = append(missing, k)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("%s 中存在未创建的 ID：%s", label, strings.Join(missing, ", "))
	}
	return out, nil
}

// BuildModule is 规划 7: collect 节点 + 内核 + 协议 + 规则 + DNS + 策略组
// + NGG 主内核 into ONE aggregation root, then validate referential integrity.
func BuildModule(c Catalog, sel Selection) (module.Module, error) {
	if strings.TrimSpace(sel.Name) == "" {
		return module.Module{}, errors.New("模块名称不能为空")
	}
	kernels, err := pick(c.Kernels, func(x Kernel) string { return x.ID }, sel.KernelIDs, "内核")
	if err != nil {
		return module.Module{}, err
	}
	groups, err := pick(c.ProtocolGroups, func(x ProtocolGroup) string { return x.ID }, sel.ProtocolGroupIDs, "协议组")
	if err != nil {
		return module.Module{}, err
	}
	rules, err := pick(c.Rules, func(x Rule) string { return x.ID }, sel.RuleIDs, "规则")
	if err != nil {
		return module.Module{}, err
	}
	sets, err := pick(c.RuleSets, func(x RuleSet) string { return x.ID }, sel.RuleSetIDs, "规则集")
	if err != nil {
		return module.Module{}, err
	}
	policies, err := pick(c.PolicyGroups, func(x PolicyGroup) string { return x.ID }, sel.PolicyGroupIDs, "策略组")
	if err != nil {
		return module.Module{}, err
	}

	m := module.Module{
		ID: newID("mod"), Name: sel.Name, Note: sel.Note, Enabled: true,
		Master: sel.Master, Nodes: sel.Nodes,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	for _, k := range kernels {
		m.Kernels = append(m.Kernels, module.KernelSlot{
			Kernel: k.ID, Enabled: k.Enabled, AutoStart: k.AutoStart,
		})
	}
	for _, g := range groups {
		m.ProtocolGroups = append(m.ProtocolGroups, module.ProtocolGroup{
			ID: g.ID, Name: g.Name, KernelIDs: g.KernelIDs, Protocols: g.Protocols, Enabled: g.Enabled,
		})
	}
	for _, r := range rules {
		m.Rules = append(m.Rules, module.RuleSpec{
			ID: r.ID, Name: r.Name, Type: r.Type, Pattern: r.Pattern, Outbound: r.Outbound, Enabled: r.Enabled,
		})
	}
	for _, rs := range sets {
		m.RuleSets = append(m.RuleSets, module.RuleSetRef{
			ID: rs.ID, Name: rs.Name, Source: rs.Source, Enabled: rs.Enabled,
		})
	}
	for _, p := range policies {
		m.PolicyGroups = append(m.PolicyGroups, module.PolicyGroup{
			ID: p.ID, Name: p.Name, Mode: p.Mode, RuleSetIDs: p.RuleSetIDs, Enabled: p.Enabled,
		})
	}
	if sel.DNSID != "" {
		found := false
		for _, d := range c.DNS {
			if d.ID == sel.DNSID {
				m.DNS = module.DNSConfig{
					ID: d.ID, Name: d.Name, Enabled: d.Enabled,
					LeakProtection: d.LeakProtection, Encrypt: d.Encrypt,
					Domestic: d.Domestic, Foreign: d.Foreign, Strategy: d.Strategy,
					DomesticIPCIDRs: d.DomesticIPCIDRs,
				}
				found = true
				break
			}
		}
		if !found {
			return module.Module{}, fmt.Errorf("DNS %q 不存在", sel.DNSID)
		}
	}
	if len(m.Nodes) == 0 && m.Master.EgressNodeID != "" {
		return module.Module{}, errors.New("主内核指定了出口节点，但模块没有选择节点")
	}
	for _, issue := range m.Validate() {
		if issue.Level == "error" {
			return module.Module{}, fmt.Errorf("%s: %s", issue.Field, issue.Detail)
		}
	}
	return m, nil
}

/* ============================ 规划 10：主内核总控 ============================ */

// RegulatePlan is the concrete set of actions the NGG master kernel must take
// to bring reality in line with the module. It is computed, not guessed.
type RegulatePlan struct {
	ModuleID     string   `json:"module_id"`
	StartKernels []string `json:"start_kernels"`
	StopKernels  []string `json:"stop_kernels"`
	Groups       []string `json:"protocol_groups"`
	Rules        []string `json:"rules"`
	RuleSets     []string `json:"rule_sets"`
	DNSPolicy    string   `json:"dns_policy"`
	Policies     []string `json:"policy_groups"`
	EgressNodeID string   `json:"egress_node_id"`
	Actions      []string `json:"actions"`
}

// Regulate is 规划 10: read the module and produce the full control plan.
// `running` is the set of kernels currently up; the difference drives start /
// stop actions so the master can self-correct.
func Regulate(m module.Module, running []string) RegulatePlan {
	up := map[string]bool{}
	for _, id := range running {
		up[id] = true
	}
	want := map[string]bool{}
	for _, k := range m.Kernels {
		if k.Enabled {
			want[k.Kernel] = true
		}
	}
	p := RegulatePlan{ModuleID: m.ID, EgressNodeID: m.Master.EgressNodeID}
	for _, id := range module.KernelIDs {
		if want[id] && !up[id] {
			p.StartKernels = append(p.StartKernels, id)
			p.Actions = append(p.Actions, "启动内核 "+id)
		}
		if !want[id] && up[id] {
			p.StopKernels = append(p.StopKernels, id)
			p.Actions = append(p.Actions, "停止内核 "+id)
		}
	}
	for _, g := range m.ProtocolGroups {
		if g.Enabled {
			p.Groups = append(p.Groups, g.Name)
		}
	}
	for _, r := range m.Rules {
		if r.Enabled {
			p.Rules = append(p.Rules, r.Name)
		}
	}
	for _, rs := range m.RuleSets {
		if rs.Enabled {
			p.RuleSets = append(p.RuleSets, rs.Name)
		}
	}
	for _, pg := range m.PolicyGroups {
		if pg.Enabled {
			p.Policies = append(p.Policies, pg.Name)
		}
	}
	if m.DNS.Enabled {
		p.DNSPolicy = m.DNS.Name
		p.Actions = append(p.Actions, "下发 DNS 策略 "+m.DNS.Name+"（"+m.DNS.Strategy+"/"+m.DNS.Encrypt+"）")
	}
	if len(p.Groups) > 0 {
		p.Actions = append(p.Actions, fmt.Sprintf("下发 %d 个协议组", len(p.Groups)))
	}
	if len(p.Rules) > 0 {
		p.Actions = append(p.Actions, fmt.Sprintf("下发 %d 条规则与 %d 个规则集", len(p.Rules), len(p.RuleSets)))
	}
	if len(p.Policies) > 0 {
		p.Actions = append(p.Actions, fmt.Sprintf("下发 %d 个策略组", len(p.Policies)))
	}
	if p.EgressNodeID != "" {
		p.Actions = append(p.Actions, "出口指向 "+p.EgressNodeID)
	}
	if len(p.Actions) == 0 {
		p.Actions = append(p.Actions, "模块状态已一致，无需调控")
	}
	return p
}
