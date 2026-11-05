package profiles

import (
	"encoding/json"
	"path/filepath"
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

func TestSectionOrderIsUserVisibleOrder(t *testing.T) {
	want := []string{"protocols", "inbounds", "outbounds", "routes", "dns", "extra"}
	if strings.Join(SectionKeys, ",") != strings.Join(want, ",") {
		t.Fatalf("section order: got %v want %v", SectionKeys, want)
	}
	for _, kernel := range []string{"mihomo", "sing-box", "xray-core"} {
		p := Template(kernel, "order")
		for i, s := range p.Sections {
			if s.Key != want[i] {
				t.Fatalf("%s section %d = %s, want %s", kernel, i, s.Key, want[i])
			}
		}
	}
	// Profiles saved by older releases are normalised on read.
	st, _ := NewStore(t.TempDir())
	p := Template("mihomo", "legacy")
	p.Sections[0], p.Sections[1] = p.Sections[1], p.Sections[0]
	p.Sections[2], p.Sections[3] = p.Sections[3], p.Sections[2]
	created, err := st.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := st.Get(created.ID)
	if !ok {
		t.Fatal("profile missing")
	}
	for i, s := range got.Sections {
		if s.Key != want[i] {
			t.Fatalf("normalised section %d = %s, want %s", i, s.Key, want[i])
		}
	}
}

func TestStoreBulkToggle(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	a, _ := st.Create(Template("mihomo", "A"))
	b, _ := st.Create(Template("sing-box", "B"))
	c, _ := st.Create(Template("xray-core", "C"))

	changed, err := st.SetEnabled([]string{a.ID, c.ID}, false, false)
	if err != nil || changed != 2 {
		t.Fatalf("selection toggle: changed=%d err=%v", changed, err)
	}
	for _, tc := range []struct {
		id   string
		want bool
	}{{a.ID, false}, {b.ID, true}, {c.ID, false}} {
		p, _ := st.Get(tc.id)
		if p.Enabled != tc.want {
			t.Fatalf("%s enabled=%v want %v", tc.id, p.Enabled, tc.want)
		}
	}
	// Different kernels can be enabled together — no mutual exclusion.
	changed, err = st.SetEnabled(nil, true, true)
	if err != nil || changed != 2 {
		t.Fatalf("enable all: changed=%d err=%v", changed, err)
	}
	list, _ := st.List()
	for _, p := range list {
		if !p.Enabled {
			t.Fatalf("%s should be enabled", p.ID)
		}
	}
	// 4 profiles total: the auto-created Default-Profile plus A/B/C.
	changed, err = st.SetEnabled(nil, true, false)
	if err != nil || changed != 4 {
		t.Fatalf("disable all: changed=%d err=%v", changed, err)
	}
	list, _ = st.List()
	for _, p := range list {
		if p.Enabled {
			t.Fatalf("%s should be disabled", p.ID)
		}
	}
	// Reopen: the enabled flags survived the atomic persist.
	st2, _ := NewStore(filepath.Dir(st.path))
	b2, _ := st2.Get(b.ID)
	if b2.Enabled {
		t.Fatal("disable-all did not persist")
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
