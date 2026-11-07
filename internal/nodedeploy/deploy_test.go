package nodedeploy

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/nodeapi"
	"github.com/ngg-project/ngg/internal/nodepki"
)

// withCerts fills the transient TLS material Script needs, the way the API
// layer does after Prepare.
func withCerts(t *testing.T, s Spec) Spec {
	t.Helper()
	ca, err := nodepki.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	leaf, err := ca.IssueNode(s.HostID, s.Address)
	if err != nil {
		t.Fatalf("IssueNode: %v", err)
	}
	s.NodeCertPEM, s.NodeKeyPEM, s.CAPEM = leaf.CertPEM, leaf.KeyPEM, ca.CAPEM()
	return s
}

func mustPrepare(t *testing.T, s Spec) Spec {
	t.Helper()
	out, err := Prepare(s)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return withCerts(t, out)
}

func TestPrepareDefaultsAndCredentials(t *testing.T) {
	s, err := Prepare(Spec{HostID: "vps-main", HostName: "主 VPS", Address: "203.0.113.9"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if s.Mode != ModeReality {
		t.Fatalf("mode = %q, want reality", s.Mode)
	}
	if s.Port != DefaultPort {
		t.Fatalf("port = %d, want %d", s.Port, DefaultPort)
	}
	if s.NodePort != DefaultNodePort {
		t.Fatalf("node port = %d, want %d", s.NodePort, DefaultNodePort)
	}
	if s.SNI != DefaultRealitySNI {
		t.Fatalf("sni = %q", s.SNI)
	}
	if s.UUID == "" || s.RealityPrivate == "" || s.RealityPublic == "" || s.ShortID == "" {
		t.Fatalf("credentials not minted: %+v", s)
	}
	if len(s.ShortID) != 8 {
		t.Fatalf("short id = %q, want 8 hex chars", s.ShortID)
	}
	if !strings.HasPrefix(s.NodeBinBase, "https://github.com/") {
		t.Fatalf("node bin base = %q", s.NodeBinBase)
	}

	// Re-preparing an existing spec must not rotate live credentials.
	again, err := Prepare(s)
	if err != nil {
		t.Fatalf("Prepare(again): %v", err)
	}
	if again.UUID != s.UUID || again.RealityPrivate != s.RealityPrivate || again.ShortID != s.ShortID {
		t.Fatal("Prepare rotated credentials on a re-run")
	}
	if again.NodePort != s.NodePort {
		t.Fatal("Prepare changed the management port on a re-run")
	}
}

func TestPrepareRejectsCollidingPorts(t *testing.T) {
	_, err := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", Port: 2222, NodePort: 2222})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec when data port == management port", err)
	}
	_, err = Prepare(Spec{HostID: "n1", Address: "203.0.113.9", NodePort: 70000})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for an out-of-range port", err)
	}
}

// TestPublicSpecHidesSecrets: the browser projection must never carry the
// REALITY private key, the node password or the profile source.
func TestPublicSpecHidesSecrets(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "n1", Address: "203.0.113.9"})
	pub := s.PublicSpec()
	if pub.RealityPrivate != "" || pub.Password != "" {
		t.Fatal("PublicSpec leaked a secret")
	}
	if pub.RealityPublic == "" || pub.UUID == "" {
		t.Fatal("PublicSpec dropped client-visible parameters")
	}
}

// TestRealityKeypairEncoding pins the encoding against a pair produced by
// `xray x25519 -i` on a real node: same private key, same public key.
func TestRealityKeypairEncoding(t *testing.T) {
	const (
		priv = "AJVD3oSeL-gbJAI-Vi7z8v4h4dD23JfAo6U_zKZRg3Y"
		want = "KqZwrC9WwBvKINcUEYRBBUx4V0XPRnwJBLIUVSSoVlc"
	)
	raw, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	got := base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
	if got != want {
		t.Fatalf("public key = %q, want %q (encoding drifted from xray)", got, want)
	}
}

