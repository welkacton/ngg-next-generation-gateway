// Package hosts implements the Config Engine's remote-host registry.
//
// A "host" is a physical/virtual machine (VPS, home PC, soft router, ...) that
// can be added from anywhere and then freely assigned as an INBOUND, an
// OUTBOUND or BOTH. This is deliberately separate from internal/nodes, which
// describes protocol endpoints of a locally running kernel: here the unit of
// management is the machine itself, matching the node-centric console UX.
//
// No simulated telemetry: the only live signal is a REAL TCP dial to the
// host's management/service port. Anything NGG cannot measure is reported as
// "not collected" instead of a fabricated number.
package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Role is the traffic direction assigned to a remote machine. Any host can be
// switched between roles at any time; there is no single fixed "main" host.
type Role string

const (
	RoleInbound  Role = "inbound"
	RoleOutbound Role = "outbound"
	RoleBoth     Role = "both"
)

func ValidRole(r Role) bool {
	return r == RoleInbound || r == RoleOutbound || r == RoleBoth
}

// MaxChainHops caps how many proxy hops may be stacked in front of an
// outbound host. Eight is already absurd for real traffic and keeps the
// rendered kernel config readable.
const MaxChainHops = 8

// Chain-hop kinds.
const (
	ChainKindSOCKS5 = "socks5"
	ChainKindHTTP   = "http"
)

// ChainHop is one upstream proxy between NGG and the egress host, used to
// reach a host that is only reachable through an intermediate proxy.
type ChainHop struct {
	Kind     string `json:"kind"` // "socks5" | "http"
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// Password is echoed back like the existing SecretKey field: the console
	// needs it to edit the hop, and hiding it would make a PUT from the UI
	// silently clear the credential. It is never written to logs.
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}

// Host is one registered remote machine.
type Host struct {
	ID       string `json:"id"`
	Name     string `json:"name"`      // display name, e.g. "US us01"
	Internal string `json:"internal"`  // short internal name, e.g. "us01"
	Country  string `json:"country"`   // ISO-3166 alpha-2, e.g. "US"
	Address  string `json:"address"`   // IPv4 / IPv6 / DNS name, no scheme
	NodePort int    `json:"node_port"` // management / agent port
	SvcPort  int    `json:"svc_port"`  // proxy service port

	Role     Role   `json:"role"`
	Enabled  bool   `json:"enabled"`
	Protocol string `json:"protocol"`
	CoreID   string `json:"core_id"`
	Profile  string `json:"profile"`

	// Chain is the optional proxy chain in front of this host. nil (old data)
	// means "direct", exactly as before v2.7.0.
	Chain []ChainHop `json:"chain"`

	SecretKey string `json:"secret_key"`
	ProxyURL  string `json:"proxy_url"`
	Plugin    string `json:"plugin"`
	Provider  string `json:"provider"`

	IPs   []string `json:"ip_addresses"`
	Tags  []string `json:"tags"`
	Note  string   `json:"note"`
	Track bool     `json:"track_traffic"`

	UserRatio float64 `json:"user_ratio"`
	NodeRatio float64 `json:"node_ratio"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProbeResult is the outcome of a REAL TCP dial. It is never persisted: a
// restart clears stale "online" state rather than showing a fake success.
type ProbeResult struct {
	Online    bool      `json:"online"`
	LatencyMS int64     `json:"latency_ms"`
	Target    string    `json:"target"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

// HostView is a host plus its last real probe result for the UI.
type HostView struct {
	Host
	Probe *ProbeResult `json:"probe"`
}

type Stats struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Inbound  int `json:"inbound"`
	Outbound int `json:"outbound"`
	Both     int `json:"both"`
	Online   int `json:"online"`
	Checked  int `json:"checked"`
}

var idRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
var hostRE = regexp.MustCompile(`^[a-zA-Z0-9.:_-]+$`)

