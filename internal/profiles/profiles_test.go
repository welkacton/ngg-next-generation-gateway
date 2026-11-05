package profiles

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/protocols"
)

func TestTemplatesAreValidForAllKernels(t *testing.T) {
	for _, kernel := range []string{"mihomo", "sing-box", "xray-core"} {
		p := Template(kernel, "Test-"+kernel)
		if len(p.Sections) != len(SectionKeys) {
			t.Fatalf("%s: got %d sections, want %d", kernel, len(p.Sections), len(SectionKeys))
		}
		if err := Validate(p); err != nil {
			t.Fatalf("%s template must parse: %v", kernel, err)
		}
		content, format, err := Render(p)
		if err != nil {
			t.Fatalf("%s render: %v", kernel, err)
		}
		if err := protocols.ValidateConfig([]byte(content), format); err != nil {
			t.Fatalf("%s assembled config must parse: %v\n%s", kernel, err, content)
		}
	}
}

// The three kernels genuinely use different grammars. This guards against a
// regression that silently emits one shared schema for all of them.
func TestKernelGrammarsAreDistinct(t *testing.T) {
	m, _, _ := Render(Template("mihomo", "m"))
	s, _, _ := Render(Template("sing-box", "s"))
	x, _, _ := Render(Template("xray-core", "x"))

	for _, tc := range []struct {
		name, content, want, forbidden string
	}{
		{"mihomo", m, "proxies:", "server_port"},
		{"sing-box", s, "server_port", "proxy-groups"},
		{"xray-core", x, "settings", "server_port"},
	} {
		if !strings.Contains(tc.content, tc.want) {
			t.Errorf("%s should contain %q", tc.name, tc.want)
		}
		if strings.Contains(tc.content, tc.forbidden) {
			t.Errorf("%s must not contain %q (syntax leaked from another kernel)", tc.name, tc.forbidden)
		}
	}
	if strings.HasPrefix(m, "{") {
		t.Error("mihomo must be YAML, not JSON")
	}
	if !json.Valid([]byte(s)) {
		t.Error("sing-box assembled config is not valid JSON")
	}
	if !json.Valid([]byte(x)) {
		t.Error("xray-core assembled config is not valid JSON")
	}
}

// Two sections both defining "outbounds" (protocols + selector) must merge
// into one array — that is how sing-box / Xray actually work.
func TestDuplicateJSONKeysMergeIntoOneArray(t *testing.T) {
	content, _, err := Render(Template("sing-box", "merge"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("merged config must be valid JSON: %v", err)
	}
	outs, ok := doc["outbounds"].([]any)
	if !ok {
		t.Fatalf("outbounds missing: %v", doc)
	}
	if len(outs) != 4 { // 1 vmess + selector + direct + block
		t.Fatalf("outbounds should merge 4 entries, got %d: %v", len(outs), outs)
	}
	if _, ok := doc["route"]; !ok {
		t.Error("route section missing from merged config")
	}
	if _, ok := doc["inbounds"]; !ok {
		t.Error("inbounds section missing from merged config")
	}
}

func TestValidateRejectsBrokenSection(t *testing.T) {
	p := Template("sing-box", "broken")
	p.Sections[2].Content = `"inbounds": [ { "type": ` // truncated JSON
	if err := Validate(p); err == nil {
		t.Fatal("malformed section must be rejected")
	}
	p2 := Template("mihomo", "broken-yaml")
	p2.Sections[0].Content = "proxies:\n  - name: x\n   type: vmess\n" // bad indent
	if err := Validate(p2); err == nil {
		t.Fatal("malformed YAML must be rejected")
	}
}

func TestCommentsAreStrippedButURLsSurvive(t *testing.T) {
	p := Template("sing-box", "comments")
	p.Sections[0].Content = `// https://example.com/a//b — a doc link must survive
"outbounds": [ { "type": "direct", "tag": "d" } ]`
	if err := Validate(p); err != nil {
		t.Fatalf("line comments must be stripped before validation: %v", err)
	}
}

func TestStoreCRUDAndDefaultProtection(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	list, active := st.List()
	if len(list) != 1 || list[0].Name != "Default-Profile" || active != list[0].ID {
		t.Fatalf("expected one default profile, got %+v active=%s", list, active)
	}
	if !list[0].Default {
		t.Fatal("first profile must be marked default")
	}

	created, err := st.Create(Template("xray-core", "Work"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.Create(Template("mihomo", "work")); err == nil {
		t.Fatal("duplicate names must be rejected (case-insensitive)")
	}
	if err := st.Delete(list[0].ID); err == nil {
		t.Fatal("deleting the default profile must be blocked")
	}
	if err := st.SetActive(created.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := st.Delete(list[0].ID); err != nil {
		t.Fatalf("old default should now be deletable: %v", err)
	}
	list2, active2 := st.List()
	if len(list2) != 1 || active2 != created.ID || !list2[0].Default {
		t.Fatalf("unexpected final state: %+v active=%s", list2, active2)
	}
}

func TestStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	p, err := st.Create(Template("sing-box", "Persisted"))
	if err != nil {
		t.Fatal(err)
	}
	p.Description = "edited"
	if _, err := st.Update(p.ID, p); err != nil {
		t.Fatal(err)
	}
	st2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := st2.Get(p.ID)
	if !ok || got.Description != "edited" {
		t.Fatalf("profile did not survive reopen: %+v", got)
	}
	dup, err := st2.Duplicate(p.ID, "Copy")
	if err != nil {
		t.Fatal(err)
	}
	if dup.ID == p.ID || dup.Name != "Copy" {
		t.Fatalf("duplicate produced wrong profile: %+v", dup)
	}
}
