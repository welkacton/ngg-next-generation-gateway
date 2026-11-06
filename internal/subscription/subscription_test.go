package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ngg-project/ngg/internal/gateway"
	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/rulesets"
	"github.com/ngg-project/ngg/internal/users"
)

const testUUID = "8f0da0b2-9f4b-4a63-9d1f-0d6a2c9f8b11"

type fixture struct {
	r      *Renderer
	users  *users.Registry
	hosts  *hosts.Registry
	gw     *gateway.Store
	userID string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()

	ur := users.NewRegistry(filepath.Join(dir, "users-config.json"))
	if err := ur.Load(); err != nil {
		t.Fatal(err)
	}
	u, err := ur.Add(users.User{Username: "US-01", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	hr := hosts.NewRegistry(filepath.Join(dir, "hosts.json"))
	if err := hr.Load(); err != nil {
		t.Fatal(err)
	}
	// 127.0.0.1:1 refuses instantly, so Add's real probe does not slow the suite.
	if _, err := hr.Add(context.Background(), hosts.Host{
		ID: "us01", Name: "US us01", Address: "127.0.0.1",
		NodePort: 1, SvcPort: 8443, Role: hosts.RoleOutbound, Enabled: true,
		Protocol: "vless", SecretKey: testUUID,
	}); err != nil {
		t.Fatal(err)
	}

	rr := rulesets.NewRegistry(filepath.Join(dir, "rulesets.json"))
	if err := rr.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := rr.Add(context.Background(), rulesets.RuleSet{
		ID: "rs-ads", Name: "广告拦截", Source: rulesets.SourceInline,
		Content: "DOMAIN-SUFFIX,ads.example.com,PROXY",
	}); err != nil {
		t.Fatal(err)
	}

	gw := gateway.NewStore(dir)
	if err := gw.Load(); err != nil {
		t.Fatal(err)
	}

	r := New(Options{
		Users:    ur,
		Hosts:    hr,
		RuleSets: rr,
		Gateway:  gw,
		DataDir:  dir,
		BaseURL:  func() string { return "https://vpn.example.com:9191/" },
	})
	return &fixture{r: r, users: ur, hosts: hr, gw: gw, userID: u.ID}
}

// setHostCredential rewrites the node secret so protocol-specific credential
// shapes (shadowsocks "method:password", socks "user:pass") can be exercised.
func (f *fixture) setHostCredential(t *testing.T, cred string) {
	t.Helper()
	h, ok := f.hosts.Get("us01")
	if !ok {
		t.Fatal("host us01 missing")
	}
	h.SecretKey = cred
	if _, err := f.hosts.Update("us01", h); err != nil {
		t.Fatalf("update host: %v", err)
	}
}

func (f *fixture) bind(t *testing.T, protocol, dns string, sets ...string) users.User {
	t.Helper()
	u, err := f.users.SetSubscription(f.userID, "us01", protocol, dns, sets)
	if err != nil {
		t.Fatalf("SetSubscription: %v", err)
	}
	return u
}

func TestBuildRendersEveryTarget(t *testing.T) {
	f := newFixture(t)
	f.bind(t, users.ProtocolVLESS, users.DNSProfileGateway, "rs-ads")

	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Token == "" || p.NodeID != "us01" || p.NodeName != "US us01" || p.Protocol != "vless" {
		t.Fatalf("payload header wrong: %+v", p)
	}
	if p.BaseURL != "https://vpn.example.com:9191" {
		t.Fatalf("base url must be trimmed: %q", p.BaseURL)
	}
	for _, target := range Targets {
		body, ok := p.Body[target]
		if !ok || strings.TrimSpace(body) == "" {
			t.Fatalf("%s body missing", target)
		}
		if !strings.Contains(p.LinksFor(target), "/sub/u/"+p.Token+"?target="+target) {
			t.Fatalf("%s link wrong: %q", target, p.LinksFor(target))
		}
	}

	// Clash must be parseable mihomo YAML with the three mandatory sections.
	var clash map[string]any
	if err := yaml.Unmarshal([]byte(p.Body[TargetClash]), &clash); err != nil {
		t.Fatalf("clash is not valid YAML: %v\n%s", err, p.Body[TargetClash])
	}
	for _, key := range []string{"proxies", "proxy-groups", "rules", "rule-providers"} {
		if _, ok := clash[key]; !ok {
			t.Fatalf("clash doc missing %q:\n%s", key, p.Body[TargetClash])
		}
	}
	proxies, _ := clash["proxies"].([]any)
	if len(proxies) != 1 {
		t.Fatalf("clash must carry exactly one proxy, got %d", len(proxies))
	}
	proxy, _ := proxies[0].(map[string]any)
	if proxy["type"] != "vless" || proxy["uuid"] != testUUID || proxy["server"] != "127.0.0.1" {
		t.Fatalf("clash proxy wrong: %v", proxy)
	}

	// sing-box must be valid JSON with outbounds + route.final.
	var sb map[string]any
	if err := json.Unmarshal([]byte(p.Body[TargetSingbox]), &sb); err != nil {
		t.Fatalf("sing-box is not valid JSON: %v\n%s", err, p.Body[TargetSingbox])
	}
	outs, _ := sb["outbounds"].([]any)
	if len(outs) != 2 {
		t.Fatalf("sing-box must have proxy + direct, got %d", len(outs))
	}
	route, _ := sb["route"].(map[string]any)
	if route["final"] != singBoxTag {
		t.Fatalf("sing-box route.final = %v", route["final"])
	}
	if _, ok := route["rule_set"]; !ok {
		t.Fatalf("bound rule set must appear as route.rule_set:\n%s", p.Body[TargetSingbox])
	}

	// Surge must carry the sections a Surge profile needs.
	for _, section := range []string{"[Proxy]", "[Proxy Group]", "[Rule]", "dns-server"} {
		if !strings.Contains(p.Body[TargetSurge], section) {
			t.Fatalf("surge body missing %q:\n%s", section, p.Body[TargetSurge])
		}
	}

	// v2rayN is a base64 document of share links.
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(p.Body[TargetV2ray]))
	if err != nil {
		t.Fatalf("v2ray body is not base64: %v", err)
	}
	if !strings.Contains(string(decoded), "vless://"+testUUID+"@127.0.0.1:8443") {
		t.Fatalf("v2ray link wrong: %s", decoded)
	}

	// Plain SOCKS is host:port:user:pass.
	if got := strings.TrimSpace(p.Body[TargetSocks]); got != "127.0.0.1:8443:"+testUUID+":" {
		t.Fatalf("socks body = %q", got)
	}
}

func TestBuildRequiresAUsableNode(t *testing.T) {
	f := newFixture(t)

	if _, err := f.r.Build(f.userID); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unbound user must fail with ErrNotBound, got %v", err)
	}

	// A node id that is not in the registry anymore.
	if _, err := f.users.SetSubscription(f.userID, "ghost", "vless", "system", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Build(f.userID); !errors.Is(err, ErrNodeIncomplete) {
		t.Fatalf("unknown node must fail with ErrNodeIncomplete, got %v", err)
	}

	// A real node without a service port: the renderer must refuse instead of
	// writing port 0 into a client config.
	dir := t.TempDir()
	hr := hosts.NewRegistry(filepath.Join(dir, "hosts.json"))
	if err := hr.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := hr.Add(context.Background(), hosts.Host{
		ID: "noport", Name: "no port", Address: "127.0.0.1",
		NodePort: 1, Role: hosts.RoleOutbound, Enabled: true,
		Protocol: "trojan", SecretKey: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	r := New(Options{Users: f.users, Hosts: hr, DataDir: dir})
	if _, err := f.users.SetSubscription(f.userID, "noport", "trojan", "system", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Build(f.userID); !errors.Is(err, ErrNodeIncomplete) {
		t.Fatalf("node without svc_port must fail, got %v", err)
	}

	// A node without a credential for a protocol that needs one.
	if _, err := hr.Add(context.Background(), hosts.Host{
		ID: "nokey", Name: "no key", Address: "127.0.0.1",
		NodePort: 1, SvcPort: 443, Role: hosts.RoleOutbound, Enabled: true,
		Protocol: "vless",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.users.SetSubscription(f.userID, "nokey", "vless", "system", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Build(f.userID); !errors.Is(err, ErrNodeIncomplete) {
		t.Fatalf("node without credential must fail, got %v", err)
	}
}

func TestRenderTokenGuards(t *testing.T) {
	f := newFixture(t)
	f.bind(t, users.ProtocolTrojan, users.DNSProfileSystem)
	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}

	body, headers, ok := f.r.RenderToken(p.Token, TargetClash)
	if !ok || !strings.Contains(body, "type: trojan") {
		t.Fatalf("known token must render: ok=%v body=%s", ok, body)
	}
	if got := headers["subscription-userinfo"]; got != "upload=0; download=0; total=0" {
		t.Fatalf("subscription-userinfo = %q", got)
	}
	if headers["profile-update-interval"] != "24" {
		t.Fatalf("profile-update-interval = %q", headers["profile-update-interval"])
	}
	if ct := headers["Content-Type"]; !strings.HasPrefix(ct, "text/yaml") {
		t.Fatalf("clash content type = %q", ct)
	}
	if _, _, ok := f.r.RenderToken(p.Token, "nginx"); ok {
		t.Fatal("an unknown target must not render")
	}
	if _, _, ok := f.r.RenderToken("nope", TargetClash); ok {
		t.Fatal("an unknown token must not render")
	}

	// Quota is reported, not invented.
	if err := f.users.AddUsage(f.userID, 4096); err != nil {
		t.Fatal(err)
	}
	_, headers, ok = f.r.RenderToken(p.Token, TargetClash)
	if !ok {
		t.Fatal("render must still succeed after usage grows")
	}
	if headers["subscription-userinfo"] != "upload=0; download=4096; total=0" {
		t.Fatalf("usage header = %q", headers["subscription-userinfo"])
	}
}

func TestRenderTokenRejectsDisabledAndExpired(t *testing.T) {
	f := newFixture(t)
	f.bind(t, users.ProtocolTrojan, users.DNSProfileOff)
	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}

	u, _ := f.users.Get(f.userID)
	u.Enabled = false
	if _, err := f.users.Update(f.userID, u); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.r.RenderToken(p.Token, TargetClash); ok {
		t.Fatal("a disabled account must not download a config")
	}

	u.Enabled = true
	u.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	if _, err := f.users.Update(f.userID, u); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.r.RenderToken(p.Token, TargetClash); ok {
		t.Fatal("an expired account must not download a config")
	}

	// Turning the account back on makes the same token work again.
	u.ExpiresAt = time.Now().UTC().Add(time.Hour)
	if _, err := f.users.Update(f.userID, u); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.r.RenderToken(p.Token, TargetClash); !ok {
		t.Fatal("a re-enabled, unexpired account must work")
	}
}

func TestRotatedTokenStopsWorking(t *testing.T) {
	f := newFixture(t)
	f.bind(t, users.ProtocolTrojan, users.DNSProfileOff)
	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := f.users.RotateSubscriptionToken(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.r.RenderToken(p.Token, TargetClash); ok {
		t.Fatal("the old token must be rejected after a rotation")
	}
	if _, _, ok := f.r.RenderToken(rotated.SubscriptionToken, TargetClash); !ok {
		t.Fatal("the new token must work")
	}
}

func TestDNSProfiles(t *testing.T) {
	f := newFixture(t)

	// off: no dns object at all.
	f.bind(t, users.ProtocolTrojan, users.DNSProfileOff)
	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{TargetClash, TargetSingbox} {
		if strings.Contains(strings.ToLower(p.Body[target]), `"dns"`) || strings.Contains(p.Body[target], "dns:") {
			t.Fatalf("%s must not contain a dns section when the profile is off:\n%s", target, p.Body[target])
		}
	}
	if strings.Contains(p.Body[TargetSurge], "dns-server") {
		t.Fatalf("surge must not set dns-server when the profile is off:\n%s", p.Body[TargetSurge])
	}

	// system: Clash disables its own resolver, sing-box has no dns block.
	f.bind(t, users.ProtocolTrojan, users.DNSProfileSystem)
	p, err = f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body[TargetClash], "enable: false") {
		t.Fatalf("system profile must disable clash dns:\n%s", p.Body[TargetClash])
	}
	if strings.Contains(p.Body[TargetSingbox], `"dns"`) {
		t.Fatalf("system profile must not add a sing-box dns block:\n%s", p.Body[TargetSingbox])
	}

	// gateway: the servers come from the gateway store.
	cfg := f.gw.Get()
	cfg.DNS.Servers = []string{"10.0.0.53", "10.0.0.54"}
	if _, _, _, err := f.gw.Save(cfg); err != nil {
		t.Fatalf("gateway save: %v", err)
	}
	f.bind(t, users.ProtocolTrojan, users.DNSProfileGateway)
	p, err = f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body[TargetClash], "10.0.0.53") {
		t.Fatalf("gateway dns not applied to clash:\n%s", p.Body[TargetClash])
	}
	if !strings.Contains(p.Body[TargetSurge], "dns-server = 10.0.0.53, 10.0.0.54") {
		t.Fatalf("gateway dns not applied to surge:\n%s", p.Body[TargetSurge])
	}
	if !strings.Contains(p.Body[TargetSingbox], "10.0.0.53") {
		t.Fatalf("gateway dns not applied to sing-box:\n%s", p.Body[TargetSingbox])
	}
}

func TestRuleSetWarningAndReferences(t *testing.T) {
	f := newFixture(t)
	f.bind(t, users.ProtocolTrojan, users.DNSProfileOff, "rs-ads", "rs-missing")
	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body[TargetClash], "RULE-SET,广告拦截,PROXY") {
		t.Fatalf("clash rules must reference the bound set:\n%s", p.Body[TargetClash])
	}
	if !strings.Contains(p.Body[TargetSurge], "RULE-SET,广告拦截,PROXY") {
		t.Fatalf("surge rule missing:\n%s", p.Body[TargetSurge])
	}
	joined := strings.Join(p.Warnings, "\n")
	if !strings.Contains(joined, "rs-missing") {
		t.Fatalf("a missing rule set must be reported: %v", p.Warnings)
	}
	if !strings.Contains(joined, "尚未由 NGG 托管") {
		t.Fatalf("the rule-set download gap must be stated: %v", p.Warnings)
	}
}

func TestProtocolMatrix(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		protocol   string
		credential string
		clashHas   []string
	}{
		{users.ProtocolVLESS, testUUID, []string{"type: vless", "uuid: " + testUUID}},
		{users.ProtocolShadowsocks, "aes-128-gcm:test-pass", []string{"type: ss", "cipher: aes-128-gcm"}},
		{users.ProtocolSOCKS5, "sock-user:sock-pass", []string{"type: socks5", "username: sock-user"}},
		{users.ProtocolHTTP, "http-user:http-pass", []string{"type: http", "username: http-user"}},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			f.setHostCredential(t, tc.credential)
			f.bind(t, tc.protocol, users.DNSProfileOff)
			p, err := f.r.Build(f.userID)
			if err != nil {
				t.Fatalf("Build(%s): %v", tc.protocol, err)
			}
			for _, want := range tc.clashHas {
				if !strings.Contains(p.Body[TargetClash], want) {
					t.Fatalf("clash missing %q:\n%s", want, p.Body[TargetClash])
				}
			}
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(p.Body[TargetClash]), &doc); err != nil {
				t.Fatalf("clash yaml: %v", err)
			}
			var sb map[string]any
			if err := json.Unmarshal([]byte(p.Body[TargetSingbox]), &sb); err != nil {
				t.Fatalf("sing-box json: %v", err)
			}
			// socks5 / http cannot be expressed as a v2rayN share link; the
			// target is skipped with a warning instead of emitting a fake link.
			if tc.protocol == users.ProtocolSOCKS5 || tc.protocol == users.ProtocolHTTP {
				if p.Links.V2ray != "" {
					t.Fatalf("%s must not get a v2ray link", tc.protocol)
				}
				if !strings.Contains(strings.Join(p.Warnings, "\n"), "v2rayN 分享链接只支持") {
					t.Fatalf("missing v2ray skip warning: %v", p.Warnings)
				}
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(p.Body[TargetV2ray]))
			if err != nil {
				t.Fatalf("v2ray base64: %v", err)
			}
			if tc.protocol == users.ProtocolShadowsocks {
				if !strings.HasPrefix(string(decoded), "ss://") {
					t.Fatalf("ss share link wrong: %s", decoded)
				}
			}
		})
	}
}

