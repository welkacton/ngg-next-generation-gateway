package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
)

var (
	// ErrUnauthorized is returned when an agent token does not match a known
	// agent. Endpoint handlers translate it into 401 and never say why.
	ErrUnauthorized = errors.New("agent token 无效")
	// ErrNotFound is returned by ID lookups.
	ErrNotFound = errors.New("远程节点不存在")
	// ErrInvalid wraps every validation failure so handlers can answer 422.
	ErrInvalid = errors.New("远程节点参数无效")
)

var (
	agentIDRE  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	addrRE     = regexp.MustCompile(`^[a-zA-Z0-9.:_-]+$`)
	tokenHexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const agentsFileVersion = 1

type agentsFile struct {
	Version int     `json:"version"`
	Agents  []Agent `json:"agents"`
}

// Store is the agent registry. It is safe for concurrent use.
type Store struct {
	mu     sync.RWMutex
	path   string
	agents map[string]Agent
	// history is an in-memory ring per agent; it is intentionally never
	// written to disk (see maxHistory).
	history map[string][]AgentSample

	// now is injectable so tests can drive the online/offline boundary.
	now func() time.Time
}

// NewStore creates a registry rooted at dir (the NGG data directory).
func NewStore(dir string) *Store {
	return &Store{
		path:    filepath.Join(dir, "agents.json"),
		agents:  map[string]Agent{},
		history: map[string][]AgentSample{},
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Path is the persisted file location.
func (s *Store) Path() string { return s.path }

// Load reads agents.json. A missing file is not an error (fresh install).
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f agentsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	next := map[string]Agent{}
	for _, a := range f.Agents {
		normalizeAgent(&a)
		if err := validateAgent(a); err != nil {
			// Fail-closed: a corrupt record must not enter the registry with a
			// silently rewritten token or address.
			return fmt.Errorf("invalid persisted agent %q: %w", a.ID, err)
		}
		next[a.ID] = a
	}
	s.agents = next
	return nil
}

func normalizeAgent(a *Agent) {
	a.ID = strings.TrimSpace(a.ID)
	a.Name = strings.TrimSpace(a.Name)
	a.Address = strings.TrimSpace(a.Address)
	a.Note = strings.TrimSpace(a.Note)
	a.Role = strings.ToLower(strings.TrimSpace(a.Role))
	a.TokenSHA256 = strings.ToLower(strings.TrimSpace(a.TokenSHA256))
	a.OS = strings.TrimSpace(a.OS)
	a.Arch = strings.TrimSpace(a.Arch)
	a.Hostname = strings.TrimSpace(a.Hostname)
	a.AgentVersion = strings.TrimSpace(a.AgentVersion)
	if a.Role == "" {
		a.Role = RoleOutbound
	}
	if a.IntervalSec <= 0 {
		a.IntervalSec = DefaultIntervalSec
	}
}

func validateAgent(a Agent) error {
	if !agentIDRE.MatchString(a.ID) {
		return fmt.Errorf("%w: id 非法", ErrInvalid)
	}
	if a.Name == "" || len(a.Name) > 128 {
		return fmt.Errorf("%w: 名称必填且不超过 128 字符", ErrInvalid)
	}
	if a.Address == "" || !addrRE.MatchString(a.Address) || strings.Contains(a.Address, "://") {
		return fmt.Errorf("%w: 地址需为 IP 或域名（不含协议前缀）", ErrInvalid)
	}
	if a.Port < 1 || a.Port > 65535 {
		return fmt.Errorf("%w: 端口需在 1..65535", ErrInvalid)
	}
	if !ValidRole(a.Role) {
		return fmt.Errorf("%w: 角色必须是 outbound / inbound / both", ErrInvalid)
	}
	if !tokenHexRE.MatchString(a.TokenSHA256) {
		return fmt.Errorf("%w: token 摘要损坏", ErrInvalid)
	}
	return nil
}

func (s *Store) persistLocked() error {
	list := make([]Agent, 0, len(s.agents))
	for _, a := range s.agents {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	if err := atomicfile.WriteJSON(s.path, agentsFile{Version: agentsFileVersion, Agents: list}); err != nil {
		return err
	}
	return nil
}

// List returns every agent sorted by creation time.
func (s *Store) List() []AgentView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	list := make([]Agent, 0, len(s.agents))
	for _, a := range s.agents {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Created.Equal(list[j].Created) {
			return list[i].ID < list[j].ID
		}
		return list[i].Created.Before(list[j].Created)
	})
	out := make([]AgentView, 0, len(list))
	for i := range list {
		out = append(out, list[i].view(now))
	}
	return out
}

// Get returns one agent view.
func (s *Store) Get(id string) (AgentView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[id]
	if !ok {
		return AgentView{}, false
	}
	return a.view(s.now()), true
}

// Count returns the number of registered agents.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.agents)
}

