package pipeline

import (
	"encoding/base64"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/rulesets"
)

func testInputs() Inputs {
	return Inputs{
		Hosts: []hosts.Host{
			{
				ID: "us01", Name: "US us01", Internal: "us01", Address: "203.0.113.10",
				SvcPort: 443, Role: hosts.RoleOutbound, Enabled: true,
				Protocol: "vless-reality", SecretKey: "11111111-2222-3333-4444-555555555555",
			},
			{
				ID: "hk01", Name: "HK hk01", Internal: "hk01", Address: "203.0.113.11",
				SvcPort: 8443, Role: hosts.RoleInbound, Enabled: true,
				ProxyURL: "trojan://pass123@203.0.113.11:8443?sni=hk.example.com#HK%20entry",
			},
		},
		RuleSets: []rulesets.RuleSet{
			{
				ID: "rs-cn", Name: "国内直连", Enabled: true, RuleCount: 2,
				Rules: []rulesets.ParsedRule{
					{Type: "DOMAIN-SUFFIX", Value: "cn", Action: "DIRECT"},
					{Type: "GEOIP", Value: "CN", Action: "DIRECT"},
				},
			},
			{
				ID: "rs-ads", Name: "广告拦截", Enabled: false, RuleCount: 1,
				Rules: []rulesets.ParsedRule{{Type: "DOMAIN-SUFFIX", Value: "ads.example", Action: "REJECT"}},
			},
		},
		DNSUpstream: []string{"223.5.5.5:53"},
		DNSEnabled:  true,
		ActiveUsers: 3,
		KernelReady: true,
		KernelState: "running",
		PortFree:    func(string) bool { return true },
	}
}

func TestStorePersistsAndValidates(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got.Inbound.Port != 7891 {
		t.Fatalf("unexpected default: %+v", got)
	}
	cfg := s.Get()
	cfg.Inbound.Port = 0
	cfg.Inbound.Listen = "127.0.0.1"
	cfg.Outbound.HostIDs = []string{"us01", "us01"}
	cfg.Outbound.Mode = "select"
	saved, err := s.Set(cfg)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if saved.Inbound.Port != 7891 {
		t.Fatalf("zero port must fall back to the default, got %d", saved.Inbound.Port)
	}
	if len(saved.Outbound.HostIDs) != 1 {
		t.Fatalf("host ids must be deduped: %v", saved.Outbound.HostIDs)
	}

	reopened := NewStore(dir)
	if err := reopened.Load(); err != nil {
		t.Fatal(err)
	}
	if reopened.Get().Outbound.HostIDs[0] != "us01" {
		t.Fatal("config must survive a restart")
	}

	bad := s.Get()
	bad.Outbound.Mode = "round-robin"
	if _, err := s.Set(bad); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
}

func TestValidateReturnsChainOrderAndDetectsFailures(t *testing.T) {
	in := testInputs()
	cfg := Default()
	cfg.Outbound.HostIDs = []string{"us01"}
	cfg.Inbound.HostID = "hk01"
	cfg.RuleSetIDs = []string{"rs-cn"}

	steps := Validate(cfg, in)
	if len(steps) != 7 {
		t.Fatalf("expected 7 steps, got %d", len(steps))
	}
	wantOrder := []string{"protocol", "inbound", "rules", "dns", "outbound", "users", "kernel"}
	for i, key := range wantOrder {
		if steps[i].Key != key {
			t.Fatalf("step %d = %q, want %q", i, steps[i].Key, key)
		}
		if steps[i].Order != i+1 {
			t.Fatalf("step %q order = %d", steps[i].Key, steps[i].Order)
		}
	}
	if s, bad := HasBlockingFailure(steps); bad {
		t.Fatalf("healthy chain must not block: %+v", s)
	}

	// Rules present, users present, DNS present -> those steps are ok.
	for _, s := range steps {
		if s.Key == "rules" && s.Status != "ok" {
			t.Fatalf("rules should be ok: %+v", s)
		}
	}

	// Kernel missing -> hard failure.
	in.KernelReady = false
	if s, bad := HasBlockingFailure(Validate(cfg, in)); !bad || s.Key != "kernel" {
		t.Fatalf("missing kernel must block on the kernel step, got %+v", s)
	}

	// Port taken -> hard failure on the inbound step.
	in = testInputs()
	in.PortFree = func(string) bool { return false }
	if s, bad := HasBlockingFailure(Validate(cfg, in)); !bad || s.Key != "inbound" {
		t.Fatalf("busy port must block on the inbound step, got %+v", s)
	}

	// No rule set and no outbound -> warnings, not failures.
	empty := Default()
	empty.RuleSetIDs = nil
	steps = Validate(empty, Inputs{KernelReady: true, PortFree: func(string) bool { return true }})
	if _, bad := HasBlockingFailure(steps); bad {
		t.Fatal("an empty but valid chain must not block")
	}
	if steps[2].Status != "warn" || steps[4].Status != "warn" {
		t.Fatalf("expected warnings for rules/outbound: %+v", steps)
	}
}

