package api

import (
	"errors"
	"net/http"

	"github.com/ngg-project/ngg/internal/subscription"
	"github.com/ngg-project/ngg/internal/users"
)

// 配置引擎 · 用户订阅（节点 + 协议 + 规则集 + DNS 一体化的客户端订阅）
//
// The handlers are thin on purpose: the binding is validated and stored by
// internal/users, and the client config is produced by internal/subscription.
// Nothing here fabricates a value — an unbound user or a node without a port /
// credential is reported as 422 with the real reason.
type subscriptionHandlers struct {
	users    *users.Registry
	renderer *subscription.Renderer
}

func newSubscriptionHandlers(store *users.Registry, r *subscription.Renderer) *subscriptionHandlers {
	return &subscriptionHandlers{users: store, renderer: r}
}

// Register mounts the three subscription routes. They are separate from the
// generic /users/{id} handlers so the binding can never be changed by the plain
// user editor.
func (h *subscriptionHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/users/{id}/subscription", h.handleGet)
	mux.HandleFunc("PUT /api/v1/users/{id}/subscription", h.handlePut)
	mux.HandleFunc("POST /api/v1/users/{id}/subscription/rotate", h.handleRotate)
}

func (h *subscriptionHandlers) ready(w http.ResponseWriter) bool {
	if h.users == nil || h.renderer == nil {
		writeErr(w, http.StatusServiceUnavailable, "订阅服务未初始化")
		return false
	}
	return true
}

// fail maps a package error onto the documented status code. The message is
// passed through verbatim: it names the real missing field.
func (h *subscriptionHandlers) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrNotFound), errors.Is(err, subscription.ErrUnknownUser):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, users.ErrInvalidSubscription),
		errors.Is(err, subscription.ErrNotBound),
		errors.Is(err, subscription.ErrNodeIncomplete),
		errors.Is(err, subscription.ErrUnsupportedProtocol),
		errors.Is(err, subscription.ErrUnknownTarget):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// userOrFail sanitises {id} and resolves it. filepath.Base guards against a
// path-shaped id; an empty result is a 404 rather than a lookup of "".
func (h *subscriptionHandlers) userOrFail(w http.ResponseWriter, r *http.Request) (users.User, bool) {
	id := pathID(r)
	if id == "" {
		writeErr(w, http.StatusNotFound, "user not found")
		return users.User{}, false
	}
	u, ok := h.users.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "user "+id+" not found")
		return users.User{}, false
	}
	return u, true
}

func (h *subscriptionHandlers) handleGet(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u, ok := h.userOrFail(w, r)
	if !ok {
		return
	}
	// A subscription URL is only useful once the user has one; the first read
	// mints it so the console can show the address immediately.
	if _, err := h.users.EnsureSubscriptionToken(u.ID); err != nil {
		h.fail(w, err)
		return
	}
	payload, err := h.renderer.Build(u.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

type subscriptionBinding struct {
	NodeID     string   `json:"node_id"`
	Protocol   string   `json:"protocol"`
	RuleSetIDs []string `json:"rule_set_ids"`
	DNSProfile string   `json:"dns_profile"`
}

func (h *subscriptionHandlers) handlePut(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u, ok := h.userOrFail(w, r)
	if !ok {
		return
	}
	var body subscriptionBinding
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	protocol := users.NormalizeProtocol(body.Protocol)
	if !users.ValidProtocol(protocol) {
		writeErr(w, http.StatusUnprocessableEntity, "协议必须是 vless / trojan / shadowsocks / socks5 / http")
		return
	}
	dnsProfile := users.NormalizeDNSProfile(body.DNSProfile)
	if !users.ValidDNSProfile(dnsProfile) {
		writeErr(w, http.StatusUnprocessableEntity, "DNS 策略必须是 system / gateway / off")
		return
	}
	if _, err := h.users.SetSubscription(u.ID, body.NodeID, protocol, dnsProfile, body.RuleSetIDs); err != nil {
		h.fail(w, err)
		return
	}
	if _, err := h.users.EnsureSubscriptionToken(u.ID); err != nil {
		h.fail(w, err)
		return
	}
	// Build reports "node missing a port / credential" as 422; the binding is
	// already saved, so the operator can fix the node and read it again.
	payload, err := h.renderer.Build(u.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *subscriptionHandlers) handleRotate(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u, ok := h.userOrFail(w, r)
	if !ok {
		return
	}
	if _, err := h.users.RotateSubscriptionToken(u.ID); err != nil {
		h.fail(w, err)
		return
	}
	payload, err := h.renderer.Build(u.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
