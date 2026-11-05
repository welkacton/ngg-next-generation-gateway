package profiles

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// semanticEqual compares configs by meaning, not by whitespace/comment text.
func semanticEqual(t *testing.T, a, b, format string) bool {
	t.Helper()
	if format == "json" {
		var x, y any
		if err := json.Unmarshal([]byte(a), &x); err != nil {
			t.Fatalf("left JSON invalid: %v\n%s", err, a)
		}
		if err := json.Unmarshal([]byte(b), &y); err != nil {
			t.Fatalf("right JSON invalid: %v\n%s", err, b)
		}
		return reflect.DeepEqual(x, y)
	}
	var x, y any
	if err := yaml.Unmarshal([]byte(a), &x); err != nil {
		t.Fatalf("left YAML invalid: %v\n%s", err, a)
	}
	if err := yaml.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("right YAML invalid: %v\n%s", err, b)
	}
	return reflect.DeepEqual(x, y)
}

func sectionByKey(p Profile, key string) string {
	for _, s := range p.Sections {
		if s.Key == key {
			return s.Content
		}
	}
	return ""
}

// The core contract: sections -> full config -> sections -> full config must
// not change a single key or value, for all three kernels.
func TestSplitRoundTripPreservesConfigForAllKernels(t *testing.T) {
	for _, kernel := range []string{"mihomo", "sing-box", "xray-core"} {
		p := Template(kernel, "Round-"+kernel)
		original, format, err := Render(p)
		if err != nil {
			t.Fatalf("%s render: %v", kernel, err)
		}
		res, err := Split(p, original)
		if err != nil {
			t.Fatalf("%s split: %v", kernel, err)
		}
		if len(res.Sections) != len(SectionKeys) {
			t.Fatalf("%s: split produced %d sections", kernel, len(res.Sections))
		}
		for i, s := range res.Sections {
			if s.Key != SectionKeys[i] {
				t.Fatalf("%s: section %d = %s want %s", kernel, i, s.Key, SectionKeys[i])
			}
			if s.Label == "" {
				t.Fatalf("%s: section %s lost its label", kernel, s.Key)
			}
		}
		p2 := p
		p2.Sections = res.Sections
		again, _, err := Render(p2)
		if err != nil {
			t.Fatalf("%s re-render: %v", kernel, err)
		}
		if !semanticEqual(t, original, again, format) {
			t.Fatalf("%s round trip changed the config\n--- before ---\n%s\n--- after ---\n%s", kernel, original, again)
		}
		if err := Validate(p2); err != nil {
			t.Fatalf("%s re-assembled profile must validate: %v", kernel, err)
		}
	}
}

// Editing the full file must land in the matching section, not be discarded.
func TestSplitPropagatesFullConfigEditsToSections(t *testing.T) {
	t.Run("mihomo-yaml", func(t *testing.T) {
		p := Template("mihomo", "YAML-edit")
		full, _, _ := Render(p)
		edited := strings.Replace(full, "port: 7890", "port: 7891", 1)
		edited = strings.Replace(edited, "- MATCH,PROXY", "- DOMAIN-SUFFIX,new.example,DIRECT\n  - MATCH,PROXY", 1)
		res, err := Split(p, edited)
		if err != nil {
			t.Fatal(err)
		}
		in := sectionByKey(Profile{Sections: res.Sections}, "inbounds")
		if !strings.Contains(in, "7891") || strings.Contains(in, "7890") {
			t.Fatalf("inbound port edit did not propagate: %s", in)
		}
		routes := sectionByKey(Profile{Sections: res.Sections}, "routes")
		if !strings.Contains(routes, "DOMAIN-SUFFIX,new.example,DIRECT") {
			t.Fatalf("new rule did not propagate: %s", routes)
		}
		if !contains(res.Changed, "inbounds") || !contains(res.Changed, "routes") {
			t.Fatalf("changed sections should include inbounds/routes, got %v", res.Changed)
		}
	})

	t.Run("sing-box-json", func(t *testing.T) {
		p := Template("sing-box", "SB-edit")
		full, _, _ := Render(p)
		edited := strings.Replace(full, `"listen_port": 7890`, `"listen_port": 7891`, 1)
		res, err := Split(p, edited)
		if err != nil {
			t.Fatal(err)
		}
		in := sectionByKey(Profile{Sections: res.Sections}, "inbounds")
		if !strings.Contains(in, "7891") {
			t.Fatalf("inbound edit did not propagate: %s", in)
		}
	})

	t.Run("xray-json", func(t *testing.T) {
		p := Template("xray-core", "XR-edit")
		full, _, _ := Render(p)
		edited := strings.Replace(full, `"port": 1080`, `"port": 1081`, 1)
		res, err := Split(p, edited)
		if err != nil {
			t.Fatal(err)
		}
		in := sectionByKey(Profile{Sections: res.Sections}, "inbounds")
		if !strings.Contains(in, "1081") {
			t.Fatalf("inbound edit did not propagate: %s", in)
		}
	})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// A key we have never heard of must survive in 「其他」 and be reported.
func TestSplitKeepsUnknownTopLevelKeysInExtra(t *testing.T) {
	p := Template("mihomo", "Unknown")
	full, _, _ := Render(p)
	res, err := Split(p, full+"\ncustom_future_option: 42\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sectionByKey(Profile{Sections: res.Sections}, "extra"), "custom_future_option") {
		t.Fatalf("unknown key was dropped: %+v", res.Sections)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "custom_future_option") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown key should be reported, got %v", res.Warnings)
	}
	// ...and it must survive the next render.
	p2 := p
	p2.Sections = res.Sections
	again, _, err := Render(p2)
	if err != nil || !strings.Contains(again, "custom_future_option") {
		t.Fatalf("unknown key lost on re-render: err=%v\n%s", err, again)
	}
}

