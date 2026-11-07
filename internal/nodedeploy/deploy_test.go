package nodedeploy

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

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
	if s.SNI != DefaultRealitySNI {
		t.Fatalf("sni = %q", s.SNI)
	}
	if s.UUID == "" || s.RealityPrivate == "" || s.RealityPublic == "" || s.ShortID == "" {
		t.Fatalf("credentials not minted: %+v", s)
	}
	if len(s.ShortID) != 8 {
		t.Fatalf("short id = %q, want 8 hex chars", s.ShortID)
	}

	// Re-preparing an existing spec must not rotate live credentials.
	again, err := Prepare(s)
	if err != nil {
		t.Fatalf("Prepare(again): %v", err)
	}
	if again.UUID != s.UUID || again.RealityPrivate != s.RealityPrivate || again.ShortID != s.ShortID {
		t.Fatal("Prepare rotated credentials on a re-run")
	}
}

// TestPrepareKeepsReportIdentity: the probe id/token are minted by the API
// layer (agent registry), so Prepare must carry them through untouched — a
// re-render that silently dropped the token would cut the node off the panel.
func TestPrepareKeepsReportIdentity(t *testing.T) {
	s, err := Prepare(Spec{HostID: "vps-main", Address: "203.0.113.9",
		AgentID: "ag-abc123", AgentToken: "deadbeef", PanelURL: "https://panel.example.com/"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if s.AgentID != "ag-abc123" || s.AgentToken != "deadbeef" {
		t.Fatalf("report identity lost: %+v", s)
	}
	if s.PanelURL != "https://panel.example.com" {
		t.Fatalf("panel url = %q, want trailing slash trimmed", s.PanelURL)
	}
	if got := s.PublicSpec(); got.AgentToken != "" || got.RealityPrivate != "" {
		t.Fatal("PublicSpec leaked a secret")
	}
	if _, err := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", PanelURL: "panel:9191"}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for a url without scheme", err)
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
	s, err := Prepare(Spec{HostID: "vps-main", HostName: "主 VPS", Address: "203.0.113.9", PanelURL: "http://10.0.0.2:9191"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, want := range []string{
		"#!/usr/bin/env bash",
		`step 1 "`, `step 6 "`, `step 12 "`,
		"ok 12",
		"NGG_NODE_READY node=%s",
		"/etc/ngg/node.json",
		"/etc/ngg/xray/config.json",
		"ngg-node.service",
		"ngg-probe.service",
		"/usr/local/bin/ngg-node",
		"/usr/local/bin/ngg-probe",
		"/etc/ngg/agent.conf",
		"/v1/agent/ping",
		"set -uo pipefail",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
	if strings.Contains(script, "{{") {
		t.Fatal("script still contains placeholders")
	}
	if got := len(regexp.MustCompile(`(?m)^step [0-9]+ "`).FindAllString(script, -1)); got != 12 {
		t.Fatalf("expected 12 step calls, got %d", got)
	}
	// The probe must learn where to report from the plan, not from a guess.
	if !strings.Contains(script, "PANEL_URL='http://10.0.0.2:9191'") {
		t.Fatal("agent.conf does not carry the panel url")
	}
	// The panel must never ship a panel credential; only node-local secrets.
	if strings.Contains(script, "NGG-TOKEN") || strings.Contains(script, "session=") {
		t.Fatal("script mentions panel session material")
	}
	// No SSH-based state collection may come back into the generated script.
	if strings.Contains(script, "ngg-node handshake") {
		t.Fatal("script still installs the SSH handshake path")
	}
}

func TestScriptQuotesConfigHeredoc(t *testing.T) {
	s, _ := Prepare(Spec{HostID: "n1", Address: "203.0.113.9"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	if !strings.Contains(script, "<<'"+configDelimiter+"'") {
		t.Fatal("kernel config heredoc must be quoted so bash does not expand it")
	}
	if !strings.Contains(script, "\n"+configDelimiter+"\n") {
		t.Fatal("kernel config heredoc not terminated")
	}
	// A literal $ inside the config must survive: it is a real xray field
	// (e.g. password hashes) and must not be expanded by the shell.
	if strings.Contains(script, `"$REALITY_PUBLIC"`) == false {
		t.Fatal("reality verification step missing")
	}
}

func TestParseLog(t *testing.T) {
	out := strings.Join([]string{
		"[NGG] NGG 节点部署开始：n1（reality，203.0.113.9:3372）",
		"",
		"NGG_STEP 1 预检：root / systemd / 架构 / 端口 / 磁盘",
		"NGG_NOTE ufw 未启用或未安装，跳过 ufw",
		"NGG_OK 1",
		"NGG_STEP 2 安装依赖（只装缺少的包）",
		"NGG_FAIL 2 依赖安装失败：curl",
		"[NGG] 部署中止：依赖安装失败：curl",
	}, "\n")
	steps, notes, ready := ParseLog(out)
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2: %+v", len(steps), steps)
	}
	if steps[0].Status != StepDone || steps[0].Title == "" {
		t.Fatalf("step 1 = %+v", steps[0])
	}
	if steps[1].Status != StepFailed || steps[1].Detail != "依赖安装失败：curl" {
		t.Fatalf("step 2 = %+v", steps[1])
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "ufw") {
		t.Fatalf("notes = %v", notes)
	}
	if ready != nil {
		t.Fatal("ready must be nil for a failed run")
	}
}

func TestParseLogReady(t *testing.T) {
	out := "NGG_STEP 12 自检：监听 / 单元 / 上报\nNGG_OK 12\n" +
		"NGG_NODE_READY node=vps-main port=3372 mode=reality listen=1 config_sha=abc123 probe=up\n"
	_, _, ready := ParseLog(out)
	if ready == nil {
		t.Fatal("ready marker not parsed")
	}
	if ready.NodeID != "vps-main" || ready.Port != 3372 || ready.Mode != "reality" || !ready.Listening || ready.ConfigSHA != "abc123" {
		t.Fatalf("ready = %+v", ready)
	}
	if ready.Probe != "up" {
		t.Fatalf("probe = %q, want up", ready.Probe)
	}
}

// TestScriptRejectsQuoteInPanelURL: the probe config is a shell file with
// single-quoted values, so a value that could break out of the assignment must
// be refused instead of rendered.
func TestScriptRejectsQuoteInPanelURL(t *testing.T) {
	s, err := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", PanelURL: "http://ok.example.com"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	s.PanelURL = "http://x'; rm -rf / #"
	if _, err := Script(s); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
}

// TestProbeIsInstalledQuoted: the probe body and its config must be written
// byte-for-byte (quoted heredoc) and the config must be 0600.
func TestProbeIsInstalledQuoted(t *testing.T) {
	s, _ := Prepare(Spec{HostID: "n1", Address: "203.0.113.9", PanelURL: "http://10.0.0.2:9191"})
	script, err := Script(s)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, want := range []string{
		"<<'" + probeDelimiter + "'",
		"<<'" + probeConfDelimiter + "'",
		`chmod 600 "$PROBE_CONF"`,
		`sh -n "$PROBE_BIN"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
	if strings.Count(script, "\n"+probeDelimiter+"\n") != 1 {
		t.Fatal("probe heredoc not terminated exactly once")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	s, _ := Prepare(Spec{HostID: "vps-main", Address: "203.0.113.9", AgentID: "ag-1", AgentToken: "tok-1"})
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
	if got.Spec.AgentToken != s.AgentToken || got.Script != script {
		t.Fatal("round trip changed the record")
	}

	// The browser projection must not leak node secrets.
	v := got.View(true)
	if v.Spec.AgentToken != "" || v.Spec.RealityPrivate != "" {
		t.Fatal("View leaked node secrets")
	}
	if v.Spec.AgentID == "" {
		t.Fatal("View dropped the probe id (needed to show the report identity)")
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
