// Package auth implements the single-administrator login used by the NGG web
// console.
//
// The first browser that reaches a fresh installation gets a SETUP screen and
// must create the administrator account by hand; from then on the console only
// ever shows a login form. Credentials are hashed with Argon2id, sessions are
// random 32-byte tokens stored (SHA-256 hashed) on disk so a service restart
// does not log the operator out, and repeated failures are rate limited.
//
// The legacy static API token in ngg.yaml keeps working for scripts and the
// CLI: it is an API credential, not a browser session.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var (
	ErrSetupRequired     = errors.New("administrator account has not been created yet")
	ErrAlreadySetup      = errors.New("administrator account already exists")
	ErrInvalidCredential = errors.New("用户名或密码不正确")
	ErrLocked            = errors.New("连续登录失败次数过多，请稍后再试")
	ErrWeakPassword      = errors.New("密码至少 8 位，且不能全部是同一个字符")
	ErrInvalidUsername   = errors.New("用户名需为 3-32 位字母、数字、点、下划线或连字符")
	ErrNoSession         = errors.New("未登录或会话已过期")
)

const (
	// SessionTTL is how long a normal login stays valid.
	SessionTTL = 7 * 24 * time.Hour
	// RememberTTL is used when the operator ticks "记住我".
	RememberTTL = 30 * 24 * time.Hour

	maxFailures = 5
	lockWindow  = 5 * time.Minute
	lockFor     = 60 * time.Second

	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

// Account is the single administrator record.
type Account struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type session struct {
	TokenHash string    `json:"token_hash"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UserAgent string    `json:"user_agent,omitempty"`
}

type fileFormat struct {
	Version  int       `json:"version"`
	Admin    *Account  `json:"admin,omitempty"`
	Sessions []session `json:"sessions,omitempty"`
}

type failure struct {
	count int
	last  time.Time
	until time.Time
}

// Store owns the credential file and the in-memory lockout state.
type Store struct {
	mu    sync.Mutex
	path  string
	data  fileFormat
	fails map[string]*failure
}

// NewStore creates a store rooted at dir (normally the NGG data directory).
func NewStore(dir string) *Store {
	return &Store{
		path:  filepath.Join(dir, "auth.json"),
		data:  fileFormat{Version: 1},
		fails: map[string]*failure{},
	}
}

// NewStoreAt is used by tests to point at an exact file.
func NewStoreAt(path string) *Store {
	return &Store{path: path, data: fileFormat{Version: 1}, fails: map[string]*failure{}}
}

func (s *Store) Path() string { return s.path }

// Load reads the credential file. A missing file simply means "brand new
// installation": SetupRequired reports true and no error is returned.
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
	var f fileFormat
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("auth.json 解析失败: %w", err)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	s.data = f
	s.pruneLocked()
	return nil
}

// SetupRequired is true until the administrator account exists.
func (s *Store) SetupRequired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Admin == nil
}

// Username returns the configured administrator name ("" before setup).
func (s *Store) Username() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Admin == nil {
		return ""
	}
	return s.data.Admin.Username
}

// Setup creates the administrator. It can only ever succeed once.
func (s *Store) Setup(username, password string) error {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return ErrInvalidUsername
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Admin != nil {
		return ErrAlreadySetup
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	s.data.Admin = &Account{Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now}
	return s.persistLocked()
}

// Session is what a successful login hands back to the HTTP layer.
type Session struct {
	Token     string
	Username  string
	ExpiresAt time.Time
}

// Login verifies the credentials and mints a session token.
func (s *Store) Login(username, password, userAgent string, remember bool) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Admin == nil {
		return Session{}, ErrSetupRequired
	}
	key := strings.ToLower(strings.TrimSpace(username))
	now := time.Now()
	if f := s.fails[key]; f != nil && now.Before(f.until) {
		return Session{}, fmt.Errorf("%w（还需等待 %d 秒）", ErrLocked, int(f.until.Sub(now).Seconds())+1)
	}
	if !strings.EqualFold(strings.TrimSpace(username), s.data.Admin.Username) {
		s.recordFailureLocked(key, now)
		return Session{}, ErrInvalidCredential
	}
	if !verifyPassword(s.data.Admin.PasswordHash, password) {
		s.recordFailureLocked(key, now)
		return Session{}, ErrInvalidCredential
	}
	delete(s.fails, key)

	ttl := SessionTTL
	if remember {
		ttl = RememberTTL
	}
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	sess := session{
		TokenHash: hashToken(token),
		Username:  s.data.Admin.Username,
		CreatedAt: now.UTC(),
		ExpiresAt: now.Add(ttl).UTC(),
		UserAgent: truncate(userAgent, 200),
	}
	s.pruneLocked()
	s.data.Sessions = append(s.data.Sessions, sess)
	if err := s.persistLocked(); err != nil {
		return Session{}, err
	}
	return Session{Token: token, Username: sess.Username, ExpiresAt: sess.ExpiresAt}, nil
}

// Lookup validates a session token.
func (s *Store) Lookup(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	changed := false
	for i := range s.data.Sessions {
		sess := &s.data.Sessions[i]
		if subtle.ConstantTimeCompare([]byte(sess.TokenHash), []byte(h)) != 1 {
			continue
		}
		if now.After(sess.ExpiresAt) {
			s.data.Sessions = append(s.data.Sessions[:i], s.data.Sessions[i+1:]...)
			changed = true
			break
		}
		username := sess.Username
		if changed {
			_ = s.persistLocked()
		}
		return username, true
	}
	if changed {
		_ = s.persistLocked()
	}
	return "", false
}

// Logout revokes a single session.
func (s *Store) Logout(token string) {
	if token == "" {
		return
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Sessions {
		if subtle.ConstantTimeCompare([]byte(s.data.Sessions[i].TokenHash), []byte(h)) == 1 {
			s.data.Sessions = append(s.data.Sessions[:i], s.data.Sessions[i+1:]...)
			_ = s.persistLocked()
			return
		}
	}
}

// ChangePassword rotates the administrator password and drops every existing
// session, so a stolen cookie stops working immediately.
func (s *Store) ChangePassword(oldPassword, newPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Admin == nil {
		return ErrSetupRequired
	}
	if !verifyPassword(s.data.Admin.PasswordHash, oldPassword) {
		return ErrInvalidCredential
	}
	if err := checkPassword(newPassword); err != nil {
		return err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	s.data.Admin.PasswordHash = hash
	s.data.Admin.UpdatedAt = time.Now().UTC()
	s.data.Sessions = nil
	return s.persistLocked()
}

// Reset wipes the administrator and every session. Only used by an explicit
// operator action on the host (never exposed over HTTP).
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = fileFormat{Version: 1}
	s.fails = map[string]*failure{}
	return s.persistLocked()
}

func (s *Store) recordFailureLocked(key string, now time.Time) {
	f := s.fails[key]
	if f == nil || now.Sub(f.last) > lockWindow {
		f = &failure{}
		s.fails[key] = f
	}
	f.count++
	f.last = now
	if f.count >= maxFailures {
		f.until = now.Add(lockFor)
		f.count = 0
	}
}

func (s *Store) pruneLocked() {
	now := time.Now()
	kept := s.data.Sessions[:0]
	for _, sess := range s.data.Sessions {
		if now.Before(sess.ExpiresAt) {
			kept = append(kept, sess)
		}
	}
	s.data.Sessions = kept
}

func (s *Store) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func checkPassword(pw string) error {
	if utf8.RuneCountInString(pw) < 8 {
		return ErrWeakPassword
	}
	allSame := true
	for i := 1; i < len(pw); i++ {
		if pw[i] != pw[0] {
			allSame = false
			break
		}
	}
	if allSame {
		return ErrWeakPassword
	}
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte("ngg-session:" + token))
	return hex.EncodeToString(sum[:])
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
