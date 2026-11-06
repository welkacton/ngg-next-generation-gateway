package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/nodes"
	"github.com/ngg-project/ngg/pkg/contracts"
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

// handleParseNative parses a COMPLETE kernel native config (the kind shown in
// the operator's Xray/sing-box JSON) into NGG nodes + routing bindings.
// It only reads; nothing is applied until the operator confirms.
func (s *Server) handleParseNative(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kernel  string `json:"kernel"`
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := nodes.ParseNative(body.Kernel, body.Content)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleFromNative parses and merges the detected nodes into the registry.
func (s *Server) handleFromNative(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	var body struct {
		Kernel  string `json:"kernel"`
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := nodes.ParseNative(body.Kernel, body.Content)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	n, err := reg.Merge(result.Nodes)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "imported": n, "kernel": result.Kernel,
		"detected": result.Detected, "routing": result.Routing, "warnings": result.Warnings,
	})
}

// handleNodeTemplates exposes the shipped native-config examples so the UI can
// load one with a single click. Content is the same as configs/examples/*.json.
func (s *Server) handleNodeTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, nodes.Templates())
}

// ---------------------------------------------------------- 节点运行时端点 ---

// GET /nodes/{id}/metrics — REAL process sample + REAL session traffic
// aggregation. When the bound kernel has no running process we honestly
// report source:"unavailable" with zeroed numbers and an explanatory note;
// no CPU/memory value is ever invented.
func (s *Server) handleNodeMetrics(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	id := r.PathValue("id")
	node, ok := reg.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "node "+id+" not found")
		return
	}
	out := nodes.NodeMetrics{OK: true, NodeID: id, SampledAt: time.Now().UTC().Format(time.RFC3339)}

	var cpu float64
	var mem uint64
	haveProc := false
	if node.CoreID != "" {
		if st, err := s.engine.Stats(contracts.CoreID(node.CoreID)); err == nil {
			cpu, mem, haveProc = st.CPUPercent, st.MemoryRSS, true
		}
	}

	var bytesIn, bytesOut uint64
	var active int
	if ne := s.engine.Network(); ne != nil {
		addr := strings.TrimSpace(node.Address)
		// SessionRec embeds a sync.Mutex: iterate by index so we never copy it.
		sessions := ne.Sessions().List(1000)
		for i := range sessions {
			rec := &sessions[i]
			matched := false
			if addr != "" {
				if rec.Session.Src != nil && strings.Contains(rec.Session.Src.String(), addr) {
					matched = true
				}
				if !matched && rec.Session.Dst != nil && strings.Contains(rec.Session.Dst.String(), addr) {
					matched = true
				}
			}
			if !matched {
				continue
			}
			bytesIn += rec.BytesIn
			bytesOut += rec.BytesOut
			if rec.State == "active" || rec.State == "establishing" {
				active++
			}
		}
	}

	if !haveProc {
		out.Source = "unavailable"
		out.Note = fmt.Sprintf("节点绑定的内核 %q 当前没有可采样的进程（未运行或未注册），CPU/内存不可用；为避免伪造，所有数值返回 0。", node.CoreID)
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Source = "local"
	out.CPUPercent, out.MemoryRSS = cpu, mem
	out.BytesIn, out.BytesOut, out.ActiveSessions = bytesIn, bytesOut, active
	out.Note = fmt.Sprintf("CPU/内存来自内核 %q 的真实进程采样；bytes_in/out 与 active_sessions 来自 netengine 会话中匹配地址 %q 的真实聚合。", node.CoreID, node.Address)
	writeJSON(w, http.StatusOK, out)
}

// POST /nodes/keys/generate — private material is returned ONLY in the
// response; it is never logged or written to disk here.
func (s *Server) handleNodeKeysGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	kp, err := nodes.GenerateKeyPair(strings.TrimSpace(body.Kind))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, kp)
}

// POST /nodes/keys/pair — generate one pair, write private_key /
// peer_public_key into the two nodes' Params, persist nodes-config.json.
func (s *Server) handleNodeKeysPair(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	var body struct {
		InboundID  string `json:"inbound_id"`
		OutboundID string `json:"outbound_id"`
		Kind       string `json:"kind"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	inID, outID := strings.TrimSpace(body.InboundID), strings.TrimSpace(body.OutboundID)
	if inID == "" || outID == "" {
		writeErr(w, http.StatusBadRequest, "inbound_id and outbound_id are required")
		return
	}
	for _, id := range []string{inID, outID} {
		if _, ok := reg.Get(id); !ok {
			writeErr(w, http.StatusNotFound, "node "+id+" not found")
			return
		}
	}
	kind := strings.TrimSpace(body.Kind)
	if kind == "" {
		kind = "wireguard"
	}
	kp, err := nodes.GenerateKeyPair(kind)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	result := nodes.NewPairResult(kind, kp, inID, outID)
	writeParam := func(n *nodes.Node, key, value string) {
		if value == "" {
			return
		}
		if n.Params == nil {
			n.Params = map[string]any{}
		}
		n.Params[key] = value
	}
	if _, err := reg.Patch(inID, func(n *nodes.Node) {
		writeParam(n, "private_key", kp.PrivateKey)
		writeParam(n, "uuid", kp.UUID)
		writeParam(n, "password", kp.Password)
	}); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "persist inbound node: "+err.Error())
		return
	}
	if _, err := reg.Patch(outID, func(n *nodes.Node) {
		writeParam(n, "peer_public_key", kp.PublicKey)
		writeParam(n, "uuid", kp.UUID)
		writeParam(n, "password", kp.Password)
	}); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "persist outbound node: "+err.Error())
		return
	}
	result.Persisted = true
	writeJSON(w, http.StatusOK, result)
}

// POST /nodes/{id}/probe — REAL TCP dial, then persist status / last_seen.
func (s *Server) handleNodeProbe(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	id := r.PathValue("id")
	node, ok := reg.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "node "+id+" not found")
		return
	}
	var body struct {
		TimeoutMS int `json:"timeout_ms"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	res := nodes.NodeProbeResult{
		OK: true, NodeID: id, Address: node.Address, Port: node.Port, CheckedAt: checkedAt,
	}
	if strings.TrimSpace(node.Address) == "" || node.Port <= 0 {
		res.Error = "node address or port is not configured"
	} else {
		lat, err := nodes.Probe(node.Address, node.Port, time.Duration(body.TimeoutMS)*time.Millisecond)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Reachable = true
			res.LatencyMs = lat
		}
	}
	status := "offline"
	if res.Reachable {
		status = "online"
	}
	if _, err := reg.Patch(id, func(n *nodes.Node) {
		n.Status = status
		if res.Reachable {
			n.LastSeen = checkedAt
		}
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not persist probe result: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// PUT /nodes/{id}/config-file — write <data>/node-configs/<id>.<ext> (0600)
// and record the path on the node.
func (s *Server) handleNodeConfigFile(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.Nodes()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "node registry not initialized")
		return
	}
	id := r.PathValue("id")
	if _, ok := reg.Get(id); !ok {
		writeErr(w, http.StatusNotFound, "node "+id+" not found")
		return
	}
	var body struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	path, nbytes, validated, err := nodes.WriteConfigFile(s.cfg.Storage.Dir, id, body.Format, body.Content)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not write config file: "+err.Error())
		return
	}
	if _, err := reg.Patch(id, func(n *nodes.Node) { n.ConfigFile = path }); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not persist node config_file: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodes.NodeConfigFileResult{
		OK: true, NodeID: id, Path: path, Bytes: nbytes, Validated: validated,
	})
}
