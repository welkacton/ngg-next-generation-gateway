package clients

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
)

// credentialBytes is the raw entropy of a client secret. 24 bytes -> 32
// base64url characters, which is the format shown to the operator exactly once.
const credentialBytes = 24

// Store owns <dir>/clients.json.
//
// Write ordering is the rollback promise: the new document is written
// atomically first and the in-memory state is replaced only afterwards, so a
// failed write leaves the previous config (and every credential in it) intact.
type Store struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

// NewStore points at <dir>/clients.json and starts from the documented default.
func NewStore(dir string) *Store {
	return &Store{
		path: filepath.Join(dir, "clients.json"),
		cfg:  Config{Access: DefaultAccess()},
	}
}

// Path is the on-disk location, exposed for diagnostics and tests.
func (s *Store) Path() string { return s.path }

// Load reads the persisted document. A missing file is not an error: the
// defaults stay in place and the first save creates it.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 %s 失败: %w", s.path, err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("clients.json 解析失败: %w", err)
	}
	cfg.Access = normalizeAccess(cfg.Access)
	for i := range cfg.Clients {
		if cfg.Clients[i].Protocol == "" {
			cfg.Clients[i].Protocol = cfg.Access.Protocol
		}
	}
	if cfg.Clients == nil {
		cfg.Clients = []Client{}
	}
	s.cfg = cfg
	return nil
}

// Get returns a deep copy of the whole document, credentials included. The API
// layer must use List/Access instead so the secret never reaches a response.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneConfig(s.cfg)
}

// Access returns the shared listener config.
func (s *Store) Access() AccessConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Access
}

// List returns every client in creation order as a public view.
func (s *Store) List() []ClientView {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	out := make([]ClientView, 0, len(s.cfg.Clients))
	for _, c := range s.cfg.Clients {
		out = append(out, c.View(now))
	}
	return out
}

// GetClient returns the stored record of one client.
func (s *Store) GetClient(id string) (Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cfg.Clients {
		if c.ID == id {
			return c, true
		}
	}
	return Client{}, false
}

// CreateRequest is the POST /clients payload.
type CreateRequest struct {
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Protocol   string `json:"protocol"`
	ExpiresAt  string `json:"expires_at"`
	QuotaBytes uint64 `json:"quota_bytes"`
	Note       string `json:"note"`
}

// UpdateRequest is the PUT /clients/{id} payload. Every field is a pointer so
// "absent" and "set to zero" stay distinguishable.
type UpdateRequest struct {
	Name       *string `json:"name"`
	Platform   *string `json:"platform"`
	Protocol   *string `json:"protocol"`
	Enabled    *bool   `json:"enabled"`
	ExpiresAt  *string `json:"expires_at"`
	QuotaBytes *uint64 `json:"quota_bytes"`
	Note       *string `json:"note"`
}

// Create validates, mints a credential and persists the new client. The
// credential is returned here and here only.
func (s *Store) Create(req CreateRequest) (ClientView, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()

	name := strings.TrimSpace(req.Name)
	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if protocol == "" {
		protocol = s.cfg.Access.Protocol
	}
	expires := strings.TrimSpace(req.ExpiresAt)

	if err := validateName(name); err != nil {
		return ClientView{}, "", err
	}
	if err := validatePlatform(platform); err != nil {
		return ClientView{}, "", err
	}
	if err := validateProtocol(protocol); err != nil {
		return ClientView{}, "", err
	}
	if err := validateExpires(expires, now); err != nil {
		return ClientView{}, "", err
	}

	id, err := newClientID()
	if err != nil {
		return ClientView{}, "", err
	}
	cred, err := newCredential()
	if err != nil {
		return ClientView{}, "", err
	}
	c := Client{
		ID:         id,
		Name:       name,
		Platform:   platform,
		Protocol:   protocol,
		Enabled:    true,
		Credential: cred,
		Note:       strings.TrimSpace(req.Note),
		CreatedAt:  now.Format(time.RFC3339),
		ExpiresAt:  expires,
		QuotaBytes: req.QuotaBytes,
	}
	next := cloneConfig(s.cfg)
	next.Clients = append(next.Clients, c)
	if err := s.persistLocked(next); err != nil {
		return ClientView{}, "", err
	}
	return c.View(now), cred, nil
}

