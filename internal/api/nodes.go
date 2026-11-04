package api

import (
	"net/http"

	"github.com/ngg-project/ngg/internal/nodes"
)

// Config Engine: inbound / outbound node registry (encrypted protocol nodes).
// All mutations are validated and persisted to data/nodes-config.json.

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	if r.Method == http.MethodPost {
		var n nodes.Node
		if err := decodeJSON(r, &n); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Add(n)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": saved})
		return
	}
	kind := nodes.Kind(r.URL.Query().Get("kind"))
	if kind != "" && kind != nodes.KindInbound && kind != nodes.KindOutbound {
		writeErr(w, http.StatusBadRequest, "kind must be inbound or outbound")
		return
	}
	list := reg.List(kind)
	if list == nil {
		list = []nodes.Node{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": list, "count": len(list), "path": reg.Path()})
}

func (s *Server) handleNodeProtocols(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	kind := nodes.Kind(r.URL.Query().Get("kind"))
	if kind != "" && kind != nodes.KindInbound && kind != nodes.KindOutbound {
		writeErr(w, http.StatusBadRequest, "kind must be inbound or outbound")
		return
	}
	writeJSON(w, http.StatusOK, reg.Protocols(kind))
}

func (s *Server) handleNodeExport(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": reg.List(""), "count": len(reg.List(""))})
}

func (s *Server) handleNodeImport(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	var body struct {
		Nodes []nodes.Node `json:"nodes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	n, err := reg.Replace(body.Nodes)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported": n})
}

func (s *Server) handleNodeByID(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		n, ok := reg.Get(id)
		if !ok {
			writeErr(w, http.StatusNotFound, "node "+id+" not found")
			return
		}
		writeJSON(w, http.StatusOK, n)
	case http.MethodPut:
		var n nodes.Node
		if err := decodeJSON(r, &n); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Update(id, n)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": saved})
	case http.MethodDelete:
		if err := reg.Remove(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, "node removed")
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// handleNodeConfig generates a REAL kernel-native config fragment for a node.
// This is what makes "configure an encrypted protocol" actionable: the output
// is format-validated and can be merged into a kernel config by an operator.
func (s *Server) handleNodeConfig(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	n, ok := reg.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	var body struct {
		Kernel string `json:"kernel"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	content, format, err := nodes.GenerateKernelConfig(n, body.Kernel)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "node_id": n.ID, "kernel": body.Kernel,
		"format": format, "content": string(content), "validated": true,
		"note": "这是内核原生配置片段，需合并进内核完整配置后由内核原生校验器校验；NGG 不重写内核协议栈",
	})
}
