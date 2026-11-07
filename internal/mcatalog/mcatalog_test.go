package mcatalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/module"
)

func seeded(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 规划 3~6 的种子数据（按顺序，不跳号）
	mustUpsert(t, s, "rule-sets", `{"name":"国内域名"}`)
	mustUpsert(t, s, "rules", `{"name":"直连国内","type":"domain","pattern":"cn","enabled":true}`)
	mustUpsert(t, s, "dns", `{"name":"分流DNS","enabled":true,"leak_protection":true,"encrypt":"doh","strategy":"geo","domestic":["https://dns.alidns.com/dns-query"],"foreign":["https://cloudflare-dns.com/dns-query"]}`)
	mustUpsert(t, s, "policy-groups", `{"name":"默认策略","mode":"select"}`)
	mustUpsert(t, s, "protocol-groups", `{"name":"全内核组","kernel_ids":["mihomo","xray-core","sing-box"],"enabled":true}`)
	return s
}

func mustUpsert(t *testing.T, s *Store, kind, raw string) {
	t.Helper()
	if _, err := s.Upsert(kind, []byte(raw)); err != nil {
		t.Fatalf("upsert %s: %v", kind, err)
	}
}

// 规划 2：三内核 single / multiple / all。
func TestResolveStartModes(t *testing.T) {
	s := seeded(t)
	c := s.Get()
	all, err := ResolveStart(c, module.StartModeAll, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %v, %v", all, err)
	}
	one, err := ResolveStart(c, module.StartModeSingle, []string{"xray-core"})
	if err != nil || len(one) != 1 || one[0] != "xray-core" {
		t.Fatalf("single = %v, %v", one, err)
	}
	two, err := ResolveStart(c, module.StartModeMultiple, []string{"mihomo", "sing-box"})
	if err != nil || len(two) != 2 {
		t.Fatalf("multiple = %v, %v", two, err)
	}
	if _, err := ResolveStart(c, module.StartModeSingle, []string{"mihomo", "sing-box"}); err == nil {
		t.Fatal("single 传两个内核必须失败")
	}
	if _, err := ResolveStart(c, module.StartModeAll, []string{"mihomo"}); err == nil {
		t.Fatal("all 传 ids 必须失败")
	}
	if _, err := ResolveStart(c, "", []string{"not-declared"}); err == nil {
		t.Fatal("未声明内核必须失败")
	}
	if err := s.SetKernelStates([]string{"mihomo"}, "running"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get().RunningKernels(); len(got) != 1 || got[0] != "mihomo" {
		t.Fatalf("运行态 = %v", got)
	}
}

// 规划 3/6：协议组绑三内核、策略组绑规则集，且悬挂引用被拒。
func TestCatalogRelations(t *testing.T) {
	s := seeded(t)
	c := s.Get()
	if len(c.ProtocolGroups[0].KernelIDs) != 3 {
		t.Fatal("一个协议组应可绑定三个内核")
	}
	if _, err := s.Upsert("protocol-groups", []byte(`{"name":"坏组","kernel_ids":["ghost"]}`)); err == nil {
		t.Fatal("协议组引用未知内核必须失败")
	}
	if _, err := s.Upsert("policy-groups", []byte(`{"name":"坏策略","rule_set_ids":["ghost"]}`)); err == nil {
		t.Fatal("策略组引用不存在的规则集必须失败")
	}
}

// 规划 5：国内域名/IP 走国内上游，国外走加密上游，防泄密拒绝明文。
func TestDNSSplitAndLeakProtection(t *testing.T) {
	p := DNSPolicy{
		Encrypt: "doh", Strategy: "geo", LeakProtection: true,
		Domestic: []string{"https://dns.alidns.com/dns-query"},
		Foreign:  []string{"https://cloudflare-dns.com/dns-query"},
	}
	cases := []struct {
		domain, ip, wantRegion string
	}{
		{"www.baidu.com", "39.156.66.10", "domestic"},
		{"example.cn", "1.0.1.1", "domestic"},
		{"cdn.example.com", "39.1.2.3", "domestic"},
		{"www.google.com", "8.8.8.8", "foreign"},
	}
	for _, tc := range cases {
		got := ResolveDNS(p, tc.domain, tc.ip)
		if got.Region != tc.wantRegion || got.Upstream == "" {
			t.Fatalf("%s/%s -> %+v, want %s", tc.domain, tc.ip, got, tc.wantRegion)
		}
		if !got.LeakSafe {
			t.Fatal("防泄密场景下不应判定为有泄露风险")
		}
	}
	p.Encrypt = "none"
	got := ResolveDNS(p, "www.google.com", "8.8.8.8")
	if got.Upstream != "" || got.LeakSafe {
		t.Fatal("防泄密 + 明文国外上游必须拒绝解析")
	}
}

// 规划 9：一个 token 派生多个客户端专属链接。
func TestBuildLinks(t *testing.T) {
	u := User{ID: "u1", Token: "tok123", ModuleID: "m1"}
	links := BuildLinks(u, "https://panel.example.com/api/v1/sub", []string{"clash", "sing-box", "shadowrocket"})
	if len(links) != 3 {
		t.Fatalf("links = %+v", links)
	}
	byClient := map[string]Link{}
	for _, l := range links {
		byClient[l.Client] = l
	}
	if !strings.HasPrefix(byClient["clash"].URL, "clash://install-config?url=") {
		t.Fatal("Clash 深链不对: " + byClient["clash"].URL)
	}
	if !strings.HasPrefix(byClient["sing-box"].URL, "sing-box://import-remote-profile?url=") {
		t.Fatal("sing-box 深链不对")
	}
	if !strings.Contains(byClient["clash"].URL, "tok123") || !strings.Contains(byClient["clash"].URL, "target") {
		t.Fatal("订阅链接缺少 token / target")
	}
}

// 规划 7：把 2~6 + 节点 + 主内核打包成一个模块并校验。
func TestBuildModuleCollectsAllSixDomains(t *testing.T) {
	s := seeded(t)
	c := s.Get()
	node, _, err := module.NewNodeCredentials("", "50.118.185.194", 6691, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sel := Selection{
		Name:             "主模块",
		KernelIDs:        []string{"mihomo", "xray-core", "sing-box"},
		ProtocolGroupIDs: []string{c.ProtocolGroups[0].ID},
		RuleIDs:          []string{c.Rules[0].ID},
		RuleSetIDs:       []string{c.RuleSets[0].ID},
		DNSID:            c.DNS[0].ID,
		PolicyGroupIDs:   []string{c.PolicyGroups[0].ID},
		Nodes:            []module.NodeSpec{node},
		Master:           module.MasterConfig{Enabled: true, Mode: "auto", AutoRegulate: true, EgressNodeID: node.ID},
	}
	m, err := BuildModule(c, sel)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Kernels) != 3 || len(m.ProtocolGroups) != 1 || len(m.Rules) != 1 ||
		len(m.RuleSets) != 1 || len(m.PolicyGroups) != 1 || !m.DNS.Enabled || !m.Master.Enabled {
		t.Fatalf("模块没有收集全六项: %+v", m)
	}
	if issues := m.Validate(); len(issues) != 0 {
		t.Fatalf("打包后的模块应通过校验: %+v", issues)
	}
	snap, err := m.Snapshot()
	if err != nil || snap.Hash == "" {
		t.Fatalf("Snapshot: %v", err)
	}
}

// 规划 10：主内核按模块与当前运行态计算调控动作。
func TestRegulateComputesStartStop(t *testing.T) {
	s := seeded(t)
	c := s.Get()
	node, _, _ := module.NewNodeCredentials("", "50.118.185.194", 6691, t.TempDir())
	m, err := BuildModule(c, Selection{
		Name: "主模块", KernelIDs: []string{"mihomo", "xray-core"},
		Nodes:  []module.NodeSpec{node},
		Master: module.MasterConfig{Enabled: true, EgressNodeID: node.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := Regulate(m, []string{"sing-box"})
	if len(plan.StartKernels) != 2 || len(plan.StopKernels) != 1 {
		t.Fatalf("调控动作不对: %+v", plan)
	}
	if plan.EgressNodeID != node.ID || len(plan.Actions) < 3 {
		t.Fatalf("总控没有覆盖节点与内核: %+v", plan)
	}
	stable := Regulate(m, []string{"mihomo", "xray-core"})
	if len(stable.StartKernels) != 0 || len(stable.StopKernels) != 0 {
		t.Fatalf("状态一致时不应重启内核: %+v", stable)
	}
}

func TestStoreUpsertRemove(t *testing.T) {
	s := seeded(t)
	if _, err := s.Upsert("unknown", []byte(`{}`)); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("未知类型应返回 ErrUnknownKind, got %v", err)
	}
	id := s.Get().Rules[0].ID
	if err := s.Remove("rules", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("rules", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应返回 ErrNotFound, got %v", err)
	}
	s2, err := NewStore(t.TempDir()) // 新目录 = 独立实例，仅验证默认三内核
	if err != nil || len(s2.Get().Kernels) != 3 {
		t.Fatalf("默认内核槽位应为 3 个: %v %+v", err, s2.Get().Kernels)
	}
}
