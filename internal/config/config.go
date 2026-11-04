// Package config loads NGG's own configuration. It has nothing to do with
// kernel configs — those are owned by engine.ConfigManager.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ngg-project/ngg/internal/netengine"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server" json:"server"`
	Storage    StorageConfig    `yaml:"storage" json:"storage"`
	Cores      CoresConfig      `yaml:"cores" json:"cores"`
	Supervisor SupervisorConfig `yaml:"supervisor" json:"supervisor"`
	Logging    LoggingConfig    `yaml:"logging" json:"logging"`
	Network    netengine.Config `yaml:"network" json:"network"`
	Build      BuildConfig      `yaml:"build" json:"build"`
}

type ServerConfig struct {
	Listen string `yaml:"listen" json:"listen"`
	Port   int    `yaml:"port" json:"port"`
	Token  string `yaml:"token" json:"-"`
	WebDir string `yaml:"web_dir" json:"web_dir"`
}

type StorageConfig struct {
	Dir string `yaml:"dir" json:"dir"`
}

type CoresConfig struct {
	DescriptorDir string `yaml:"descriptor_dir" json:"descriptor_dir"`
	PortBase      int    `yaml:"port_base" json:"port_base"`
	PortStride    int    `yaml:"port_stride" json:"port_stride"`
}

type SupervisorConfig struct {
	StopTimeout    string `yaml:"stop_timeout" json:"stop_timeout"`
	RestartMax     int    `yaml:"restart_max" json:"restart_max"`
	RestartBackoff string `yaml:"restart_backoff" json:"restart_backoff"`
}

type BuildConfig struct {
	GoBinary string `yaml:"go_binary" json:"go_binary"`
}

type LoggingConfig struct {
	Level        string `yaml:"level" json:"level"`
	Dir          string `yaml:"dir" json:"dir"`
	RingCapacity int    `yaml:"ring_capacity" json:"ring_capacity"`
}

func Defaults() Config {
	return Config{
		Server:     ServerConfig{Listen: "127.0.0.1", Port: 9090, WebDir: "web/dist"},
		Storage:    StorageConfig{Dir: "data"},
		Cores:      CoresConfig{DescriptorDir: "configs/cores.d", PortBase: 17890, PortStride: 10},
		Supervisor: SupervisorConfig{StopTimeout: "5s", RestartMax: 3, RestartBackoff: "2s"},
		Logging:    LoggingConfig{Level: "info", Dir: "data/logs", RingCapacity: 4000},
		Network:    netengine.DefaultConfig(),
	}
}

// Load reads the config file, applies defaults and environment overrides, and
// generates + persists a strong random API token on first run.
func Load(path string) (*Config, error) {
	c := Defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			if err := yaml.Unmarshal(b, &c); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
	if v := os.Getenv("NGG_LISTEN"); v != "" {
		c.Server.Listen = v
	}
	if v := os.Getenv("NGG_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Server.Port = n
		}
	}
	if v := os.Getenv("NGG_DATA_DIR"); v != "" {
		c.Storage.Dir = v
	}
	if v := os.Getenv("NGG_TOKEN"); v != "" {
		c.Server.Token = v
	}
	if c.Server.Token == "" {
		token, err := randomToken()
		if err != nil {
			return nil, err
		}
		c.Server.Token = token
		if path != "" {
			if err := Save(path, &c); err != nil {
				return nil, fmt.Errorf("persist generated token: %w", err)
			}
			fmt.Fprintf(os.Stderr, "NGG: generated API token and saved it to %s\n", path)
		}
		fmt.Fprintf(os.Stderr, "NGG API Token: %s\n", c.Server.Token)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be 1..65535")
	}
	if c.Server.Token == "" {
		return fmt.Errorf("server.token must not be empty")
	}
	if c.Cores.PortBase < 1024 || c.Cores.PortBase+200*c.Cores.PortStride > 65535 {
		return fmt.Errorf("cores.port_base/port_stride out of usable range")
	}
	if c.Cores.PortStride < 5 {
		return fmt.Errorf("cores.port_stride must be >= 5 (one port per role)")
	}
	return nil
}

func (c *Config) StopTimeout() time.Duration {
	d, err := time.ParseDuration(c.Supervisor.StopTimeout)
	if err != nil || d <= 0 {
		return 5 * time.Second
	}
	return d
}

func (c *Config) RestartBackoff() time.Duration {
	d, err := time.ParseDuration(c.Supervisor.RestartBackoff)
	if err != nil || d <= 0 {
		return 2 * time.Second
	}
	return d
}

func (c *Config) DBPath() string { return filepath.Join(c.Storage.Dir, "ngg.db") }

func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
