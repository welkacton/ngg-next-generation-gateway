package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxReportBytes caps the report body. Metrics are small; 2 MiB is already
// generous and keeps a hostile client from exhausting memory.
const maxReportBytes = 2 << 20

// tokenHeader carries the agent token. Tokens are never accepted from the
// query string on the report endpoint (only /sub/{token} is path-based).
const tokenHeader = "X-NGG-Agent-Token"

// SubscriptionFunc renders a client subscription body for a device token.
// It is injected by the control plane (internal/clients) so this package does
// not depend on the client registry. ok=false means "unknown / disabled /
// expired" and is reported as 404 without distinguishing the reason.
type SubscriptionFunc func(ctx context.Context, token string) (body string, headers map[string]string, ok bool)

// UserSubscriptionFunc renders a per-user subscription body (节点 + 协议 +
// 规则集 + DNS) for the token minted by internal/users. target is one of
// clash / surge / singbox / v2ray / socks. ok=false covers every rejection
// reason so the public route cannot be used to enumerate tokens.
type UserSubscriptionFunc func(ctx context.Context, token, target string) (body string, headers map[string]string, ok bool)

// Endpoint is the public listener agents report to.
type Endpoint struct {
	store *Store
	cfg   *ServerStore
	mux   *http.ServeMux

	mu      sync.RWMutex
	sub     SubscriptionFunc
	userSub UserSubscriptionFunc
	logger  func(format string, args ...any)

	httpSrv *http.Server
}

// NewEndpoint wires the report handler with the registry and its config.
func NewEndpoint(store *Store, cfg *ServerStore) *Endpoint {
	e := &Endpoint{store: store, cfg: cfg, logger: log.Printf}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/report", e.handleReport)
	mux.HandleFunc("GET /v1/agent/ping", e.handlePing)
	mux.HandleFunc("GET /sub/{token}", e.handleSubscription)
	mux.HandleFunc("GET /sub/u/{token}", e.handleUserSubscription)
	e.mux = mux
	return e
}

// SetSubscription installs the subscription renderer. While unset, /sub
// answers 404 (no fake content is ever produced).
func (e *Endpoint) SetSubscription(fn SubscriptionFunc) {
	e.mu.Lock()
	e.sub = fn
	e.mu.Unlock()
}

// SetUserSubscription installs the per-user renderer behind /sub/u/{token}.
// While unset that route answers 404, exactly like /sub/{token} without a
// renderer.
func (e *Endpoint) SetUserSubscription(fn UserSubscriptionFunc) {
	e.mu.Lock()
	e.userSub = fn
	e.mu.Unlock()
}

// SetLogger overrides the default log.Printf sink.
func (e *Endpoint) SetLogger(fn func(format string, args ...any)) {
	if fn == nil {
		return
	}
	e.mu.Lock()
	e.logger = fn
	e.mu.Unlock()
}

func (e *Endpoint) logf(format string, args ...any) {
	e.mu.RLock()
	fn := e.logger
	e.mu.RUnlock()
	if fn != nil {
		fn(format, args...)
	}
}

// Handler exposes the public routes (used directly by tests).
func (e *Endpoint) Handler() http.Handler { return e.mux }

// Enabled reports whether the operator turned the public listener on.
func (e *Endpoint) Enabled() bool { return e.cfg.Get().Enabled }

// Run serves until ctx is cancelled, then shuts down gracefully. A disabled
// config makes Run a no-op, so nothing is ever exposed by accident.
func (e *Endpoint) Run(ctx context.Context) error {
	if !e.Enabled() {
		e.logf("NGG agent endpoint is disabled in agent-server.json; not listening")
		return nil
	}
	addr := e.cfg.Addr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("agent endpoint listen %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           e.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	e.mu.Lock()
	e.httpSrv = srv
	e.mu.Unlock()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	e.logf("NGG agent endpoint listening on %s", addr)
	err = srv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	return nil
}

func (e *Endpoint) handleReport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxReportBytes)
	var p ReportPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		if _, tooBig := err.(*http.MaxBytesError); tooBig {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "PAYLOAD_TOO_LARGE: 上报内容超过 2MiB"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "INVALID_ARGUMENT: 请求体不是合法 JSON"})
		return
	}
	p.AgentID = strings.TrimSpace(p.AgentID)
	if p.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "INVALID_ARGUMENT: agent_id 必填"})
		return
	}
	token := strings.TrimSpace(r.Header.Get(tokenHeader))
	err := e.store.ReportFull(p, token)
	switch {
	case errors.Is(err, ErrUnauthorized):
		// One message for "unknown agent" and "wrong token": the caller must
		// not be able to enumerate valid agent ids.
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "UNAUTHORIZED: token 无效"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "INTERNAL_ERROR: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "interval_sec": DefaultIntervalSec, "config_version": 1,
	})
}

func (e *Endpoint) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "ngg-agent-endpoint"})
}

func (e *Endpoint) handleSubscription(w http.ResponseWriter, r *http.Request) {
	token := filepath.Base(strings.TrimSpace(r.PathValue("token")))
	e.mu.RLock()
	fn := e.sub
	e.mu.RUnlock()
	if fn == nil || token == "" || token == "." || token == "/" {
		// No renderer injected yet, or no token: identical empty 404 so the
		// endpoint cannot be used to probe device tokens.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, headers, ok := fn(r.Context(), token)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	for k, v := range headers {
		if strings.EqualFold(k, "Content-Type") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

// handleUserSubscription serves GET /sub/u/{token}?target=<format>. It is a
// separate route from /sub/{token} so the device-token behaviour stays exactly
// as it was; the path shapes differ in depth, so ServeMux never confuses them.
func (e *Endpoint) handleUserSubscription(w http.ResponseWriter, r *http.Request) {
	token := filepath.Base(strings.TrimSpace(r.PathValue("token")))
	e.mu.RLock()
	fn := e.userSub
	e.mu.RUnlock()
	if fn == nil || token == "" || token == "." || token == "/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	target := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target")))
	if target == "" {
		target = "clash"
	}
	body, headers, ok := fn(r.Context(), token, target)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ct := ""
	for k, v := range headers {
		switch {
		case strings.EqualFold(k, "Content-Length"):
			continue
		case strings.EqualFold(k, "Content-Type"):
			ct = v
			continue
		}
		w.Header().Set(k, v)
	}
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
