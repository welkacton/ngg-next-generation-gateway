// Package api exposes the unified REST + WebSocket control plane.
// SECURITY: it binds to loopback by default and requires a Bearer token on
// every route. The UI never executes a shell command; it can only call these
// endpoints, which in turn call Core Manager.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
	"github.com/ngg-project/ngg/internal/update"
	"github.com/ngg-project/ngg/pkg/contracts"
)

type Server struct {
	cfg     *config.Config
	engine  *engine.Engine
	mux     *http.ServeMux
	hub     *wsHub
	http    *http.Server
	updater *update.Checker
}

func New(cfg *config.Config, eng *engine.Engine) *Server {
	s := &Server{cfg: cfg, engine: eng, mux: http.NewServeMux(), updater: update.New(cfg.Storage.Dir)}
	s.hub = newWSHub(eng)
	s.routes()
	s.http = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Listen, cfg.Server.Port),
		Handler:           s.middleware(s.mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func (s *Server) Handler() http.Handler { return s.http.Handler }
func (s *Server) Addr() string          { return s.http.Addr }

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	return s.http.Serve(ln)
}

func (s *Server) Shutdown() error { return s.http.Close() }

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/v1/system", s.handleSystem)
	m.HandleFunc("GET /api/v1/health", s.handleHealth)

	m.HandleFunc("GET /api/v1/cores", s.handleListCores)
	m.HandleFunc("GET /api/v1/cores/{id}", s.handleGetCore)
	m.HandleFunc("POST /api/v1/cores/{id}/start", s.handleStart)
	m.HandleFunc("POST /api/v1/cores/{id}/stop", s.handleStop)
	m.HandleFunc("POST /api/v1/cores/{id}/restart", s.handleRestart)
	m.HandleFunc("POST /api/v1/cores/{id}/install", s.handleInstall)
	m.HandleFunc("DELETE /api/v1/cores/{id}", s.handleUninstall)
	m.HandleFunc("POST /api/v1/cores/{id}/upgrade", s.handleVersionOp)
	m.HandleFunc("POST /api/v1/cores/{id}/rollback", s.handleVersionOp)
	m.HandleFunc("GET /api/v1/cores/{id}/versions", s.handleAvailableVersions)
	m.HandleFunc("GET /api/v1/cores/{id}/version", s.handleInstalledVersion)
	m.HandleFunc("POST /api/v1/cores/batch/start", s.handleBatchStart)
	m.HandleFunc("POST /api/v1/cores/batch/stop", s.handleBatchStop)

	m.HandleFunc("GET /api/v1/cores/{id}/config", s.handleConfigGet)
	m.HandleFunc("PUT /api/v1/cores/{id}/config", s.handleConfigPut)
	m.HandleFunc("POST /api/v1/cores/{id}/config/validate", s.handleConfigValidate)
	m.HandleFunc("GET /api/v1/cores/{id}/config/backups", s.handleConfigBackups)
	m.HandleFunc("POST /api/v1/cores/{id}/config/rollback", s.handleConfigRollback)
	m.HandleFunc("GET /api/v1/cores/{id}/config/export", s.handleConfigExport)
	m.HandleFunc("POST /api/v1/cores/{id}/config/import", s.handleConfigImport)

	m.HandleFunc("GET /api/v1/cores/{id}/logs", s.handleLogs)
	m.HandleFunc("DELETE /api/v1/cores/{id}/logs", s.handleClearLogs)
	m.HandleFunc("GET /api/v1/cores/{id}/stats", s.handleStats)

	m.HandleFunc("GET /api/v1/registry", s.handleRegistry)
	m.HandleFunc("POST /api/v1/registry/reload", s.handleRegistryReload)
	m.HandleFunc("GET /api/v1/ports", s.handlePorts)
	m.HandleFunc("GET /api/v1/ports/check", s.handlePortCheck)
	m.HandleFunc("GET /api/v1/backups", s.handleAllBackups)
	m.HandleFunc("GET /api/v1/audit", s.handleAudit)
	m.HandleFunc("GET /api/v1/ws", s.hub.ServeHTTP)

	// Product generation / in-app repository updates
	m.HandleFunc("GET /api/v1/update/status", s.handleUpdateStatus)
	m.HandleFunc("POST /api/v1/update/check", s.handleUpdateCheck)
	m.HandleFunc("POST /api/v1/update/download", s.handleUpdateDownload)
	m.HandleFunc("POST /api/v1/update/apply", s.handleUpdateApply)

	// Stage 2-3: Unified Network Engine
	m.HandleFunc("GET /api/v1/network/status", s.handleNetworkStatus)
	m.HandleFunc("POST /api/v1/network/start", s.handleNetworkStart)
	m.HandleFunc("POST /api/v1/network/stop", s.handleNetworkStop)
	m.HandleFunc("GET /api/v1/network/config", s.handleNetworkConfig)
	m.HandleFunc("PUT /api/v1/network/config", s.handleNetworkConfig)
	m.HandleFunc("GET /api/v1/network/rules", s.handleNetworkRules)

	// Config Engine: custom rule sets (URL / upload / inline)
	m.HandleFunc("GET /api/v1/rulesets", s.handleRuleSets)
	m.HandleFunc("POST /api/v1/rulesets", s.handleRuleSets)
	m.HandleFunc("GET /api/v1/rulesets/formats", s.handleRuleSetFormats)
	m.HandleFunc("POST /api/v1/rulesets/toggle", s.handleRuleSetsToggle)
	m.HandleFunc("POST /api/v1/rulesets/upload", s.handleRuleSetUpload)
	m.HandleFunc("POST /api/v1/rulesets/{id}/fetch", s.handleRuleSetFetch)
	m.HandleFunc("POST /api/v1/rulesets/{id}/apply", s.handleRuleSetApply)
	m.HandleFunc("GET /api/v1/rulesets/{id}", s.handleRuleSetByID)
	m.HandleFunc("PUT /api/v1/rulesets/{id}", s.handleRuleSetByID)
	m.HandleFunc("DELETE /api/v1/rulesets/{id}", s.handleRuleSetByID)
	m.HandleFunc("POST /api/v1/network/rules/toggle", s.handleNetworkRulesToggle)
	m.HandleFunc("POST /api/v1/network/rules", s.handleNetworkRules)
	m.HandleFunc("PUT /api/v1/network/rules", s.handleNetworkRules)
	m.HandleFunc("DELETE /api/v1/network/rules/{id}", s.handleNetworkRuleByID)
	m.HandleFunc("POST /api/v1/network/rules/load", s.handleNetworkRulesLoad)
	m.HandleFunc("GET /api/v1/network/dns", s.handleNetworkDNS)
	m.HandleFunc("POST /api/v1/network/dns/resolve", s.handleNetworkDNSResolve)
	m.HandleFunc("POST /api/v1/network/dns/hosts", s.handleNetworkDNSHosts)
	m.HandleFunc("GET /api/v1/network/sessions", s.handleNetworkSessions)
	m.HandleFunc("GET /api/v1/network/endpoints", s.handleNetworkEndpoints)
	m.HandleFunc("POST /api/v1/network/selftest", s.handleNetworkSelfTest)
	m.HandleFunc("POST /api/v1/network/packettest", s.handleNetworkPacketTest)
	m.HandleFunc("PUT /api/v1/network/rules/{id}", s.handleNetworkRuleByID)

	// Config Engine: inbound / outbound encrypted protocol nodes
	m.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	m.HandleFunc("POST /api/v1/nodes", s.handleNodes)
	m.HandleFunc("GET /api/v1/nodes/protocols", s.handleNodeProtocols)
	m.HandleFunc("GET /api/v1/nodes/templates", s.handleNodeTemplates)
	m.HandleFunc("GET /api/v1/nodes/export", s.handleNodeExport)
	m.HandleFunc("PUT /api/v1/nodes/import", s.handleNodeImport)
	m.HandleFunc("POST /api/v1/nodes/parse", s.handleParseNative)
	m.HandleFunc("POST /api/v1/nodes/from-native", s.handleFromNative)
	m.HandleFunc("POST /api/v1/nodes/{id}/config", s.handleNodeConfig)
	m.HandleFunc("GET /api/v1/nodes/{id}", s.handleNodeByID)
	m.HandleFunc("PUT /api/v1/nodes/{id}", s.handleNodeByID)
	m.HandleFunc("DELETE /api/v1/nodes/{id}", s.handleNodeByID)

	// Stage 4: VPN protocol center / post-quantum / plugin runtime
	m.HandleFunc("GET /api/v1/stages", s.handleStages)
	m.HandleFunc("GET /api/v1/protocols", s.handleProtocols)
	m.HandleFunc("GET /api/v1/protocols/{id}", s.handleProtocolByID)
	m.HandleFunc("POST /api/v1/protocols/{id}/enable", s.handleProtocolByID)
	m.HandleFunc("POST /api/v1/protocols/{id}/config", s.handleProtocolByID)
	m.HandleFunc("GET /api/v1/pq/status", s.handlePQStatus)
	m.HandleFunc("POST /api/v1/pq/selftest", s.handlePQSelfTest)
	m.HandleFunc("GET /api/v1/plugins", s.handlePlugins)
	m.HandleFunc("POST /api/v1/plugins", s.handlePlugins)
	m.HandleFunc("POST /api/v1/plugins/reload", s.handlePluginsReload)
	m.HandleFunc("POST /api/v1/plugins/{id}/enable", s.handlePluginByID)
	m.HandleFunc("POST /api/v1/plugins/{id}/test", s.handlePluginByID)
	m.HandleFunc("DELETE /api/v1/plugins/{id}", s.handlePluginByID)

	// Stage 5: SD-WAN / multi-tenant / cross-platform build matrix
	m.HandleFunc("GET /api/v1/sdwan/status", s.handleSDWANStatus)
	m.HandleFunc("GET /api/v1/sdwan/links", s.handleSDWANLinks)
	m.HandleFunc("POST /api/v1/sdwan/links", s.handleSDWANLinks)
	m.HandleFunc("DELETE /api/v1/sdwan/links/{id}", s.handleSDWANLinkByID)
	m.HandleFunc("POST /api/v1/sdwan/{action}", s.handleSDWANAction)
	m.HandleFunc("GET /api/v1/tenants", s.handleTenants)
	m.HandleFunc("POST /api/v1/tenants", s.handleTenants)
	m.HandleFunc("DELETE /api/v1/tenants/{id}", s.handleTenantByID)
	m.HandleFunc("GET /api/v1/platforms", s.handlePlatforms)
	m.HandleFunc("POST /api/v1/platforms/build", s.handlePlatformBuild)

	m.HandleFunc("/", s.handleStatic)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("NGG API panic: %v", rec)
				writeErr(w, http.StatusInternalServerError, fmt.Sprintf("internal error: %v", rec))
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			// Localhost-only is enforced at the socket level AND here, so a
			// misconfigured listen address can never expose control APIs.
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if !isLoopback(host) {
				writeErr(w, http.StatusForbidden, "NGG control API is local-only")
				return
			}
			token := bearerToken(r)
			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Server.Token)) != 1 {
				writeErr(w, http.StatusUnauthorized, "missing or invalid API token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// Browser WebSocket clients cannot set headers; allow ?token= on WS only.
	if r.URL.Path == "/api/v1/ws" {
		return r.URL.Query().Get("token")
	}
	return ""
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("NGG API encode: %v", err)
	}
}

