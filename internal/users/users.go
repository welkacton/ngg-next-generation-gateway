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
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
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

// Subscription protocols accepted by the user→node binding. "" means "follow
// the protocol of the bound host", which is what every pre-subscription record
// implicitly did.
const (
	ProtocolVLESS       = "vless"
	ProtocolTrojan      = "trojan"
	ProtocolShadowsocks = "shadowsocks"
	ProtocolSOCKS5      = "socks5"
	ProtocolHTTP        = "http"
)

// DNS profiles decide what the rendered client config does about name
// resolution. "" is treated as system.
const (
	DNSProfileSystem  = "system"
	DNSProfileGateway = "gateway"
	DNSProfileOff     = "off"
)

// Errors returned by the subscription methods. The API maps them to 404 / 422
// so the caller never has to parse a message.
var (
	// ErrNotFound means the user id does not exist.
	ErrNotFound = errors.New("用户不存在")
	// ErrInvalidSubscription means protocol / dns_profile / node_id is not a
	// value this build can render.
	ErrInvalidSubscription = errors.New("订阅参数无效")
)

// NormalizeProtocol canonicalises a protocol name. Empty stays empty ("follow
// the node"), anything unknown is returned unchanged so callers can reject it.
func NormalizeProtocol(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", ProtocolVLESS:
		return strings.ToLower(strings.TrimSpace(p))
	case "ss", ProtocolShadowsocks:
		return ProtocolShadowsocks
	case "socks", "socks5h", ProtocolSOCKS5:
		return ProtocolSOCKS5
	case "https", ProtocolHTTP:
		return ProtocolHTTP
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

// NormalizeDNSProfile canonicalises a DNS profile; empty becomes system.
func NormalizeDNSProfile(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", DNSProfileSystem:
		return DNSProfileSystem
	case DNSProfileGateway, DNSProfileOff:
		return strings.ToLower(strings.TrimSpace(p))
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func ValidProtocol(p string) bool {
	switch p {
	case "", ProtocolVLESS, ProtocolTrojan, ProtocolShadowsocks, ProtocolSOCKS5, ProtocolHTTP:
		return true
	}
	return false
}

func ValidDNSProfile(p string) bool {
	switch p {
	case DNSProfileSystem, DNSProfileGateway, DNSProfileOff:
		return true
	}
	return false
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

	// Subscription binding: which host (node) the rendered client config
	// connects to, under which protocol, with which rule sets and DNS policy.
	// All zero values are valid for records written before v2.7.x.
	NodeID     string   `json:"node_id"`
	Protocol   string   `json:"protocol"`
	RuleSetIDs []string `json:"rule_set_ids"`
	DNSProfile string   `json:"dns_profile"`

	// SubscriptionToken is the opaque path segment of /sub/u/<token>. It is
	// never serialised with the user record: it lives in a sidecar file
	// (subscription-tokens.json) and only leaves the daemon inside the
	// subscription response.
	SubscriptionToken string    `json:"-"`
	SubscriptionAt    time.Time `json:"subscription_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	mu     sync.RWMutex
	users  map[string]User
	tokens map[string]string // user id -> subscription token (sidecar file)
	path   string
}

// tokenFile is the sidecar that holds subscription tokens. Keeping them out of
// users.json means a user record never carries its own secret in the file the
// API lists, and the on-disk shape of users.json stays backward compatible.
const tokenFile = "subscription-tokens.json"

func NewRegistry(path string) *Registry {
	return &Registry{
		users:  map[string]User{},
		tokens: map[string]string{},
		path:   path,
	}
}

func (r *Registry) tokenPath() string {
	return filepath.Join(filepath.Dir(r.path), tokenFile)
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
	// A missing token file is normal (records predate subscriptions). A corrupt
	// one fails the load like a corrupt users.json does: silently dropping
	// tokens would break every already-imported client link without a trace.
	return r.loadTokensLocked()
}

// loadTokensLocked reads the token sidecar and prunes entries whose user is
// gone, so /sub/u/<token> can never resolve to a deleted record.
func (r *Registry) loadTokensLocked() error {
	tokens := map[string]string{}
	b, err := os.ReadFile(r.tokenPath())
	if err != nil {
		if os.IsNotExist(err) {
			r.tokens = tokens
			return nil
		}
		return fmt.Errorf("读取 %s 失败: %w", r.tokenPath(), err)
	}
	if len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &tokens); err != nil {
			return fmt.Errorf("解析 %s 失败: %w", r.tokenPath(), err)
		}
	}
	for id, tok := range tokens {
		if _, ok := r.users[id]; !ok || strings.TrimSpace(tok) == "" {
			delete(tokens, id)
		}
	}
	r.tokens = tokens
	return nil
}

func (r *Registry) persistTokensLocked() error {
	if len(r.tokens) == 0 {
		// Remove the file rather than keeping an empty map around.
		if err := os.Remove(r.tokenPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicfile.WriteJSON(r.tokenPath(), r.tokens)
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
	u.NodeID = strings.TrimSpace(u.NodeID)
	u.Protocol = NormalizeProtocol(u.Protocol)
	u.DNSProfile = NormalizeDNSProfile(u.DNSProfile)
	u.RuleSetIDs = normalizeIDs(u.RuleSetIDs)
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

// normalizeIDs trims, de-duplicates and preserves order. A nil/empty slice stays
// nil so an old record does not grow a "rule_set_ids": [] key on rewrite.
func normalizeIDs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		u.SubscriptionToken = r.tokens[u.ID]
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (r *Registry) Get(id string) (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if ok {
		u.SubscriptionToken = r.tokens[id]
	}
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
	// Captured before normalize, which fills an empty DNS profile with the
	// "system" default and would erase the "unchanged" signal below.
	dnsGiven := strings.TrimSpace(u.DNSProfile) != ""
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
	// The generic PUT carries the whole record as the UI knows it, and the UI
	// does not edit the subscription binding (there is a dedicated endpoint for
	// that). An empty binding therefore means "unchanged", not "clear it".
	if u.NodeID == "" {
		u.NodeID = old.NodeID
	}
	if u.Protocol == "" {
		u.Protocol = old.Protocol
	}
	if len(u.RuleSetIDs) == 0 {
		u.RuleSetIDs = old.RuleSetIDs
	}
	if !dnsGiven {
		u.DNSProfile = old.DNSProfile
	}
	u.SubscriptionAt = old.SubscriptionAt
	r.users[id] = u
	if err := r.persistLocked(); err != nil {
		r.users[id] = old
		return User{}, fmt.Errorf("持久化失败: %w", err)
	}
	u.SubscriptionToken = r.tokens[id]
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
	oldToken, hadToken := r.tokens[id]
	delete(r.tokens, id)
	if err := r.persistLocked(); err != nil {
		r.users[id] = old
		if hadToken {
			r.tokens[id] = oldToken
		}
		return fmt.Errorf("持久化失败: %w", err)
	}
	if hadToken {
		if err := r.persistTokensLocked(); err != nil {
			// The user is gone; a leftover token cannot resolve to anything
			// because UserBySubscriptionToken also checks the user map. Report
			// the failure so the operator knows the file is stale.
			return fmt.Errorf("清理订阅 token 失败: %w", err)
		}
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

// ------------------------------------------------------------------ *
// Subscription binding
//
// A subscription is the user's binding of (node, protocol, rule sets, DNS).
// The token is the opaque handle a client polls; it lives in a sidecar file so
// users.json never carries it and a listing cannot leak it.
// ------------------------------------------------------------------ *

// SetSubscription stores the binding. nodeID may be cleared ("" = unbound);
// protocol "" means "use the node's own protocol"; dnsProfile "" means system.
func (r *Registry) SetSubscription(id, nodeID, protocol, dnsProfile string, ruleSetIDs []string) (User, error) {
	protocol = NormalizeProtocol(protocol)
	if !ValidProtocol(protocol) {
		return User{}, fmt.Errorf("%w: 协议必须是 vless / trojan / shadowsocks / socks5 / http", ErrInvalidSubscription)
	}
	dnsProfile = NormalizeDNSProfile(dnsProfile)
	if !ValidDNSProfile(dnsProfile) {
		return User{}, fmt.Errorf("%w: DNS 策略必须是 system / gateway / off", ErrInvalidSubscription)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.users[id]
	if !ok {
		return User{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	next := old
	next.NodeID = strings.TrimSpace(nodeID)
	next.Protocol = protocol
	next.DNSProfile = dnsProfile
	next.RuleSetIDs = normalizeIDs(ruleSetIDs)
	next.UpdatedAt = time.Now().UTC()
	r.users[id] = next
	if err := r.persistLocked(); err != nil {
		r.users[id] = old
		return User{}, fmt.Errorf("持久化失败: %w", err)
	}
	next.SubscriptionToken = r.tokens[id]
	return next, nil
}

// EnsureSubscriptionToken returns the user's token, minting one on first use.
func (r *Registry) EnsureSubscriptionToken(id string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return User{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if tok := r.tokens[id]; tok != "" {
		u.SubscriptionToken = tok
		return u, nil
	}
	tok, err := newSubscriptionToken()
	if err != nil {
		return User{}, err
	}
	return r.setTokenLocked(u, tok)
}

// RotateSubscriptionToken replaces the token, invalidating every previously
// shared link immediately.
func (r *Registry) RotateSubscriptionToken(id string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return User{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	tok, err := newSubscriptionToken()
	if err != nil {
		return User{}, err
	}
	return r.setTokenLocked(u, tok)
}

// setTokenLocked writes the token sidecar and the user record, rolling both
// back if either write fails. The caller must hold r.mu.
func (r *Registry) setTokenLocked(u User, tok string) (User, error) {
	prevToken, hadToken := r.tokens[u.ID]
	prevAt := u.SubscriptionAt

	r.tokens[u.ID] = tok
	u.SubscriptionAt = time.Now().UTC()
	r.users[u.ID] = u

	restore := func() {
		if hadToken {
			r.tokens[u.ID] = prevToken
		} else {
			delete(r.tokens, u.ID)
		}
		u.SubscriptionAt = prevAt
		u.SubscriptionToken = ""
		r.users[u.ID] = u
	}

	if err := r.persistTokensLocked(); err != nil {
		restore()
		return User{}, fmt.Errorf("持久化订阅 token 失败: %w", err)
	}
	if err := r.persistLocked(); err != nil {
		restore()
		_ = r.persistTokensLocked() // best effort: keep the file in step
		return User{}, fmt.Errorf("持久化失败: %w", err)
	}
	u.SubscriptionToken = tok
	return u, nil
}

// UserBySubscriptionToken resolves a token to its user. A disabled or expired
// user is still returned: the renderer decides whether the subscription is
// usable, and this method must not be used to answer "does this token exist".
func (r *Registry) UserBySubscriptionToken(token string) (User, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return User{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var found string
	for id, tok := range r.tokens {
		if len(tok) == len(token) && subtle.ConstantTimeCompare([]byte(tok), []byte(token)) == 1 {
			found = id
			break
		}
	}
	if found == "" {
		return User{}, false
	}
	u, ok := r.users[found]
	if !ok {
		return User{}, false
	}
	u.SubscriptionToken = token
	return u, true
}

// SubscriptionUsable is the single definition of "this token may download a
// config": the account must be enabled and not past its expiry.
func (u User) SubscriptionUsable(now time.Time) bool {
	if !u.Enabled {
		return false
	}
	if !u.ExpiresAt.IsZero() && !now.Before(u.ExpiresAt) {
		return false
	}
	return true
}

// newSubscriptionToken mints 32 random bytes as unpadded base64url (43 chars).
func newSubscriptionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成订阅 token 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
