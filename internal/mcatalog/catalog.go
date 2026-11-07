// Package mcatalog holds the independent libraries behind 规划 2~9 of the
// module architecture. Each of them is manageable on its own; 规划 7 collects
// selected entries into one Module (the aggregation root in internal/module).
//
// One 0600 JSON file keeps the whole catalog, so "新建一个 X / 编辑 X" is a
// single atomic write and the module builder can copy entries by ID.
package mcatalog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
	"github.com/ngg-project/ngg/internal/module"
)

// ErrUnknownKind / ErrNotFound are the two business errors exposed to the API.
var (
	ErrUnknownKind = errors.New("未知的管理类型")
	ErrNotFound    = errors.New("条目不存在")
)

// Kinds is the canonical order of the independent libraries (规划 2..9).
var Kinds = []string{
	"kernels", "protocol-groups", "rules", "rule-sets",
	"dns", "policy-groups", "users", "links",
}

// 规划 2
type Kernel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	AutoStart bool      `json:"auto_start"`
	State     string    `json:"state"` // stopped | running | error
	UpdatedAt time.Time `json:"updated_at"`
}

// 规划 3
type ProtocolGroup struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	KernelIDs []string          `json:"kernel_ids"`
	Protocols []module.Protocol `json:"protocols"`
	Enabled   bool              `json:"enabled"`
}

// 规划 4
type Rule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Pattern  string `json:"pattern"`
	Outbound string `json:"outbound"`
	Enabled  bool   `json:"enabled"`
}

type RuleSet struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
}

// 规划 5
type DNSPolicy struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	LeakProtection  bool     `json:"leak_protection"`
	Encrypt         string   `json:"encrypt"` // none | doh | dot | doq
	Domestic        []string `json:"domestic"`
	Foreign         []string `json:"foreign"`
	Strategy        string   `json:"strategy"` // geo | all-domestic | all-foreign
	DomesticIPCIDRs []string `json:"domestic_ip_cidrs"`
}

// 规划 6
type PolicyGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Mode       string   `json:"mode"`
	RuleSetIDs []string `json:"rule_set_ids"`
	Enabled    bool     `json:"enabled"`
}

// 规划 8
type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ModuleID  string    `json:"module_id"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

// 规划 9
type Link struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Client    string    `json:"client"`
	Target    string    `json:"target"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// 规划 10
type MasterState struct {
	Enabled       bool      `json:"enabled"`
	AutoRegulate  bool      `json:"auto_regulate"`
	ModuleID      string    `json:"module_id"`
	ActiveKernels []string  `json:"active_kernels"`
	EgressNodeID  string    `json:"egress_node_id"`
	LastApply     time.Time `json:"last_apply"`
	LastResult    string    `json:"last_result"`
	RegulateCount int       `json:"regulate_count"`
}

// Catalog is the whole on-disk state of 规划 2~9.
type Catalog struct {
	Kernels        []Kernel        `json:"kernels"`
	ProtocolGroups []ProtocolGroup `json:"protocol_groups"`
	Rules          []Rule          `json:"rules"`
	RuleSets       []RuleSet       `json:"rule_sets"`
	DNS            []DNSPolicy     `json:"dns"`
	PolicyGroups   []PolicyGroup   `json:"policy_groups"`
	Users          []User          `json:"users"`
	Links          []Link          `json:"links"`
	Master         MasterState     `json:"master"`
}

// Store persists the catalog in <data>/module-catalog.json (0600).
type Store struct {
	mu   sync.RWMutex
	path string
	data Catalog
}

// NewStore opens the catalog, seeding the three kernel slots on first run.
func NewStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "module-catalog.json")}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取模块库失败: %w", err)
		}
		s.data = Catalog{Kernels: defaultKernels()}
		return s, nil
	}
	if len(b) == 0 {
		s.data = Catalog{Kernels: defaultKernels()}
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("解析模块库失败: %w", err)
	}
	if len(s.data.Kernels) == 0 {
		s.data.Kernels = defaultKernels()
	}
	return s, nil
}

func defaultKernels() []Kernel {
	out := make([]Kernel, 0, len(module.KernelIDs))
	for _, id := range module.KernelIDs {
		out = append(out, Kernel{ID: id, Name: id, Enabled: true, State: "stopped"})
	}
	return out
}

// Get returns a deep copy so callers cannot mutate in-memory state.
func (s *Store) Get() Catalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.data)
	var out Catalog
	_ = json.Unmarshal(b, &out)
	return out
}

