package api

import (
	"net/http"
	"strings"

	"github.com/ngg-project/ngg/internal/users"
)

// 配置引擎 · 用户管理（本地账号目录）
//
// Status is derived by users.User.Status from enabled / expires_at / quota.
// Usage only grows through AddUsage (data plane), never from a UI-supplied
// number; the reset endpoint really zeroes the counter.

func (s *Server) usersOrErr(w http.ResponseWriter) (*users.Registry, bool) {
	reg := s.engine.Users()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "user registry not initialized")
		return nil, false
	}
	return reg, true
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.usersOrErr(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		list := reg.List(status)
		if list == nil {
			list = []users.User{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"users": list, "count": len(list), "stats": reg.Stats(), "path": reg.Path(),
		})
	case http.MethodPost:
		var u users.User
		if err := decodeJSON(r, &u); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Add(u)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": saved})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

func (s *Server) handleUserStats(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.usersOrErr(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": reg.Stats()})
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.usersOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		u, exists := reg.Get(id)
		if !exists {
			writeErr(w, http.StatusNotFound, "user "+id+" not found")
			return
		}
		writeJSON(w, http.StatusOK, u)
	case http.MethodPut:
		var u users.User
		if err := decodeJSON(r, &u); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Update(id, u)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": saved})
	case http.MethodDelete:
		if err := reg.Remove(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, "user removed")
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// POST /users/{id}/reset-usage — real reset, audited by UpdatedAt.
func (s *Server) handleUserResetUsage(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.usersOrErr(w)
	if !ok {
		return
	}
	u, err := reg.ResetUsage(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
}

// POST /users/{id}/usage {"bytes":N} — data-plane accounting entry point.
// Requires the same Bearer token as every other route; there is no endpoint
// that fabricates a "used" number for display.
func (s *Server) handleUserUsage(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.usersOrErr(w)
	if !ok {
		return
	}
	var body struct {
		Bytes uint64 `json:"bytes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := reg.AddUsage(r.PathValue("id"), body.Bytes); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	u, _ := reg.Get(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u})
}
