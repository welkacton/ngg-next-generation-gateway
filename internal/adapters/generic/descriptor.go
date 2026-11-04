// Package generic implements a descriptor-driven Core Adapter. This is the
// concrete extension point of the Core Plugin Registry: adding a new kernel
// requires a YAML/JSON capability descriptor, NOT a change to NGG's Network
// Engine and NOT a recompile (architecture revision section 7).
package generic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
	"gopkg.in/yaml.v3"
)

type commandSet struct {
	Version  []string `json:"version" yaml:"version"`
	Run      []string `json:"run" yaml:"run"`
	Validate []string `json:"validate" yaml:"validate"`
	Env      []string `json:"env" yaml:"env"`
}

type configSet struct {
	FileName string `json:"file_name" yaml:"file_name"`
	Default  string `json:"default" yaml:"default"`
}

// Descriptor is the on-disk capability + command description of a kernel.
// Command and config strings are Go text/template values with the data fields
// documented inAdapter.templateData().
type Descriptor struct {
	ID           string               `json:"id" yaml:"id"`
	DisplayName  string               `json:"display_name" yaml:"display_name"`
	Description  string               `json:"description" yaml:"description"`
	Homepage     string               `json:"homepage" yaml:"homepage"`
	Repository   string               `json:"repository" yaml:"repository"`
	Capability   contracts.Capability `json:"capability" yaml:"capability"`
	Platforms    []contracts.Platform `json:"platforms" yaml:"platforms"`
	Binary       map[string]string    `json:"binary" yaml:"binary"`
	Commands     commandSet           `json:"commands" yaml:"commands"`
	Config       configSet            `json:"config" yaml:"config"`
	Release      core.ReleaseSpec     `json:"release" yaml:"release"`
	Control      core.ControlAPI      `json:"control_api" yaml:"control_api"`
	VersionRegex string               `json:"version_regex" yaml:"version_regex"`
}

// Adapter is a CoreAdapter built entirely from a Descriptor.
type Adapter struct {
	d    Descriptor
	path string
}

var _ core.CoreAdapter = (*Adapter)(nil)

// NewFromFile parses a descriptor and validates the mandatory fields. A bad
// descriptor is rejected loudly rather than silently producing a half-broken
// kernel entry.
func NewFromFile(path string) (core.CoreAdapter, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read descriptor %s: %w", path, err)
	}
	var d Descriptor
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		err = json.Unmarshal(b, &d)
	default:
		err = yaml.Unmarshal(b, &d)
	}
	if err != nil {
		return nil, fmt.Errorf("parse descriptor %s: %w", path, err)
	}
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("invalid descriptor %s: %w", path, err)
	}
	return &Adapter{d: d, path: path}, nil
}

func New(d Descriptor) (core.CoreAdapter, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &Adapter{d: d}, nil
}

func (d Descriptor) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if len(d.Commands.Version) == 0 {
		return fmt.Errorf("commands.version is required")
	}
	if len(d.Commands.Run) == 0 {
		return fmt.Errorf("commands.run is required")
	}
	if len(d.Commands.Validate) == 0 {
		return fmt.Errorf("commands.validate is required (native config validation is mandatory)")
	}
	if d.Config.FileName == "" {
		return fmt.Errorf("config.file_name is required")
	}
	if d.Release.Repository == "" {
		return fmt.Errorf("release.repository is required")
	}
	if d.Release.AssetTemplate == "" && len(d.Release.AssetTemplates) == 0 {
		return fmt.Errorf("release.asset_template(s) is required")
	}
	if !strings.Contains(d.Release.Repository, "/") {
		return fmt.Errorf("release.repository must be owner/repo")
	}
	return nil
}

func (a *Adapter) Descriptor() contracts.CoreDescriptor {
	name := a.d.DisplayName
	if name == "" {
		name = a.d.ID
	}
	repo := a.d.Repository
	if repo == "" {
		repo = a.d.Release.Repository
	}
	return contracts.CoreDescriptor{
		ID:          contracts.CoreID(a.d.ID),
		DisplayName: name,
		Description: a.d.Description,
		Homepage:    a.d.Homepage,
		Repository:  repo,
		Capability:  a.d.Capability,
		Builtin:     false,
		Source:      "descriptor:" + filepath.Base(a.path),
	}
}