// Update applies a mutation and persists it atomically.
func (s *Store) Update(fn func(*Catalog) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	backup := s.data
	if err := fn(&s.data); err != nil {
		s.data = backup
		return err
	}
	if err := atomicfile.WriteJSON(s.path, s.data); err != nil {
		s.data = backup
		return fmt.Errorf("保存模块库失败: %w", err)
	}
	return nil
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// Upsert adds or replaces one entry of the given kind. The ID is generated
// when omitted; the stored entry is returned.
func (s *Store) Upsert(kind string, raw []byte) (any, error) {
	var out any
	err := s.Update(func(c *Catalog) error {
		switch kind {
		case "kernels":
			var v Kernel
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("kernel")
			}
			v.UpdatedAt = time.Now().UTC()
			upsert(&c.Kernels, func(x Kernel) string { return x.ID }, v)
			out = v
		case "protocol-groups":
			var v ProtocolGroup
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("pg")
			}
			if err := validateKernelRefs(c, v.KernelIDs); err != nil {
				return err
			}
			upsert(&c.ProtocolGroups, func(x ProtocolGroup) string { return x.ID }, v)
			out = v
		case "rules":
			var v Rule
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("rule")
			}
			upsert(&c.Rules, func(x Rule) string { return x.ID }, v)
			out = v
		case "rule-sets":
			var v RuleSet
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("rs")
			}
			upsert(&c.RuleSets, func(x RuleSet) string { return x.ID }, v)
			out = v
		case "dns":
			var v DNSPolicy
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("dns")
			}
			if err := validateDNS(v); err != nil {
				return err
			}
			upsert(&c.DNS, func(x DNSPolicy) string { return x.ID }, v)
			out = v
		case "policy-groups":
			var v PolicyGroup
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("pol")
			}
			if err := validateRuleSetRefs(c, v.RuleSetIDs); err != nil {
				return err
			}
			upsert(&c.PolicyGroups, func(x PolicyGroup) string { return x.ID }, v)
			out = v
		case "users":
			var v User
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("user")
			}
			if v.ModuleID == "" {
				return errors.New("用户必须选择一个模块")
			}
			if v.Token == "" {
				v.Token = newToken()
			}
			if v.CreatedAt.IsZero() {
				v.CreatedAt = time.Now().UTC()
			}
			upsert(&c.Users, func(x User) string { return x.ID }, v)
			out = v
		case "links":
			var v Link
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v.ID == "" {
				v.ID = newID("link")
			}
			if v.CreatedAt.IsZero() {
				v.CreatedAt = time.Now().UTC()
			}
			upsert(&c.Links, func(x Link) string { return x.ID }, v)
			out = v
		default:
			return ErrUnknownKind
		}
		return nil
	})
	if errors.Is(err, ErrUnknownKind) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Remove deletes one entry by kind and id.
func (s *Store) Remove(kind, id string) error {
	return s.Update(func(c *Catalog) error {
		var ok bool
		switch kind {
		case "kernels":
			c.Kernels, ok = removeOne(c.Kernels, func(x Kernel) string { return x.ID }, id)
		case "protocol-groups":
			c.ProtocolGroups, ok = removeOne(c.ProtocolGroups, func(x ProtocolGroup) string { return x.ID }, id)
		case "rules":
			c.Rules, ok = removeOne(c.Rules, func(x Rule) string { return x.ID }, id)
		case "rule-sets":
			c.RuleSets, ok = removeOne(c.RuleSets, func(x RuleSet) string { return x.ID }, id)
		case "dns":
			c.DNS, ok = removeOne(c.DNS, func(x DNSPolicy) string { return x.ID }, id)
		case "policy-groups":
			c.PolicyGroups, ok = removeOne(c.PolicyGroups, func(x PolicyGroup) string { return x.ID }, id)
		case "users":
			c.Users, ok = removeOne(c.Users, func(x User) string { return x.ID }, id)
		case "links":
			c.Links, ok = removeOne(c.Links, func(x Link) string { return x.ID }, id)
		default:
			return ErrUnknownKind
		}
		if !ok {
			return ErrNotFound
		}
		return nil
	})
}

func upsert[T any](list *[]T, id func(T) string, item T) {
	key := id(item)
	for i := range *list {
		if id((*list)[i]) == key {
			(*list)[i] = item
			return
		}
	}
	*list = append(*list, item)
}

func removeOne[T any](list []T, id func(T) string, key string) ([]T, bool) {
	for i := range list {
		if id(list[i]) == key {
			return append(list[:i], list[i+1:]...), true
		}
	}
	return list, false
}

func validateKernelRefs(c *Catalog, ids []string) error {
	known := map[string]bool{}
	for _, k := range c.Kernels {
		known[k.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return fmt.Errorf("内核 %q 不在内核管理里", id)
		}
	}
	return nil
}

func validateRuleSetRefs(c *Catalog, ids []string) error {
	known := map[string]bool{}
	for _, rs := range c.RuleSets {
		known[rs.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return fmt.Errorf("规则集 %q 不存在，请先新建规则集", id)
		}
	}
	return nil
}

func validateDNS(v DNSPolicy) error {
	allowed := map[string]bool{"": true, "none": true, "doh": true, "dot": true, "doq": true}
	if !allowed[v.Encrypt] {
		return fmt.Errorf("加密方式只能是 none / doh / dot / doq")
	}
	strategies := map[string]bool{"": true, "geo": true, "all-domestic": true, "all-foreign": true}
	if !strategies[v.Strategy] {
		return fmt.Errorf("分流策略只能是 geo / all-domestic / all-foreign")
	}
	return nil
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SortForDisplay keeps the console stable regardless of write order.
func SortForDisplay(c *Catalog) {
	sort.Slice(c.Kernels, func(i, j int) bool { return c.Kernels[i].ID < c.Kernels[j].ID })
}
