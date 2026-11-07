// Package hostmon collects REAL host metrics (CPU / memory / disk / traffic)
// from remote Linux machines over SSH, using read-only commands only. It never
// installs an agent and never writes anything on the monitored server.
//
// Credentials (password / private key) are confined to the persisted config
// file. They are never logged, never embedded in error strings, and never
// returned to callers: ConfigView and Metrics are deliberately credential-free.
package hostmon

import (
	"context"
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

// SSHConfig is one monitored host. A HostID binds the entry to a specific
// node; when HostID is empty the entry is addressed by its Host (IP address),
// which is the "match any node by IP" mode used by the console.
type SSHConfig struct {
	HostID             string `json:"host_id"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	User               string `json:"user"`
	Password           string `json:"password,omitempty"`
	PrivateKey         string `json:"private_key,omitempty"`
	HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
}

// ConfigView is the credential-free projection of an SSHConfig that is safe to
// serialise to the frontend.
type ConfigView struct {
	HostID             string `json:"host_id"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	User               string `json:"user"`
	HasPassword        bool   `json:"has_password"`
	HasPrivateKey      bool   `json:"has_private_key"`
	HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
}

// Metrics is the last sampled state of one host. Rates are bytes/second and are
// derived from the delta between two real samples; the first successful sample
// reports a rate of 0 instead of a fabricated number.
type Metrics struct {
	HostID      string  `json:"host_id"`
	CollectedAt string  `json:"collected_at"` // RFC3339, UTC
	Online      bool    `json:"online"`
	Error       string  `json:"error,omitempty"`
	Hostname    string  `json:"hostname,omitempty"`
	OS          string  `json:"os,omitempty"`
	Kernel      string  `json:"kernel,omitempty"`
	UptimeSec   int64   `json:"uptime_sec"`
	CPUCores    int     `json:"cpu_cores"`
	CPUUsagePct float64 `json:"cpu_usage_pct"`
	MemTotal    uint64  `json:"mem_total"`
	MemUsed     uint64  `json:"mem_used"`
	DiskTotal   uint64  `json:"disk_total"`
	DiskUsed    uint64  `json:"disk_used"`
	NetRxBytes  uint64  `json:"net_rx_bytes"`
	NetTxBytes  uint64  `json:"net_tx_bytes"`
	NetRxRate   float64 `json:"net_rx_rate"` // bytes per second
	NetTxRate   float64 `json:"net_tx_rate"` // bytes per second
}

var (
	// ErrNotFound is returned when no config resolves for the requested host.
	ErrNotFound = errors.New("未找到该节点的监控配置")
)

type configFile struct {
	Entries []SSHConfig `json:"entries"`
}

type netSample struct {
	rx uint64
	tx uint64
	t  time.Time
}

// Monitor owns the SSH config registry, the latest metrics and the polling
// loop. It is safe for concurrent use.
type Monitor struct {
	mu      sync.RWMutex // guards path, configs, metrics, prev
	path    string
	configs map[string]SSHConfig
	metrics map[string]Metrics
	prev    map[string]netSample

	// collectFn is the collection implementation; tests replace it with a
	// deterministic fake so no unit test needs a network.
	collectFn collectFunc
	// collectMu serialises ALL collections: two collections never overlap, a
	// Start round is strictly serial, and Set/Refresh never race Start.
	collectMu sync.Mutex

	runMu   sync.Mutex // guards running / cancel / stopCh / wg
	running bool
	cancel  context.CancelFunc
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// New builds a Monitor rooted at dataDir. The config file lives at
// filepath.Join(dataDir, "host-monitor.json").
func New(dataDir string) *Monitor {
	m := &Monitor{
		path:    filepath.Join(dataDir, "host-monitor.json"),
		configs: map[string]SSHConfig{},
		metrics: map[string]Metrics{},
		prev:    map[string]netSample{},
	}
	m.collectFn = realCollect
	return m
}

// Load reads host-monitor.json. A missing file is not an error (fresh install).
func (m *Monitor) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f configFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", m.path, err)
	}
	next := map[string]SSHConfig{}
	for _, c := range f.Entries {
		normalize(&c)
		if err := validate(c); err != nil {
			// Fail-closed: a corrupt record must not silently enter the registry.
			return fmt.Errorf("配置条目 %q 无效: %w", c.Host, err)
		}
		next[configKey(c)] = c
	}
	m.configs = next
	return nil
}

