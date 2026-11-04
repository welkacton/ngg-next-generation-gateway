package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/plugins"
	"github.com/ngg-project/ngg/internal/pq"
	"github.com/ngg-project/ngg/internal/protocols"
	"github.com/ngg-project/ngg/internal/sdwan"
	"github.com/ngg-project/ngg/internal/tenants"
)

// ------------------------------------------------------------- stages ---

func (s *Server) handleStages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Stages())
}

// ---------------------------------------------------------- protocols ---

func (s *Server) handleProtocols(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Protocols().List(r.URL.Query().Get("category")))
}

func (s *Server) handleProtocolByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.HasSuffix(r.URL.Path, "/enable") {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		spec, err := s.engine.Protocols().SetEnabled(id, body.Enabled)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": spec})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/config") {
		var body struct {
			Kernel string         `json:"kernel"`
			Params map[string]any `json:"params"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		content, format, err := protocols.GenerateConfig(id, body.Kernel, body.Params)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := protocols.ValidateConfig(content, format); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "protocol": id, "kernel": body.Kernel,
			"format": format, "content": string(content), "validated": true,
		})
		return
	}
	spec, ok := s.engine.Protocols().Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "protocol "+id+" not found")
		return
	}
	writeJSON(w, http.StatusOK, spec)
}

// ------------------------------------------------------- post-quantum ---

func (s *Server) handlePQStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"suite": pq.Suite, "mlkem": "ML-KEM-768 (FIPS 203, crypto/mlkem)",
		"classical": "X25519 (crypto/ecdh)", "kdf": "HKDF-SHA256",
		"aead": "AES-256-GCM", "key_bytes": pq.KeySize,
		"implemented": true, "go_version": runtime.Version(),
		"note": "用于 NGG 节点间控制信道；内核数据面协议仍由各内核自行实现",
	})
}

func (s *Server) handlePQSelfTest(w http.ResponseWriter, r *http.Request) {
	result, err := pq.SelfTest()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ------------------------------------------------------------ plugins ---

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var man plugins.Manifest
		if err := decodeJSON(r, &man); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		p, err := s.engine.Plugins().Install(man)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugin": p.Manifest, "loaded": p.Loaded})
		return
	}
	writeJSON(w, http.StatusOK, s.engine.Plugins().List())
}

func (s *Server) handlePluginByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodDelete {
		if err := s.engine.Plugins().Remove(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, "plugin removed")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/enable") {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		p, err := s.engine.Plugins().Enable(id, body.Enabled)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugin": p.Manifest})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/test") {
		var body struct {
			Input map[string]any `json:"input"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if body.Input == nil {
			body.Input = map[string]any{"domain": "example.com", "hook": "session", "core_id": "mihomo"}
		}
		out, err := s.engine.Plugins().RunSessionHooks(r.Context(), body.Input)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": out})
		return
	}
	writeErr(w, http.StatusNotFound, "unknown plugin action")
}

func (s *Server) handlePluginsReload(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.cfg.Storage.Dir, "plugins.d")
	n, problems := s.engine.Plugins().LoadManifestDir(dir)
	writeJSON(w, http.StatusOK, map[string]any{"loaded": n, "problems": problems, "dir": dir})
}

// -------------------------------------------------------------- SD-WAN ---

func (s *Server) handleSDWANStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.SDWAN().Status())
}

func (s *Server) handleSDWANLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.engine.SDWAN().Links())
		return
	}
	var link sdwan.Link
	if err := decodeJSON(r, &link); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.engine.SDWAN().AddLink(link); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	for _, stored := range s.engine.SDWAN().Links() {
		if stored.ID == link.ID {
			link = stored
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "link": link})
}

func (s *Server) handleSDWANLinkByID(w http.ResponseWriter, r *http.Request) {
	if !s.engine.SDWAN().RemoveLink(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, "link not found")
		return
	}
	writeOK(w, "link removed")
}

func (s *Server) handleSDWANAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "start":
		s.engine.SDWAN().Start(r.Context())
		writeOK(w, "SD-WAN probing started")
	case "stop":
		s.engine.SDWAN().Stop()
		writeOK(w, "SD-WAN probing stopped")
	case "failover":
		link, err := s.engine.SDWAN().ForceFailover(r.Context())
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "active": link})
	case "policy":
		var body struct {
			Policy sdwan.Policy `json:"policy"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		s.engine.SDWAN().SetPolicy(body.Policy)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "policy": body.Policy})
	default:
		writeErr(w, http.StatusNotFound, "unknown SD-WAN action: "+action)
	}
}

// ------------------------------------------------------------ tenants ---

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.engine.Tenants().List())
		return
	}
	var t tenants.Tenant
	if err := decodeJSON(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.engine.Tenants().Add(t); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if stored, ok := s.engine.Tenants().Get(t.ID); ok {
		t = stored
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tenant": t})
}

func (s *Server) handleTenantByID(w http.ResponseWriter, r *http.Request) {
	if !s.engine.Tenants().Remove(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, "tenant not found or protected")
		return
	}
	writeOK(w, "tenant removed")
}

// ---------------------------------------------------------- platforms ---

var buildTargets = []string{
	"linux/amd64", "linux/arm64", "linux/386", "linux/arm", "linux/mipsle",
	"windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64",
	"freebsd/amd64", "android/arm64", "ios/arm64",
}

func (s *Server) handlePlatforms(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"host":    runtime.GOOS + "/" + runtime.GOARCH,
		"go":      runtime.Version(),
		"targets": buildTargets,
		"clients": []string{"Android", "iOS", "iPadOS", "HarmonyOS", "Windows", "macOS", "Linux", "Android TV", "OpenWrt / 软路由"},
		"note":    "纯 Go 核心 + gomobile 绑定；原生 UI 客户端为骨架，需各平台 SDK 打包",
	})
}

// handlePlatformBuild performs REAL cross-compilation with go build.
func (s *Server) handlePlatformBuild(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Targets []string `json:"targets"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	targets := body.Targets
	if len(targets) == 0 {
		targets = []string{"linux/amd64", "linux/arm64", "windows/amd64", "darwin/arm64"}
	}
	root, _ := filepath.Abs(".")
	outDir := filepath.Join(root, "bin", "platforms")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type result struct {
		Target string `json:"target"`
		OK     bool   `json:"ok"`
		Output string `json:"output"`
		Path   string `json:"path,omitempty"`
	}
	results := make([]result, 0, len(targets))
	for _, t := range targets {
		parts := strings.Split(t, "/")
		if len(parts) != 2 {
			results = append(results, result{Target: t, OK: false, Output: "invalid target"})
			continue
		}
		goos, goarch := parts[0], parts[1]
		name := fmt.Sprintf("nggd-%s-%s", goos, goarch)
		if goos == "windows" {
			name += ".exe"
		}
		out := filepath.Join(outDir, name)
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./cmd/nggd")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		b, err := cmd.CombinedOutput()
		cancel()
		entry := result{Target: t, OK: err == nil, Output: "ok"}
		if err != nil {
			entry.Output = truncate(string(b), 400)
			if entry.Output == "" {
				entry.Output = err.Error()
			}
		} else {
			entry.Path = out
		}
		results = append(results, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "results": results})
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
