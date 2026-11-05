package protocols

import (
	"strings"
	"testing"
)

func TestKernelSchemasAreComplete(t *testing.T) {
	schemas := Schemas()
	if len(schemas) != 3 {
		t.Fatalf("expected exactly 3 kernel schemas, got %d", len(schemas))
	}
	want := map[string]string{
		"mihomo":    DocMihomo,
		"sing-box":  DocSingbox,
		"xray-core": DocXray,
	}
	for _, s := range schemas {
		if s.DocURL != want[s.Kernel] || !strings.HasPrefix(s.DocURL, "https://") {
			t.Errorf("%s: wrong official doc URL %q", s.Kernel, s.DocURL)
		}
		if s.Syntax == "" || s.Description == "" || len(s.Fields) < 8 {
			t.Errorf("%s: schema is incomplete (%d fields)", s.Kernel, len(s.Fields))
		}
		seen := map[string]bool{}
		for _, f := range s.Fields {
			if f.Key == "" || f.Label == "" || f.Description == "" {
				t.Errorf("%s: field without key/label/description: %+v", s.Kernel, f)
			}
			if seen[f.Key] {
				t.Errorf("%s: duplicate field %s", s.Kernel, f.Key)
			}
			seen[f.Key] = true
		}
	}
	// The three grammars must actually use different keys for the same idea.
	if !hasField(SchemaFor("sing-box"), "server_port") || hasField(SchemaFor("xray-core"), "server_port") {
		t.Fatal("sing-box must use server_port while Xray uses vnext[].port")
	}
	if !hasField(SchemaFor("xray-core"), "address") || hasField(SchemaFor("mihomo"), "address") {
		t.Fatal("Xray must use address while Mihomo uses server")
	}
}

func hasField(s KernelSchema, key string) bool {
	for _, f := range s.Fields {
		if f.Key == key {
			return true
		}
	}
	return false
}

// One set of visual fields must produce three genuinely different, valid
// kernel-native configs, each following its own documented grammar.
func TestThreeKernelGrammarsAreDistinctAndValid(t *testing.T) {
	params := map[string]any{
		"server": "198.51.100.7", "address": "198.51.100.7",
		"port": 8443, "server_port": 8443,
		"uuid": "11111111-2222-3333-4444-555555555555", "id": "11111111-2222-3333-4444-555555555555",
		"password": "secret", "cipher": "aes-128-gcm", "method": "aes-128-gcm",
		"tls": true, "server_name": "example.com", "serverName": "example.com",
		"network": "ws", "transport": "ws", "stream_security": "tls",
		"path": "/ws", "Host": "example.com",
	}
	outputs := map[string]string{}
	for _, kernel := range []string{"mihomo", "sing-box", "xray-core"} {
		b, format, err := GenerateConfig("vmess", kernel, params)
		if err != nil {
			t.Fatalf("%s: %v", kernel, err)
		}
		if err := ValidateConfig(b, format); err != nil {
			t.Fatalf("%s produced invalid %s: %v\n%s", kernel, format, err, b)
		}
		outputs[kernel] = string(b)
	}
	m, s, x := outputs["mihomo"], outputs["sing-box"], outputs["xray-core"]

	for _, pair := range []struct{ name, text, want string }{
		{"mihomo is YAML proxies", m, "proxies:"},
		{"mihomo uses port", m, "port:"},
		{"sing-box uses server_port", s, `"server_port"`},
		{"sing-box nests tls", s, `"tls"`},
		{"sing-box nests transport", s, `"transport"`},
		{"xray uses settings.vnext", x, `"vnext"`},
		{"xray uses address", x, `"address"`},
		{"xray separates streamSettings", x, `"streamSettings"`},
	} {
		if !strings.Contains(pair.text, pair.want) {
			t.Errorf("%s: missing %q\n%s", pair.name, pair.want, pair.text)
		}
	}
	if strings.Contains(s, "vnext") || strings.Contains(x, `"server_port"`) {
		t.Fatal("kernel grammars leaked into each other")
	}
}

func TestAnnotateAndStripComments(t *testing.T) {
	params := map[string]any{"server": "https://example.com/path", "port": 443, "uuid": "abc"}
	content, format, err := GenerateConfig("vmess", "sing-box", params)
	if err != nil {
		t.Fatal(err)
	}
	annotated := string(Annotate(content, format, "sing-box"))
	if !strings.Contains(annotated, "// 官方文档") || !strings.Contains(annotated, DocSingbox) {
		t.Fatalf("annotation missing kernel doc header:\n%s", annotated)
	}
	if !strings.Contains(annotated, "服务器地址：") || !strings.Contains(annotated, "服务器端口") {
		t.Fatalf("annotation missing per-field explanations:\n%s", annotated)
	}
	// JSONC text must still validate (the parser strips whole-line comments).
	if err := ValidateConfig([]byte(annotated), format); err != nil {
		t.Fatalf("annotated config should validate: %v", err)
	}
	plain := string(StripComments([]byte(annotated), format))
	if strings.TrimSpace(plain) != strings.TrimSpace(string(content)) {
		t.Fatalf("strip did not restore the original:\n got: %s\nwant: %s", plain, content)
	}
	if !strings.Contains(plain, "https://example.com/path") {
		t.Fatal("strip must not damage // inside a URL string")
	}
	if strings.Contains(plain, "//") {
		// Only the URL contains //; that is intentional.
		if !strings.Contains(plain, "https://") {
			t.Fatal("unexpected leftover comment marker")
		}
	}
}

func TestQueryKernelFilterAndExcludedGroups(t *testing.T) {
	r := NewRegistry()
	all := r.List("")
	mihomo := r.Query(Filter{Kernel: "mihomo"})
	if len(mihomo) == 0 || len(mihomo) >= len(all) {
		t.Fatalf("kernel filter did not narrow the catalog: %d of %d", len(mihomo), len(all))
	}
	for _, p := range mihomo {
		found := false
		for _, k := range p.Kernels {
			if k == "mihomo" {
				found = true
			}
		}
		if !found {
			t.Errorf("kernel filter leaked %s", p.ID)
		}
	}
	without := r.Query(Filter{ExcludeGroups: []Group{GroupPlaintext, GroupPlainProxy}})
	for _, p := range without {
		if p.Group == GroupPlaintext || p.Group == GroupPlainProxy {
			t.Errorf("excluded group leaked: %s", p.ID)
		}
	}
	if len(without) >= len(all) {
		t.Fatal("excluded groups had no effect")
	}
}
