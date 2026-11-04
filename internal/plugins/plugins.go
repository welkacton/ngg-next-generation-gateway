// Package plugins implements the Stage 4 plugin runtime: a real JavaScript
// engine (goja) and a real WebAssembly runtime (wazero), with a capability
// manifest, enable/disable lifecycle and hook dispatch into NGG's data plane.
package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dop251/goja"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"gopkg.in/yaml.v3"
)

type Kind string

const (
	KindJS   Kind = "javascript"
	KindWASM Kind = "wasm"
)

type Manifest struct {
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Kind        Kind     `json:"kind" yaml:"kind"`
	Source      string   `json:"source" yaml:"source"`           // inline code or file path
	Hooks       []string `json:"hooks" yaml:"hooks"`             // session | dns
	Permissions []string `json:"permissions" yaml:"permissions"` // decision:write | log
	Enabled     bool     `json:"enabled" yaml:"enabled"`
	Description string   `json:"description" yaml:"description"`
}

type Plugin struct {
	Manifest Manifest       `json:"manifest"`
	Loaded   bool           `json:"loaded"`
	Error    string         `json:"error,omitempty"`
	program  *goja.Program  `json:"-"`
	runtime  wazero.Runtime `json:"-"`
	module   api.Module     `json:"-"`
	fn       api.Function   `json:"-"`
	mu       *sync.Mutex    `json:"-"`
}

type Manager struct {
	mu      sync.RWMutex
	plugins map[string]*Plugin
	dir     string
}

func NewManager(dir string) *Manager {
	return &Manager{plugins: map[string]*Plugin{}, dir: dir}
}

func (m *Manager) Install(man Manifest) (*Plugin, error) {
	if strings.TrimSpace(man.ID) == "" {
		return nil, fmt.Errorf("plugin id is required")
	}
	if len(man.Hooks) == 0 {
		man.Hooks = []string{"session"}
	}
	p := &Plugin{Manifest: man, mu: &sync.Mutex{}}
	if err := m.compile(p); err != nil {
		p.Error = err.Error()
		p.Loaded = false
		m.mu.Lock()
		m.plugins[man.ID] = p
		m.mu.Unlock()
		return p, err
	}
	p.Loaded = true
	m.mu.Lock()
	m.plugins[man.ID] = p
	m.mu.Unlock()
	return p, nil
}

func (m *Manager) compile(p *Plugin) error {
	src := p.Manifest.Source
	if src == "" {
		return fmt.Errorf("plugin %q has no source", p.Manifest.ID)
	}
	// If Source is a path to an existing file, load it; otherwise treat it as
	// inline code. WASM may be supplied as a base64/file path too.
	if len(src) < 512 && !strings.Contains(src, "\n") {
		candidate := src
		if !filepath.IsAbs(candidate) && m.dir != "" {
			candidate = filepath.Join(m.dir, src)
		}
		if b, err := os.ReadFile(candidate); err == nil {
			src = string(b)
		}
	}
	switch p.Manifest.Kind {
	case KindJS:
		prog, err := goja.Compile(p.Manifest.ID, src, false)
		if err != nil {
			return fmt.Errorf("JS compile: %w", err)
		}
		p.program = prog
	case KindWASM:
		data := []byte(src)
		if strings.HasPrefix(strings.TrimSpace(src), "AGFzbQ") {
			// base64-encoded wasm
			decoded, err := decodeBase64(strings.TrimSpace(src))
			if err != nil {
				return fmt.Errorf("WASM base64: %w", err)
			}
			data = decoded
		}
		if len(data) < 8 || string(data[:4]) != "\x00asm" {
			return fmt.Errorf("WASM source is not a valid module (magic missing)")
		}
		ctx := context.Background()
		rt := wazero.NewRuntime(ctx)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
			rt.Close(ctx)
			return fmt.Errorf("WASI: %w", err)
		}
		mod, err := rt.InstantiateWithConfig(ctx, data, wazero.NewModuleConfig().WithName(p.Manifest.ID))
		if err != nil {
			rt.Close(ctx)
			return fmt.Errorf("WASM instantiate: %w", err)
		}
		fn := mod.ExportedFunction("ngg_hook")
		if fn == nil {
			rt.Close(ctx)
			return fmt.Errorf("WASM module must export ngg_hook() -> i32")
		}
		p.runtime, p.module, p.fn = rt, mod, fn
	default:
		return fmt.Errorf("unknown plugin kind %q", p.Manifest.Kind)
	}
	return nil
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func (m *Manager) Enable(id string, enabled bool) (*Plugin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plugins[id]
	if !ok {
		return nil, fmt.Errorf("plugin %q not found", id)
	}
	if enabled && !p.Loaded {
		return nil, fmt.Errorf("plugin %q failed to load: %s", id, p.Error)
	}
	p.Manifest.Enabled = enabled
	return p, nil
}

