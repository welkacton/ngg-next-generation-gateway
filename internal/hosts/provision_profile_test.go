package hosts

import (
	"strings"
	"testing"
)

// Binding a 协议配置 profile must write the profile's rendered content into the
// node verbatim and pick up its kernel — no protocol/port re-derivation by hand.
func TestProvisionWithProfileWritesProfileContent(t *testing.T) {
	reg := newProvisionRegistry(t)

	content := `{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {"tag": "vless-in", "listen": "0.0.0.0", "port": 8443, "protocol": "vless",
     "streamSettings": {"security": "tls", "tlsSettings": {"serverName": "edge.example.com"}}}
  ],
  "outbounds": [{"protocol": "freedom"}]
}`
	p, err := reg.ProvisionWithProfile("us01", ProvisionProfile{
		ID: "edge-vless", Name: "Edge VLESS", Kernel: "xray-core", Format: "json",
		Content: content,
	})
	if err != nil {
		t.Fatalf("ProvisionWithProfile: %v", err)
	}
	if p.ProfileID != "edge-vless" || p.ProfileName != "Edge VLESS" || p.Kernel != "xray-core" {
		t.Fatalf("binding not recorded: %+v", p)
	}
	if p.Port != 8443 {
		t.Fatalf("port should come from the config, got %d", p.Port)
	}
	if p.Protocol != "vless" {
		t.Fatalf("protocol should come from the config, got %q", p.Protocol)
	}
	if p.Endpoint.SNI != "edge.example.com" {
		t.Fatalf("SNI should come from tlsSettings, got %q", p.Endpoint.SNI)
	}
	if !strings.Contains(p.Script, content) {
		t.Fatal("script must embed the rendered profile verbatim")
	}
	if !strings.Contains(p.Script, "run -test -config") {
		t.Fatal("script must validate the config with the kernel itself")
	}
	if !strings.Contains(p.Script, "NGG-NODE-READY protocol=vless") {
		t.Fatal("script lost the ready marker")
	}
	// The binding survives a reload, so the console can show it again.
	again, err := reg.LoadProvision("us01")
	if err != nil {
		t.Fatalf("LoadProvision: %v", err)
	}
	if again.ProfileID != "edge-vless" {
		t.Fatalf("binding not persisted: %+v", again)
	}
}

func TestProvisionWithProfileRejectsBadInput(t *testing.T) {
	reg := newProvisionRegistry(t)
	cases := []struct {
		name string
		pp   ProvisionProfile
		want string
	}{
		{"empty content", ProvisionProfile{Name: "x", Kernel: "xray-core"}, "内容为空"},
		{"bad kernel", ProvisionProfile{Name: "x", Kernel: "v2ray", Content: "{}"}, "不支持的配置文件内核"},
		{"reserved marker", ProvisionProfile{Name: "x", Kernel: "xray-core", Content: profileDelimiter}, "保留标记"},
	}
	for _, c := range cases {
		if _, err := reg.ProvisionWithProfile("us01", c.pp); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err=%v want %q", c.name, err, c.want)
		}
	}
}

// The port/protocol hints are display-only helpers; they must read both config
// dialects and never invent a value.
func TestProfileHints(t *testing.T) {
	yaml := "mixed-port: 7890\nlisteners:/n  - name: in\n    port: 7891\n"
	if got := guessPort(yaml); got != 7890 {
		t.Fatalf("yaml port = %d, want 7890", got)
	}
	jsonCfg := `{"inbounds":[{"listen_port": 2087}]}`
	if got := guessPort(jsonCfg); got != 2087 {
		t.Fatalf("json port = %d, want 2087", got)
	}
	if got := guessPort("no ports here"); got != 0 {
		t.Fatalf("missing port should be 0, got %d", got)
	}
	if got := guessProtocol("... hysteria2 ..."); got != "hysteria2" {
		t.Fatalf("protocol = %q", got)
	}
	if got := guessProtocol("nothing"); got != "" {
		t.Fatalf("unknown protocol should be empty, got %q", got)
	}
	if got := guessSNI(`"server_name": "a.b.example.com"`); got != "a.b.example.com" {
		t.Fatalf("sni = %q", got)
	}
}