// The native outbounds array is split into nodes vs selector/direct/block and
// merged back into ONE array — never two duplicate JSON keys.
func TestSplitGroupsJSONOutboundsByRole(t *testing.T) {
	full := `{
  "outbounds": [
    { "type": "selector", "tag": "PROXY", "outbounds": ["vmess"] },
    { "type": "direct", "tag": "DIRECT" },
    { "type": "vmess", "tag": "node-a", "server": "1.2.3.4", "server_port": 443 }
  ],
  "inbounds": [ { "type": "mixed", "listen_port": 7890 } ],
  "route": { "default_outbound": "PROXY" },
  "log": { "level": "info" }
}`
	p := Template("sing-box", "Grouped")
	res, err := Split(p, full)
	if err != nil {
		t.Fatal(err)
	}
	nodes := sectionByKey(Profile{Sections: res.Sections}, "protocols")
	policies := sectionByKey(Profile{Sections: res.Sections}, "outbounds")
	if !strings.Contains(nodes, "node-a") || strings.Contains(nodes, "selector") {
		t.Fatalf("nodes section wrong: %s", nodes)
	}
	if !strings.Contains(policies, "selector") || !strings.Contains(policies, "DIRECT") || strings.Contains(policies, "node-a") {
		t.Fatalf("policies section wrong: %s", policies)
	}
	if !strings.Contains(sectionByKey(Profile{Sections: res.Sections}, "extra"), `"log"`) {
		t.Fatal("known global key log should land in 其他")
	}
	p2 := p
	p2.Sections = res.Sections
	merged, _, err := Render(p2)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(merged), &doc); err != nil {
		t.Fatalf("re-render is not valid JSON: %v\n%s", err, merged)
	}
	outs, ok := doc["outbounds"].([]any)
	if !ok || len(outs) != 3 {
		t.Fatalf("outbounds should merge back to 3 entries, got %v", doc["outbounds"])
	}
}

func TestSplitRejectsInvalidAndEmptyInput(t *testing.T) {
	for _, tc := range []struct {
		name, kernel, content string
	}{
		{"empty", "mihomo", "   \n"},
		{"broken-yaml", "mihomo", "proxies: [\n  - name: x\n"},
		{"broken-json", "sing-box", `{"outbounds": [`},
		{"not-an-object", "sing-box", `[1, 2, 3]`},
		{"unknown-kernel", "bogus", "mode: rule\n"},
	} {
		if _, err := Split(Profile{Kernel: tc.kernel, Format: kernelInfo[tc.kernel].Format}, tc.content); err == nil {
			t.Errorf("%s should be rejected", tc.name)
		}
	}
}

// YAML comments belong to the key they describe and must not be lost by a
// round trip, because the section editor shows them as inline documentation.
func TestSplitKeepsYAMLLeadingComments(t *testing.T) {
	p := Template("mihomo", "Comments")
	full, _, _ := Render(p)
	res, err := Split(p, full)
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, s := range res.Sections {
		all += s.Content + "\n"
	}
	if !strings.Contains(all, "# 协议内容 / proxies") || !strings.Contains(all, "官方文档") {
		t.Fatalf("leading comments lost:\n%s", all)
	}
}
