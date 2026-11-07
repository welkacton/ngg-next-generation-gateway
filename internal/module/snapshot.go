package module

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Snapshot is a FROZEN copy of a module, bound to a user's subscription token.
// Editing a module afterwards never silently changes an already-issued link;
// the operator has to re-issue explicitly.
type Snapshot struct {
	ModuleID string    `json:"module_id"`
	Hash     string    `json:"hash"`
	TakenAt  time.Time `json:"taken_at"`
	Module   Module    `json:"module"`
}

// Snapshot deep-copies the module (so later edits cannot mutate it) and pins
// its content hash. Credential hashes stay inside; private keys are not in
// NodeSpec by design.
func (m Module) Snapshot() (Snapshot, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return Snapshot{}, fmt.Errorf("序列化模块失败: %w", err)
	}
	var frozen Module
	if err := json.Unmarshal(b, &frozen); err != nil {
		return Snapshot{}, fmt.Errorf("冻结模块失败: %w", err)
	}
	sum := sha256.Sum256(b)
	return Snapshot{
		ModuleID: m.ID,
		Hash:     hex.EncodeToString(sum[:]),
		TakenAt:  time.Now().UTC(),
		Module:   frozen,
	}, nil
}

// Preview is what the module card / subscription preview shows: a compact,
// human-readable account of everything the master kernel will regulate.
type Preview struct {
	ModuleID      string            `json:"module_id"`
	Name          string            `json:"name"`
	Hash          string            `json:"hash"`
	Master        MasterConfig      `json:"master"`
	Nodes         []string          `json:"nodes"`
	Kernels       map[string]string `json:"kernels"`
	ProtocolGroup []string          `json:"protocol_groups"`
	Rules         []string          `json:"rules"`
	RuleSets      []string          `json:"rule_sets"`
	DNS           DNSConfig         `json:"dns"`
	PolicyGroups  []string          `json:"policy_groups"`
	Warnings      []Issue           `json:"warnings"`
}

// BuildPreview is the read-only projection used by /modules/{id}/preview.
func (m Module) BuildPreview() (Preview, error) {
	snap, err := m.Snapshot()
	if err != nil {
		return Preview{}, err
	}
	p := Preview{
		ModuleID: m.ID, Name: m.Name, Hash: snap.Hash,
		Master: m.Master, DNS: m.DNS, Kernels: map[string]string{},
		Warnings: m.Validate(),
	}
	for _, n := range m.Nodes {
		p.Nodes = append(p.Nodes, fmt.Sprintf("%s:%d", n.Address, n.Port))
	}
	for _, k := range m.Kernels {
		state := "stopped"
		if k.Enabled {
			state = "enabled"
		}
		p.Kernels[k.Kernel] = state
	}
	for _, g := range m.ProtocolGroups {
		p.ProtocolGroup = append(p.ProtocolGroup,
			fmt.Sprintf("%s → %v", g.Name, g.KernelIDs))
	}
	for _, r := range m.Rules {
		if r.Enabled {
			p.Rules = append(p.Rules, r.Name)
		}
	}
	for _, rs := range m.RuleSets {
		if rs.Enabled {
			p.RuleSets = append(p.RuleSets, rs.Name)
		}
	}
	for _, pg := range m.PolicyGroups {
		if pg.Enabled {
			p.PolicyGroups = append(p.PolicyGroups, pg.Name)
		}
	}
	return p, nil
}
