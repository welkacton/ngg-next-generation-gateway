package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// The per-port WAN (PPPoE) passwords live in that file (mode 0600) and nowhere
// else: they are never logged, never returned by Get, and a save that carries
// an empty password keeps the stored secret of that port.
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

// Save validates, merges the passwords and atomically replaces the file.
//
// Ordering matters for the rollback promise: nothing is assigned to the
// in-memory state until the atomic write has succeeded, so a failed write
// leaves the previous config fully intact. A rejected config is never written
// at all.
func (s *Store) Save(c Config) (Config, []string, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c = normalize(c)
	mergePasswords(&c, s.cfg)
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

// mergePasswords keeps the stored secret of every port whose incoming password
// is empty, and always re-syncs WAN with WANs[0]. Each port has its own
// secret: rotating wan3 must not disturb wan1.
func mergePasswords(c *Config, prev Config) {
	stored := make(map[string]string, len(prev.WANs))
	for _, w := range prev.WANs {
		if w.Password != "" {
			stored[w.ID] = w.Password
		}
	}
	for i := range c.WANs {
		if c.WANs[i].Password == "" {
			c.WANs[i].Password = stored[c.WANs[i].ID]
		}
		c.WANs[i].PasswordSet = c.WANs[i].Password != ""
	}
	if len(c.WANs) > 0 {
		c.WAN = c.WANs[0]
	}
}

// warningsFor reports the honest limits of a declarative save: which parts of
// the request cannot take effect on this host, and why.
func warningsFor(c Config) []string {
	warnings := []string{
		"网关中心为声明式配置：保存只记录期望状态，NGG 不会改写本机网卡 / DHCP / 防火墙规则",
	}
	if len(c.WANs) > 1 {
		warnings = append(warnings, "多 WAN 目前只是声明式配置：NGG 尚未实现按 WAN 的策略路由 / 负载均衡 / 故障切换，实际出口仍由本机路由表决定")
	}
	proxyRouted := []string{}
	for _, w := range c.WANs {
		if !w.Enabled {
			continue
		}
		if w.Interface != "" && !interfaceExists(w.Interface) {
			warnings = append(warnings, fmt.Sprintf("WAN %s（%s）的接口 %q 不在本机网卡列表中，该设置保存后不会生效", w.Name, w.ID, w.Interface))
		}
		if w.Mode == "dhcp" && (w.Address != "" || w.Gateway != "") {
			warnings = append(warnings, fmt.Sprintf("WAN %s（%s）为 DHCP 模式，address / gateway 由上游下发，此处填写的值会被忽略", w.Name, w.ID))
		}
		if w.Route == "proxy" {
			proxyRouted = append(proxyRouted, w.ID)
		}
	}
	if len(proxyRouted) > 0 {
		warnings = append(warnings, fmt.Sprintf("WAN %s 标记为 route=proxy，本版仅记录意图：NGG 不会真的把这些出口交给国际代理引擎分流", strings.Join(proxyRouted, ", ")))
	}
	if c.DNS.Hosts != nil || len(c.DNS.Servers) > 0 || len(c.DNS.Blocklist) > 0 {
		warnings = append(warnings, "DNS 段同样为声明式配置：NGG 不会启动转发 / 递归解析器、写 hosts 或下发黑名单")
	}
	if c.LAN.Interface != "" && !interfaceExists(c.LAN.Interface) {
		warnings = append(warnings, fmt.Sprintf("LAN 接口 %q 不在本机网卡列表中，该设置保存后不会生效", c.LAN.Interface))
	}
	return warnings
}
