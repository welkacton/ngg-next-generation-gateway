package clients

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func mustCreate(t *testing.T, s *Store, req CreateRequest) (ClientView, string) {
	t.Helper()
	v, cred, err := s.Create(req)
	if err != nil {
		t.Fatalf("Create(%+v): %v", req, err)
	}
	return v, cred
}

func TestDefaultAccess(t *testing.T) {
	s := newTestStore(t)
	a := s.Access()
	if a.PublicHost != "" || a.PublicPort != 1080 || a.Protocol != ProtocolSocks5 || a.TLS {
		t.Fatalf("default access = %+v, want empty host/1080/socks5/false", a)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("new store has %d clients, want 0", len(got))
	}
}

func TestCreateRotateDelete(t *testing.T) {
	s := newTestStore(t)
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	v, cred := mustCreate(t, s, CreateRequest{
		Name: "iPhone 15", Platform: PlatformIOS, ExpiresAt: exp, QuotaBytes: 10 << 30,
	})
	if !strings.HasPrefix(v.ID, "cl-") || len(v.ID) != 11 {
		t.Fatalf("id = %q, want cl- + 8 hex", v.ID)
	}
	if !v.Enabled || !v.CredentialSet || v.CreatedAt == "" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if v.Protocol != ProtocolSocks5 {
		t.Fatalf("protocol = %q, want default socks5", v.Protocol)
	}
	if v.QuotaBytes != 10<<30 || v.ExpiresAt != exp {
		t.Fatalf("quota/expiry not stored: %+v", v)
	}

	// Rotate: the credential changes, the client does not.
	v2, cred2, err := s.Rotate(v.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if cred2 == cred || cred2 == "" {
		t.Fatalf("rotate did not mint a new credential")
	}
	if v2.ID != v.ID || v2.Name != v.Name {
		t.Fatalf("rotate changed identity: %+v", v2)
	}

	// Update: rename + disable.
	name := "iPad"
	enabled := false
	v3, err := s.Update(v.ID, UpdateRequest{Name: &name, Enabled: &enabled})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if v3.Name != "iPad" || v3.Enabled {
		t.Fatalf("update not applied: %+v", v3)
	}

	if err := s.Delete(v.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("client still listed after delete")
	}
	if err := s.Delete(v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
	if _, err := s.Update(v.ID, UpdateRequest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing err = %v, want ErrNotFound", err)
	}
}

func TestCredentialOnlyOnCreate(t *testing.T) {
	s := newTestStore(t)
	_, cred := mustCreate(t, s, CreateRequest{Name: "dev", Platform: PlatformWindows})
	if len(cred) != 32 {
		t.Fatalf("credential len = %d (%q), want 32 base64url chars of 24 bytes", len(cred), cred)
	}
	if strings.ContainsAny(cred, "+/=") {
		t.Fatalf("credential %q is not raw base64url", cred)
	}
	views := s.List()
	if len(views) != 1 {
		t.Fatalf("len(views) = %d", len(views))
	}
	blob, err := json.Marshal(views[0])
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	if strings.Contains(string(blob), cred) {
		t.Fatalf("credential leaked into ClientView: %s", blob)
	}
	if !strings.Contains(string(blob), `"credential_set":true`) {
		t.Fatalf("credential_set missing: %s", blob)
	}
}

func TestPersistenceReload(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, _, err := s.SaveAccess(AccessConfig{PublicHost: "vpn.example.com", PublicPort: 8443, Protocol: ProtocolHTTP, Note: "nginx"}); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	v, cred := mustCreate(t, s, CreateRequest{Name: "小明的 Mac", Platform: PlatformMacOS, Note: "办公"})
	now := time.Now().UTC()
	if !s.TouchLastSeen(v.ID) {
		t.Fatalf("TouchLastSeen failed")
	}
	if !s.List()[0].Online {
		t.Fatalf("client should be online right after TouchLastSeen")
	}

	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	a := s2.Access()
	if a.PublicHost != "vpn.example.com" || a.PublicPort != 8443 || a.Protocol != ProtocolHTTP || a.Note != "nginx" {
		t.Fatalf("access not persisted: %+v", a)
	}
	got := s2.List()
	if len(got) != 1 || got[0].ID != v.ID || got[0].Name != "小明的 Mac" {
		t.Fatalf("client not persisted: %+v", got)
	}
	if rec, ok := s2.GetClient(v.ID); !ok || rec.Note != "办公" {
		t.Fatalf("note not persisted: %+v", rec)
	}
	if got[0].Online {
		// last_seen is a liveness hint kept in memory only, so a restart is an
		// honest "unknown" rather than a fake "online".
		t.Fatalf("last_seen must not be persisted; online should be false after reload")
	}
	if sub, ok := s2.Subscription(cred); !ok {
		t.Fatalf("credential did not survive reload")
	} else if sub.ClientID != v.ID {
		t.Fatalf("subscription resolved to %q, want %q", sub.ClientID, v.ID)
	}
	if time.Since(now) > time.Minute {
		t.Fatalf("clock moved unexpectedly")
	}
}

func TestFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not meaningful on Windows")
	}
	s := newTestStore(t)
	mustCreate(t, s, CreateRequest{Name: "dev", Platform: PlatformLinux})
	st, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("clients.json mode = %04o, want 0600", perm)
	}
}

