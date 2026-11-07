// Package nodesvc is the node-side control daemon — NGG's counterpart to
// remnawave/node.
//
// It is a long-running process on the node that owns the kernel (xray) and
// answers a small internal API over mutual TLS on NODE_PORT:
//
//	GET  /node/xray/healthcheck        alive? which kernel? which config hash?
//	POST /node/xray/start              write xrayConfig, validate, (re)start
//	POST /node/xray/stop               stop the kernel, keep the API up
//	GET  /node/stats/get-system-stats  real host metrics
//
// The split matters: the panel no longer needs shell access, an SSH key or a
// port forward to operate a node. It needs one reachable port and a client
// certificate, and everything it does (push config, restart, read metrics) is
// an explicit, authenticated, auditable call.
//
// The process runs as root because it restarts the kernel unit; it touches
// only its own config path, its own certificate directory and its own unit.
package nodesvc

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ngg-project/ngg/internal/nodeapi"
	"github.com/ngg-project/ngg/internal/nodepki"
)

// Limits.
const (
	// maxConfigBody bounds a pushed kernel config. Real profiles are a few KiB;
	// 8 MiB leaves room for a large rule set without letting a client exhaust
	// the node's memory.
	maxConfigBody = 8 << 20
	// testTimeout bounds `kernel run -test`.
	testTimeout = 30 * time.Second
	// coreStartTimeout waits for the kernel unit to become active.
	coreStartTimeout = 20 * time.Second
	// journalTail is how much kernel journal is returned on a failed start.
	journalTail = 12
)

// Config is the daemon's runtime configuration (all flags, see cmd/ngg-node).
type Config struct {
	Listen       string // ":2222"
	CertFile     string // node certificate (PEM)
	KeyFile      string // node private key (PEM)
	CAFile       string // panel CA (PEM)
	ManifestPath string // /etc/ngg/node.json
	KernelBin    string // /usr/local/bin/ngg-xray
	CoreConfig   string // /etc/ngg/xray/config.json
	CoreUnit     string // ngg-core.service
	Version      string // node daemon version
	Logger       *log.Logger
}

// Service is the daemon.
type Service struct {
	cfg       Config
	log       *log.Logger
	col       *Collector
	nodeID    string
	startedAt time.Time
	handler   http.Handler
}

// New loads the certificate material, pins the node identity and builds the
// router. It fails fast: a node that cannot authenticate the panel must not
// come up pretending to work.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stderr, "ngg-node ", log.LstdFlags|log.LUTC)
	}
	if cfg.Listen == "" {
		cfg.Listen = ":2222"
	}
	certPEM, err := os.ReadFile(cfg.CertFile)
	if err != nil {
		return nil, fmt.Errorf("读取节点证书失败: %w", err)
	}
	keyPEM, err := os.ReadFile(cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("读取节点私钥失败: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("读取面板 CA 失败: %w", err)
	}

	nodeID, err := identityFromCert(certPEM)
	if err != nil {
		return nil, err
	}
	// Accept the panel, and accept ourselves so the deploy script can verify
	// the API end-to-end right after installing it. Any other node's
	// certificate (same CA, different CN) is rejected by the CN check.
	if _, err := nodepki.ServerConfig(certPEM, keyPEM, caPEM, nodepki.PanelCN, nodeID); err != nil {
		return nil, err
	}

	s := &Service{
		cfg:       cfg,
		log:       cfg.Logger,
		col:       NewCollector(),
		nodeID:    nodeID,
		startedAt: time.Now(),
	}
	// The manifest is what the operator and `ngg-node` tooling read; if it
	// disagrees with the certificate the machine has two identities and the
	// panel's CN pin would fail. Say so now instead of at connect time.
	if id := manifestNodeID(cfg.ManifestPath); id != "" && id != nodeID {
		cfg.Logger.Printf("警告：清单里的 node_id=%s 与证书 CN=%s 不一致，面板会拒绝连接（重新部署可修复）", id, nodeID)
	}
	s.handler = s.routes()
	return s, nil
}

// NodeID is the identity this daemon serves (certificate CN).
func (s *Service) NodeID() string { return s.nodeID }

// Handler exposes the router (used by tests and by Run).
func (s *Service) Handler() http.Handler { return s.handler }

