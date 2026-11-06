package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/ngg-project/ngg/internal/gateway"
)

// 网关中心：WAN / LAN / 防火墙的声明式配置 + 真实网卡状态。
//
// The three endpoints are thin on purpose: validation, password handling and
// persistence all live in internal/gateway, so there is exactly one write path
// and the HTTP layer cannot drift from the persisted file.

// gatewayOrFail returns the shared store or writes a 503 and returns nil.
func (s *Server) gatewayOrFail(w http.ResponseWriter) *gateway.Store {
	if s.gateway == nil {
		writeErr(w, http.StatusServiceUnavailable, "网关配置存储未初始化")
		return nil
	}
	return s.gateway
}

// handleGatewayConfigGet returns the saved config with the WAN password
// replaced by password_set. The secret never leaves the process.
func (s *Server) handleGatewayConfigGet(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	cfg := st.Get()
	updated := ""
	if ts := st.UpdatedAt(); !ts.IsZero() {
		updated = ts.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":     cfg.Public(),
		"updated_at": updated,
	})
}

// handleGatewayConfigPut validates, merges the stored password and persists
// atomically. A rejected payload returns 422 and writes nothing; a write
// failure returns 500 and keeps the previous in-memory config.
func (s *Server) handleGatewayConfigPut(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	var body gateway.Config
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	saved, changed, warnings, err := st.Save(body)
	if err != nil {
		var verr *gateway.ValidationError
		if errors.As(err, &verr) {
			writeErr(w, http.StatusUnprocessableEntity, verr.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":     saved.Public(),
		"updated_at": st.UpdatedAt().UTC().Format(time.RFC3339),
		"changed":    changed,
		"persisted":  true,
		"warnings":   warnings,
	})
}

// handleGatewayStatus reports what the host really looks like right now.
func (s *Server) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	writeJSON(w, http.StatusOK, st.Status())
}