func normalize(h *Host) {
	h.ID = strings.TrimSpace(h.ID)
	h.Name = strings.TrimSpace(h.Name)
	h.Internal = strings.TrimSpace(h.Internal)
	h.Country = strings.ToUpper(strings.TrimSpace(h.Country))
	h.Address = strings.TrimSpace(h.Address)
	// Schemes / paths are rejected, not silently stripped: silently rewriting
	// a user-supplied address would make the probe target differ from what the
	// operator sees in the form.
	h.Role = Role(strings.ToLower(strings.TrimSpace(string(h.Role))))
	h.SecretKey = strings.TrimSpace(h.SecretKey)
	h.ProxyURL = strings.TrimSpace(h.ProxyURL)
	h.Plugin = strings.TrimSpace(h.Plugin)
	h.Provider = strings.TrimSpace(h.Provider)
	h.Protocol = strings.TrimSpace(h.Protocol)
	h.CoreID = strings.TrimSpace(h.CoreID)
	h.Profile = strings.TrimSpace(h.Profile)
	h.Note = strings.TrimSpace(h.Note)
	if h.Role == "" {
		h.Role = RoleOutbound
	}
	if h.UserRatio <= 0 {
		h.UserRatio = 1
	}
	if h.NodeRatio <= 0 {
		h.NodeRatio = 1
	}
	if h.IPs == nil {
		h.IPs = []string{}
	}
	if h.Tags == nil {
		h.Tags = []string{}
	}
	h.Chain = normalizeChain(h.Chain)
}

// normalizeChain trims every hop and canonicalises the kind. It returns nil
// for an empty chain so a host without a chain serialises as null (the
// pre-v2.7.0 shape) instead of an empty array.
func normalizeChain(in []ChainHop) []ChainHop {
	if len(in) == 0 {
		return nil
	}
	out := make([]ChainHop, 0, len(in))
	for _, hop := range in {
		kind := strings.ToLower(strings.TrimSpace(hop.Kind))
		switch kind {
		case "", "socks", "socks5", "socks5h":
			// "" is kept so validation can report "kind 必填" for a hop that
			// was actually configured; a missing kind is never silently
			// turned into socks5.
			if kind == "socks" || kind == "socks5h" {
				kind = ChainKindSOCKS5
			}
		}
		out = append(out, ChainHop{
			Kind:     kind,
			Address:  strings.TrimSpace(hop.Address),
			Port:     hop.Port,
			Username: strings.TrimSpace(hop.Username),
			Password: hop.Password,
			Enabled:  hop.Enabled,
		})
	}
	return out
}

// validateChain enforces the chain contract: at most MaxChainHops hops, a
// known kind, an address without scheme, and a real port for enabled hops.
func validateChain(chain []ChainHop) error {
	if len(chain) > MaxChainHops {
		return fmt.Errorf("链式代理最多 %d 跳，当前 %d 跳", MaxChainHops, len(chain))
	}
	for i, hop := range chain {
		label := fmt.Sprintf("第 %d 跳链式代理", i+1)
		if hop.Kind != ChainKindSOCKS5 && hop.Kind != ChainKindHTTP {
			return fmt.Errorf("%s：kind 必须是 socks5 或 http", label)
		}
		if strings.Contains(hop.Address, "://") {
			return fmt.Errorf("%s：地址不含协议前缀（不要写 socks5://）", label)
		}
		if hop.Address != "" && (!hostRE.MatchString(hop.Address) || strings.Contains(hop.Address, " ")) {
			return fmt.Errorf("%s：地址需为 IP 或域名", label)
		}
		if hop.Port != 0 && (hop.Port < 1 || hop.Port > 65535) {
			return fmt.Errorf("%s：端口需在 1..65535", label)
		}
		// A disabled hop is a draft the operator saved; it only has to be
		// complete once it is switched on.
		if hop.Enabled {
			if hop.Address == "" {
				return fmt.Errorf("%s：启用后必须填写地址", label)
			}
			if hop.Port < 1 || hop.Port > 65535 {
				return fmt.Errorf("%s：启用后必须填写 1..65535 的端口", label)
			}
		}
	}
	return nil
}