func TestSubscription(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.SaveAccess(AccessConfig{PublicHost: "vpn.example.com", PublicPort: 1080, Protocol: ProtocolSocks5}); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	v, cred := mustCreate(t, s, CreateRequest{Name: "phone", Platform: PlatformAndroid, QuotaBytes: 2048})

	sub, ok := s.Subscription(cred)
	if !ok {
		t.Fatalf("Subscription rejected a valid credential")
	}
	if sub.ContentType != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", sub.ContentType)
	}
	if got := sub.Headers["subscription-userinfo"]; got != "upload=0; download=0; total=2048" {
		t.Fatalf("userinfo = %q", got)
	}
	if sub.Headers["profile-update-interval"] != "24" {
		t.Fatalf("profile-update-interval = %q", sub.Headers["profile-update-interval"])
	}
	var doc map[string]any
	if err := json.Unmarshal(sub.Body, &doc); err != nil {
		t.Fatalf("subscription body is not JSON: %v\n%s", err, sub.Body)
	}

	if _, ok := s.Subscription("not-a-real-token"); ok {
		t.Fatalf("unknown token accepted")
	}
	if _, ok := s.Subscription(""); ok {
		t.Fatalf("empty token accepted")
	}

	// Disabled clients stop working.
	off := false
	if _, err := s.Update(v.ID, UpdateRequest{Enabled: &off}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, ok := s.Subscription(cred); ok {
		t.Fatalf("disabled client still served")
	}

	// Expired clients stop working.
	on := true
	if _, err := s.Update(v.ID, UpdateRequest{Enabled: &on}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	s.mu.Lock()
	s.cfg.Clients[0].ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	s.mu.Unlock()
	if _, ok := s.Subscription(cred); ok {
		t.Fatalf("expired client still served")
	}

	// Rotate invalidates the previous token.
	s.mu.Lock()
	s.cfg.Clients[0].ExpiresAt = ""
	s.mu.Unlock()
	_, cred2, err := s.Rotate(v.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, ok := s.Subscription(cred); ok {
		t.Fatalf("old credential still accepted after rotate")
	}
	if _, ok := s.Subscription(cred2); !ok {
		t.Fatalf("new credential rejected after rotate")
	}
}

func TestRenderFormats(t *testing.T) {
	acc := AccessConfig{PublicHost: "vpn.example.com", PublicPort: 1080, Protocol: ProtocolSocks5}
	socksClient := Client{ID: "cl-12345678", Name: "Laptop", Platform: PlatformWindows, Protocol: ProtocolSocks5, Credential: "CRED"}
	wgClient := Client{ID: "cl-87654321", Name: "Phone", Platform: PlatformIOS, Protocol: ProtocolWireGuard, Credential: "CRED2"}

	t.Run("socks", func(t *testing.T) {
		name, content, err := Render(socksClient, acc, FormatSocks)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if name != "Laptop.txt" {
			t.Fatalf("filename = %q", name)
		}
		for _, want := range []string{"socks5://cl-12345678:CRED@vpn.example.com:1080", "host: vpn.example.com", "port: 1080", "username: cl-12345678", "password: CRED"} {
			if !strings.Contains(content, want) {
				t.Fatalf("socks content missing %q:\n%s", want, content)
			}
		}
	})

	t.Run("singbox", func(t *testing.T) {
		name, content, err := Render(socksClient, acc, FormatSingBox)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if name != "Laptop.json" {
			t.Fatalf("filename = %q", name)
		}
		var doc struct {
			Log       map[string]any `json:"log"`
			Outbounds []struct {
				Type       string `json:"type"`
				Tag        string `json:"tag"`
				Server     string `json:"server"`
				ServerPort int    `json:"server_port"`
				Username   string `json:"username"`
				Password   string `json:"password"`
			} `json:"outbounds"`
			Route struct {
				Final string `json:"final"`
			} `json:"route"`
		}
		if err := json.Unmarshal([]byte(content), &doc); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, content)
		}
		if len(doc.Outbounds) != 2 || doc.Outbounds[0].Type != "socks" || doc.Outbounds[0].Tag != "ngg" {
			t.Fatalf("bad outbounds: %+v", doc.Outbounds)
		}
		if doc.Outbounds[0].Server != "vpn.example.com" || doc.Outbounds[0].ServerPort != 1080 {
			t.Fatalf("bad endpoint: %+v", doc.Outbounds[0])
		}
		if doc.Outbounds[0].Username != "cl-12345678" || doc.Outbounds[0].Password != "CRED" {
			t.Fatalf("bad auth: %+v", doc.Outbounds[0])
		}
		if doc.Outbounds[1].Type != "direct" || doc.Route.Final != "ngg" {
			t.Fatalf("bad route: final=%q outbounds=%+v", doc.Route.Final, doc.Outbounds)
		}
	})

	t.Run("mihomo", func(t *testing.T) {
		name, content, err := Render(socksClient, acc, FormatMihomo)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if name != "Laptop.yaml" {
			t.Fatalf("filename = %q", name)
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
			t.Fatalf("invalid YAML: %v\n%s", err, content)
		}
		proxies, ok := doc["proxies"].([]any)
		if !ok || len(proxies) != 1 {
			t.Fatalf("proxies = %#v", doc["proxies"])
		}
		p := proxies[0].(map[string]any)
		if p["type"] != "socks5" || p["server"] != "vpn.example.com" || p["port"] != 1080 {
			t.Fatalf("bad proxy: %#v", p)
		}
		groups, ok := doc["proxy-groups"].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("proxy-groups = %#v", doc["proxy-groups"])
		}
		g := groups[0].(map[string]any)
		if g["name"] != "NGG" || g["type"] != "select" {
			t.Fatalf("bad group: %#v", g)
		}
		rules, ok := doc["rules"].([]any)
		if !ok || len(rules) != 1 || rules[0] != "MATCH,NGG" {
			t.Fatalf("rules = %#v", doc["rules"])
		}
		if _, exists := doc["mixed-port"]; exists {
			t.Fatalf("mihomo config must not set mixed-port")
		}
	})

	t.Run("wireguard", func(t *testing.T) {
		accWG := AccessConfig{PublicHost: "wg.example.com", PublicPort: 51820, Protocol: ProtocolWireGuard}
		name, content, err := Render(wgClient, accWG, FormatWireGuard)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if name != "Phone.conf" {
			t.Fatalf("filename = %q", name)
		}
		for _, want := range []string{"[Interface]", "[Peer]", "PrivateKey = ", "Address = 10.66.0.2/32", "Endpoint = wg.example.com:51820", "AllowedIPs = 0.0.0.0/0, ::/0", "PersistentKeepalive = 25"} {
			if !strings.Contains(content, want) {
				t.Fatalf("wireguard conf missing %q:\n%s", want, content)
			}
		}
		// The private key is a real, clamped X25519 key on every render.
		_, content2, err := Render(wgClient, accWG, FormatWireGuard)
		if err != nil {
			t.Fatalf("Render 2: %v", err)
		}
		if content == content2 {
			t.Fatalf("wireguard render reused the same private key")
		}
	})

	t.Run("wireguard via singbox", func(t *testing.T) {
		accWG := AccessConfig{PublicHost: "wg.example.com", PublicPort: 51820, Protocol: ProtocolWireGuard}
		_, content, err := Render(wgClient, accWG, FormatSingBox)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(content), &doc); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, content)
		}
		outs := doc["outbounds"].([]any)
		first := outs[0].(map[string]any)
		if first["type"] != "wireguard" || first["tag"] != "ngg" {
			t.Fatalf("bad wireguard outbound: %#v", first)
		}
		if first["private_key"] == "" || first["peer_public_key"] == "" {
			t.Fatalf("wireguard keys missing: %#v", first)
		}
		if doc["_ngg_public_key"] == nil || doc["_ngg_public_key"] == "" {
			t.Fatalf("client public key not surfaced")
		}
	})

	t.Run("mihomo for wireguard parses", func(t *testing.T) {
		accWG := AccessConfig{PublicHost: "wg.example.com", PublicPort: 51820, Protocol: ProtocolWireGuard}
		_, content, err := Render(wgClient, accWG, FormatMihomo)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
			t.Fatalf("invalid YAML: %v\n%s", err, content)
		}
	})
}

