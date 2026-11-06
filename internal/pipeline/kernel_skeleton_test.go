package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/rulesets"
)

func TestRenderSupportsAllThreeKernels(t *testing.T) {
	in := Inputs{
		DNSUpstream: []string{"223.5.5.5:53", "119.29.29.29:53"},
		RuleSets: []rulesets.RuleSet{
			{ID: "rs-1", Enabled: true, Rules: []rulesets.ParsedRule{
				{Type: "DOMAIN-SUFFIX", Value: "example.com"},
			}},
		},
	}

	for _, kernel := range []string{"mihomo", "sing-box", "xray-core"} {
		c := Default()
		c.Kernel = kernel
		content, warnings, err := Render(c, in)
		if err != nil {
			t.Fatalf("Render(%s) failed: %v", kernel, err)
		}
		if strings.TrimSpace(content) == "" {
			t.Fatalf("Render(%s) returned empty content", kernel)
		}
		if kernel == "mihomo" {
			if !strings.Contains(content, "mixed-port") {
				t.Fatalf("mihomo render lost its full config: %s", content)
			}
			continue
		}
		// Non-mihomo kernels get an honest, parseable official-syntax skeleton.
		if !json.Valid([]byte(content)) {
			t.Fatalf("%s skeleton is not valid JSON: %s", kernel, content)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(content), &doc); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"inbounds", "outbounds", "dns"} {
			if _, ok := doc[key]; !ok {
				t.Errorf("%s skeleton missing %q", kernel, key)
			}
		}
		note := strings.Join(warnings, " ")
		if !strings.Contains(note, "骨架") || !strings.Contains(note, "原生校验") {
			t.Errorf("%s warnings must state the skeleton boundary: %v", kernel, warnings)
		}
	}

	c := Default()
	c.Kernel = "not-a-kernel"
	if _, _, err := Render(c, in); err == nil {
		t.Fatal("unknown carrier kernel must be rejected, not silently rendered")
	}
}

func TestValidateReportsHonestKernelBoundary(t *testing.T) {
	c := Default()
	c.Kernel = "sing-box"
	steps := Validate(c, Inputs{DNSUpstream: []string{"223.5.5.5:53"}})
	if len(steps) == 0 {
		t.Fatal("Validate returned no steps")
	}
	var protocol *Step
	for i := range steps {
		if steps[i].Key == "protocol" {
			protocol = &steps[i]
		}
	}
	if protocol == nil {
		t.Fatal("no protocol step")
	}
	if protocol.Status != "warn" {
		t.Fatalf("non-mihomo protocol step should be a warn, got %q", protocol.Status)
	}
	if strings.Contains(protocol.Detail, "暂未接入") {
		t.Fatalf("protocol step still claims the kernel is unimplemented: %q", protocol.Detail)
	}
	if !strings.Contains(protocol.Detail, "骨架") {
		t.Fatalf("protocol step must describe the honest skeleton boundary: %q", protocol.Detail)
	}
	if protocol.FixHint == "" {
		t.Fatal("non-mihomo protocol step should offer a fix hint")
	}
}
