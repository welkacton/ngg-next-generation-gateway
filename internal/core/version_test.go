package core_test

import (
	"testing"

	"github.com/ngg-project/ngg/internal/core"
)

func TestExtractVersionFromRealKernelOutputs(t *testing.T) {
	cases := []struct {
		name, raw, regex, want string
	}{
		{"mihomo", "Mihomo Meta v1.19.33 windows amd64 with go1.26.10", `Meta(?: v)?([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-]*)`, "1.19.33"},
		{"xray", "Xray 26.10.31 (Xray, Penetrates Everything.) a4a3dff", `Xray\s+([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-]*)`, "26.10.31"},
		{"singbox", "sing-box version 1.14.4\n\nEnvironment: go1.26.10", `sing-box version ([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-]*)`, "1.14.4"},
		{"generic", "build v2.3.4-beta1", "", "2.3.4-beta1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := core.ExtractVersion(c.raw, c.regex)
			if err != nil {
				t.Fatalf("ExtractVersion: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestExtractVersionRejectsUnparsableOutput(t *testing.T) {
	if _, err := core.ExtractVersion("no version here", ""); err == nil {
		t.Fatal("expected an error instead of a fabricated version")
	}
	if _, err := core.ExtractVersion("", ""); err == nil {
		t.Fatal("expected an error for empty output")
	}
}

func TestExtractVersionAnyFallsBackToGeneric(t *testing.T) {
	got, err := core.ExtractVersionAny("tool v9.8.7", `NEVER-MATCH-([0-9.]+)`)
	if err != nil || got != "9.8.7" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := core.ExtractVersionAny("nothing", `NEVER`); err == nil {
		t.Fatal("expected error")
	}
}
