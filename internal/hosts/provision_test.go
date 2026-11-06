package hosts

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// newProvisionRegistry builds a registry with one real host record. Replace is
// used instead of Add so no TCP probe is attempted against TEST-NET-3.
func newProvisionRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	reg := NewRegistry(filepath.Join(dir, "hosts.json"))
	_, err := reg.Replace([]Host{{
		ID:       "us01",
		Name:     "US us01",
		Internal: "us01",
		Country:  "US",
		Address:  "203.0.113.10",
		NodePort: 2222,
		Role:     RoleOutbound,
		Enabled:  true,
	}})
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return reg
}

func TestProvisionVLESS(t *testing.T) {
	reg := newProvisionRegistry(t)
	p, err := reg.Provision("us01", "vless", 0, "")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !uuidRE.MatchString(p.Credential) {
		t.Fatalf("vless credential is not a UUIDv4: %q", p.Credential)
	}
	if p.Port != 443 {
		t.Fatalf("default port = %d, want 443", p.Port)
	}
	if p.Endpoint.Protocol != "vless" || p.Endpoint.Credential != p.Credential {
		t.Fatalf("endpoint mismatch: %+v", p.Endpoint)
	}
	if p.Endpoint.Address != "203.0.113.10" || p.Endpoint.Transport != "tcp" || !p.Endpoint.TLS {
		t.Fatalf("endpoint shape: %+v", p.Endpoint)
	}
	// A bare IP cannot be a TLS server name.
	if p.Endpoint.SNI != fallbackSNI {
		t.Fatalf("sni = %q, want %q", p.Endpoint.SNI, fallbackSNI)
	}
	for _, want := range []string{
		"set -euo pipefail",
		`if [ "$(id -u)" -ne 0 ]`,
		"install-release.sh",
		"openssl req -x509",
		"/usr/local/etc/xray/config.json",
		`"protocol": "vless"`,
		p.Credential,
		"systemctl enable xray",
		"ufw allow",
		"iptables -I INPUT",
		"NGG-NODE-READY protocol=vless port=443",
	} {
		if !strings.Contains(p.Script, want) {
			t.Errorf("vless script missing %q", want)
		}
	}
	assertNoPanelCredential(t, p.Script)
	if len(p.Notes) < 2 {
		t.Fatalf("want at least 2 honest notes, got %d", len(p.Notes))
	}
}