func TestRenderProducesValidMihomoYAML(t *testing.T) {
	in := testInputs()
	cfg := Default()
	cfg.Outbound.HostIDs = []string{"us01"}
	cfg.Inbound.HostID = "hk01"
	cfg.RuleSetIDs = []string{"rs-cn", "rs-ads"}
	cfg.DNSEnabled = true

	out, warnings, err := Render(cfg, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered config is not valid YAML: %v\n%s", err, out)
	}
	if doc["mixed-port"] != 7891 {
		t.Fatalf("mixed-port missing: %v", doc["mixed-port"])
	}
	proxies, _ := doc["proxies"].([]any)
	if len(proxies) != 2 {
		t.Fatalf("expected entry + outbound proxies, got %d\n%s", len(proxies), out)
	}
	var outbound map[string]any
	for _, raw := range proxies {
		p := raw.(map[string]any)
		if p["name"] == "out-us01" {
			outbound = p
		}
	}
	if outbound == nil {
		t.Fatalf("outbound proxy missing:\n%s", out)
	}
	if outbound["dialer-proxy"] != "entry-hk01" {
		t.Fatalf("outbound must chain through the entry node: %v", outbound)
	}
	if outbound["type"] != "vless" || outbound["server"] != "203.0.113.10" {
		t.Fatalf("unexpected outbound: %v", outbound)
	}

	groups, _ := doc["proxy-groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected one proxy group, got %d", len(groups))
	}
	g := groups[0].(map[string]any)
	if g["name"] != groupName {
		t.Fatalf("group name = %v", g["name"])
	}
	members, _ := g["proxies"].([]any)
	if len(members) != 3 || members[0] != "entry-hk01" || members[1] != "out-us01" || members[2] != "DIRECT" {
		t.Fatalf("select mode must keep DIRECT as a manual option: %v", members)
	}

	// url-test must NOT contain DIRECT, otherwise it wins on latency and the
	// traffic silently bypasses the VPS.
	cfg.Outbound.Mode = "url-test"
	out2, _, err := Render(cfg, in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "- DIRECT") {
		t.Fatalf("url-test must not list DIRECT://n%s", out2)
	}
	cfg.Outbound.Mode = "select"

	rules, _ := doc["rules"].([]any)
	if len(rules) != 3 { // 2 from rs-cn, rs-ads disabled, + MATCH
		t.Fatalf("expected 2 rules + MATCH, got %v", rules)
	}
	if rules[0] != "DOMAIN-SUFFIX,cn,DIRECT" {
		t.Fatalf("rule rendering wrong: %v", rules[0])
	}
	if rules[len(rules)-1] != "MATCH,"+groupName {
		t.Fatalf("missing MATCH rule: %v", rules[len(rules)-1])
	}
}