func normalize(c *SSHConfig) {
	c.HostID = strings.TrimSpace(c.HostID)
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	c.HostKeyFingerprint = strings.TrimSpace(c.HostKeyFingerprint)
	// Password and PrivateKey are credentials and must stay byte-for-byte
	// identical; never trim or rewrite them.
	if c.Port == 0 {
		c.Port = 22
	}
}

func validate(c SSHConfig) error {
	if c.Host == "" || strings.ContainsAny(c.Host, " \t/") {
		return errors.New("host 需为 IP 或域名（不含协议前缀与空格）")
	}
	if c.User == "" {
		return errors.New("user 不能为空")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port 需在 1..65535")
	}
	if c.Password == "" && c.PrivateKey == "" {
		return errors.New("需提供密码或私钥")
	}
	return nil
}

// configKey is the registry/metrics key. A non-empty HostID is used verbatim;
// an IP-matched entry (HostID == "") is keyed by its Host.
func configKey(c SSHConfig) string {
	if c.HostID != "" {
		return c.HostID
	}
	return c.Host
}

// resolveLocked maps a caller-supplied identifier to a config key. Order:
//  1. exact HostID match,
//  2. Host (IP) match,
//  3. a single "match any" entry (HostID == "") applies to every identifier.
//
// The caller must hold m.mu (read or write).
func (m *Monitor) resolveLocked(id string) (string, SSHConfig, bool) {
	if id != "" {
		if c, ok := m.configs[id]; ok {
			return id, c, true
		}
	}
	keys := make([]string, 0, len(m.configs))
	for k := range m.configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if id != "" {
		for _, k := range keys {
			if m.configs[k].Host == id {
				return k, m.configs[k], true
			}
		}
	}
	var anyKey string
	count := 0
	for _, k := range keys {
		if m.configs[k].HostID == "" {
			anyKey, count = k, count+1
		}
	}
	if count == 1 {
		return anyKey, m.configs[anyKey], true
	}
	return "", SSHConfig{}, false
}

func (m *Monitor) persistLocked() error {
	list := make([]SSHConfig, 0, len(m.configs))
	for _, c := range m.configs {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Host < list[j].Host })
	b, err := json.MarshalIndent(configFile{Entries: list}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(m.path, b, 0o600)
}

func viewOf(c SSHConfig) ConfigView {
	return ConfigView{
		HostID:             c.HostID,
		Host:               c.Host,
		Port:               c.Port,
		User:               c.User,
		HasPassword:        c.Password != "",
		HasPrivateKey:      c.PrivateKey != "",
		HostKeyFingerprint: c.HostKeyFingerprint,
	}
}

// Configs returns a credential-free, Host-sorted copy of the registry.
func (m *Monitor) Configs() []ConfigView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]ConfigView, 0, len(m.configs))
	for _, c := range m.configs {
		list = append(list, viewOf(c))
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Host == list[j].Host {
			return list[i].HostID < list[j].HostID
		}
		return list[i].Host < list[j].Host
	})
	return list
}

// Metrics returns the last sample for hostID and whether it exists.
func (m *Monitor) Metrics(hostID string) (Metrics, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key, _, ok := m.resolveLocked(hostID)
	if !ok {
		return Metrics{}, false
	}
	met, ok := m.metrics[key]
	return met, ok
}

// All returns a copy of every collected sample keyed by configKey.
func (m *Monitor) All() map[string]Metrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Metrics, len(m.metrics))
	for k, v := range m.metrics {
		out[k] = v
	}
	return out
}