func TestProvisionTrojan(t *testing.T) {
	reg := newProvisionRegistry(t)
	p, err := reg.Provision("us01", "trojan", 8443, "node.example.com")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	// 24 random bytes → 32 URL-safe chars, no padding.
	if len(p.Credential) != 32 || !regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`).MatchString(p.Credential) {
		t.Fatalf("trojan credential shape: %q", p.Credential)
	}
	if p.Port != 8443 || p.Endpoint.SNI != "node.example.com" {
		t.Fatalf("trojan endpoint: %+v", p.Endpoint)
	}
	if !p.Endpoint.TLS {
		t.Fatalf("trojan must be TLS")
	}
	for _, want := range []string{
		`"protocol": "trojan"`,
		`"password": "` + p.Credential + `"`,
		`-subj "/CN=${SNI}"`,
		`SNI="node.example.com"`,
		"NGG-NODE-READY protocol=trojan port=8443",
	} {
		if !strings.Contains(p.Script, want) {
			t.Errorf("trojan script missing %q", want)
		}
	}
	assertNoPanelCredential(t, p.Script)
}

func TestProvisionShadowsocks(t *testing.T) {
	reg := newProvisionRegistry(t)
	p, err := reg.Provision("us01", "shadowsocks", 0, "")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if p.Port != 8388 {
		t.Fatalf("default port = %d, want 8388", p.Port)
	}
	raw, err := base64.StdEncoding.DecodeString(p.Credential)
	if err != nil || len(raw) != 16 {
		t.Fatalf("shadowsocks password must be base64 of 16 bytes: %q (%v)", p.Credential, err)
	}
	if p.Endpoint.TLS {
		t.Fatalf("shadowsocks must not claim TLS")
	}
	for _, want := range []string{
		"sing-box.app/install.sh",
		`"method": "aes-256-gcm"`,
		`"password": "` + p.Credential + `"`,
		`"type": "shadowsocks"`,
		"/etc/sing-box/config.json",
		"check -c /etc/sing-box/config.json",
		"NGG-NODE-READY protocol=shadowsocks port=8388",
	} {
		if !strings.Contains(p.Script, want) {
			t.Errorf("shadowsocks script missing %q", want)
		}
	}
	if strings.Contains(p.Script, "openssl req -x509") {
		t.Errorf("shadowsocks script must not generate a certificate")
	}
	assertNoPanelCredential(t, p.Script)
}

// TestProvisionCredentialsAreFresh only asserts randomness, not quality: two
// generations must not reuse a credential.
func TestProvisionCredentialsAreFresh(t *testing.T) {
	reg := newProvisionRegistry(t)
	a, err := reg.Provision("us01", "vless", 0, "")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := reg.Provision("us01", "vless", 0, "")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.Credential == b.Credential {
		t.Fatal("credential reused across provisions")
	}
}

func TestProvisionPersistsAndReloads(t *testing.T) {
	reg := newProvisionRegistry(t)
	saved, err := reg.Provision("us01", "trojan", 443, "")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	path := filepath.Join(filepath.Dir(reg.Path()), "provisions", "us01.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat provision file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("provision file mode = %o, want 600", mode)
		}
	}

	loaded, err := reg.LoadProvision("us01")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.HostID != saved.HostID || loaded.Protocol != saved.Protocol ||
		loaded.Port != saved.Port || loaded.Credential != saved.Credential ||
		loaded.Script != saved.Script || loaded.Endpoint != saved.Endpoint {
		t.Fatalf("reloaded provision differs:\n saved=%+v\n loaded=%+v", saved, loaded)
	}
	if !loaded.GeneratedAt.Equal(saved.GeneratedAt) {
		t.Fatalf("generated_at drifted: %v vs %v", saved.GeneratedAt, loaded.GeneratedAt)
	}
	if len(loaded.Notes) != len(saved.Notes) {
		t.Fatalf("notes drifted: %d vs %d", len(saved.Notes), len(loaded.Notes))
	}
}

func TestLoadProvisionMissing(t *testing.T) {
	reg := newProvisionRegistry(t)
	if _, err := reg.LoadProvision("us01"); !errors.Is(err, ErrProvisionNotFound) {
		t.Fatalf("want ErrProvisionNotFound, got %v", err)
	}
	// Path traversal must not reach the filesystem.
	if _, err := reg.LoadProvision("../hosts"); !errors.Is(err, ErrProvisionNotFound) {
		t.Fatalf("traversal must be rejected, got %v", err)
	}
}

func TestProvisionRejectsBadInput(t *testing.T) {
	reg := newProvisionRegistry(t)

	if _, err := reg.Provision("nope", "vless", 0, ""); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("unknown host: want ErrUnknownHost, got %v", err)
	}
	if _, err := reg.Provision("us01", "hysteria2", 0, ""); err == nil {
		t.Fatal("unknown protocol must fail")
	}
	for _, port := range []int{-1, 65536, 70000} {
		if _, err := reg.Provision("us01", "vless", port, ""); err == nil {
			t.Fatalf("port %d must be rejected", port)
		}
	}
	if _, err := reg.Provision("us01", "vless", 443, "https://bad sni"); err == nil {
		t.Fatal("invalid SNI must be rejected")
	}
}

// assertNoPanelCredential enforces the hard rule: the script may contain the
// node's own UUID/password, never a panel/API credential.
func assertNoPanelCredential(t *testing.T, script string) {
	t.Helper()
	for _, forbidden := range []string{
		"NGG_TOKEN",
		"NGGA_TOKEN",
		"API_TOKEN",
		"api_token",
		"session_token",
		"ngg_session",
		"Cookie:",
		"Authorization",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("script leaks panel credential marker %q", forbidden)
		}
	}
}

// TestProvisionScriptIsValidBash runs `bash -n` so a syntax error in the
// generated script fails the build instead of the operator's VPS. Nothing is
// executed: -n only parses.
func TestProvisionScriptIsValidBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	reg := newProvisionRegistry(t)
	for _, proto := range []string{ProvProtoVLESS, ProvProtoTrojan, ProvProtoShadowsocks} {
		p, err := reg.Provision("us01", proto, 0, "")
		if err != nil {
			t.Fatalf("%s: provision: %v", proto, err)
		}
		path := filepath.Join(t.TempDir(), proto+".sh")
		if err := os.WriteFile(path, []byte(p.Script), 0o600); err != nil {
			t.Fatalf("%s: write script: %v", proto, err)
		}
		cmd := exec.Command(bash, "-n", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: bash -n failed: %v\n%s", proto, err, out)
		}
		if strings.TrimSpace(string(out)) != "" {
			t.Fatalf("%s: bash -n emitted diagnostics: %s", proto, out)
		}
	}
}