func TestProvisionOverridesHostRecord(t *testing.T) {
	f := newFixture(t)
	dataDir := f.r.opts.DataDir
	if err := os.MkdirAll(filepath.Join(dataDir, "provisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"host_id":"us01","protocol":"trojan","port":2053,"credential":"prov-pass",
	         "endpoint":{"address":"edge.example.com","port":2053,"protocol":"trojan",
	                     "credential":"prov-pass","transport":"","tls":true,"sni":"cdn.example.com"}}`
	if err := os.WriteFile(filepath.Join(dataDir, "provisions", "us01.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	f.bind(t, "", users.DNSProfileOff) // "" = follow the node protocol
	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Protocol != "trojan" {
		t.Fatalf("protocol must come from the provision, got %q", p.Protocol)
	}
	if !strings.Contains(p.Body[TargetClash], "server: edge.example.com") ||
		!strings.Contains(p.Body[TargetClash], "port: 2053") ||
		!strings.Contains(p.Body[TargetClash], "password: prov-pass") {
		t.Fatalf("provision was not applied:\n%s", p.Body[TargetClash])
	}
	if !strings.Contains(p.Body[TargetClash], "sni: cdn.example.com") {
		t.Fatalf("provision sni was not applied:\n%s", p.Body[TargetClash])
	}
}

// A provisioned Shadowsocks node stores only the password (the cipher is the
// provisioner's constant). The renderer must use that cipher without pretending
// the cipher was guessed.
func TestProvisionShadowsocksUsesProvisionCipher(t *testing.T) {
	f := newFixture(t)
	dataDir := f.r.opts.DataDir
	if err := os.MkdirAll(filepath.Join(dataDir, "provisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"host_id":"us01","protocol":"shadowsocks","port":8388,"credential":"c2VjcmV0LXBhc3MvKw==",
	         "endpoint":{"address":"ss.example.com","port":8388,"protocol":"shadowsocks",
	                     "credential":"c2VjcmV0LXBhc3MvKw==","transport":"tcp","tls":false,"sni":""}}`
	if err := os.WriteFile(filepath.Join(dataDir, "provisions", "us01.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	f.bind(t, users.ProtocolShadowsocks, users.DNSProfileOff)
	p, err := f.r.Build(f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body[TargetClash], "cipher: aes-256-gcm") {
		t.Fatalf("the provision cipher must be used:\n%s", p.Body[TargetClash])
	}
	if strings.Contains(strings.Join(p.Warnings, "\n"), "未携带加密方式") {
		t.Fatalf("the cipher came from the provision and must not be reported as assumed: %v", p.Warnings)
	}
	if p.Links.V2ray == "" {
		t.Fatalf("shadowsocks must be expressible as a v2rayN link: %v", p.Warnings)
	}
}
