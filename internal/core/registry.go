package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// DescriptorFactory builds an adapter from an on-disk capability descriptor.
// The Registry depends only on this function type, never on a concrete adapter
// package — that is what keeps the Core Plugin Registry open for new kernels.
type DescriptorFactory func(path string) (CoreAdapter, error)

// Registry is the Core Plugin Registry required by architecture revision §7.
// Mihomo / Xray-core / sing-box are merely three entries registered at boot;
// nothing in this type (or in the Network Engine) hardcodes them.
type Registry struct {
	mu       sync.RWMutex
	adapters map[contracts.CoreID]CoreAdapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: make(map[contracts.CoreID]CoreAdapter)}
}

func (r *Registry) Register(a CoreAdapter) error {
	if a == nil {
		return fmt.Errorf("cannot register a nil adapter")
	}
	d := a.Descriptor()
	if strings.TrimSpace(string(d.ID)) == "" {
		return fmt.Errorf("adapter descriptor has empty id")
	}
	if len(a.VersionArgs()) == 0 {
		return fmt.Errorf("adapter %q has no version args", d.ID)
	}
	if len(a.RunArgs("config")) == 0 {
		return fmt.Errorf("adapter %q has no run args", d.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[d.ID]; exists {
		return fmt.Errorf("kernel %q is already registered", d.ID)
	}
	r.adapters[d.ID] = a
	return nil
}

// Unregister removes a dynamically added kernel. Builtin kernels are protected
// so the management center can never lose its baseline capabilities.
func (r *Registry) Unregister(id contracts.CoreID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.adapters[id]
	if !ok {
		return fmt.Errorf("kernel %q is not registered", id)
	}
	if a.Descriptor().Builtin {
		return fmt.Errorf("builtin kernel %q cannot be unregistered", id)
	}
	delete(r.adapters, id)
	return nil
}

func (r *Registry) Get(id contracts.CoreID) (CoreAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[id]
	if !ok {
		return nil, fmt.Errorf("kernel %q is not registered", id)
	}
	return a, nil
}

func (r *Registry) Has(id contracts.CoreID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.adapters[id]
	return ok
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.adapters)
}

// List returns descriptors sorted by ID for deterministic API output.
func (r *Registry) List() []contracts.CoreDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.CoreDescriptor, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a.Descriptor())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Adapters() []CoreAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CoreAdapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Descriptor().ID < out[j].Descriptor().ID })
	return out
}

// LoadDescriptorDir scans *.yaml / *.yml / *.json capability files and
// registers each through the supplied factory. Returns the number registered
// and the list of non-fatal problems (bad descriptors never abort the boot).
func (r *Registry) LoadDescriptorDir(dir string, factory DescriptorFactory) (int, []string) {
	problems := []string{}
	if factory == nil {
		return 0, []string{"no descriptor factory supplied"}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, []string{fmt.Sprintf("read descriptor dir: %v", err)}
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		a, err := factory(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		if err := r.Register(a); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		count++
	}
	return count, problems
}
