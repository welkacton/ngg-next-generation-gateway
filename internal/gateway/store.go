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

	// A payload without a "master" object (the pre-v2.7 shape) must not wipe
	// the master switch: keep the stored one and let normalisation run over it.
	if !c.masterSet {
		c.Master = clone(s.cfg).Master
		c.masterSet = true
	}

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

// SaveMaster replaces only the master switch and writes the whole document
// through the one write path, so a master-only PUT can never drop the WAN /
// LAN / DNS / firewall policy that is already stored.
func (s *Store) SaveMaster(m Master) (Config, []string, []string, error) {
	cfg := s.Get()
	cfg.Master = m
	cfg.masterSet = true
	return s.Save(cfg)
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
	warnings = append(warnings, masterWarnings(c)...)
	return warnings
}

// masterWarnings reports the honest limits of the v2.7 master switch: nothing
// here is applied by a save, and the parts that can be applied need a
// privilege the daemon usually does not have.
func masterWarnings(c Config) []string {
	m := c.Master
	warnings := []string{}
	if m.Enabled {
		warnings = append(warnings,
			"数据面接管（虚拟网卡 + 默认路由）需要 Windows 管理员权限与 wintun.dll；未满足时 NGG 只生成命令，不会执行，也不会报告已生效")
	} else {
		warnings = append(warnings, "网关总控当前为关闭状态：保存只记录意图，NGG 不会创建虚拟网卡或改写默认路由")
	}
	if m.AdapterName != "" && !interfaceExists(m.AdapterName) {
		warnings = append(warnings, fmt.Sprintf("虚拟网卡 %q 不在本机网卡列表中，需要先创建 Wintun 适配器才能接管流量", m.AdapterName))
	}
	if len(m.Split.ProxyDomains) > 0 || len(m.Split.ProxyCIDRs) > 0 ||
		len(m.Split.LocalDomains) > 0 || len(m.Split.LocalCIDRs) > 0 {
		warnings = append(warnings, "分流规则需要单独写入规则引擎（POST /gateway/master/split-rules），且需重新加载内核后才会作用于真实流量")
	}
	switch m.Uplink.Mode {
	case "pipeline":
		if m.Uplink.PipelineID == "" {
			warnings = append(warnings, "master.uplink.mode=pipeline 但未指定 pipeline_id，将回落到默认链路")
		}
	case "gateway-wan":
		if m.Uplink.WANID != "" {
			found, enabled := false, false
			for _, w := range c.WANs {
				if w.ID == m.Uplink.WANID {
					found, enabled = true, w.Enabled
				}
			}
			if !found {
				warnings = append(warnings, fmt.Sprintf("master.uplink.wan_id=%s 不是已知的 WAN 口", m.Uplink.WANID))
			} else if !enabled {
				warnings = append(warnings, fmt.Sprintf("master.uplink.wan_id=%s 当前未启用", m.Uplink.WANID))
			}
		}
	}
	return warnings
}