// Run serves until SIGTERM/SIGINT.
func (s *Service) Run() error {
	certPEM, err := os.ReadFile(s.cfg.CertFile)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(s.cfg.KeyFile)
	if err != nil {
		return err
	}
	caPEM, err := os.ReadFile(s.cfg.CAFile)
	if err != nil {
		return err
	}
	tlsCfg, err := nodepki.ServerConfig(certPEM, keyPEM, caPEM, nodepki.PanelCN, s.nodeID)
	if err != nil {
		return err
	}

	s.col.Start(time.Second)
	defer s.col.Stop()

	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", s.cfg.Listen, err)
	}
	// tlsCfg already carries the certificate and the client-certificate
	// requirements (CA pool + CN allow-list).
	ln = tls.NewListener(ln, tlsCfg)

	s.log.Printf("节点 API 启动：node=%s listen=%s core_unit=%s version=%s", s.nodeID, s.cfg.Listen, s.cfg.CoreUnit, s.cfg.Version)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sig:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

/* ------------------------------------------------------------------ *
 * 路由
 * ------------------------------------------------------------------ */

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /node/xray/healthcheck", s.handleHealth)
	mux.HandleFunc("POST /node/xray/start", s.handleStart)
	mux.HandleFunc("POST /node/xray/stop", s.handleStop)
	mux.HandleFunc("GET /node/stats/get-system-stats", s.handleStats)
	return s.withLog(mux)
}

// withLog writes one line per request to the journal: the node side of an
// audit trail for "who pushed what".
func (s *Service) withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		who := "?"
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			who = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		s.log.Printf("%s %s cn=%s status=%d %s", r.Method, r.URL.Path, who, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

/* ------------------------------------------------------------------ *
 * 处理函数
 * ------------------------------------------------------------------ */

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	active := s.coreActive()
	writeOK(w, map[string]any{
		"isAlive":                  true,
		"xrayInternalStatusCached": false,
		"xrayVersion":              s.kernelVersion(r.Context()),
		"nodeVersion":              s.cfg.Version,
		"nodeId":                   s.nodeID,
		"coreActive":               active,
		"uptimeSec":                int64(time.Since(s.startedAt).Seconds()),
		"configSha256":             s.configSHA(),
		"checkedAt":                time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Service) handleStats(w http.ResponseWriter, r *http.Request) {
	writeOK(w, s.col.System())
}

// startRequest is Remnawave's StartXrayCommand.Request: the config travels in
// the body, so the panel never needs a shared filesystem with the node.
type startRequest struct {
	Internals struct {
		ForceRestart bool `json:"forceRestart"`
	} `json:"internals"`
	XrayConfig json.RawMessage `json:"xrayConfig"`
}

func (s *Service) handleStart(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return
	}
	var req startRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	if len(req.XrayConfig) == 0 || string(req.XrayConfig) == "null" {
		writeErr(w, http.StatusBadRequest, "请求缺少 xrayConfig")
		return
	}
	if !json.Valid(req.XrayConfig) {
		writeErr(w, http.StatusBadRequest, "xrayConfig 不是合法 JSON")
		return
	}

	sum := sha256.Sum256(req.XrayConfig)
	want := hex.EncodeToString(sum[:])
	if !req.Internals.ForceRestart && want == s.configSHA() && s.coreActive() {
		// Nothing changed: report success without bouncing a healthy kernel.
		// A needless restart drops every live connection on the node.
		writeOK(w, startResponse(true, s.kernelVersion(r.Context()), "", s.col.System()))
		return
	}

	tmp, err := s.stageConfig(req.XrayConfig)
	if err != nil {
		writeOK(w, startResponse(false, "", err.Error(), nodeapi.System{}))
		return
	}
	if out, terr := s.testConfig(r.Context(), tmp); terr != nil {
		// Rejected: drop the candidate and leave the running config alone.
		_ = os.Remove(tmp)
		s.log.Printf("配置校验失败：%v", terr)
		writeOK(w, startResponse(false, "", "配置校验未通过："+out, nodeapi.System{}))
		return
	}
	if err := s.commitConfig(tmp); err != nil {
		writeOK(w, startResponse(false, "", err.Error(), nodeapi.System{}))
		return
	}
	if err := s.restartCore(r.Context()); err != nil {
		writeOK(w, startResponse(false, "", err.Error(), nodeapi.System{}))
		return
	}
	s.log.Printf("已下发配置并重启内核：sha=%s", want[:16])
	writeOK(w, startResponse(true, s.kernelVersion(r.Context()), "", s.col.System()))
}

func (s *Service) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.runSystemctl(r.Context(), "stop", s.cfg.CoreUnit); err != nil {
		writeOK(w, startResponse(false, "", "停止内核失败："+err.Error(), nodeapi.System{}))
		return
	}
	s.log.Printf("内核已停止：%s", s.cfg.CoreUnit)
	writeOK(w, startResponse(false, s.kernelVersion(r.Context()), "", s.col.System()))
}