func TestConfigJSONReality(t *testing.T) {
	s, err := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", Port: 8443})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	raw, err := ConfigJSON(s)
	if err != nil {
		t.Fatalf("ConfigJSON: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Port           int    `json:"port"`
			Protocol       string `json:"protocol"`
			StreamSettings struct {
				Security        string `json:"security"`
				RealitySettings struct {
					Dest        string   `json:"dest"`
					PrivateKey  string   `json:"privateKey"`
					ServerNames []string `json:"serverNames"`
					ShortIDs    []string `json:"shortIds"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
		Routing struct {
			Rules []struct {
				IP []string `json:"ip"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	if len(cfg.Inbounds) != 1 {
		t.Fatalf("inbounds = %d", len(cfg.Inbounds))
	}
	in := cfg.Inbounds[0]
	if in.Port != 8443 || in.Protocol != "vless" {
		t.Fatalf("inbound = %+v", in)
	}
	if in.StreamSettings.Security != "reality" {
		t.Fatalf("security = %q", in.StreamSettings.Security)
	}
	if in.StreamSettings.RealitySettings.PrivateKey != s.RealityPrivate {
		t.Fatal("reality private key not written")
	}
	if got := in.StreamSettings.RealitySettings.Dest; got != s.SNI+":443" {
		t.Fatalf("dest = %q", got)
	}
	if strings.Contains(raw, "geoip:") {
		t.Fatal("config requires geoip.dat; private ranges must be literal CIDRs")
	}
	if len(cfg.Routing.Rules) != 1 || len(cfg.Routing.Rules[0].IP) == 0 {
		t.Fatal("private-range block rule missing")
	}
}

func TestConfigJSONTLSModes(t *testing.T) {
	for _, mode := range []string{ModeVLESS, ModeTrojan} {
		s, err := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", Mode: mode})
		if err != nil {
			t.Fatalf("%s: Prepare: %v", mode, err)
		}
		raw, err := ConfigJSON(s)
		if err != nil {
			t.Fatalf("%s: ConfigJSON: %v", mode, err)
		}
		if !strings.Contains(raw, NodeDir+"/tls/cert.pem") {
			t.Fatalf("%s: certificate path missing", mode)
		}
		if mode == ModeTrojan && !strings.Contains(raw, s.Password) {
			t.Fatalf("%s: password missing", mode)
		}
	}
}

func TestProfileModeValidation(t *testing.T) {
	_, err := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", Mode: ModeProfile,
		Kernel: "mihomo", ProfileContent: "mixed-port: 7890"})
	if !errors.Is(err, ErrProfileKernel) {
		t.Fatalf("err = %v, want ErrProfileKernel", err)
	}

	_, err = Prepare(Spec{HostID: "n1", Address: "203.0.113.9", Mode: ModeProfile,
		Kernel: "xray-core", ProfileContent: "not json"})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}

	s, err := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", Mode: ModeProfile,
		Kernel: "xray-core", ProfileContent: `{"inbounds":[{"port":8080,"protocol":"socks"}]}`})
	if err != nil {
		t.Fatalf("Prepare(profile): %v", err)
	}
	raw, err := ConfigJSON(s)
	if err != nil {
		t.Fatalf("ConfigJSON(profile): %v", err)
	}
	if raw != s.ProfileContent {
		t.Fatal("profile content must be written verbatim")
	}
}

func TestScriptContainsEveryStep(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "vps-main", HostName: "主 VPS", Address: "203.0.113.9"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -uo pipefail",
		// The node API and the kernel are separate units.
		"ngg-node.service",
		"ngg-core.service",
		"/usr/local/bin/ngg-node",
		"/usr/local/bin/nggctl",
		"/etc/ngg/node.json",
		"/etc/ngg/xray/config.json",
		"/etc/ngg/node/node.crt",
		"/etc/ngg/node/node.key",
		"/etc/ngg/node/ca.crt",
		// The panel dials in; the node never reports out.
		"https://127.0.0.1:${NODE_PORT}/node/xray/healthcheck",
		"-listen :${NODE_PORT}",
		"NGG_NODE_READY node=%s port=%s ports=%s",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
	if strings.Contains(script, "{{") {
		t.Fatal("script still contains placeholders")
	}
	if got := len(regexp.MustCompile(`(?m)^step [0-9]+ "`).FindAllString(script, -1)); got != 13 {
		t.Fatalf("expected 13 step calls, got %d", got)
	}
	// The Nezha-style probe is gone: nothing may ship to the panel. The only
	// remaining mention is the migration that removes it from an old node.
	for _, unwanted := range []string{"agent.conf", "/v1/agent/report", "X-NGG-Agent-Token", "PROBE_UNIT"} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("script still carries the outbound probe (%q)", unwanted)
		}
	}
	if !strings.Contains(script, "rm -f /etc/systemd/system/ngg-probe.service /usr/local/bin/ngg-probe") {
		t.Fatal("script does not remove the v2.12 probe from an existing node")
	}
	// No SSH-based execution may come back either.
	if strings.Contains(script, "ssh ") || strings.Contains(script, "sshd_config") {
		t.Fatal("script must not touch sshd")
	}
}

