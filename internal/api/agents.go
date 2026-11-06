package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ngg-project/ngg/internal/agent"
)

// 远程代理节点（Agent 插件）的控制面端点。
//
// 这些路由挂在需要登录的本地控制 API 下；公网上报走 internal/agent 的
// 独立监听（默认 0.0.0.0:9191），与这里完全分离。
//
// SECURITY: token 明文只在创建 / 轮换响应里出现一次，服务端只保存 SHA-256。
// install 脚本在事后生成时使用 placeholder，避免二次泄露。
type agentHandlers struct {
	store *agent.Store
	cfg   *agent.ServerStore
}

func newAgentHandlers(agents *agent.Store, cfg *agent.ServerStore) *agentHandlers {
	return &agentHandlers{store: agents, cfg: cfg}
}

// Register wires every v2.7.0 agent route. Literal /agents/server is
// registered before the wildcard for readability; Go 1.22+ ServeMux resolves
// the more specific literal pattern regardless of registration order.
func (h *agentHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agents", h.list)
	mux.HandleFunc("POST /api/v1/agents", h.create)
	mux.HandleFunc("GET /api/v1/agents/server", h.getServer)
	mux.HandleFunc("PUT /api/v1/agents/server", h.putServer)
	mux.HandleFunc("GET /api/v1/agents/{id}", h.get)
	mux.HandleFunc("PUT /api/v1/agents/{id}", h.update)
	mux.HandleFunc("DELETE /api/v1/agents/{id}", h.remove)
	mux.HandleFunc("POST /api/v1/agents/{id}/rotate-token", h.rotate)
	mux.HandleFunc("GET /api/v1/agents/{id}/metrics", h.metrics)
	mux.HandleFunc("GET /api/v1/agents/{id}/install", h.install)
}

// ready guards against a daemon started without the agent subsystem.
func (h *agentHandlers) ready(w http.ResponseWriter) bool {
	if h == nil || h.store == nil || h.cfg == nil {
		writeErr(w, http.StatusServiceUnavailable, "agent registry not initialized")
		return false
	}
	return true
}

func (h *agentHandlers) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, agent.ErrInvalid):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, agent.ErrUnauthorized):
		writeErr(w, http.StatusUnauthorized, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// cleanID strips any path traversal attempt from a path parameter.
func cleanID(r *http.Request) string {
	id := filepath.Base(strings.TrimSpace(r.PathValue("id")))
	if id == "." || id == "/" || id == ".." {
		return ""
	}
	return id
}

func (h *agentHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	list := h.store.List()
	if list == nil {
		list = []agent.AgentView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agents": list, "count": len(list), "server": h.cfg.Get(),
	})
}

func (h *agentHandlers) create(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Address string `json:"address"`
		Port    int    `json:"port"`
		Note    string `json:"note"`
		Role    string `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	view, token, err := h.store.Create(body.Name, body.Address, body.Port, body.Note, body.Role)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.enrollPayload(view, token))
}

// enrollPayload is the shared shape of POST /agents and rotate-token. It is a
// superset of the contract (agent + token + config_yaml + install) so both the
// documented response and the typed frontend client work unchanged.
func (h *agentHandlers) enrollPayload(view agent.AgentView, token string) map[string]any {
	p := agent.InstallParams{
		Server:      h.cfg.PublicBase(),
		AgentID:     view.ID,
		Token:       token,
		IntervalSec: agent.DefaultIntervalSec,
	}
	return map[string]any{
		"agent":       view,
		"token":       token,
		"config_yaml": agent.ConfigYAML(p),
		"install":     agent.Scripts(p),
	}
}

func (h *agentHandlers) get(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := cleanID(r)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	view, ok := h.store.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "远程节点 "+id+" 不存在")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *agentHandlers) update(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := cleanID(r)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	var patch agent.Patch
	if err := decodeJSON(r, &patch); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	view, err := h.store.Update(id, patch)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *agentHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := cleanID(r)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	if err := h.store.Delete(id); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *agentHandlers) rotate(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := cleanID(r)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	token, err := h.store.RotateToken(id)
	if err != nil {
		h.fail(w, err)
		return
	}
	view, ok := h.store.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "远程节点 "+id+" 不存在")
		return
	}
	writeJSON(w, http.StatusOK, h.enrollPayload(view, token))
}

func (h *agentHandlers) metrics(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := cleanID(r)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	if _, ok := h.store.Get(id); !ok {
		writeErr(w, http.StatusNotFound, "远程节点 "+id+" 不存在")
		return
	}
	limit := 60
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "limit 必须是正整数")
			return
		}
		limit = n
	}
	samples := h.store.Samples(id, limit)
	if samples == nil {
		samples = []agent.AgentSample{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": samples})
}

func (h *agentHandlers) install(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := cleanID(r)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	view, ok := h.store.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "远程节点 "+id+" 不存在")
		return
	}
	osName := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("os")))
	if osName == "" {
		osName = "linux"
	}
	// The plaintext token is not stored, so a script fetched later can only
	// carry a placeholder. Say it explicitly instead of emitting a broken one.
	script, err := agent.Script(osName, agent.InstallParams{
		Server: h.cfg.PublicBase(), AgentID: view.ID, IntervalSec: agent.DefaultIntervalSec,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "os 必须是 windows / linux / docker")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"os": osName, "script": script})
}

func (h *agentHandlers) getServer(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	writeJSON(w, http.StatusOK, h.cfg.Get())
}

func (h *agentHandlers) putServer(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body agent.ServerConfig
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	old := h.cfg.Get()
	saved, warnings, err := h.cfg.Save(body)
	if err != nil {
		h.fail(w, err)
		return
	}
	if warnings == nil {
		warnings = []string{}
	}
	// Port / listen / enable changes only take effect when the listener is
	// rebuilt; the response never pretends the new address is already live.
	requiresRestart := old.Enabled != saved.Enabled || old.Listen != saved.Listen || old.Port != saved.Port
	writeJSON(w, http.StatusOK, map[string]any{
		"server": saved, "requires_restart": requiresRestart, "warnings": warnings,
	})
}