func TestRenderCrossProtocolRejected(t *testing.T) {
	acc := AccessConfig{PublicHost: "vpn.example.com", PublicPort: 1080, Protocol: ProtocolSocks5}
	socksClient := Client{ID: "cl-1", Name: "a", Platform: PlatformIOS, Protocol: ProtocolSocks5, Credential: "C"}
	wgClient := Client{ID: "cl-2", Name: "b", Platform: PlatformIOS, Protocol: ProtocolWireGuard, Credential: "C"}

	if _, _, err := Render(wgClient, acc, FormatSocks); err == nil {
		t.Fatalf("socks config generated for a wireguard device")
	}
	if _, _, err := Render(socksClient, acc, FormatWireGuard); err == nil {
		t.Fatalf("wireguard config generated for a socks5 device")
	}
	if _, _, err := Render(socksClient, acc, "openvpn"); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("err = %v, want ErrUnknownFormat", err)
	}
}

func TestRenderNoPublicHost(t *testing.T) {
	c := Client{ID: "cl-1", Name: "a", Platform: PlatformIOS, Protocol: ProtocolSocks5, Credential: "C"}
	for _, f := range []string{FormatSocks, FormatSingBox, FormatMihomo} {
		if _, _, err := Render(c, DefaultAccess(), f); !errors.Is(err, ErrNoPublicHost) {
			t.Fatalf("%s: err = %v, want ErrNoPublicHost", f, err)
		}
	}
	wg := Client{ID: "cl-2", Name: "b", Platform: PlatformIOS, Protocol: ProtocolWireGuard, Credential: "C"}
	if _, _, err := Render(wg, DefaultAccess(), FormatWireGuard); !errors.Is(err, ErrNoPublicHost) {
		t.Fatalf("wireguard: err = %v, want ErrNoPublicHost", err)
	}
	if _, _, err := Render(c, DefaultAccess(), FormatSingBox); err == nil || !strings.Contains(err.Error(), "未设置对外接入地址") {
		t.Fatalf("error message must state the missing address, got %v", err)
	}
}

