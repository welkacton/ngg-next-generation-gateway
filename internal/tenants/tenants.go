// Package tenants implements Stage 5 enterprise multi-tenant isolation.
package tenants

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/ngg-project/ngg/internal/netengine"
)

type Tenant struct {
	ID               string   `json:"id" yaml:"id"`
	Name             string   `json:"name" yaml:"name"`
	Enabled          bool     `json:"enabled" yaml:"enabled"`
	MaxSessions      int      `json:"max_sessions" yaml:"max_sessions"`
	QuotaBytes       uint64   `json:"quota_bytes" yaml:"quota_bytes"`
	UsedBytes        uint64   `json:"used_bytes" yaml:"used_bytes"`
	AllowedCores     []string `json:"allowed_cores" yaml:"allowed_cores"`
	AllowedProtocols []string `json:"allowed_protocols" yaml:"allowed_protocols"`
	DefaultCore      string   `json:"default_core" yaml:"default_core"`
	RuleSetTag       string   `json:"rule_set_tag" yaml:"rule_set_tag"`
}

type Registry struct {
	mu      sync.RWMutex
	tenants map[string]*Tenant
}

var _ netengine.TenantResolver = (*Registry)(nil)

func NewRegistry() *Registry {
	return &Registry{tenants: map[string]*Tenant{
		"default": {ID: "default", Name: "Default tenant", Enabled: true, MaxSessions: 100000, QuotaBytes: 0},
	}}
}

func (r *Registry) Add(t Tenant) error {
	if t.ID == "" {
		return fmt.Errorf("tenant id is required")
	}
	if t.MaxSessions <= 0 {
		t.MaxSessions = 1000
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tenants[t.ID] = &t
	return nil
}

func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "default" {
		return false
	}
	if _, ok := r.tenants[id]; !ok {
		return false
	}
	delete(r.tenants, id)
	return true
}

func (r *Registry) List() []Tenant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tenant, 0, len(r.tenants))
	for _, t := range r.tenants {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Get(id string) (Tenant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tenants[id]
	if !ok {
		return Tenant{}, false
	}
	return *t, true
}

func (r *Registry) ResolveTenant(_ context.Context, key string) (string, error) {
	if key == "" {
		return "default", nil
	}
	r.mu.RLock()
	t, ok := r.tenants[key]
	r.mu.RUnlock()
	if !ok || !t.Enabled {
		return "", fmt.Errorf("tenant %q not found or disabled", key)
	}
	return key, nil
}

// Allowed enforces per-tenant core / outbound isolation at the data plane.
func (r *Registry) Allowed(tenantID string, d netengine.RouteDecision) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tenants[tenantID]
	if !ok || !t.Enabled {
		return tenantID == "default"
	}
	if t.QuotaBytes > 0 && t.UsedBytes >= t.QuotaBytes {
		return false
	}
	if d.CoreID == "" || d.Direct {
		return true
	}
	if len(t.AllowedCores) == 0 {
		return true
	}
	for _, c := range t.AllowedCores {
		if c == string(d.CoreID) {
			return true
		}
	}
	return false
}

func (r *Registry) Charge(tenantID string, bytes uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tenants[tenantID]; ok {
		t.UsedBytes += bytes
	}
}
