package core_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ngg-project/ngg/internal/adapters/generic"
	"github.com/ngg-project/ngg/internal/adapters/mihomo"
	"github.com/ngg-project/ngg/internal/adapters/singbox"
	"github.com/ngg-project/ngg/internal/adapters/xray"
	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

func TestRegistryRegisterListAndProtectBuiltins(t *testing.T) {
	r := core.NewRegistry()
	for _, a := range []core.CoreAdapter{mihomo.New(), xray.New(), singbox.New()} {
		if err := r.Register(a); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	if r.Count() != 3 {
		t.Fatalf("count=%d", r.Count())
	}
	if err := r.Register(mihomo.New()); err == nil {
		t.Fatal("duplicate registration must fail")
	}
	if err := r.Unregister(contracts.CoreMihomo); err == nil {
		t.Fatal("builtin kernels must be protected from unregister")
	}
	list := r.List()
	if len(list) != 3 || list[0].ID > list[1].ID {
		t.Fatalf("list not sorted / complete: %+v", list)
	}
	for _, d := range list {
		if !d.Capability.WorkerMode {
			t.Fatalf("kernel %s is not WorkerMode", d.ID)
		}
		if len(d.Capability.Protocols) == 0 {
			t.Fatalf("kernel %s registered no protocol capability", d.ID)
		}
	}
	if _, err := r.Get("not-registered"); err == nil {
		t.Fatal("Get on unknown id must fail")
	}
}

const sampleDescriptor = `
id: example-core
display_name: Example Core
description: descriptor-driven kernel used by tests
repository: example/example-core
capability:
  protocols: [socks5, example]
  worker_mode: true
commands:
  version: ["-v"]
  run: ["run", "-c", "{{.Config}}"]
  validate: ["check", "-c", "{{.Config}}"]
config:
  file_name: config.json
  default: '{"port": {{.MixedPort}}}'
release:
  repository: example/example-core
  asset_template: "example-{os}-{arch}-{version}.zip"
  archive: zip
  binary_in_archive: "example*"
version_regex: "example ([0-9.]+)"
`

func TestRegistryLoadsDescriptorKernels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "example.yaml"), []byte(sampleDescriptor), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("id: broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := core.NewRegistry()
	n, problems := r.LoadDescriptorDir(dir, generic.NewFromFile)
	if n != 1 || len(problems) != 1 {
		t.Fatalf("loaded=%d problems=%v", n, problems)
	}
	a, err := r.Get("example-core")
	if err != nil {
		t.Fatal(err)
	}
	if a.Descriptor().Builtin {
		t.Fatal("descriptor kernels must not be builtin")
	}
	if a.Descriptor().Source == "" {
		t.Fatal("descriptor source not recorded")
	}
	v, err := a.ParseVersion("example 3.2.1")
	if err != nil || v != "3.2.1" {
		t.Fatalf("version=%q err=%v", v, err)
	}
	args := a.RunArgs("/tmp/config.json")
	if len(args) != 3 || args[2] != "/tmp/config.json" {
		t.Fatalf("template rendering failed: %v", args)
	}
	if err := r.Unregister("example-core"); err != nil {
		t.Fatalf("dynamic kernel should be unregisterable: %v", err)
	}
}