func TestFormatsList(t *testing.T) {
	socks := Formats(Client{Protocol: ProtocolSocks5})
	if strings.Join(socks, ",") != "singbox,mihomo,socks" {
		t.Fatalf("socks formats = %v", socks)
	}
	wg := Formats(Client{Protocol: ProtocolWireGuard})
	if strings.Join(wg, ",") != "singbox,mihomo,wireguard" {
		t.Fatalf("wireguard formats = %v", wg)
	}
}

func TestValidation(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	long := strings.Repeat("x", 65)
	cases := []struct {
		name    string
		request CreateRequest
	}{
		{"empty name", CreateRequest{Name: "  ", Platform: PlatformIOS}},
		{"name too long", CreateRequest{Name: long, Platform: PlatformIOS}},
		{"bad platform", CreateRequest{Name: "a", Platform: "solaris"}},
		{"empty platform", CreateRequest{Name: "a"}},
		{"bad protocol", CreateRequest{Name: "a", Platform: PlatformIOS, Protocol: "vless"}},
		{"expired timestamp", CreateRequest{Name: "a", Platform: PlatformIOS, ExpiresAt: past}},
		{"malformed timestamp", CreateRequest{Name: "a", Platform: PlatformIOS, ExpiresAt: "2026-13-45"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			_, _, err := s.Create(tc.request)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if len(s.List()) != 0 {
				t.Fatalf("invalid request was persisted")
			}
		})
	}

	accessCases := []struct {
		name string
		acc  AccessConfig
	}{
		{"scheme in host", AccessConfig{PublicHost: "http://example.com", PublicPort: 1080, Protocol: ProtocolSocks5}},
		{"port in host", AccessConfig{PublicHost: "example.com:443", PublicPort: 1080, Protocol: ProtocolSocks5}},
		{"space in host", AccessConfig{PublicHost: "exa mple.com", PublicPort: 1080, Protocol: ProtocolSocks5}},
		{"path in host", AccessConfig{PublicHost: "example.com/x", PublicPort: 1080, Protocol: ProtocolSocks5}},
		{"ipv6 literal", AccessConfig{PublicHost: "::1", PublicPort: 1080, Protocol: ProtocolSocks5}},
		{"port out of range", AccessConfig{PublicHost: "example.com", PublicPort: 70000, Protocol: ProtocolSocks5}},
		{"bad access protocol", AccessConfig{PublicHost: "example.com", PublicPort: 1080, Protocol: "vless"}},
	}
	for _, tc := range accessCases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			_, _, err := s.SaveAccess(tc.acc)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if got := s.Access(); got.PublicHost != "" || got.PublicPort != 1080 {
				t.Fatalf("rejected access was persisted: %+v", got)
			}
		})
	}

	// Valid hosts must pass.
	for _, host := range []string{"", "example.com", "vpn.example.com", "1.2.3.4", "my-host_1.example"} {
		s := newTestStore(t)
		if _, _, err := s.SaveAccess(AccessConfig{PublicHost: host, PublicPort: 1080, Protocol: ProtocolSocks5}); err != nil {
			t.Fatalf("host %q rejected: %v", host, err)
		}
	}
}

