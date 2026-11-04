package core

import (
	"fmt"
	"strings"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// RuntimeParams is the isolated resource slice NGG allocates for one kernel.
// Every kernel gets its own directory and its own port block. This is the
// Stage-1 implementation of the architecture revision's "resource isolation"
// requirement (section 8): kernels never compete for the same listener.
type RuntimeParams struct {
	Dir       string `json:"dir"`
	MixedPort int    `json:"mixed_port"`
	SocksPort int    `json:"socks_port"`
	HTTPPort  int    `json:"http_port"`
	APIPort   int    `json:"api_port"`
	DNSPort   int    `json:"dns_port"`
	Secret    string `json:"secret"`
}

func (p RuntimeParams) Validate() error {
	seen := map[int]string{}
	for _, pair := range []struct {
		name string
		port int
	}{
		{"mixed", p.MixedPort}, {"socks", p.SocksPort}, {"http", p.HTTPPort},
		{"api", p.APIPort}, {"dns", p.DNSPort},
	} {
		if pair.port == 0 {
			continue
		}
		if pair.port < 1 || pair.port > 65535 {
			return fmt.Errorf("port %s=%d out of range", pair.name, pair.port)
		}
		if other, ok := seen[pair.port]; ok {
			return fmt.Errorf("port %d used by both %s and %s", pair.port, other, pair.name)
		}
		seen[pair.port] = pair.name
	}
	return nil
}

// PortNames maps the allocated ports back to their logical role, for the UI.
func (p RuntimeParams) PortNames() map[int]string {
	m := map[int]string{}
	for _, pair := range []struct {
		name string
		port int
	}{
		{"mixed", p.MixedPort}, {"socks", p.SocksPort}, {"http", p.HTTPPort},
		{"api", p.APIPort}, {"dns", p.DNSPort},
	} {
		if pair.port > 0 {
			m[pair.port] = pair.name
		}
	}
	return m
}

func (p RuntimeParams) Ports() []int {
	var out []int
	for _, v := range []int{p.MixedPort, p.SocksPort, p.HTTPPort, p.APIPort, p.DNSPort} {
		if v > 0 {
			out = append(out, v)
		}
	}
	return out
}

// ControlAPI describes a kernel's native management endpoint. In Stage 1 NGG
// only records it; Stage 2's Core Adapter Bridge will use it for per-connection
// dispatch. An empty Type means the kernel exposes no usable control API.
type ControlAPI struct {
	Type   string `json:"type" yaml:"type"` // "clash" | "none"
	URL    string `json:"url" yaml:"url"`
	Secret string `json:"secret,omitempty" yaml:"secret,omitempty"`
}

// ReleaseSpec is a data-driven description of a kernel's GitHub release
// assets. It is intentionally declarative so a new kernel can be added with a
// descriptor file instead of Go code (architecture revision, section 7).
type ReleaseSpec struct {
	Repository string `json:"repository" yaml:"repository"`

	// AssetTemplate is the fallback; AssetTemplates[GOOS] wins when present.
	// Upstreams use different archive extensions per OS, so this is a map.
	AssetTemplate  string            `json:"asset_template" yaml:"asset_template"`
	AssetTemplates map[string]string `json:"asset_templates" yaml:"asset_templates"`

	// DigestTemplate renders an OPTIONAL upstream digest asset, e.g.
	// "{asset}.dgst" for Xray-core. Independently, NGG always verifies the
	// sha256 digest published by the GitHub Release Assets API.
	DigestTemplate string `json:"digest_template" yaml:"digest_template"`

	Archive         string            `json:"archive" yaml:"archive"` // zip | gz | tar.gz | none
	BinaryInArchive string            `json:"binary_in_archive" yaml:"binary_in_archive"`
	OSMap           map[string]string `json:"os_map" yaml:"os_map"`
	ArchMap         map[string]string `json:"arch_map" yaml:"arch_map"`
	// ArchMapByOS overrides ArchMap for a specific GOOS (e.g. Android uses
	// "arm64-v8" while Windows uses "arm64").
	ArchMapByOS     map[string]map[string]string `json:"arch_map_by_os" yaml:"arch_map_by_os"`
	VersionPrefix   string                       `json:"version_prefix" yaml:"version_prefix"`
	DigestFormat    string                       `json:"digest_format" yaml:"digest_format"` // api | dgst | sums | single
	VersionInURLTag bool                         `json:"version_in_url_tag" yaml:"version_in_url_tag"`
}

// AssetTemplateFor returns the best template for the platform.
func (s ReleaseSpec) AssetTemplateFor(goos string) string {
	if t, ok := s.AssetTemplates[goos]; ok && t != "" {
		return t
	}
	return s.AssetTemplate
}

// CoreAdapter is the ONLY contract between NGG and a kernel binary.
//
// Stage 1 scope: lifecycle + configuration + version + release metadata.
// An adapter is pure: it computes descriptors, arguments and config text. It
// never spawns a process, touches the network, or writes a file. All side
// effects belong to Core Manager / Process Supervisor. This makes every
// adapter fully unit-testable and keeps "UI must not execute shell" enforceable.
//
// Stage 2 will extend (not replace) this with StreamAdapter / DatagramAdapter
// in internal/netengine — see the interfaces reserved there.
type CoreAdapter interface {
	// Descriptor returns identity, capability registration and source.
	Descriptor() contracts.CoreDescriptor

	// SupportedPlatforms lists GOOS/GOARCH pairs this adapter can install.
	SupportedPlatforms() []contracts.Platform

	// BinaryName is the executable filename for a given GOOS.
	BinaryName(goos string) string

	// VersionArgs / ParseVersion implement real version detection by executing
	// the native binary and parsing its stdout. No hardcoded version strings.
	VersionArgs() []string
	ParseVersion(raw string) (string, error)

	// ValidateArgs must invoke the kernel's OWN config validator.
	ValidateArgs(configPath string) []string
	RunArgs(configPath string) []string
	Environment(configPath string) []string

	ConfigFileName() string
	DefaultConfig(p RuntimeParams) ([]byte, error)

	Release() ReleaseSpec
	// AssetNames renders the release asset + digest asset names for a platform.
	AssetNames(p contracts.Platform, version string) (asset string, digest string, err error)

	// ControlAPI reports the native management endpoint for this config.
	ControlAPI(p RuntimeParams) ControlAPI
}

// ValidateCandidateProvider is an OPTIONAL extension. Upstreams occasionally
// change their config-check CLI dialect between releases; an adapter may
// expose several real, native validation invocations and ConfigManager will
// try them in order, accepting the first that exits 0. This is native
// validation with CLI-dialect adaptation — never a fake "valid" result.
type ValidateCandidateProvider interface {
	ValidateCandidates(configPath string) [][]string
}

// RenderAsset applies the small template language used by ReleaseSpec:
//
//	{os} {arch} {version} {vversion} {asset}
func RenderAsset(tmpl string, p contracts.Platform, version string, spec ReleaseSpec) string {
	osName := p.OS
	if v, ok := spec.OSMap[p.OS]; ok {
		osName = v
	}
	archName := p.Arch
	if m, ok := spec.ArchMapByOS[p.OS]; ok {
		if v, ok2 := m[p.Arch]; ok2 {
			archName = v
		}
	} else if v, ok := spec.ArchMap[p.Arch]; ok {
		archName = v
	}
	v := strings.TrimPrefix(version, "v")
	r := strings.NewReplacer(
		"{os}", osName,
		"{arch}", archName,
		"{version}", v,
		"{vversion}", spec.VersionPrefix+v,
	)
	return r.Replace(tmpl)
}

// DefaultAssetNames renders the asset name (and optional upstream digest
// asset) for a platform. Shared by builtin and descriptor-driven adapters.
func DefaultAssetNames(spec ReleaseSpec, p contracts.Platform, version string) (string, string, error) {
	tmpl := spec.AssetTemplateFor(p.OS)
	if tmpl == "" {
		return "", "", fmt.Errorf("no release asset template for %s", p.String())
	}
	asset := RenderAsset(tmpl, p, version, spec)
	var digest string
	if spec.DigestTemplate != "" {
		digest = strings.ReplaceAll(spec.DigestTemplate, "{asset}", asset)
		digest = RenderAsset(digest, p, version, spec)
	}
	return asset, digest, nil
}

// StandardPlatforms is the default platform matrix advertised by builtin
// kernels. Actual installability is determined by AssetNames + upstream assets.
func StandardPlatforms() []contracts.Platform {
	return []contracts.Platform{
		{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"},
		{OS: "linux", Arch: "386"}, {OS: "linux", Arch: "arm"},
		{OS: "windows", Arch: "amd64"}, {OS: "windows", Arch: "arm64"},
		{OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"},
		{OS: "android", Arch: "arm64"},
	}
}