// Set normalises, validates and persists cfg, then performs one synchronous
// collection. A collection failure is recorded in Metrics but never rolls back
// the saved config, so a bad password can never prevent the config from being
// stored.
func (m *Monitor) Set(cfg SSHConfig) error {
	normalize(&cfg)
	key := configKey(cfg)

	m.mu.Lock()
	// The console submits empty credentials when the operator does not want to
	// change them. Recover the stored ones instead of rejecting the edit. A
	// preset entry keyed by IP is also a valid source when the same machine is
	// promoted to a named node (HostID goes from "" to the node id).
	if cfg.Password == "" && cfg.PrivateKey == "" {
		if prev, ok := m.configs[key]; ok {
			cfg.Password, cfg.PrivateKey = prev.Password, prev.PrivateKey
		} else {
			for _, prev := range m.configs {
				if prev.HostID == "" && prev.Host == cfg.Host {
					cfg.Password, cfg.PrivateKey = prev.Password, prev.PrivateKey
					break
				}
			}
		}
	}
	if err := validate(cfg); err != nil {
		m.mu.Unlock()
		return err
	}
	// Dropping the superseded IP-keyed entry prevents two configs (and two SSH
	// connections per round) for the very same machine. Carry its TOFU
	// fingerprint over so the operator is not asked to trust the host again.
	for k, prev := range m.configs {
		if k == key || prev.HostID != "" || prev.Host != cfg.Host {
			continue
		}
		if cfg.HostKeyFingerprint == "" {
			cfg.HostKeyFingerprint = prev.HostKeyFingerprint
		}
		delete(m.configs, k)
		delete(m.metrics, k)
		delete(m.prev, k)
	}

	old, had := m.configs[key]
	if had && cfg.HostKeyFingerprint == "" && old.HostKeyFingerprint != "" {
		// Never downgrade a stored TOFU fingerprint back to "trust on first
		// use": callers that do not round-trip the fingerprint must not silently
		// disable man-in-the-middle protection.
		cfg.HostKeyFingerprint = old.HostKeyFingerprint
	}
	m.configs[key] = cfg
	if err := m.persistLocked(); err != nil {
		if had {
			m.configs[key] = old
		} else {
			delete(m.configs, key)
		}
		m.mu.Unlock()
		return fmt.Errorf("持久化监控配置失败: %w", err)
	}
	m.mu.Unlock()

	m.collectAndStore(context.Background(), key)
	return nil
}

// Remove deletes a config. Resolution is a deterministic two-pass lookup:
//  1. remove an entry whose HostID equals key, if any;
//  2. otherwise remove an entry whose Host (address) equals key.
//
// A preset entry (HostID == "") is therefore deleted by its Host address, not
// by an arbitrary node id. DELETE is idempotent: when nothing matches, Remove
// returns nil. Only a persistence failure produces an error.
func (m *Monitor) Remove(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys := make([]string, 0, len(m.configs))
	for k := range m.configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	target := ""
	// An empty key is not a real identifier: it must not match the empty
	// HostID of a preset entry and delete an unrelated config.
	if key != "" {
		for _, k := range keys {
			if m.configs[k].HostID == key {
				target = k
				break
			}
		}
	}
	if target == "" && key != "" {
		for _, k := range keys {
			if m.configs[k].Host == key {
				target = k
				break
			}
		}
	}
	if target == "" {
		return nil
	}

	old := m.configs[target]
	oldMetrics, hadMetrics := m.metrics[target]
	oldPrev, hadPrev := m.prev[target]
	delete(m.configs, target)
	delete(m.metrics, target)
	delete(m.prev, target)
	if err := m.persistLocked(); err != nil {
		m.configs[target] = old
		if hadMetrics {
			m.metrics[target] = oldMetrics
		}
		if hadPrev {
			m.prev[target] = oldPrev
		}
		return fmt.Errorf("持久化监控配置失败: %w", err)
	}
	return nil
}

// Refresh performs one collection immediately. It returns ErrNotFound only
// when no config matches; collection failures are reported inside the returned
// Metrics (Online=false, Error set) rather than as a Go error.
func (m *Monitor) Refresh(hostID string) (Metrics, error) {
	m.mu.RLock()
	key, _, ok := m.resolveLocked(hostID)
	m.mu.RUnlock()
	if !ok {
		return Metrics{}, ErrNotFound
	}
	return m.collectAndStore(context.Background(), key), nil
}

