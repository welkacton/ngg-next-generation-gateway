package protocols

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogCoversAllThreeProtocolGroups(t *testing.T) {
	r := NewRegistry()
	all := r.List("")
	if len(all) < 30 {
		t.Fatalf("expected a full protocol catalog, got %d", len(all))
	}
	for _, g := range []Group{GroupEncrypted, GroupPlaintext, GroupPlainProxy} {
		n := len(r.Query(Filter{Group: string(g)}))
		if n < 5 {
			t.Errorf("group %s only has %d protocols", g, n)
		}
	}
	// Encrypted protocols must include VPN, encrypted proxies and PQ.
	if len(r.Query(Filter{Category: string(CatVPN)})) < 5 ||
		len(r.Query(Filter{Category: string(CatEncryptedProxy)})) < 10 ||
		len(r.Query(Filter{Category: string(CatPQ)})) < 1 {
		t.Fatal("missing encrypted VPN / proxy / post-quantum families")
	}
	// Plaintext group must genuinely be unencrypted.
	for _, s := range r.Query(Filter{Group: string(GroupPlaintext)}) {
		if !strings.Contains(s.Security, "无加密") {
			t.Errorf("plaintext protocol %s has security %q", s.ID, s.Security)
		}
	}
	if got := len(r.Query(Filter{Search: "wireguard"})); got != 1 {
		t.Errorf("search wireguard = %d, want 1", got)
	}
}

func TestEveryBuiltinGeneratesValidConfigOnEveryKernel(t *testing.T) {
	r := NewRegistry()
	kernels := []string{"mihomo", "sing-box", "xray-core", "ngg-native", "external-helper"}
	for _, s := range r.List("") {
		for _, k := range kernels {
			b, format, err := GenerateConfig(s.ID, k, map[string]any{"server": "198.51.100.7", "port": 8443})
			if err != nil {
				t.Errorf("%s/%s: %v", s.ID, k, err)
				continue
			}
			if len(b) == 0 {
				t.Errorf("%s/%s: empty config", s.ID, k)
			}
			if err := ValidateConfig(b, format); err != nil {
				t.Errorf("%s/%s produced invalid %s: %v\n%s", s.ID, k, format, err, b)
			}
		}
	}
}

func TestBatchTogglePersistsAndRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "protocols-config.json")
	r := NewRegistry(path)
	all := r.List("")
	if n, err := r.SetEnabledBatch(nil, true, false); err != nil || n != len(all) {
		t.Fatalf("disable all: changed=%d err=%v", n, err)
	}
	if len(r.Query(Filter{Enabled: "true"})) != 0 {
		t.Fatal("disable-all left enabled protocols")
	}
	ids := []string{all[0].ID, all[1].ID}
	if n, err := r.SetEnabledBatch(ids, false, true); err != nil || n != 2 {
		t.Fatalf("enable selected: changed=%d err=%v", n, err)
	}
	if len(r.Query(Filter{Enabled: "true"})) != 2 {
		t.Fatal("enable-selected affected more than the selection")
	}

	// A fresh process sees the persisted state, and new built-ins still appear.
	r2 := NewRegistry(path)
	if err := r2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r2.Query(Filter{Enabled: "true"})) != 2 {
		t.Fatal("persisted enabled state was not restored")
	}
	if len(r2.List("")) != len(all) {
		t.Fatal("catalog changed after reload")
	}

	// Persist failure must restore the in-memory map. Point the state file at
	// an existing directory so os.WriteFile deterministically fails.
	badPath := filepath.Join(t.TempDir(), "state-is-a-dir")
	if err := os.MkdirAll(badPath, 0o755); err != nil {
		t.Fatal(err)
	}
	r3 := NewRegistry(badPath)
	if _, err := r3.SetEnabledBatch(nil, true, false); err == nil {
		t.Fatal("expected a persistence failure")
	}
	if n := len(r3.Query(Filter{Enabled: "true"})); n == 0 {
		t.Fatal("in-memory state was not rolled back after a failed write")
	}
}

func TestCustomProtocolCRUDAndDeletionGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protocols-config.json")
	r := NewRegistry(path)
	custom := Spec{
		ID: "my-corp-proxy", Name: "Corp Proxy", Category: CatPlainProxy,
		Group: GroupPlainProxy, Security: "自定义", Transport: "TCP", DefaultPort: 9999,
		ConfigKind: KindOutbound, Kernels: []string{"mihomo"}, Enabled: true,
		Params: map[string]any{"server": "10.0.0.1", "port": 9999, "token": "abc"},
	}
	if err := r.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got, ok := r.Get("my-corp-proxy"); !ok || !got.Custom {
		t.Fatal("custom protocol not stored with Custom=true")
	}
	// Params from the spec are merged into generation.
	b, format, err := r.GenerateFor("my-corp-proxy", "mihomo", nil, false)
	if err != nil || format != "yaml" || !strings.Contains(string(b), "10.0.0.1") {
		t.Fatalf("GenerateFor: format=%s err=%v content=%s", format, err, b)
	}
	// Built-ins cannot be deleted.
	if err := r.Delete("wireguard"); err == nil {
		t.Fatal("deleting a built-in protocol must be refused")
	}
	if err := r.Delete("my-corp-proxy"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := r.Get("my-corp-proxy"); ok {
		t.Fatal("custom protocol still present after delete")
	}
}

func TestConfigOverrideAndRegenerate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protocols-config.json")
	r := NewRegistry(path)
	tmpl, format, err := r.GenerateFor("vmess", "sing-box", map[string]any{"server": "203.0.113.9"}, true)
	if err != nil {
		t.Fatalf("GenerateFor template: %v", err)
	}
	edited := `{"type":"vmess","tag":"hand-edited","server":"203.0.113.9"}`
	if _, err := r.SaveConfig("vmess", edited, "json"); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, gotFormat, err := r.GenerateFor("vmess", "sing-box", nil, false)
	if err != nil || string(got) != edited || gotFormat != "json" {
		t.Fatalf("override not honored: format=%s err=%v content=%s", gotFormat, err, got)
	}
	if _, _, err := r.GenerateFor("vmess", "sing-box", nil, true); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if _, err := r.ClearConfig("vmess"); err != nil {
		t.Fatalf("ClearConfig: %v", err)
	}
	back, _, err := r.GenerateFor("vmess", "sing-box", map[string]any{"server": "203.0.113.9"}, false)
	if err != nil || string(back) != string(tmpl) {
		t.Fatalf("template not restored: err=%v", err)
	}
	if _, err := r.SaveConfig("vmess", "{not json", "json"); err == nil {
		t.Fatal("malformed JSON override must be rejected")
	}
	if format != "json" {
		t.Logf("template format for sing-box is %s", format)
	}
}

func TestDisabledProtocolCannotGenerate(t *testing.T) {
	r := NewRegistry()
	if _, err := r.SetEnabled("vmess", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if _, _, err := r.GenerateFor("vmess", "mihomo", nil, false); err == nil {
		t.Fatal("a disabled protocol must not generate config")
	}
	// The low-level generator is intentionally registry-free.
	if _, _, err := GenerateConfig("vmess", "mihomo", nil); err != nil {
		t.Fatalf("GenerateConfig should stay available: %v", err)
	}
	if _, err := r.SetEnabled("vmess", true); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, _, err := r.GenerateFor("vmess", "mihomo", nil, false); err != nil {
		t.Fatalf("re-enabled protocol should generate: %v", err)
	}
}

func TestUpdateKeepsBuiltinStructureButSavesState(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "p.json"))
	old, _ := r.Get("trojan")
	in := old
	in.Kernels = []string{"bogus-kernel"} // must be ignored for a built-in
	in.Description = "edited description"
	in.Enabled = false
	in.Params = map[string]any{"server": "192.0.2.1"}
	if err := r.Update(in); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := r.Get("trojan")
	if len(got.Kernels) == 0 || got.Kernels[0] == "bogus-kernel" {
		t.Fatalf("built-in structure must not be overwritten by the editor: %+v", got.Kernels)
	}
	if got.Description != "edited description" || got.Enabled || got.Params["server"] != "192.0.2.1" {
		t.Fatalf("editable state not applied: %+v", got)
	}
	// A JSON round-trip keeps the catalog parseable.
	b, _ := json.Marshal(r.List(""))
	var check []Spec
	if err := json.Unmarshal(b, &check); err != nil {
		t.Fatalf("catalog not JSON serializable: %v", err)
	}
}
