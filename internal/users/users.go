// Package users implements the local user directory shown under
// 配置引擎 → 用户管理.
//
// A user is a local record (account, quota, expiry, grouping). Status is
// DERIVED from real fields — disabled flag, expiry timestamp and quota — never
// from a hand-set label. Traffic usage is accumulated by the data plane through
// AddUsage; until an ingress is wired to a user it stays at its real value (0),
// and the UI says so instead of inventing numbers.
package users

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusExpired  Status = "expired"
	StatusLimited  Status = "limited"
	StatusDisabled Status = "disabled"
)

type ResetStrategy string

const (
	ResetNever   ResetStrategy = "never"
	ResetMonthly ResetStrategy = "monthly"
	ResetWeekly  ResetStrategy = "weekly"
)

func ValidReset(s ResetStrategy) bool {
	return s == ResetNever || s == ResetMonthly || s == ResetWeekly
}

// User is one local account. Timestamps use RFC3339 in JSON.
type User struct {
	ID            string        `json:"id"`
	Username      string        `json:"username"`
	Enabled       bool          `json:"enabled"`
	Email         string        `json:"email"`
	TelegramID    string        `json:"telegram_id"`
	Tag           string        `json:"tag"`
	Group         string        `json:"group"`
	Note          string        `json:"note"`
	LastNodeID    string        `json:"last_node_id"`
	LastNodeName  string        `json:"last_node_name"`
	LastSeenAt    time.Time     `json:"last_seen_at"`
	ExpiresAt     time.Time     `json:"expires_at"`
	DataLimit     uint64        `json:"data_limit"`
	UsedBytes     uint64        `json:"used_bytes"`
	ResetStrategy ResetStrategy `json:"reset_strategy"`
	HWIDDisabled  bool          `json:"hwid_disabled"`
	HWIDLimit     int           `json:"hwid_limit"`
	Subscription  string        `json:"subscription_url"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// Status is computed from the real record. The reference time is explicit so
// tests and API responses agree on one clock.
func (u User) Status(now time.Time) Status {
	if !u.Enabled {
		return StatusDisabled
	}
	if !u.ExpiresAt.IsZero() && !now.Before(u.ExpiresAt) {
		return StatusExpired
	}
	if u.DataLimit > 0 && u.UsedBytes >= u.DataLimit {
		return StatusLimited
	}
	return StatusActive
}

// Remaining is DataLimit - UsedBytes; 0 limit means unlimited.
func (u User) Remaining() uint64 {
	if u.DataLimit == 0 {
		return 0
	}
	if u.UsedBytes >= u.DataLimit {
		return 0
	}
	return u.DataLimit - u.UsedBytes
}

type Stats struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Expired  int `json:"expired"`
	Limited  int `json:"limited"`
	Disabled int `json:"disabled"`
}

type Registry struct {
	mu    sync.RWMutex
	users map[string]User
	path  string
}

func NewRegistry(path string) *Registry {
	return &Registry{users: map[string]User{}, path: path}
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
	var list []User
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("parse %s: %w", r.path, err)
	}
	m := map[string]User{}
	for _, u := range list {
		normalize(&u)
		if err := validate(u); err != nil {
			return fmt.Errorf("invalid persisted user %q: %w", u.ID, err)
		}
		m[u.ID] = u
	}
	r.users = m
	return nil
}

func (r *Registry) persistLocked() error {
	list := make([]User, 0, len(r.users))
	for _, u := range r.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Username < list[j].Username })
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

func normalize(u *User) {
	u.ID = strings.TrimSpace(u.ID)
	u.Username = strings.TrimSpace(u.Username)
	u.Email = strings.TrimSpace(u.Email)
	u.TelegramID = strings.TrimSpace(u.TelegramID)
	u.Tag = strings.TrimSpace(u.Tag)
	u.Group = strings.TrimSpace(u.Group)
	u.Note = strings.TrimSpace(u.Note)
	u.Subscription = strings.TrimSpace(u.Subscription)
	u.LastNodeID = strings.TrimSpace(u.LastNodeID)
	u.LastNodeName = strings.TrimSpace(u.LastNodeName)
	if u.ResetStrategy == "" {
		u.ResetStrategy = ResetNever
	}
	if u.HWIDLimit < 0 {
		u.HWIDLimit = 0
	}
	if u.ID == "" {
		u.ID = "usr_" + randomSuffix(6)
	}
}

func randomSuffix(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())[:n]
	}
	return hex.EncodeToString(b)[:n]
}

func validate(u User) error {
	if len(u.ID) > 64 {
		return fmt.Errorf("id 过长")
	}
	if u.Username == "" {
		return fmt.Errorf("用户名必填")
	}
	if len(u.Username) > 64 {
		return fmt.Errorf("用户名最长 64 字符")
	}
	if !ValidReset(u.ResetStrategy) {
		return fmt.Errorf("重置策略必须是 never / monthly / weekly")
	}
	if u.DataLimit > 1<<50 {
		return fmt.Errorf("流量上限过大")
	}
	return nil
}

func (r *Registry) List(status string) []User {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now().UTC()
	out := []User{}
	for _, u := range r.users {
		if status != "" && status != "all" && string(u.Status(now)) != status {
			continue
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (r *Registry) Get(id string) (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	return u, ok
}

func (r *Registry) Add(u User) (User, error) {
	normalize(&u)
	if err := validate(u); err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	u.CreatedAt, u.UpdatedAt = now, now
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.users {
		if strings.EqualFold(existing.Username, u.Username) {
			return User{}, fmt.Errorf("用户名 %q 已存在", u.Username)
		}
	}
	if _, exists := r.users[u.ID]; exists {
		return User{}, fmt.Errorf("用户 id %q 已存在", u.ID)
	}
	r.users[u.ID] = u
	if err := r.persistLocked(); err != nil {
		delete(r.users, u.ID)
		return User{}, fmt.Errorf("持久化失败: %w", err)
	}
	return u, nil
}

func (r *Registry) Update(id string, u User) (User, error) {
	u.ID = id
	normalize(&u)
	if err := validate(u); err != nil {
		return User{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.users[id]
	if !ok {
		return User{}, fmt.Errorf("用户 %q 不存在", id)
	}
	for otherID, existing := range r.users {
		if otherID != id && strings.EqualFold(existing.Username, u.Username) {
			return User{}, fmt.Errorf("用户名 %q 已存在", u.Username)
		}
	}
	u.CreatedAt = old.CreatedAt
	u.UpdatedAt = time.Now().UTC()
	r.users[id] = u
	if err := r.persistLocked(); err != nil {
		r.users[id] = old
		return User{}, fmt.Errorf("持久化失败: %w", err)
	}
	return u, nil
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.users[id]
	if !ok {
		return fmt.Errorf("用户 %q 不存在", id)
	}
	delete(r.users, id)
	if err := r.persistLocked(); err != nil {
		r.users[id] = old
		return fmt.Errorf("持久化失败: %w", err)
	}
	return nil
}

// AddUsage is the data-plane entry point. It is the ONLY way used_bytes grows;
// there is no REST endpoint that lets a caller invent traffic.
func (r *Registry) AddUsage(id string, bytes uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return fmt.Errorf("用户 %q 不存在", id)
	}
	u.UsedBytes += bytes
	u.UpdatedAt = time.Now().UTC()
	r.users[id] = u
	return r.persistLocked()
}

func (r *Registry) ResetUsage(id string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return User{}, fmt.Errorf("用户 %q 不存在", id)
	}
	u.UsedBytes = 0
	u.UpdatedAt = time.Now().UTC()
	r.users[id] = u
	if err := r.persistLocked(); err != nil {
		return User{}, fmt.Errorf("持久化失败: %w", err)
	}
	return u, nil
}

func (r *Registry) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now().UTC()
	s := Stats{Total: len(r.users)}
	for _, u := range r.users {
		switch u.Status(now) {
		case StatusActive:
			s.Active++
		case StatusExpired:
			s.Expired++
		case StatusLimited:
			s.Limited++
		case StatusDisabled:
			s.Disabled++
		}
	}
	return s
}