func TestRenderFallsBackToDirectWithoutHosts(t *testing.T) {
	cfg := Default()
	out, _, err := Render(cfg, Inputs{KernelReady: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "DIRECT") {
		t.Fatalf("direct fallback missing:\n%s", out)
	}
	if strings.Contains(out, "dialer-proxy") {
		t.Fatal("no entry node means no dialer-proxy")
	}
}

func TestParseShareLinks(t *testing.T) {
	vless := "vless://11111111-2222-3333-4444-555555555555@example.com:443?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome&pbk=PUBKEY123&sid=abcd&type=tcp&flow=xtls-rprx-vision#US%20node"
	p, err := ParseShare(vless)
	if err != nil {
		t.Fatal(err)
	}
	if p["type"] != "vless" || p["server"] != "example.com" || p["port"] != 443 {
		t.Fatalf("bad vless parse: %v", p)
	}
	ro, ok := p["reality-opts"].(map[string]any)
	if !ok || ro["public-key"] != "PUBKEY123" || ro["short-id"] != "abcd" {
		t.Fatalf("reality opts lost: %v", p)
	}
	if p["flow"] != "xtls-rprx-vision" || p["servername"] != "www.microsoft.com" {
		t.Fatalf("flow/sni lost: %v", p)
	}

	if _, err := ParseShare("vless://uuid@example.com:443?security=reality"); err == nil {
		t.Fatal("reality without pbk must be rejected")
	}

	ssUser := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:secret-pass"))
	ss, err := ParseShare("ss://" + ssUser + "@1.2.3.4:8388#SS")
	if err != nil {
		t.Fatal(err)
	}
	if ss["cipher"] != "aes-256-gcm" || ss["password"] != "secret-pass" || ss["port"] != 8388 {
		t.Fatalf("bad ss parse: %v", ss)
	}

	vmessJSON := `{"ps":"VM","add":"5.6.7.8","port":"443","id":"9cf3","net":"ws","tls":"tls","sni":"a.com","host":"a.com","path":"/ray"}`
	vm, err := ParseShare("vmess://" + base64.StdEncoding.EncodeToString([]byte(vmessJSON)))
	if err != nil {
		t.Fatal(err)
	}
	if vm["type"] != "vmess" || vm["uuid"] != "9cf3" || vm["network"] != "ws" {
		t.Fatalf("bad vmess parse: %v", vm)
	}

	if _, err := ParseShare("wireguard://x@y:1"); err == nil {
		t.Fatal("unknown scheme must be rejected instead of silently broken")
	}
}

// fakeSocks accepts one CONNECT and echoes, proving SocksDial really speaks
// the protocol instead of just dialling the port.
func fakeSocks(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 3)
				if _, err := io.ReadFull(conn, buf); err != nil {
					return
				}
				_, _ = conn.Write([]byte{0x05, 0x00})
				head := make([]byte, 4)
				if _, err := io.ReadFull(conn, head); err != nil {
					return
				}
				switch head[3] {
				case 0x01:
					_, _ = io.ReadFull(conn, make([]byte, 6))
				case 0x03:
					l := make([]byte, 1)
					_, _ = io.ReadFull(conn, l)
					_, _ = io.ReadFull(conn, make([]byte, int(l[0])+2))
				case 0x04:
					_, _ = io.ReadFull(conn, make([]byte, 18))
				}
				_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x1f, 0x90})
				_, _ = conn.Write([]byte("ok"))
			}(c)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestSocksDialVerifiesTheTunnel(t *testing.T) {
	addr, closeFn := fakeSocks(t)
	defer closeFn()

	d, err := SocksDial(addr, "example.com:80", 3*time.Second)
	if err != nil {
		t.Fatalf("socks dial: %v", err)
	}
	if d <= 0 {
		t.Fatal("latency must be positive")
	}

	if _, err := SocksDial(addr, "not-a-target", 2*time.Second); err == nil {
		t.Fatal("target without port must be rejected")
	}
	if _, err := SocksDial("127.0.0.1:1", "example.com:80", 500*time.Millisecond); err == nil {
		t.Fatal("dead listener must fail")
	}
}

func TestStoreFileLocation(t *testing.T) {
	s := NewStore(filepath.Join("tmp", "data"))
	if !strings.HasSuffix(s.Path(), filepath.Join("data", "pipeline.json")) {
		t.Fatalf("unexpected path: %s", s.Path())
	}
}