func validate(h Host) error {
	if !idRE.MatchString(h.ID) {
		return fmt.Errorf("id 必填，只允许字母数字 . _ -（最多 64 字符）")
	}
	if h.Name == "" {
		return fmt.Errorf("节点名称必填")
	}
	if !ValidRole(h.Role) {
		return fmt.Errorf("角色必须是 inbound / outbound / both")
	}
	if h.Address == "" || !hostRE.MatchString(h.Address) || strings.Contains(h.Address, " ") {
		return fmt.Errorf("地址需为 IP 或域名（不含协议前缀与空格）")
	}
	if h.NodePort < 1 || h.NodePort > 65535 {
		return fmt.Errorf("node_port 需在 1..65535")
	}
	if h.SvcPort != 0 && (h.SvcPort < 1 || h.SvcPort > 65535) {
		return fmt.Errorf("svc_port 需在 1..65535（或 0 表示不指定）")
	}
	if h.UserRatio < 0.1 || h.UserRatio > 100 || h.NodeRatio < 0.1 || h.NodeRatio > 100 {
		return fmt.Errorf("流量倍率需在 0.1 .. 100")
	}
	for _, ip := range h.IPs {
		if strings.TrimSpace(ip) == "" {
			return fmt.Errorf("IP 地址列表含有空项")
		}
	}
	if err := validateChain(h.Chain); err != nil {
		return err
	}
	return nil
}

// dialTarget picks the management port first; when the host has no agent port
// the service port is used so a plain proxy server can still be probed.
func dialTarget(h Host) string {
	port := h.NodePort
	if port == 0 {
		port = h.SvcPort
	}
	return net.JoinHostPort(h.Address, strconv.Itoa(port))
}

// ProbeHost performs a real TCP connect. No HTTP assumption, no cached
// success: an unreachable host returns Online=false with the OS error text.
func ProbeHost(ctx context.Context, h Host) ProbeResult {
	res := ProbeResult{Target: dialTarget(h), CheckedAt: time.Now().UTC()}
	if res.Target == "" || h.Address == "" {
		res.Error = "no dial target"
		return res
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", res.Target)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	_ = conn.Close()
	res.Online = true
	res.LatencyMS = time.Since(start).Milliseconds()
	return res
}

type Registry struct {
	mu     sync.RWMutex
	hosts  map[string]Host
	probes map[string]ProbeResult
	path   string
}

func NewRegistry(path string) *Registry {
	return &Registry{
		hosts:  map[string]Host{},
		probes: map[string]ProbeResult{},
		path:   path,
	}
}

func (r *Registry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []Host
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("parse %s: %w", r.path, err)
	}
	m := map[string]Host{}
	for _, h := range list {
		normalize(&h)
		if err := validate(h); err != nil {
			// Fail-closed: a corrupt record must not silently enter the registry.
			return fmt.Errorf("invalid persisted host %q: %w", h.ID, err)
		}
		m[h.ID] = h
	}
	r.hosts = m
	return nil
}

func (r *Registry) persistLocked() error {
	list := make([]Host, 0, len(r.hosts))
	for _, h := range r.hosts {
		list = append(list, h)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o600)
}

func (r *Registry) Path() string { return r.path }

