// Command ngg-agent is the remote metrics reporter for NGG.
//
// It collects REAL host metrics (CPU, load, memory, swap, disks, interfaces,
// uptime) with gopsutil and POSTs them to the NGG agent endpoint. It never
// prints its token: logs contain the agent id and error summaries only.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ngg-project/ngg/internal/agent"
	"github.com/ngg-project/ngg/internal/version"
)

// fileConfig mirrors the documented agent.yaml layout.
type fileConfig struct {
	Server             string `yaml:"server"`
	AgentID            string `yaml:"agent_id"`
	Token              string `yaml:"token"`
	IntervalSec        int    `yaml:"interval_sec"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

const (
	backoffStart = 1 * time.Second
	backoffMax   = 60 * time.Second
	reportPath   = "/v1/agent/report"
	reportTO     = 10 * time.Second
)

func main() {
	var (
		configPath  = flag.String("config", "", "path to agent.yaml")
		server      = flag.String("server", "", "NGG control-plane base URL, e.g. http://203.0.113.5:9191")
		token       = flag.String("token", "", "agent token issued by NGG (never logged)")
		id          = flag.String("id", "", "agent id issued by NGG")
		interval    = flag.Int("interval", agent.DefaultIntervalSec, "report interval in seconds")
		printConfig = flag.Bool("print-config", false, "print an agent.yaml template and exit")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("ngg-agent %s (%s/%s)\n", version.Release, runtime.GOOS, runtime.GOARCH)
		return
	}

	cfg, err := resolveConfig(*configPath, *server, *token, *id, *interval)
	if err != nil {
		fail(err)
	}

	if *printConfig {
		fmt.Print(agent.ConfigYAML(agent.InstallParams{
			Server: cfg.Server, AgentID: cfg.AgentID, Token: "<token>", IntervalSec: cfg.IntervalSec,
		}))
		return
	}
	if err := cfg.validate(); err != nil {
		fail(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "ngg-agent: %v\n", err)
	os.Exit(1)
}

// resolveConfig layers flags over environment over the config file over
// defaults, in that order of precedence.
func resolveConfig(path, server, token, id string, interval int) (fileConfig, error) {
	cfg := fileConfig{IntervalSec: agent.DefaultIntervalSec}
	if strings.TrimSpace(path) != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("读取配置 %s 失败: %w", path, err)
		}
		var fromFile fileConfig
		if err := yaml.Unmarshal(b, &fromFile); err != nil {
			return cfg, fmt.Errorf("解析配置 %s 失败: %w", path, err)
		}
		cfg = fromFile
		if cfg.IntervalSec <= 0 {
			cfg.IntervalSec = agent.DefaultIntervalSec
		}
	}
	// Environment variables let the Docker image run without a config file.
	if v := os.Getenv("NGG_SERVER"); v != "" {
		cfg.Server = v
	}
	if v := os.Getenv("NGG_AGENT_ID"); v != "" {
		cfg.AgentID = v
	}
	if v := os.Getenv("NGG_AGENT_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("NGG_INTERVAL_SEC"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.IntervalSec = n
		}
	}
	if strings.TrimSpace(server) != "" {
		cfg.Server = server
	}
	if strings.TrimSpace(token) != "" {
		cfg.Token = token
	}
	if strings.TrimSpace(id) != "" {
		cfg.AgentID = id
	}
	if flagSet("interval") && interval > 0 {
		cfg.IntervalSec = interval
	}
	cfg.Server = strings.TrimRight(strings.TrimSpace(cfg.Server), "/")
	cfg.AgentID = strings.TrimSpace(cfg.AgentID)
	cfg.Token = strings.TrimSpace(cfg.Token)
	return cfg, nil
}

func flagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func (c fileConfig) validate() error {
	if c.Server == "" {
		return errors.New("缺少 server：用 -server 或 agent.yaml 的 server 指定 NGG 地址")
	}
	if !strings.HasPrefix(c.Server, "http://") && !strings.HasPrefix(c.Server, "https://") {
		return fmt.Errorf("server 必须以 http:// 或 https:// 开头，当前为 %q", c.Server)
	}
	if c.AgentID == "" {
		return errors.New("缺少 agent_id：用 -id 或 agent.yaml 的 agent_id 指定")
	}
	if c.Token == "" {
		return errors.New("缺少 token：用 -token 或 agent.yaml 的 token 指定（创建节点时只返回一次）")
	}
	if c.IntervalSec < 1 || c.IntervalSec > 3600 {
		return fmt.Errorf("interval_sec 需在 1..3600，当前为 %d", c.IntervalSec)
	}
	return nil
}

// run is the reporting loop: collect -> POST -> sleep, with exponential
// backoff on failure (1s -> 2s -> 4s ... -> 60s) and a reset on success.
func run(ctx context.Context, cfg fileConfig) error {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}, //nolint:gosec // opt-in, documented in agent.yaml
	}
	client := &http.Client{Timeout: reportTO, Transport: transport}
	smp := newSampler()
	backoff := backoffStart
	interval := time.Duration(cfg.IntervalSec) * time.Second

	logf("starting: agent_id=%s server=%s interval=%ds version=%s", cfg.AgentID, cfg.Server, cfg.IntervalSec, version.Release)
	for {
		metrics, hostname := smp.collect()
		err := postReport(ctx, client, cfg, metrics, hostname)
		if err == nil {
			backoff = backoffStart
			logf("report ok: agent_id=%s cpu=%.1f%% mem=%d/%d", cfg.AgentID, metrics.CPUPct, metrics.MemUsed, metrics.MemTotal)
			if !sleep(ctx, interval) {
				return nil
			}
			continue
		}
		logf("report failed (retry in %s): %s", backoff, redact(err.Error(), cfg.Token))
		if !sleep(ctx, backoff) {
			return nil
		}
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

func postReport(ctx context.Context, client *http.Client, cfg fileConfig, m agent.AgentMetrics, hostname string) error {
	body, err := json.Marshal(agent.ReportPayload{
		AgentID:      cfg.AgentID,
		Hostname:     hostname,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		AgentVersion: version.Release,
		IntervalSec:  cfg.IntervalSec,
		Metrics:      m,
	})
	if err != nil {
		return fmt.Errorf("编码上报内容: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Server+reportPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-NGG-Agent-Token", cfg.Token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", cfg.Server+reportPath, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("服务端拒绝 token（401）：节点已被删除或 token 已轮换，请在控制台重新获取")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("服务端返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

// sleep waits d or until ctx is cancelled. It returns false when cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// redact removes the token from any string before it reaches a log line.
func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "<redacted>")
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, time.Now().Format("2006-01-02T15:04:05Z07:00")+" "+format+"\n", args...)
}
