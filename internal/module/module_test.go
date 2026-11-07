package module

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func sampleModule(t *testing.T, pkiDir string) Module {
	t.Helper()
	node, _, err := NewNodeCredentials("", "50.118.185.194", 6691, pkiDir)
	if err != nil {
		t.Fatalf("NewNodeCredentials: %v", err)
	}
	return Module{
		ID: "mod-1", Name: "主模块", Enabled: true,
		Master: MasterConfig{
			Enabled: true, Mode: "auto", AutoRegulate: true,
			PollIntervalSec: 30, EgressNodeID: node.ID,
		},
		Nodes: []NodeSpec{node},
		Kernels: []KernelSlot{
			{Kernel: KernelMihomo, Enabled: true},
			{Kernel: KernelXray, Enabled: true},
			{Kernel: KernelSingbox, Enabled: true},
		},
		ProtocolGroups: []ProtocolGroup{{
			ID: "pg-1", Name: "全内核协议组", KernelIDs: []string{KernelMihomo, KernelXray, KernelSingbox},
			Protocols: []Protocol{{Type: "vless", Tag: "in-1"}}, Enabled: true,
		}},
		Rules:    []RuleSpec{{ID: "r-1", Name: "直连国内", Type: "domain", Pattern: "cn", Enabled: true}},
		RuleSets: []RuleSetRef{{ID: "rs-1", Name: "国内域名", Enabled: true}},
		DNS: DNSConfig{
			ID: "dns-1", Name: "分流 DNS", Enabled: true, LeakProtection: true,
			Encrypt: DNSEncryptDoH, Strategy: "geo",
			Domestic: []string{"https://dns.alidns.com/dns-query"},
			Foreign:  []string{"https://cloudflare-dns.com/dns-query"},
		},
		PolicyGroups: []PolicyGroup{{ID: "pol-1", Name: "默认策略", Mode: "select", RuleSetIDs: []string{"rs-1"}, Enabled: true}},
	}
}

func errorIssues(issues []Issue) []Issue {
	var out []Issue
	for _, i := range issues {
		if i.Level == "error" {
			out = append(out, i)
		}
	}
	return out
}

// 规划 1：新建空白节点只需 IP + 端口，其余自动生成，私钥不落库。
func TestNewNodeCredentialsOnlyNeedsAddressAndPort(t *testing.T) {
	dir := t.TempDir()
	spec, secrets, err := NewNodeCredentials("", "50.118.185.194", 6691, filepath.Join(dir, "pki"))
	if err != nil {
		t.Fatal(err)
	}
	if spec.APIPasswordHash == "" || spec.PQPublic == "" || spec.PQFingerprint == "" ||
		spec.MTLSFingerprint == "" || spec.NodeCertPEM == "" || spec.CACertPEM == "" {
		t.Fatalf("自动生成的凭据不完整: %+v", spec)
	}
	if secrets.APIPassword == "" || secrets.NodeKeyPEM == "" {
		t.Fatal("一次性私钥/密码没有返回")
	}
	if !spec.VerifyAPIPassword(secrets.APIPassword) {
		t.Fatal("API 密码校验失败")
	}
	if spec.VerifyAPIPassword("wrong-password") {
		t.Fatal("错误密码不应通过")
	}
	if strings.Contains(spec.NodeCertPEM, "PRIVATE KEY") || strings.Contains(spec.CACertPEM, "PRIVATE KEY") {
		t.Fatal("NodeSpec 不得包含私钥")
	}
	if _, _, err := NewNodeCredentials("", "50.118.185.194", 0, filepath.Join(dir, "pki")); err == nil {
		t.Fatal("端口 0 必须被拒绝")
	}
	if _, _, err := NewNodeCredentials("", "", 6691, filepath.Join(dir, "pki")); err == nil {
		t.Fatal("空地址必须被拒绝")
	}
}