// collectAndStore runs one collection for key and updates all derived state
// (metrics, traffic rates, TOFU fingerprint). Collections are serialised by
// collectMu; state mutations happen under m.mu.
func (m *Monitor) collectAndStore(parent context.Context, key string) Metrics {
	m.mu.RLock()
	cfg, ok := m.configs[key]
	m.mu.RUnlock()
	if !ok {
		return Metrics{
			HostID:      key,
			CollectedAt: time.Now().UTC().Format(time.RFC3339),
			Online:      false,
			Error:       "监控配置已移除",
		}
	}

	m.collectMu.Lock()
	defer m.collectMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, collectBudget)
	defer cancel()
	s, fp, err := m.collectFn(ctx, cfg)
	now := time.Now().UTC()

	met := Metrics{
		HostID:      key,
		CollectedAt: now.Format(time.RFC3339),
		Online:      err == nil,
	}
	if err != nil {
		met.Error = safeError(err, cfg)
	} else {
		met.Hostname = s.hostname
		met.OS = s.os
		met.Kernel = s.kernel
		met.UptimeSec = s.uptimeSec
		met.CPUCores = s.cpuCores
		met.CPUUsagePct = s.cpuPct
		met.MemTotal = s.memTotal
		met.MemUsed = s.memUsed
		met.DiskTotal = s.diskTotal
		met.DiskUsed = s.diskUsed
		met.NetRxBytes = s.netRx
		met.NetTxBytes = s.netTx
	}

	m.mu.Lock()
	if err == nil {
		if p, ok := m.prev[key]; ok {
			dt := now.Sub(p.t).Seconds()
			if dt > 0 {
				// A reboot resets the counters; a decreasing delta is not a
				// real rate, so leave it at 0 rather than invent a negative one.
				if s.netRx >= p.rx {
					met.NetRxRate = float64(s.netRx-p.rx) / dt
				}
				if s.netTx >= p.tx {
					met.NetTxRate = float64(s.netTx-p.tx) / dt
				}
			}
		}
		m.prev[key] = netSample{rx: s.netRx, tx: s.netTx, t: now}
	}
	m.metrics[key] = met
	if fp != "" {
		// TOFU: persist the freshly trusted key. Only do so while the stored
		// fingerprint is still empty, so a concurrent Set wins and is not
		// overwritten with an older value.
		if cur, ok := m.configs[key]; ok && cur.HostKeyFingerprint == "" {
			cur.HostKeyFingerprint = fp
			m.configs[key] = cur
			_ = m.persistLocked() // best-effort; must not fail the collection
		}
	}
	m.mu.Unlock()
	return met
}

func (m *Monitor) snapshotKeys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.configs))
	for k := range m.configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m *Monitor) refreshAll(ctx context.Context, stop <-chan struct{}) {
	for _, key := range m.snapshotKeys() {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		default:
		}
		m.collectAndStore(ctx, key)
	}
}

func (m *Monitor) loop(ctx context.Context, interval time.Duration, stop <-chan struct{}) {
	defer m.wg.Done()
	// Collect once immediately so a freshly started panel has real data;
	// thereafter the ticker drives strictly serial rounds.
	m.refreshAll(ctx, stop)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			m.refreshAll(ctx, stop)
		}
	}
}

// Start launches the polling loop. An interval <= 0 falls back to
// defaultInterval. Start is idempotent while the loop is already running.
func (m *Monitor) Start(interval time.Duration) {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if m.running {
		return
	}
	if interval <= 0 {
		interval = defaultInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})
	m.cancel = cancel
	m.stopCh = stop
	m.running = true
	m.wg.Add(1)
	go m.loop(ctx, interval, stop)
}

// Stop cancels the loop (including any in-flight collection) and waits for it
// to exit. It is safe to call more than once.
func (m *Monitor) Stop() {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if !m.running {
		return
	}
	m.running = false
	m.cancel()
	close(m.stopCh)
	m.wg.Wait()
}

// safeError maps every internal/SSH error to a fixed, credential-free,
// user-facing Chinese message. Raw library text is only ever included in the
// fallback branch, after scrub has removed all secrets.
func safeError(err error, cfg SSHConfig) string {
	if err == nil {
		return ""
	}
	low := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrHostKeyMismatch) ||
		strings.Contains(low, "host key fingerprint mismatch") ||
		strings.Contains(low, "host key mismatch"):
		return "SSH 主机密钥校验失败：与已保存的指纹不一致（服务端密钥可能已更换）"
	case errors.Is(err, ErrNotFound):
		return ErrNotFound.Error()
	case errors.Is(err, errParse) || strings.Contains(low, "parse"):
		return "采集数据解析失败"
	}
	switch {
	case containsAny(low, "unable to authenticate", "permission denied",
		"no supported methods remain", "authentication failed", "all authentication methods"):
		return "SSH 认证失败（用户名或凭据错误）"
	case containsAny(low, "invalid private key", "no key found", "passphrase", "private key"):
		return "SSH 私钥无效或无法解析"
	case containsAny(low, "timeout", "deadline exceeded", "i/o timeout",
		"connection refused", "no route to host", "network is unreachable", "connection reset"):
		return "SSH 连接失败或超时"
	case containsAny(low, "handshake", "no matching", "no common"):
		return "SSH 握手失败"
	default:
		return "SSH 采集失败：" + scrub(err.Error(), cfg)
	}
}

// scrub removes all credential material from s. It is the last line of defence
// for any dynamic text that could reach a log, an error or the frontend.
func scrub(s string, cfg SSHConfig) string {
	for _, secret := range []string{cfg.Password, cfg.PrivateKey} {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[已隐藏]")
		if t := strings.TrimSpace(secret); t != "" && t != secret {
			s = strings.ReplaceAll(s, t, "[已隐藏]")
		}
	}
	return s
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
