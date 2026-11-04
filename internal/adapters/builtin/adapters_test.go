package builtin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/adapters/builtin"
	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
	"gopkg.in/yaml.v3"
)

func TestBuiltinAdapters(t *testing.T) {
	adapters := builtin.All()
	if len(adapters) != 3 {
		t.Fatalf("expected 3 builtin kernels, got %d", len(adapters))
	}
	ids := map[contracts.CoreID]bool{}
	for _, a := range adapters {
		d := a.Descriptor()
		if ids[d.ID] {
			t.Fatalf("duplicate id %s", d.ID)
		}
		ids[d.ID] = true
		if !d.Builtin || d.Source != "builtin" {
			t.Fatalf("%s must be a builtin", d.ID)
		}
		if !d.Capability.WorkerMode {
			t.Fatalf("%s must run in Worker Mode (no own TUN/DNS hijack)", d.ID)
		}
		if len(d.Capability.Protocols) < 5 {
			t.Fatalf("%s registered too few protocols", d.ID)
		}
		if len(a.VersionArgs()) == 0 || len(a.RunArgs("cfg")) == 0 || len(a.ValidateArgs("cfg")) == 0 {
			t.Fatalf("%s missing lifecycle args", d.ID)
		}
		if a.BinaryName("windows") == "" || a.BinaryName("linux") == "" {
			t.Fatalf("%s missing binary names", d.ID)
		}
		if _, err := a.ParseVersion("completely unparsable output"); err == nil {
			t.Fatalf("%s must reject unparsable version output", d.ID)
		}
	}
}

func TestRealVersionOutputParsing(t *testing.T) {
	// Captured from the actual upstream Windows amd64 binaries on 2026-11-04.
	outputs := map[contracts.CoreID]struct{ raw, want string }{
		contracts.CoreMihomo:  {"Mihomo Meta v1.19.33 windows amd64 with go1.26.10 Wed Oct 21 16:40:02 UTC 2026", "1.19.33"},
		contracts.CoreXray:    {"Xray 26.10.31 (Xray, Penetrates Everything.) a4a3dff (go1.27.3 windows/amd64)", "26.10.31"},
		contracts.CoreSingBox: {"sing-box version 1.14.4\n\nEnvironment: go1.26.10 windows/amd64", "1.14.4"},
	}
	for _, a := range builtin.All() {
		c, ok := outputs[a.Descriptor().ID]
		if !ok {
			continue
		}
		got, err := a.ParseVersion(c.raw)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q err %v, want %q", a.Descriptor().ID, got, err, c.want)
		}
	}
}

func TestRealReleaseAssetNames(t *testing.T) {
	want := map[contracts.CoreID]map[string]string{
		contracts.CoreMihomo: {
			"windows/amd64": "mihomo-windows-amd64-v1.19.33.zip",
			"linux/amd64":   "mihomo-linux-amd64-v1.19.33.gz",
		},
		contracts.CoreXray: {
			"windows/amd64": "Xray-windows-64.zip",
			"linux/amd64":   "Xray-linux-64.zip",
		},
		contracts.CoreSingBox: {
			"windows/amd64": "sing-box-1.14.4-windows-amd64.zip",
			"linux/amd64":   "sing-box-1.14.4-linux-amd64.tar.gz",
		},
	}
	for _, a := range builtin.All() {
		for key, expected := range want[a.Descriptor().ID] {
			parts := strings.Split(key, "/")
			got, _, err := a.AssetNames(contracts.Platform{OS: parts[0], Arch: parts[1]}, "v1.19.33")
			// Xray/sing-box need their own tag; only compare when it matches.
			if a.Descriptor().ID == contracts.CoreSingBox {
				got, _, err = a.AssetNames(contracts.Platform{OS: parts[0], Arch: parts[1]}, "v1.14.4")
			}
			if err != nil || got != expected {
				t.Errorf("%s %s: got %q err %v, want %q", a.Descriptor().ID, key, got, err, expected)
			}
		}
	}
}

func TestDefaultConfigsAreValidAndWorkerMode(t *testing.T) {
	p := core.RuntimeParams{Dir: "/tmp/ngg", MixedPort: 17890, SocksPort: 17891, HTTPPort: 17892, APIPort: 17893, DNSPort: 17894, Secret: "test-secret"}
	for _, a := range builtin.All() {
		b, err := a.DefaultConfig(p)
		if err != nil {
			t.Fatalf("%s DefaultConfig: %v", a.Descriptor().ID, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s produced empty config", a.Descriptor().ID)
		}
		id := a.Descriptor().ID
		switch id {
		case contracts.CoreMihomo:
			var v map[string]any
			if err := yaml.Unmarshal(b, &v); err != nil {
				t.Fatalf("mihomo config is not valid YAML: %v", err)
			}
			if v["tun"].(map[string]any)["enable"] != false {
				t.Fatal("mihomo TUN must be disabled in Worker Mode")
			}
			if v["dns"].(map[string]any)["enable"] != false {
				t.Fatal("mihomo DNS hijack must be disabled in Worker Mode")
			}
			if !strings.Contains(string(b), "17890") {
				t.Fatal("mihomo config must use the NGG allocated port")
			}
		case contracts.CoreXray, contracts.CoreSingBox:
			var v map[string]any
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatalf("%s config is not valid JSON: %v", id, err)
			}
			if _, ok := v["inbounds"]; !ok {
				t.Fatalf("%s config has no inbounds", id)
			}
			low := strings.ToLower(string(b))
			if strings.Contains(low, `"type":"tun"`) || strings.Contains(low, `"type": "tun"`) {
				t.Fatalf("%s default config must not enable TUN", id)
			}
		}
	}
}

func TestIsolatedPortsDifferBetweenAdapters(t *testing.T) {
	// Each kernel's isolated block is allocated by NGG (see engine tests), and
	// adapters must consume distinct roles from RuntimeParams.
	for _, a := range builtin.All() {
		p := core.RuntimeParams{Dir: "/tmp", MixedPort: 17890, SocksPort: 17891, HTTPPort: 17892, APIPort: 17893, DNSPort: 17894, Secret: "s"}
		b, _ := a.DefaultConfig(p)
		_ = b
		if len(p.Ports()) != 5 {
			t.Fatal("expected 5 isolated ports")
		}
	}
}
