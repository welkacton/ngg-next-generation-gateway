package mnode

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/module"
)

// 规划 1：新建空白节点只需 IP + 端口，三种凭据自动生成，安装脚本自包含。
func TestCreateBlankNodeOnlyNeedsIPAndPort(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	view, secrets, err := CreateBlankNode(s, "50.118.185.194", 6691, filepath.Join(dir, "pki"))
	if err != nil {
		t.Fatal(err)
	}
	if view.Node.Address != "50.118.185.194" || view.Node.Port != 6691 {
		t.Fatalf("节点基本字段错误: %+v", view.Node)
	}
	if view.Node.APIPasswordHash == "" || view.Node.PQFingerprint == "" || view.Node.MTLSFingerprint == "" {
		t.Fatal("三种凭据没有自动生成")
	}
	if !view.HasScript {
		t.Fatal("没有生成安装脚本")
	}
	if secrets.APIPassword == "" || secrets.NodeKeyPEM == "" {
		t.Fatal("一次性凭据没有返回")
	}

	// 列表视图绝不回显任何私钥或明文密码。
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("List = %+v", list)
	}
	if strings.Contains(mustJSON(list[0]), "PRIVATE KEY") ||
		strings.Contains(mustJSON(list[0]), secrets.APIPassword) {
		t.Fatal("列表视图泄露凭据")
	}

	rec, ok := s.Record(view.Node.ID)
	if !ok {
		t.Fatal("Record 找不到节点")
	}
	script := rec.Script
	for _, want := range []string{
		"NGG_MNODE_READY", "X-NGG-API-Password", "X-NGG-PQ-Fingerprint",
		"verify_mode = ssl.CERT_REQUIRED", "ngg-mnode.service",
		secrets.APIPassword, secrets.PQFingerprint,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("安装脚本缺少 %q", want)
		}
	}
	if strings.Contains(script, "{{") {
		t.Fatal("安装脚本仍有占位符")
	}
}

func TestScriptRejectsHeredocInjection(t *testing.T) {
	n, secrets, err := module.NewNodeCredentials("", "50.118.185.194", 6691, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secrets.NodeCertPEM = "x\nNGG_CERT\necho injected"
	if _, err := Script(n, secrets); err == nil {
		t.Fatal("构造的分隔符注入必须被拒绝")
	}
}

func TestStoreProbeAndDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := CreateBlankNode(s, "50.118.185.194", 6691, filepath.Join(dir, "pki"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetProbe(view.Node.ID, &module.NodeProbe{Online: true, Hostname: "vps-01", CPUPercent: 12.5}); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get(view.Node.ID)
	if !ok || got.Node.Probe == nil || !got.Node.Probe.Online || got.Node.Probe.Hostname != "vps-01" {
		t.Fatalf("探测结果未持久化: %+v", got)
	}
	// 重新打开仍然在（0600 文件）
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get(view.Node.ID); !ok {
		t.Fatal("节点未持久化")
	}
	if err := s.Delete(view.Node.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(view.Node.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应返回 ErrNotFound, got %v", err)
	}
}

func TestParseProbe(t *testing.T) {
	body := []byte(`{"online":true,"hostname":"vps-01","os":"Ubuntu 24.04","arch":"x86_64",
"cpu_percent":7.8,"memory_total":2100000000,"memory_used":1200000000,
"disk_total":20000000000,"disk_used":8000000000,"uptime_sec":123456,
"net_rx_bytes":90000000000,"net_tx_bytes":80000000000}`)
	got, err := parseProbe(body, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Online || got.CPUPercent != 7.8 || got.NetRxBytes != 90000000000 || got.LatencyMS != 42 {
		t.Fatalf("解析结果不对: %+v", got)
	}
	if _, err := parseProbe([]byte(`{"online":false,"error":"down"}`), 0); err == nil {
		t.Fatal("节点自报离线应返回错误")
	}
	if _, err := parseProbe([]byte(`not-json`), 0); err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}

func TestFriendlyProbeErrorIsActionable(t *testing.T) {
	for in, want := range map[string]string{
		"dial tcp: connect: connection refused":         "安装脚本",
		"context deadline exceeded":                     "超时",
		"x509: certificate signed by unknown authority": "双向 TLS",
	} {
		if got := friendlyProbeError(errors.New(in)); !strings.Contains(got, want) {
			t.Fatalf("%q -> %q, 期望含 %q", in, got, want)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The installer is a real bash script; validate its syntax when bash exists
// (Linux CI always has one, Windows dev shells usually do too).
func TestScriptBashSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash 不可用，跳过脚本语法检查")
	}
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := CreateBlankNode(s, "50.118.185.194", 6691, filepath.Join(dir, "pki"))
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := s.Record(view.Node.ID)
	p := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(p, []byte(rec.Script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bash, "-n", p).CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n 失败: %v\n%s", err, out)
	}
}