// Update patches the mutable fields of an existing client.
func (s *Store) Update(id string, req UpdateRequest) (ClientView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()

	idx := -1
	for i, c := range s.cfg.Clients {
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ClientView{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next := cloneConfig(s.cfg)
	c := next.Clients[idx]

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if err := validateName(name); err != nil {
			return ClientView{}, err
		}
		c.Name = name
	}
	if req.Platform != nil {
		p := strings.ToLower(strings.TrimSpace(*req.Platform))
		if err := validatePlatform(p); err != nil {
			return ClientView{}, err
		}
		c.Platform = p
	}
	if req.Protocol != nil {
		p := strings.ToLower(strings.TrimSpace(*req.Protocol))
		if err := validateProtocol(p); err != nil {
			return ClientView{}, err
		}
		c.Protocol = p
	}
	if req.ExpiresAt != nil {
		exp := strings.TrimSpace(*req.ExpiresAt)
		if exp != c.ExpiresAt {
			if err := validateExpires(exp, now); err != nil {
				return ClientView{}, err
			}
		}
		c.ExpiresAt = exp
	}
	if req.QuotaBytes != nil {
		c.QuotaBytes = *req.QuotaBytes
	}
	if req.Note != nil {
		n := strings.TrimSpace(*req.Note)
		if utf8len(n) > 200 {
			return ClientView{}, invalid("note", "不能超过 200 个字符")
		}
		c.Note = n
	}
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	next.Clients[idx] = c
	if err := s.persistLocked(next); err != nil {
		return ClientView{}, err
	}
	return c.View(now), nil
}

// Delete removes a client. The credential dies with it.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneConfig(s.cfg)
	out := make([]Client, 0, len(next.Clients))
	found := false
	for _, c := range next.Clients {
		if c.ID == id {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next.Clients = out
	return s.persistLocked(next)
}

// Rotate mints a new credential for an existing client. The old credential
// stops working as soon as the write succeeds.
func (s *Store) Rotate(id string) (ClientView, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()

	next := cloneConfig(s.cfg)
	idx := -1
	for i, c := range next.Clients {
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ClientView{}, "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	cred, err := newCredential()
	if err != nil {
		return ClientView{}, "", err
	}
	c := next.Clients[idx]
	c.Credential = cred
	c.LastSeen = ""
	next.Clients[idx] = c
	if err := s.persistLocked(next); err != nil {
		return ClientView{}, "", err
	}
	return c.View(now), cred, nil
}

// SaveAccess validates and persists the shared listener config, reporting the
// fields that actually changed.
func (s *Store) SaveAccess(a AccessConfig) (AccessConfig, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a = normalizeAccess(a)
	if err := validateAccess(a); err != nil {
		return AccessConfig{}, nil, err
	}
	prev := s.cfg.Access
	changed := diffAccess(prev, a)
	next := cloneConfig(s.cfg)
	next.Access = a
	if err := s.persistLocked(next); err != nil {
		return AccessConfig{}, nil, err
	}
	return a, changed, nil
}

// ConfigFor renders one of the four config formats for a client.
func (s *Store) ConfigFor(id, format string) (filename, content string, err error) {
	if !ValidFormat(format) {
		return "", "", fmt.Errorf("%w: %q", ErrUnknownFormat, format)
	}
	s.mu.Lock()
	var (
		c   Client
		acc AccessConfig
		ok  bool
	)
	for _, item := range s.cfg.Clients {
		if item.ID == id {
			c, ok = item, true
			break
		}
	}
	acc = s.cfg.Access
	s.mu.Unlock()
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return Render(c, acc, format)
}

// Subscription is what GET /sub/{token} returns.
type Subscription struct {
	ClientID string
	Filename string
	// ContentType is always application/json: the body is a sing-box profile.
	ContentType string
	Body        []byte
	// Headers carries subscription-userinfo and profile-update-interval.
	Headers map[string]string
}

// Subscription resolves a credential to a sing-box profile.
//
// Unknown, disabled and expired clients all return ok=false so the caller can
// answer 404 without letting a prober distinguish the three cases. A successful
// fetch is the only real "the device is alive" signal available today, so it is
// what Online is derived from.
func (s *Store) Subscription(token string) (Subscription, bool) {
	if token == "" {
		return Subscription{}, false
	}
	s.mu.Lock()
	now := time.Now().UTC()
	idx := -1
	for i, c := range s.cfg.Clients {
		if c.Credential != "" && c.Credential == token {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return Subscription{}, false
	}
	c := s.cfg.Clients[idx]
	if !c.Active(now) {
		s.mu.Unlock()
		return Subscription{}, false
	}
	acc := s.cfg.Access
	// Remember the fetch in memory only: it is a liveness hint, not policy, and
	// a subscription poll must not rewrite clients.json on every request.
	s.cfg.Clients[idx].LastSeen = now.Format(time.RFC3339)
	s.mu.Unlock()

	filename, content, err := Render(c, acc, FormatSingBox)
	if err != nil {
		return Subscription{}, false
	}
	return Subscription{
		ClientID:    c.ID,
		Filename:    filename,
		ContentType: "application/json; charset=utf-8",
		Body:        []byte(content),
		Headers: map[string]string{
			"subscription-userinfo":   fmt.Sprintf("upload=0; download=%d; total=%d", c.UsedBytes, c.QuotaBytes),
			"profile-update-interval": "24",
		},
	}, true
}

// TouchLastSeen records a client activity timestamp without rewriting the file.
func (s *Store) TouchLastSeen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.Clients {
		if s.cfg.Clients[i].ID == id {
			s.cfg.Clients[i].LastSeen = time.Now().UTC().Format(time.RFC3339)
			return true
		}
	}
	return false
}

// -------------------------------------------------------------- internals ---

func (s *Store) persistLocked(next Config) error {
	if err := atomicfile.WriteJSON(s.path, next); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", s.path, err)
	}
	s.cfg = next
	return nil
}

func cloneConfig(c Config) Config {
	out := Config{Access: c.Access}
	out.Clients = make([]Client, len(c.Clients))
	copy(out.Clients, c.Clients)
	return out
}

func diffAccess(prev, next AccessConfig) []string {
	changed := []string{}
	if prev.PublicHost != next.PublicHost {
		changed = append(changed, "public_host")
	}
	if prev.PublicPort != next.PublicPort {
		changed = append(changed, "public_port")
	}
	if prev.Protocol != next.Protocol {
		changed = append(changed, "protocol")
	}
	if prev.TLS != next.TLS {
		changed = append(changed, "tls")
	}
	if prev.Note != next.Note {
		changed = append(changed, "note")
	}
	return changed
}

func newCredential() (string, error) {
	buf := make([]byte, credentialBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成客户端凭据失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// newClientID mints "cl-" + 8 hex characters.
func newClientID() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成客户端 ID 失败: %w", err)
	}
	return "cl-" + hex.EncodeToString(buf), nil
}

func utf8len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
