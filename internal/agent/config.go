package agent

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ngg-project/ngg/internal/atomicfile"
)

// ServerConfig is the public listener settings of the agent endpoint. It is
// independent from the local control API: agents on the internet must not need
// a console session.
type ServerConfig struct {
	Enabled   bool   `json:"enabled"`
	Listen    string `json:"listen"`
	Port      int    `json:"port"`
	PublicURL string `json:"public_url"`
}

// DefaultServerConfig matches the documented v2.7.0 defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{Enabled: true, Listen: "0.0.0.0", Port: 9191, PublicURL: ""}
}

// ServerStore persists agent-server.json (0600, atomic replace).
type ServerStore struct {
	mu   sync.RWMutex
	path string
	cfg  ServerConfig
}

// NewServerStore roots the store at the NGG data directory.
func NewServerStore(dir string) *ServerStore {
	return &ServerStore{
		path: filepath.Join(dir, "agent-server.json"),
		cfg:  DefaultServerConfig(),
	}
}

// Path is the persisted file location.
func (s *ServerStore) Path() string { return s.path }

// Load reads the persisted config; a missing file keeps the defaults.
func (s *ServerStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	c := DefaultServerConfig()
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	if _, _, err := validateServer(c); err != nil {
		return fmt.Errorf("invalid %s: %w", s.path, err)
	}
	if c.Listen == "" {
		c.Listen = DefaultServerConfig().Listen
	}
	c.PublicURL = strings.TrimRight(strings.TrimSpace(c.PublicURL), "/")
	s.cfg = c
	return nil
}

// Get returns a copy of the current config.
func (s *ServerStore) Get() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Addr is the listen address in host:port form.
func (s *ServerStore) Addr() string {
	c := s.Get()
	return net.JoinHostPort(c.Listen, fmt.Sprint(c.Port))
}

// Save validates and persists c. Warnings describe real operational caveats
// (firewall, plaintext token) and are surfaced to the operator.
func (s *ServerStore) Save(c ServerConfig) (ServerConfig, []string, error) {
	warnings, normalized, err := validateServer(c)
	if err != nil {
		return ServerConfig{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cfg
	s.cfg = normalized
	if err := atomicfile.WriteJSON(s.path, normalized); err != nil {
		s.cfg = old
		return ServerConfig{}, nil, fmt.Errorf("持久化失败: %w", err)
	}
	return normalized, warnings, nil
}

func validateServer(c ServerConfig) ([]string, ServerConfig, error) {
	warnings := []string{}
	c.Listen = strings.TrimSpace(c.Listen)
	c.PublicURL = strings.TrimRight(strings.TrimSpace(c.PublicURL), "/")
	if c.Listen == "" {
		c.Listen = DefaultServerConfig().Listen
	}
	if ip := net.ParseIP(strings.Trim(c.Listen, "[]")); ip == nil {
		return nil, c, fmt.Errorf("%w: 监听地址必须是 IP（如 0.0.0.0 / 127.0.0.1 / ::）", ErrInvalid)
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, c, fmt.Errorf("%w: 端口需在 1..65535", ErrInvalid)
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, c, fmt.Errorf("%w: public_url 需为 http:// 或 https:// 开头的完整地址", ErrInvalid)
		}
		if u.Scheme != "https" {
			warnings = append(warnings, "public_url 不是 HTTPS：agent token 与订阅内容会以明文经过网络，建议放在 TLS 反向代理之后。")
		}
	} else {
		warnings = append(warnings, "未设置 public_url：安装脚本与订阅链接只能给出相对路径或占位地址，远程机器无法直接使用。")
	}
	if ip := net.ParseIP(strings.Trim(c.Listen, "[]")); ip != nil && !ip.IsLoopback() {
		warnings = append(warnings, fmt.Sprintf("监听 %s:%d 会暴露到网络：请确认防火墙/安全组已放行该端口，且 token 不会在不可信网络中明文传输。", c.Listen, c.Port))
	}
	return warnings, c, nil
}

// PublicBase returns the base URL used in install scripts and subscription
// links. When public_url is unset it degrades honestly: a concrete listen IP
// is used if it is routable, otherwise an explicit placeholder is returned so
// nobody ships a config pointing at 0.0.0.0.
func (s *ServerStore) PublicBase() string {
	c := s.Get()
	if c.PublicURL != "" {
		return c.PublicURL
	}
	host := strings.Trim(c.Listen, "[]")
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		return fmt.Sprintf("http://%s", net.JoinHostPort(host, fmt.Sprint(c.Port)))
	}
	return ""
}