// List returns hosts matching role ("" or "all" = every host). "inbound"
// includes both-role hosts, "outbound" also includes both-role hosts.
func (r *Registry) List(role string) []Host {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Host{}
	for _, h := range r.hosts {
		if role != "" && role != "all" && !matches(h.Role, role) {
			continue
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func matches(role Role, filter string) bool {
	if role == RoleBoth {
		return true
	}
	return string(role) == filter
}

func (r *Registry) Views(role string) []HostView {
	list := r.List(role)
	r.mu.RLock()
	defer r.mu.RUnlock()
	views := make([]HostView, 0, len(list))
	for _, h := range list {
		v := HostView{Host: h}
		if p, ok := r.probes[h.ID]; ok {
			cp := p
			v.Probe = &cp
		}
		views = append(views, v)
	}
	return views
}

func (r *Registry) Get(id string) (Host, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.hosts[id]
	return h, ok
}

func (r *Registry) Add(ctx context.Context, h Host) (Host, error) {
	normalize(&h)
	if err := validate(h); err != nil {
		return Host{}, err
	}
	now := time.Now().UTC()
	h.CreatedAt, h.UpdatedAt = now, now
	r.mu.Lock()
	if _, ok := r.hosts[h.ID]; ok {
		r.mu.Unlock()
		return Host{}, fmt.Errorf("节点 %q 已存在", h.ID)
	}
	r.hosts[h.ID] = h
	if err := r.persistLocked(); err != nil {
		delete(r.hosts, h.ID)
		r.mu.Unlock()
		return Host{}, fmt.Errorf("持久化失败: %w", err)
	}
	r.mu.Unlock()
	// Real probe on add: never claim a node is online without dialling it.
	r.ProbeOne(ctx, h.ID)
	return h, nil
}

func (r *Registry) Update(id string, h Host) (Host, error) {
	h.ID = id
	normalize(&h)
	if err := validate(h); err != nil {
		return Host{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.hosts[id]
	if !ok {
		return Host{}, fmt.Errorf("节点 %q 不存在", id)
	}
	h.CreatedAt = old.CreatedAt
	h.UpdatedAt = time.Now().UTC()
	r.hosts[id] = h
	if err := r.persistLocked(); err != nil {
		r.hosts[id] = old
		return Host{}, fmt.Errorf("持久化失败: %w", err)
	}
	return h, nil
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.hosts[id]
	if !ok {
		return fmt.Errorf("节点 %q 不存在", id)
	}
	delete(r.hosts, id)
	delete(r.probes, id)
	if err := r.persistLocked(); err != nil {
		r.hosts[id] = old
		return fmt.Errorf("持久化失败: %w", err)
	}
	return nil
}

// SetRole changes a host's direction in place. This is the operation that
// lets any VPS become the outbound while another one becomes the inbound.
func (r *Registry) SetRole(id string, role Role) (Host, error) {
	h, ok := r.Get(id)
	if !ok {
		return Host{}, fmt.Errorf("节点 %q 不存在", id)
	}
	if !ValidRole(role) {
		return Host{}, fmt.Errorf("角色必须是 inbound / outbound / both")
	}
	h.Role = role
	return r.Update(id, h)
}

// Replace is an all-or-nothing JSON import.
func (r *Registry) Replace(list []Host) (int, error) {
	normalized := make([]Host, 0, len(list))
	seen := map[string]bool{}
	for i := range list {
		h := list[i]
		normalize(&h)
		if err := validate(h); err != nil {
			return 0, fmt.Errorf("第 %d 条: %w", i+1, err)
		}
		if seen[h.ID] {
			return 0, fmt.Errorf("重复 id %q", h.ID)
		}
		seen[h.ID] = true
		now := time.Now().UTC()
		if h.CreatedAt.IsZero() {
			h.CreatedAt = now
		}
		h.UpdatedAt = now
		normalized = append(normalized, h)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.hosts
	next := map[string]Host{}
	for _, h := range normalized {
		next[h.ID] = h
	}
	r.hosts = next
	r.probes = map[string]ProbeResult{}
	if err := r.persistLocked(); err != nil {
		r.hosts = old
		return 0, fmt.Errorf("持久化失败: %w", err)
	}
	return len(normalized), nil
}

func (r *Registry) ProbeOne(ctx context.Context, id string) (ProbeResult, error) {
	h, ok := r.Get(id)
	if !ok {
		return ProbeResult{}, fmt.Errorf("节点 %q 不存在", id)
	}
	res := ProbeHost(ctx, h)
	r.mu.Lock()
	r.probes[id] = res
	r.mu.Unlock()
	return res, nil
}

// ProbeAll dials every enabled host concurrently; disabled hosts are left
// unprobed and their cached result is cleared.
func (r *Registry) ProbeAll(ctx context.Context) map[string]ProbeResult {
	list := r.List("")
	results := map[string]ProbeResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, h := range list {
		if !h.Enabled {
			continue
		}
		wg.Add(1)
		go func(h Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := ProbeHost(ctx, h)
			mu.Lock()
			results[h.ID] = res
			mu.Unlock()
		}(h)
	}
	wg.Wait()
	r.mu.Lock()
	for id, res := range results {
		r.probes[id] = res
	}
	for _, h := range list {
		if !h.Enabled {
			delete(r.probes, h.ID)
		}
	}
	r.mu.Unlock()
	return results
}

func (r *Registry) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := Stats{Total: len(r.hosts)}
	for id, h := range r.hosts {
		if h.Enabled {
			s.Enabled++
		}
		switch h.Role {
		case RoleInbound:
			s.Inbound++
		case RoleOutbound:
			s.Outbound++
		case RoleBoth:
			s.Both++
		}
		if p, ok := r.probes[id]; ok {
			s.Checked++
			if p.Online {
				s.Online++
			}
		}
	}
	return s
}