func (a *Adapter) SupportedPlatforms() []contracts.Platform {
	if len(a.d.Platforms) > 0 {
		return a.d.Platforms
	}
	return core.StandardPlatforms()
}

func (a *Adapter) BinaryName(goos string) string {
	if n, ok := a.d.Binary[goos]; ok && n != "" {
		return n
	}
	if n, ok := a.d.Binary["default"]; ok && n != "" {
		return n
	}
	if goos == "windows" {
		return a.d.ID + ".exe"
	}
	return a.d.ID
}

func (a *Adapter) VersionArgs() []string { return a.d.Commands.Version }

func (a *Adapter) ParseVersion(raw string) (string, error) {
	return core.ExtractVersionAny(raw, a.d.VersionRegex)
}

func (a *Adapter) ConfigFileName() string { return a.d.Config.FileName }

type templateData struct {
	Config    string
	Dir       string
	MixedPort int
	SocksPort int
	HTTPPort  int
	APIPort   int
	DNSPort   int
	Secret    string
}

func (a *Adapter) renderList(items []string, configPath string) []string {
	data := templateData{
		Config: configPath,
		Dir:    filepath.Dir(configPath),
	}
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, a.render(s, data))
	}
	return out
}

func (a *Adapter) render(s string, data any) string {
	t, err := template.New("t").Option("missingkey=zero").Parse(s)
	if err != nil {
		return s
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return s
	}
	return buf.String()
}

func (a *Adapter) ValidateArgs(configPath string) []string {
	return a.renderList(a.d.Commands.Validate, configPath)
}

func (a *Adapter) RunArgs(configPath string) []string {
	return a.renderList(a.d.Commands.Run, configPath)
}

func (a *Adapter) Environment(configPath string) []string {
	return a.renderList(a.d.Commands.Env, configPath)
}

func (a *Adapter) DefaultConfig(p core.RuntimeParams) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if a.d.Config.Default == "" {
		return nil, fmt.Errorf("descriptor %q has no default config template", a.d.ID)
	}
	s := a.render(a.d.Config.Default, templateData{
		MixedPort: p.MixedPort, SocksPort: p.SocksPort, HTTPPort: p.HTTPPort,
		APIPort: p.APIPort, DNSPort: p.DNSPort, Secret: p.Secret, Dir: p.Dir,
		Config: filepath.Join(p.Dir, a.ConfigFileName()),
	})
	return []byte(s), nil
}

func (a *Adapter) Release() core.ReleaseSpec { return a.d.Release }

func (a *Adapter) AssetNames(p contracts.Platform, version string) (string, string, error) {
	return core.DefaultAssetNames(a.d.Release, p, version)
}

func (a *Adapter) ControlAPI(p core.RuntimeParams) core.ControlAPI {
	c := a.d.Control
	if c.Type == "" {
		c.Type = "none"
	}
	c.URL = a.render(c.URL, templateData{APIPort: p.APIPort, Secret: p.Secret})
	if c.Secret == "" {
		c.Secret = p.Secret
	}
	return c
}

// LocalProxy renders the capability descriptor's local TCP endpoint. This is
// what lets a descriptor-only kernel join the Stage 2 Protocol Dispatcher with
// no Go code changes.
func (a *Adapter) LocalProxy(p core.RuntimeParams) core.ProxyEndpoint {
	raw := a.d.Capability.LocalTCPEndpoint
	if raw == "" {
		return core.ProxyEndpoint{}
	}
	addr := a.render(raw, templateData{
		MixedPort: p.MixedPort, SocksPort: p.SocksPort, HTTPPort: p.HTTPPort,
		APIPort: p.APIPort, DNSPort: p.DNSPort, Secret: p.Secret,
	})
	kind := core.ProxyKind(a.d.Capability.LocalProxyKind)
	if kind == "" {
		kind = core.ProxySOCKS5
	}
	return core.ProxyEndpoint{Kind: kind, Address: addr, Networks: []string{"tcp", "udp"}}
}

var _ core.LocalProxyProvider = (*Adapter)(nil)