// errorCodeFor maps a real failure to a stable, machine-readable prefix.
// Codes are derived from the actual failure, not invented per endpoint.
func errorCodeFor(status int, msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not installed"):
		return "CORE_NOT_INSTALLED"
	case strings.Contains(lower, "backup"):
		return "BACKUP_NOT_FOUND"
	case strings.Contains(lower, "version"):
		return "VERSION_NOT_FOUND"
	case strings.Contains(lower, "not registered") || strings.Contains(lower, "not found"):
		return "CORE_NOT_FOUND"
	case strings.Contains(lower, "already running"):
		return "ALREADY_RUNNING"
	case strings.Contains(lower, "not running"):
		return "NOT_RUNNING"
	case strings.Contains(lower, "port conflict"):
		return "PORT_CONFLICT"
	case strings.Contains(lower, "does not declare support"):
		return "ARCH_UNSUPPORTED"
	case strings.Contains(lower, "sha256") || strings.Contains(lower, "digest"):
		return "DIGEST_MISMATCH"
	case strings.Contains(lower, "download") || strings.Contains(lower, "release") || strings.Contains(lower, "refusing install"):
		return "DOWNLOAD_FAILED"
	case strings.Contains(lower, "validation") || strings.Contains(lower, "校验"):
		return "CONFIG_INVALID"
	case strings.Contains(lower, "start kernel") || strings.Contains(lower, "execute") || strings.Contains(lower, "version check"):
		return "CORE_EXEC_FAILED"
	}
	switch status {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "STATE_CONFLICT"
	case http.StatusUnprocessableEntity:
		return "CONFIG_INVALID"
	case http.StatusBadGateway:
		return "CORE_EXEC_FAILED"
	default:
		return "INTERNAL_ERROR"
	}
}

// writeErr emits the documented single-field error envelope:
//
//	{"error":"ERROR_CODE: 人类可读描述"}
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": errorCodeFor(status, msg) + ": " + msg})
}

func writeOK(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, contracts.OperationResult{OK: true, Message: msg})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	return dec.Decode(v)
}
