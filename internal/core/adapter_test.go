package core_test

import (
	"testing"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

func TestRuntimeParamsValidate(t *testing.T) {
	valid := core.RuntimeParams{Dir: "/tmp/x", MixedPort: 17890, SocksPort: 17891, HTTPPort: 17892, APIPort: 17893, DNSPort: 17894}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	if len(valid.Ports()) != 5 {
		t.Fatalf("expected 5 ports, got %v", valid.Ports())
	}
	if valid.PortNames()[17890] != "mixed" {
		t.Fatalf("port names wrong: %v", valid.PortNames())
	}
	bad := valid
	bad.SocksPort = bad.MixedPort
	if err := bad.Validate(); err == nil {
		t.Fatal("duplicate ports must be rejected")
	}
	bad = valid
	bad.DNSPort = 70000
	if err := bad.Validate(); err == nil {
		t.Fatal("out-of-range port must be rejected")
	}
}

func TestDefaultAssetNamesPerOS(t *testing.T) {
	spec := core.ReleaseSpec{
		Repository: "MetaCubeX/mihomo",
		AssetTemplates: map[string]string{
			"windows": "mihomo-windows-{arch}-{vversion}.zip",
			"linux":   "mihomo-linux-{arch}-{vversion}.gz",
		},
		VersionPrefix: "v",
		ArchMapByOS: map[string]map[string]string{
			"android": {"arm64": "arm64-v8"},
		},
	}
	win, _, err := core.DefaultAssetNames(spec, contracts.Platform{OS: "windows", Arch: "amd64"}, "v1.19.33")
	if err != nil || win != "mihomo-windows-amd64-v1.19.33.zip" {
		t.Fatalf("windows asset = %q, err %v", win, err)
	}
	lin, _, err := core.DefaultAssetNames(spec, contracts.Platform{OS: "linux", Arch: "amd64"}, "1.19.33")
	if err != nil || lin != "mihomo-linux-amd64-v1.19.33.gz" {
		t.Fatalf("linux asset = %q, err %v", lin, err)
	}
	if _, _, err := core.DefaultAssetNames(spec, contracts.Platform{OS: "darwin", Arch: "arm64"}, "v1"); err == nil {
		t.Fatal("missing OS template must be an error")
	}
}

func TestRenderAssetArchOverride(t *testing.T) {
	spec := core.ReleaseSpec{
		AssetTemplate: "x-{os}-{arch}-{version}.zip",
		ArchMap:       map[string]string{"arm64": "arm64"},
		ArchMapByOS:   map[string]map[string]string{"android": {"arm64": "arm64-v8a"}},
	}
	got := core.RenderAsset(spec.AssetTemplate, contracts.Platform{OS: "android", Arch: "arm64"}, "v1.2.3", spec)
	if got != "x-android-arm64-v8a-1.2.3.zip" {
		t.Fatalf("got %q", got)
	}
}