// 规划 3：一个协议组可以同时对应三个内核；三个协议组各绑一个内核也能共存。
func TestValidateOneGroupToThreeKernels(t *testing.T) {
	m := sampleModule(t, filepath.Join(t.TempDir(), "pki"))
	if got := errorIssues(m.Validate()); len(got) != 0 {
		t.Fatalf("一对三内核应当合法: %+v", got)
	}
	m.ProtocolGroups = []ProtocolGroup{
		{ID: "pg-a", KernelIDs: []string{KernelMihomo}},
		{ID: "pg-b", KernelIDs: []string{KernelXray}},
		{ID: "pg-c", KernelIDs: []string{KernelSingbox}},
	}
	if got := errorIssues(m.Validate()); len(got) != 0 {
		t.Fatalf("三组各对应一个内核应当合法: %+v", got)
	}
}

func TestValidateRejectsDanglingReferences(t *testing.T) {
	m := sampleModule(t, filepath.Join(t.TempDir(), "pki"))
	m.PolicyGroups[0].RuleSetIDs = []string{"does-not-exist"}
	issues := errorIssues(m.Validate())
	if len(issues) == 0 || !strings.Contains(issues[0].Detail, "规则集不存在") {
		t.Fatalf("悬挂引用必须被拒绝: %+v", issues)
	}
	m = sampleModule(t, filepath.Join(t.TempDir(), "pki"))
	m.ProtocolGroups[0].KernelIDs = []string{"unknown-core"}
	if got := errorIssues(m.Validate()); len(got) == 0 {
		t.Fatal("未知内核必须被拒绝")
	}
}

// 规划 2：同时 / 单独 / 多个启动。
func TestStartTargets(t *testing.T) {
	m := sampleModule(t, filepath.Join(t.TempDir(), "pki"))
	all, err := m.StartTargets(StartModeAll, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %v, %v", all, err)
	}
	one, err := m.StartTargets(StartModeSingle, []string{KernelXray})
	if err != nil || len(one) != 1 || one[0] != KernelXray {
		t.Fatalf("single = %v, %v", one, err)
	}
	multi, err := m.StartTargets(StartModeMultiple, []string{KernelMihomo, KernelSingbox})
	if err != nil || len(multi) != 2 {
		t.Fatalf("multiple = %v, %v", multi, err)
	}
	if _, err := m.StartTargets(StartModeSingle, []string{KernelMihomo, KernelXray}); err == nil {
		t.Fatal("single 传两个内核必须失败")
	}
	if _, err := m.StartTargets(StartModeAll, []string{"unknown"}); err == nil {
		t.Fatal("未声明的内核必须失败")
	}
}

func TestSnapshotIsFrozenAndPreviewIsHonest(t *testing.T) {
	m := sampleModule(t, filepath.Join(t.TempDir(), "pki"))
	snap, err := m.Snapshot()
	if err != nil || snap.Hash == "" {
		t.Fatalf("Snapshot: %v %+v", err, snap)
	}
	m.Name = "改过的名字"
	m.Kernels = nil
	if snap.Module.Name == "改过的名字" || len(snap.Module.Kernels) != 3 {
		t.Fatal("快照被后续修改污染")
	}
	p, err := m.BuildPreview()
	if err != nil {
		t.Fatal(err)
	}
	// after the mutation the preview must reflect the CURRENT module, not the snapshot
	if p.Kernels[KernelMihomo] != "" {
		t.Fatal("预览应当反映当前模块状态")
	}
	_ = errors.Is
}

func TestStoreCRUD(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := sampleModule(t, filepath.Join(t.TempDir(), "pki"))
	if err := s.Save(m); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("mod-1")
	if !ok || got.Name != "主模块" || got.CreatedAt.IsZero() {
		t.Fatalf("Get = %+v, %v", got, ok)
	}
	got.Name = "改名"
	if err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	if got2, ok := s2.Get("mod-1"); !ok || got2.Name != "改名" {
		t.Fatalf("持久化失败: %+v %v", got2, ok)
	}
	if err := s.Delete("mod-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("mod-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应返回 ErrNotFound, got %v", err)
	}
}