func TestScriptPinsNodeBinaryDigest(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "n1", Address: "203.0.113.9"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	// No embedded digest (dev build): the script must say so at run time
	// instead of pretending it verified the download.
	if !strings.Contains(script, "没有为 ${NGG_ARCH} 内置摘要") {
		t.Fatal("script does not disclose the missing digest")
	}

	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	s.NodeBinSHA256AMD64 = digest
	script, err = Script(s)
	if err != nil {
		t.Fatalf("Script(digest): %v", err)
	}
	if !strings.Contains(script, `SHA_AMD64="`+digest+`"`) {
		t.Fatal("embedded digest not rendered into the script")
	}
	if !strings.Contains(script, `sha256sum /tmp/ngg-node.pkg`) {
		t.Fatal("script does not verify the downloaded binary")
	}
	if !strings.Contains(script, `if [ -n "$WANT_SHA" ]`) {
		t.Fatal("digest verification is not conditional on the digest being present")
	}
}

func TestScriptWritesCertificatesQuoted(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "n1", Address: "203.0.113.9"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, want := range []string{
		"<<'" + certDelimiter + "'",
		"<<'" + keyDelimiter + "'",
		"<<'" + caDelimiter + "'",
		`chmod 600 "$KEY"`,
		`openssl x509 -in "$CERT" -noout -subject`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
	for _, delim := range []string{certDelimiter, keyDelimiter, caDelimiter} {
		if got := strings.Count(script, "\n"+delim+"\n"); got != 1 {
			t.Fatalf("%s heredoc appears %d times, want exactly 1", delim, got)
		}
	}
	if !strings.Contains(script, string(s.NodeKeyPEM)) {
		t.Fatal("node private key not embedded")
	}
}

// TestScriptRejectsUnsafeValues: everything that lands in a double-quoted
// assignment or a heredoc delimiter must be refused, not escaped.
func TestScriptRejectsUnsafeValues(t *testing.T) {
	base := mustPrepare(t, Spec{HostID: "n1", HostName: "节点", Address: "203.0.113.9"})

	cases := []struct {
		name string
		alt  func(*Spec)
	}{
		{"quote in name", func(s *Spec) { s.HostName = `x"; rm -rf / #` }},
		{"command substitution in name", func(s *Spec) { s.HostName = "x$(id)" }},
		{"newline in name", func(s *Spec) { s.HostName = "a\nb" }},
		{"space in address", func(s *Spec) { s.Address = "1.2.3.4 ; id" }},
		{"bad node bin scheme", func(s *Spec) { s.NodeBinBase = "file:///etc/passwd" }},
		{"non-hex digest", func(s *Spec) { s.NodeBinSHA256AMD64 = "zz" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.alt(&s)
			if _, err := Script(s); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("err = %v, want ErrInvalidSpec", err)
			}
		})
	}

	// Missing certificate material is a hard error, not an empty heredoc.
	s := base
	s.NodeCertPEM = nil
	if _, err := Script(s); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for missing certificates", err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	s := mustPrepare(t, Spec{HostID: "vps-main", Address: "203.0.113.9"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	rec := Record{HostID: s.HostID, Spec: s, Script: script, GeneratedAt: s.GeneratedAt}
	if err := st.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := st.Load("vps-main")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Script != script {
		t.Fatal("round trip changed the script")
	}
	// The certificate material is transient on purpose: the script holds it.
	if len(got.Spec.NodeKeyPEM) != 0 {
		t.Fatal("node private key was persisted a second time")
	}
	if got.Spec.NodePort != s.NodePort || got.Spec.UUID != s.UUID {
		t.Fatal("round trip lost spec fields")
	}

	// The browser projection must not leak node secrets.
	v := got.View(true)
	if v.Spec.RealityPrivate != "" {
		t.Fatal("View leaked node secrets")
	}
	if len(v.Params) == 0 {
		t.Fatal("View lost client params")
	}

	if _, err := st.Load("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(missing) err = %v", err)
	}
	if _, err := st.Load("../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("path traversal must be rejected, got %v", err)
	}

	// List is what the metrics poller iterates.
	all := st.List()
	if len(all) != 1 || all[0].HostID != "vps-main" {
		t.Fatalf("List = %+v", all)
	}
}