// Create registers a new agent and returns its plaintext token exactly once.
func (s *Store) Create(name, address string, port int, note, role string) (AgentView, string, error) {
	token, err := newToken()
	if err != nil {
		return AgentView{}, "", err
	}
	now := s.now()
	a := Agent{
		ID:          newID(),
		Name:        name,
		Address:     address,
		Port:        port,
		Note:        note,
		Role:        role,
		TokenSHA256: hashToken(token),
		IntervalSec: DefaultIntervalSec,
		Created:     now,
		Updated:     now,
	}
	normalizeAgent(&a)
	if err := validateAgent(a); err != nil {
		return AgentView{}, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.agents[a.ID]; dup {
		// Astronomically unlikely with 48 random bits, but never overwrite.
		return AgentView{}, "", fmt.Errorf("%w: id 冲突，请重试", ErrInvalid)
	}
	s.agents[a.ID] = a
	if err := s.persistLocked(); err != nil {
		delete(s.agents, a.ID)
		return AgentView{}, "", fmt.Errorf("持久化失败: %w", err)
	}
	delete(s.history, a.ID)
	return a.view(s.now()), token, nil
}

// Patch is a partial update; nil fields are left untouched.
type Patch struct {
	Name    *string `json:"name"`
	Address *string `json:"address"`
	Port    *int    `json:"port"`
	Note    *string `json:"note"`
	Role    *string `json:"role"`
}

// Update applies a partial change and returns the new view.
func (s *Store) Update(id string, p Patch) (AgentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.agents[id]
	if !ok {
		return AgentView{}, ErrNotFound
	}
	a := old
	if p.Name != nil {
		a.Name = *p.Name
	}
	if p.Address != nil {
		a.Address = *p.Address
	}
	if p.Port != nil {
		a.Port = *p.Port
	}
	if p.Note != nil {
		a.Note = *p.Note
	}
	if p.Role != nil {
		a.Role = *p.Role
	}
	a.Updated = s.now()
	normalizeAgent(&a)
	if err := validateAgent(a); err != nil {
		return AgentView{}, err
	}
	s.agents[id] = a
	if err := s.persistLocked(); err != nil {
		s.agents[id] = old
		return AgentView{}, fmt.Errorf("持久化失败: %w", err)
	}
	return a.view(s.now()), nil
}

// Delete removes an agent and its in-memory sample history.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.agents[id]
	if !ok {
		return ErrNotFound
	}
	oldHist, hadHist := s.history[id]
	delete(s.agents, id)
	delete(s.history, id)
	if err := s.persistLocked(); err != nil {
		s.agents[id] = old
		if hadHist {
			s.history[id] = oldHist
		}
		return fmt.Errorf("持久化失败: %w", err)
	}
	return nil
}

// RotateToken issues a new token and invalidates the previous one.
func (s *Store) RotateToken(id string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.agents[id]
	if !ok {
		return "", ErrNotFound
	}
	a := old
	a.TokenSHA256 = hashToken(token)
	a.Updated = s.now()
	s.agents[id] = a
	if err := s.persistLocked(); err != nil {
		s.agents[id] = old
		return "", fmt.Errorf("持久化失败: %w", err)
	}
	return token, nil
}