func TestSaveAccessChanged(t *testing.T) {
	s := newTestStore(t)
	_, changed, err := s.SaveAccess(AccessConfig{PublicHost: "a.example.com", PublicPort: 8443, Protocol: ProtocolHTTP})
	if err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	want := map[string]bool{"public_host": true, "public_port": true, "protocol": true}
	if len(changed) != len(want) {
		t.Fatalf("changed = %v, want %v", changed, want)
	}
	for _, c := range changed {
		if !want[c] {
			t.Fatalf("unexpected changed field %q", c)
		}
	}
	_, changed2, err := s.SaveAccess(AccessConfig{PublicHost: "a.example.com", PublicPort: 8443, Protocol: ProtocolHTTP})
	if err != nil {
		t.Fatalf("SaveAccess 2: %v", err)
	}
	if len(changed2) != 0 {
		t.Fatalf("no-op save reported changes: %v", changed2)
	}
	// Port 0 is normalised to the documented default rather than rejected.
	got, _, err := s.SaveAccess(AccessConfig{PublicHost: "a.example.com", PublicPort: 0, Protocol: ProtocolHTTP})
	if err != nil {
		t.Fatalf("SaveAccess with port 0: %v", err)
	}
	if got.PublicPort != 1080 {
		t.Fatalf("port 0 normalised to %d, want 1080", got.PublicPort)
	}
}