func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plugins[id]
	if !ok {
		return fmt.Errorf("plugin %q not found", id)
	}
	if p.runtime != nil {
		_ = p.runtime.Close(context.Background())
	}
	delete(m.plugins, id)
	return nil
}

func (m *Manager) List() []Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		cp := *p
		cp.program, cp.runtime, cp.module, cp.fn = nil, nil, nil, nil
		cp.mu = &sync.Mutex{}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

func (m *Manager) Enabled() []*Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Plugin
	for _, p := range m.plugins {
		if p.Manifest.Enabled && p.Loaded {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

func (m *Manager) Count() int { return len(m.Enabled()) }

// RunSessionHooks executes every enabled plugin on a session. Plugins may
// write `decision.core_id`, `decision.block`, `decision.direct` and `tags`.
// WASM plugins return 1 to block, 0 to allow. Errors are Fail-Closed: the
// caller decides, but a broken plugin never silently changes policy.
func (m *Manager) RunSessionHooks(ctx context.Context, input map[string]any) (map[string]any, error) {
	result := map[string]any{}
	for _, p := range m.Enabled() {
		if !hasHook(p, "session") {
			continue
		}
		switch p.Manifest.Kind {
		case KindJS:
			out, err := runJS(p, input)
			if err != nil {
				return result, fmt.Errorf("plugin %q: %w", p.Manifest.ID, err)
			}
			for k, v := range out {
				result[k] = v
			}
		case KindWASM:
			p.mu.Lock()
			res, err := p.fn.Call(ctx)
			p.mu.Unlock()
			if err != nil {
				return result, fmt.Errorf("WASM plugin %q: %w", p.Manifest.ID, err)
			}
			if len(res) > 0 && res[0] == 1 {
				result["block"] = true
			}
		}
	}
	return result, nil
}

func (m *Manager) RunDNSHooks(ctx context.Context, name string) (bool, error) {
	input := map[string]any{"name": name, "domain": name, "hook": "dns"}
	for _, p := range m.Enabled() {
		if !hasHook(p, "dns") {
			continue
		}
		if p.Manifest.Kind == KindJS {
			out, err := runJS(p, input)
			if err != nil {
				return false, err
			}
			if b, ok := out["block"].(bool); ok && b {
				return true, nil
			}
		}
	}
	return false, nil
}

func hasHook(p *Plugin, hook string) bool {
	for _, h := range p.Manifest.Hooks {
		if strings.EqualFold(h, hook) {
			return true
		}
	}
	return false
}

func runJS(p *Plugin, input map[string]any) (map[string]any, error) {
	vm := goja.New()
	// Sandbox: no require, no filesystem, no network. Only ngg.log/input and
	// a plain `result` object are available.
	_ = vm.Set("require", goja.Undefined())
	logger := func(call goja.FunctionCall) goja.Value { return goja.Undefined() }
	_ = vm.Set("console", map[string]any{"log": logger})
	_ = vm.Set("ngg", map[string]any{"input": input, "log": logger})
	if _, err := vm.RunProgram(p.program); err != nil {
		return nil, err
	}
	v := vm.Get("result")
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return map[string]any{}, nil
	}
	exported := v.Export()
	b, err := json.Marshal(exported)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("JS plugin result must be an object: %w", err)
	}
	return out, nil
}

// LoadManifestDir loads *.yaml/yml/json plugin manifests from a directory.
func (m *Manager) LoadManifestDir(dir string) (int, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil
	}
	var problems []string
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		var man Manifest
		if err := yaml.Unmarshal(b, &man); err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		if _, err := m.Install(man); err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		count++
	}
	return count, problems
}
