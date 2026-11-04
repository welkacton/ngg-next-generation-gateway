package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptPluginChangesDecision(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Install(Manifest{
		ID: "route-js", Name: "Route by domain", Kind: KindJS,
		Hooks: []string{"session", "dns"}, Enabled: true,
		Source: `var result = {};
if (ngg.input.domain && ngg.input.domain.indexOf("example") >= 0) { result.core_id = "sing-box"; result.tags = ["js-hit"]; }
if (ngg.input.name === "blocked.example") { result.block = true; }
`,
	})
	if err != nil || !p.Loaded {
		t.Fatalf("install JS plugin: %v %+v", err, p)
	}
	out, err := m.RunSessionHooks(context.Background(), map[string]any{"domain": "www.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if out["core_id"] != "sing-box" {
		t.Fatalf("plugin did not change decision: %+v", out)
	}
	blocked, err := m.RunDNSHooks(context.Background(), "blocked.example")
	if err != nil || !blocked {
		t.Fatalf("DNS hook did not block: %v %v", blocked, err)
	}
	if m.Count() != 1 {
		t.Fatalf("enabled plugin count = %d", m.Count())
	}
}

func TestRealWASMPlugin(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "ngg_hook.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hook.wasm")
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	p, err := m.Install(Manifest{
		ID: "wasm-hook", Name: "WASM hook", Kind: KindWASM,
		Hooks: []string{"session"}, Enabled: true,
		Source: path,
	})
	if err != nil || !p.Loaded {
		t.Fatalf("install WASM plugin: %v %+v", err, p)
	}
	out, err := m.RunSessionHooks(context.Background(), map[string]any{"domain": "a.com"})
	if err != nil {
		t.Fatalf("wazero call failed: %v", err)
	}
	if out["block"] == true {
		t.Fatal("test module returns 42 (allow), must not block")
	}
	if err := m.Remove("wasm-hook"); err != nil {
		t.Fatal(err)
	}
}

func TestBrokenPluginIsReported(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, err := m.Install(Manifest{ID: "bad", Kind: KindJS, Source: "var x = ;"}); err == nil {
		t.Fatal("invalid JS must be rejected")
	}
	list := m.List()
	if len(list) != 1 || list[0].Loaded || list[0].Error == "" {
		t.Fatalf("broken plugin not reported honestly: %+v", list)
	}
}