func startResponse(started bool, version, errMsg string, sys nodeapi.System) map[string]any {
	out := map[string]any{
		"isStarted": started,
		"error":     nil,
		"version":   nil,
		"system":    nil,
	}
	if version != "" {
		out["version"] = version
	}
	if errMsg != "" {
		out["error"] = errMsg
	}
	if started && sys.Info.Hostname != "" {
		out["system"] = sys
	}
	return out
}

/* ------------------------------------------------------------------ *
 * 内核控制
 * ------------------------------------------------------------------ */

// stageConfig writes the candidate config next to the live one, so a config
// the kernel rejects never reaches the path the next restart reads.
func (s *Service) stageConfig(cfg []byte) (string, error) {
	dir := filepath.Dir(s.cfg.CoreConfig)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}
	tmp := s.cfg.CoreConfig + ".tmp"
	if err := os.WriteFile(tmp, append(cfg, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("写入配置失败: %w", err)
	}
	return tmp, nil
}

// commitConfig atomically replaces the live config, keeping the previous file
// as .bak for a manual rollback.
func (s *Service) commitConfig(tmp string) error {
	if prev, err := os.ReadFile(s.cfg.CoreConfig); err == nil {
		_ = os.WriteFile(s.cfg.CoreConfig+".bak", prev, 0o600)
	}
	if err := os.Rename(tmp, s.cfg.CoreConfig); err != nil {
		return fmt.Errorf("替换配置失败: %w", err)
	}
	return nil
}

// testConfig runs the kernel's own validator on path. Its output is the only
// thing that can prove a pushed profile is loadable.
func (s *Service) testConfig(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.cfg.KernelBin, "run", "-test", "-config", path)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return clipLines(text, journalTail), err
	}
	return text, nil
}

func (s *Service) restartCore(ctx context.Context) error {
	if err := s.runSystemctl(ctx, "restart", s.cfg.CoreUnit); err != nil {
		return fmt.Errorf("重启 %s 失败：%w", s.cfg.CoreUnit, err)
	}
	deadline := time.Now().Add(coreStartTimeout)
	for time.Now().Before(deadline) {
		if s.coreActive() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	tail := s.journal(ctx)
	if tail == "" {
		return fmt.Errorf("%s 未在 %s 内进入 active", s.cfg.CoreUnit, coreStartTimeout)
	}
	return fmt.Errorf("%s 未进入 active，内核日志：\n%s", s.cfg.CoreUnit, tail)
}

func (s *Service) runSystemctl(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%v: %s", err, msg)
	}
	return nil
}

func (s *Service) coreActive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", s.cfg.CoreUnit).Output()
	if err != nil {
		// is-active exits non-zero for inactive units; the stdout is still the
		// state word, which is what we want.
		if state := strings.TrimSpace(string(out)); state != "" {
			return state == "active"
		}
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

func (s *Service) kernelVersion(parent context.Context) string {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.cfg.KernelBin, "version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

func (s *Service) configSHA() string {
	b, err := os.ReadFile(s.cfg.CoreConfig)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Service) journal(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", s.cfg.CoreUnit, "-n", "40", "--no-pager").Output()
	if err != nil {
		return ""
	}
	return clipLines(strings.TrimSpace(string(out)), journalTail)
}

/* ------------------------------------------------------------------ *
 * 小工具
 * ------------------------------------------------------------------ */

func writeOK(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"response": payload})
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"response": nil, "error": msg})
}

func clipLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// identityFromCert returns the CN of the node certificate — the identity the
// panel pins when it dials.
func identityFromCert(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("节点证书不是 PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("解析节点证书失败: %w", err)
	}
	cn := strings.TrimSpace(cert.Subject.CommonName)
	if cn == "" {
		return "", errors.New("节点证书缺少 CN")
	}
	return cn, nil
}

// manifestNodeID reads node_id out of node.json without pulling the whole
// manifest shape into this package (the deploy script owns that schema).
func manifestNodeID(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m.NodeID)
}