// ReportPayload is the JSON body an agent POSTs to /v1/agent/report.
type ReportPayload struct {
	AgentID      string       `json:"agent_id"`
	Hostname     string       `json:"hostname"`
	OS           string       `json:"os"`
	Arch         string       `json:"arch"`
	AgentVersion string       `json:"agent_version"`
	IntervalSec  int          `json:"interval_sec"`
	Metrics      AgentMetrics `json:"metrics"`
}

// Report applies a metrics payload after verifying the agent token.
//
// The comparison is a constant-time compare of the SHA-256 digests, so a wrong
// token and an unknown agent are indistinguishable to the caller (both return
// ErrUnauthorized).
func (s *Store) Report(agentID, token string, m AgentMetrics) error {
	return s.ReportFull(ReportPayload{AgentID: agentID, Metrics: m}, token)
}

// ReportFull verifies the token and stores host identity + metrics.
func (s *Store) ReportFull(p ReportPayload, token string) error {
	if strings.TrimSpace(token) == "" {
		return ErrUnauthorized
	}
	digest := hashToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.agents[p.AgentID]
	if !ok {
		// Burn the same work as a real comparison so timing does not reveal
		// whether the agent id exists.
		subtle.ConstantTimeCompare([]byte(digest), []byte(strings.Repeat("0", 64)))
		return ErrUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(digest), []byte(old.TokenSHA256)) != 1 {
		return ErrUnauthorized
	}

	now := s.now()
	a := old
	a.LastSeen = now
	a.Updated = now
	a.Hostname = firstNonEmpty(p.Hostname, a.Hostname)
	a.OS = firstNonEmpty(p.OS, a.OS)
	a.Arch = firstNonEmpty(p.Arch, a.Arch)
	a.AgentVersion = firstNonEmpty(p.AgentVersion, a.AgentVersion)
	if p.IntervalSec > 0 && p.IntervalSec <= 3600 {
		a.IntervalSec = p.IntervalSec
	}
	m := p.Metrics
	if strings.TrimSpace(m.SampledAt) == "" {
		m.SampledAt = fmtTime(now)
	}
	if strings.TrimSpace(m.Source) == "" {
		m.Source = "agent"
	}
	if m.Disks == nil {
		m.Disks = []AgentDisk{}
	}
	if m.Interfaces == nil {
		m.Interfaces = []AgentInterface{}
	}
	a.Metrics = &m

	s.agents[p.AgentID] = a
	if err := s.persistLocked(); err != nil {
		s.agents[p.AgentID] = old
		return fmt.Errorf("持久化失败: %w", err)
	}
	s.appendSampleLocked(p.AgentID, sampleOf(&m))
	return nil
}

func (s *Store) appendSampleLocked(id string, sample AgentSample) {
	ring := append(s.history[id], sample)
	if len(ring) > maxHistory {
		ring = ring[len(ring)-maxHistory:]
	}
	s.history[id] = ring
}

// Samples returns up to limit history points, oldest first. The history lives
// only in memory and is empty after a restart.
func (s *Store) Samples(id string, limit int) []AgentSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ring := s.history[id]
	if limit <= 0 {
		limit = 60
	}
	if limit > maxHistory {
		limit = maxHistory
	}
	if len(ring) > limit {
		ring = ring[len(ring)-limit:]
	}
	out := make([]AgentSample, len(ring))
	copy(out, ring)
	return out
}

// HasHistory reports whether any sample was recorded for id (test helper).
func (s *Store) HasHistory(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.history[id]) > 0
}

// ------------------------------------------------------------- token helpers ---

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 token 失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a timestamp-derived id; collisions are checked above.
		return fmt.Sprintf("ag-%d", time.Now().UnixNano())
	}
	return "ag-" + hex.EncodeToString(b)
}

func firstNonEmpty(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return fallback
}
