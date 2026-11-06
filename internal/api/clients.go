package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ngg-project/ngg/internal/clients"
)

// 客户端接入（v2.7.0 · C 节）：iPhone / Windows / Mac 等设备的接入账号与配置分发。
//
// The handlers are deliberately thin: validation, credential minting and the
// atomic write all live in internal/clients, so there is exactly one write path
// and the HTTP layer cannot drift from the persisted file. Nothing here ever
// echoes a credential back except on create / rotate, where the operator must
// copy it once.
type clientHandlers struct {
	store *clients.Store
	// subscriptionBase optionally reports the public base URL of the agent
	// endpoint (e.g. https://vpn.example.com:9191). When it returns "" the
	// subscription URL stays relative so the browser resolves it against the
	// page it was served from — that is the honest answer when no public URL
	// has been configured.
	subscriptionBase func() string
}

func newClientHandlers(store *clients.Store) *clientHandlers {
	return &clientHandlers{store: store}
}

// SetSubscriptionBase wires the optional public base URL provider.
func (h *clientHandlers) SetSubscriptionBase(fn func() string) { h.subscriptionBase = fn }

// Register mounts the seven client-access routes on mux. The literal
// /clients/access patterns are more specific than /clients/{id}, so ServeMux
// resolves them without ambiguity.
func (h *clientHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/clients", h.handleClientsList)
	mux.HandleFunc("POST /api/v1/clients", h.handleClientCreate)
	mux.HandleFunc("GET /api/v1/clients/access", h.handleClientsAccessGet)
	mux.HandleFunc("PUT /api/v1/clients/access", h.handleClientsAccessPut)
	mux.HandleFunc("PUT /api/v1/clients/{id}", h.handleClientUpdate)
	mux.HandleFunc("DELETE /api/v1/clients/{id}", h.handleClientDelete)
	mux.HandleFunc("POST /api/v1/clients/{id}/rotate", h.handleClientRotate)
	mux.HandleFunc("GET /api/v1/clients/{id}/config", h.handleClientConfig)
}

// storeOrFail writes 503 when the store is missing (engine not wired).
func (h *clientHandlers) storeOrFail(w http.ResponseWriter) bool {
	if h.store == nil {
		writeErr(w, http.StatusServiceUnavailable, "客户端接入存储未初始化")
		return false
	}
	return true
}

// fail maps a package error to the documented status code.
func (h *clientHandlers) fail(w http.ResponseWriter, err error) {
	var verr *clients.ValidationError
	switch {
	case errors.As(err, &verr):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, clients.ErrNoPublicHost):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, clients.ErrUnknownFormat):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, clients.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// pathID sanitises the {id} route value. filepath.Base alone is not enough:
// Base("") == ".", so an empty id must be rejected explicitly.
func pathID(r *http.Request) string {
	id := filepath.Base(strings.TrimSpace(r.PathValue("id")))
	switch id {
	case "", ".", "/", "..", "\\":
		return ""
	}
	return id
}

// subscriptionURL builds the absolute URL when a public base is known and falls
// back to the relative /sub/<token> path otherwise.
func (h *clientHandlers) subscriptionURL(credential string) string {
	base := ""
	if h.subscriptionBase != nil {
		base = strings.TrimSpace(h.subscriptionBase())
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		return "/sub/" + credential
	}
	return base + "/sub/" + credential
}

func (h *clientHandlers) handleClientsList(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	list := h.store.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"clients": list,
		"count":   len(list),
		"access":  h.store.Access(),
	})
}

func (h *clientHandlers) handleClientCreate(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	var body clients.CreateRequest
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	view, credential, err := h.store.Create(body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": view, "credential": credential})
}

func (h *clientHandlers) handleClientUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	id := pathID(r)
	if id == "" {
		writeErr(w, http.StatusNotFound, clients.ErrNotFound.Error())
		return
	}
	var body clients.UpdateRequest
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	view, err := h.store.Update(id, body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *clientHandlers) handleClientDelete(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	id := pathID(r)
	if id == "" {
		writeErr(w, http.StatusNotFound, clients.ErrNotFound.Error())
		return
	}
	if err := h.store.Delete(id); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *clientHandlers) handleClientRotate(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	id := pathID(r)
	if id == "" {
		writeErr(w, http.StatusNotFound, clients.ErrNotFound.Error())
		return
	}
	view, credential, err := h.store.Rotate(id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": view, "credential": credential})
}

func (h *clientHandlers) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	id := pathID(r)
	if id == "" {
		writeErr(w, http.StatusNotFound, clients.ErrNotFound.Error())
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = clients.FormatSingBox
	}
	if !clients.ValidFormat(format) {
		writeErr(w, http.StatusBadRequest, "未知的配置格式 "+format+"：可选 singbox / mihomo / wireguard / socks")
		return
	}
	filename, content, err := h.store.ConfigFor(id, format)
	if err != nil {
		h.fail(w, err)
		return
	}
	c, ok := h.store.GetClient(id)
	if !ok {
		writeErr(w, http.StatusNotFound, clients.ErrNotFound.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"format":           format,
		"filename":         filename,
		"content":          content,
		"subscription_url": h.subscriptionURL(c.Credential),
	})
}

func (h *clientHandlers) handleClientsAccessGet(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	writeJSON(w, http.StatusOK, h.store.Access())
}

func (h *clientHandlers) handleClientsAccessPut(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrFail(w) {
		return
	}
	var body clients.AccessConfig
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	access, changed, err := h.store.SaveAccess(body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access": access, "changed": changed})
}
