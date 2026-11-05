package api

import (
	"net/http"
	"strings"

	"github.com/ngg-project/ngg/internal/hosts"
)

// Config Engine: remote host registry (VPS / PC / soft router) for the
// inbound & outbound consoles. A host can be added from anywhere and freely
// assigned as inbound, outbound or both. Online state comes from a REAL TCP
// dial, never from a click counter.

func (s *Server) hostsOrErr(w http.ResponseWriter) (*hosts.Registry, bool) {
	reg := s.engine.Hosts()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "host registry not initialized")
		return nil, false
	}
	return reg, true
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		role := strings.TrimSpace(r.URL.Query().Get("role"))
		if role != "" && role != "all" && !hosts.ValidRole(hosts.Role(role)) {
			writeErr(w, http.StatusBadRequest, "role must be inbound / outbound / both / all")
			return
		}
		views := reg.Views(role)
		if views == nil {
			views = []hosts.HostView{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hosts": views, "count": len(views), "stats": reg.Stats(), "path": reg.Path(),
		})
	case http.MethodPost:
		var h hosts.Host
		if err := decodeJSON(r, &h); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Add(r.Context(), h)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host": saved})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// handleHostStats merges registry counts with REAL NGG network-engine metrics.
// Nothing here is synthesised: if the network engine is off, the counters are
// reported as nil and the UI shows "未采集".
func (s *Server) handleHostStats(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	out := map[string]any{"registry": reg.Stats()}
	status := s.engine.NetworkStatus()
	out["network_running"] = status["running"]
	if m, ok := status["metrics"].(map[string]any); ok {
		out["bytes_in"] = m["bytes_in"]
		out["bytes_out"] = m["bytes_out"]
		out["active"] = m["active"]
		out["connections"] = m["connections"]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleHostProbeAll(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	res := reg.ProbeAll(r.Context())
	online := 0
	for _, p := range res {
		if p.Online {
			online++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "checked": len(res), "online": online, "results": res,
	})
}

func (s *Server) handleHostExport(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	list := reg.List("")
	writeJSON(w, http.StatusOK, map[string]any{"hosts": list, "count": len(list)})
}

func (s *Server) handleHostImport(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	var body struct {
		Hosts []hosts.Host `json:"hosts"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	n, err := reg.Replace(body.Hosts)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported": n})
}

func (s *Server) handleHostByID(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		h, exists := reg.Get(id)
		if !exists {
			writeErr(w, http.StatusNotFound, "host "+id+" not found")
			return
		}
		writeJSON(w, http.StatusOK, h)
	case http.MethodPut:
		var h hosts.Host
		if err := decodeJSON(r, &h); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Update(id, h)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host": saved})
	case http.MethodDelete:
		if err := reg.Remove(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, "host removed")
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

func (s *Server) handleHostProbe(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	res, err := reg.ProbeOne(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "probe": res})
}

func (s *Server) handleHostRole(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	var body struct {
		Role hosts.Role `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	h, err := reg.SetRole(r.PathValue("id"), body.Role)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host": h})
}
