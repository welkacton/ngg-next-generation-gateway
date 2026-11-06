package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
)

// document is the on-disk shape. Keeping updated_at beside the config means
// GET /gateway/config survives a restart without inventing a timestamp.
type document struct {
	Config    Config    `json:"config"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store persists the gateway config to <data>/gateway-config.json.
//
// The WAN password lives in that file (mode 0600) and nowhere else: it is
// never logged, never returned by Get, and a save that carries an empty
// password keeps the stored secret.
type Store struct {
	mu        sync.Mutex
	path      string
	cfg       Config
	updatedAt time.Time
	loaded    bool
}

// NewStore points at <dir>/gateway-config.json and starts from Default().
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "gateway-config.json"), cfg: Default()}
}

func (s *Store) Path() string { return s.path }

// Load reads the persisted document. A missing file is not an error: the
// defaults stay in place and Loaded() keeps reporting false, so Status() can
// tell "never saved" from "saved".
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
	var doc document
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("gateway-config.json 解析失败: %w", err)
	}
	s.cfg = normalize(doc.Config)
	s.updatedAt = doc.UpdatedAt
	s.loaded = true
	return nil
}

// Get returns a copy of the in-memory config, secret included. Callers that
// answer an HTTP request must use Config.Public() instead.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.cfg)
}

// UpdatedAt is the time of the last successful save (zero if never saved).
func (s *Store) UpdatedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updatedAt
}

// Loaded reports whether a persisted document was read or written. It is the
// "this is a real, saved policy" signal behind firewall_active.
func (s *Store) Loaded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded
}

// Save validates, merges the password and atomically replaces the file.
//
// Ordering matters for the rollback promise: nothing is assigned to the
// in-memory state until the atomic write has succeeded, so a failed write
// leaves the previous config fully intact. A rejected config is never written
// at all.
func (s *Store) Save(c Config) (Config, []string, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c = normalize(c)
	if c.WAN.Password == "" {
		c.WAN.Password = s.cfg.WAN.Password
	}
	c.WAN.PasswordSet = c.WAN.Password != ""
	if err := validate(c); err != nil {
		return Config{}, nil, nil, err
	}

	changed := diffPaths(s.cfg, c)
	warnings := warningsFor(c)
	now := time.Now().UTC()
	doc := document{Config: c, UpdatedAt: now}
	if err := atomicfile.WriteJSON(s.path, doc); err != nil {
		return Config{}, nil, nil, fmt.Errorf("写入 %s 失败: %w", s.path, err)
	}
	s.cfg = c
	s.updatedAt = now
	s.loaded = true
	return clone(c), changed, warnings, nil
}

// warningsFor reports the honest limits of a declarative save: which parts of
// the request cannot take effect on this host, and why.
func warningsFor(c Config) []string {
	warnings := []string{
		"网关中心为声明式配置：保存只记录期望状态，NGG 不会改写本机网卡 / DHCP / 防火墙规则",
	}
	if c.WAN.Interface != "" && !interfaceExists(c.WAN.Interface) {
		warnings = append(warnings, fmt.Sprintf("WAN 接口 %q 不在本机网卡列表中，该设置保存后不会生效", c.WAN.Interface))
	}
	if c.LAN.Interface != "" && !interfaceExists(c.LAN.Interface) {
		warnings = append(warnings, fmt.Sprintf("LAN 接口 %q 不在本机网卡列表中，该设置保存后不会生效", c.LAN.Interface))
	}
	if c.WAN.Mode == "dhcp" && (c.WAN.Address != "" || c.WAN.Gateway != "") {
		warnings = append(warnings, "DHCP 模式下 address / gateway 由上游下发，此处填写的值会被忽略")
	}
	return warnings
}