func TestShareURIUsesReadableFragment(t *testing.T) {
	s, err := Prepare(Spec{
		HostID: "vps-main", HostName: "主 VPS · 203.0.113.9", Address: "203.0.113.9",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	uri := s.ShareURI()
	if !strings.HasSuffix(uri, "#VPS-203-0-113-9") {
		t.Fatalf("fragment = %q, want a collapsed ASCII name", uri)
	}
	if strings.Contains(uri, "flow=") {
		t.Fatal("plain REALITY without xtls-rprx-vision must not advertise a flow")
	}
	if !strings.Contains(uri, "security=reality") || !strings.Contains(uri, s.RealityPublic) {
		t.Fatalf("uri missing reality parameters: %s", uri)
	}
}

// Remnawave's remnanode listens on 2222 by default. If NGG used the same port,
// a VPS running both products would always fail the mTLS handshake against the
// wrong service, so the defaults must stay apart.
func TestDefaultNodePortAvoidsRemnawave(t *testing.T) {
	if DefaultNodePort == 2222 {
		t.Fatal("default node port must not collide with remnanode's 2222")
	}
	if DefaultNodePort != nodeapi.DefaultPort {
		t.Fatalf("nodedeploy default %d != nodeapi default %d", DefaultNodePort, nodeapi.DefaultPort)
	}
}

// A hung `xray run -test` used to leave the install sitting at step 7 with no
// output at all, which is exactly what an operator reported. It must be bounded
// with a readable failure, and a busy management port must name the cause.
func TestScriptBoundsKernelValidationAndExplainsPortCollision(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "01", Address: "50.118.185.194"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, want := range []string{
		"timeout 45",
		"内核校验超时（45 秒）",
		"改用默认 REALITY 模式",
		"2222 通常已被 Remnawave remnanode 占用",
		"把管理端口改为 2244",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
}

// The install script must diagnose missing TLS files itself (step 6), not wait
// for Xray's nested "failed to build TLS config" error at step 7.
func TestScriptChecksReferencedCertificateFiles(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "01", Address: "50.118.185.194"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, want := range []string{
		"MISSING_FILES",
		`certificateFile|keyFile`,
		"配置引用了节点上不存在的证书/密钥文件",
		"改用内置 REALITY 模式重新生成脚本",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
}

// A profile decides its own listen ports and may reference TLS files that only
// exist on the node. The script must follow the profile's ports (not the number
// typed in the panel) and self-sign missing certificates, otherwise the install
// either aborts at step 6 or fails its port check at step 13.
func TestScriptFollowsProfilePortsAndSelfSignsCertificates(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "01", Address: "50.118.185.194"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, want := range []string{
		`CFG_PORTS="$(grep -oE '"port"`,
		`档案声明监听的端口`,
		`NGG_CERT_PAIRS`,
		`openssl req -x509`,
		`TLS_SELF_SIGNED=1`,
		`for p in $CFG_PORTS; do`,
		`档案声明的端口（${CFG_PORTS}）都没有处于监听状态`,
		`tls_self_signed=%s`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
}

// The API unit template used to contain a raw ${NODE_DIR} instead of the
// {{NODE_DIR}} placeholder. The script runs under `set -u`, so expanding an
// undefined NODE_DIR aborted the heredoc, the old v2.12 ngg-node.service stayed
// in place and the panel ended up talking to Xray on :3372. Guard both halves:
// the variable is defined, and the unit carries a resolved path.
func TestScriptWritesUnitsWithResolvedPaths(t *testing.T) {
	s := mustPrepare(t, Spec{HostID: "01", Address: "50.118.185.194"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	if strings.Contains(script, "ReadWritePaths=${NODE_DIR}") {
		t.Fatal("unit template still uses an undefined ${NODE_DIR}")
	}
	if !strings.Contains(script, `NODE_DIR="`+NodeDir+`"`) {
		t.Fatalf("script does not define NODE_DIR=%q", NodeDir)
	}
	if !strings.Contains(script, "ReadWritePaths="+NodeDir+" /run") {
		t.Fatalf("unit does not carry the resolved directory %q", NodeDir)
	}
	for _, want := range []string{
		`[ -s "/etc/systemd/system/${API_UNIT}" ] || die 9`,
		`[ -s "/etc/systemd/system/${CORE_UNIT}" ] || die 10`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing unit-write guard %q", want)
		}
	}
}
