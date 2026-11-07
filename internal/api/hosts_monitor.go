package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/hostmon"
)

// 配置引擎 · 节点系统信息（SSH 只读采集）
//
// The collector lives in internal/hostmon. Everything this file returns is a
// value that was actually read from the remote machine; an unconfigured node
// produces no entry at all so the console can show 「未采集」 instead of a
// fabricated zero. Credentials are never returned: Configs() is the redacted
// view, and the request body is the only place a password ever appears.

// hostMonitorInterval is the collector's polling period and the value the
// console uses for its own refresh timer.
const hostMonitorInterval = 30 * time.Second

func (s *Server) monitorOrErr(w http.ResponseWriter) (*hostmon.Monitor, bool) {
	if s.monitor == nil {
		writeErr(w, http.StatusServiceUnavailable, "节点系统信息采集未初始化")
		return nil, false
	}
	return s.monitor, true
}

// monitorPayload is the single response shape shared by every monitor route.
func monitorPayload(m *hostmon.Monitor) map[string]any {
	all := m.All()
	configs := m.Configs()

	// A preset entry has an empty HostID and is matched by address. Normalize
	// here so the collector's internal map key can never leak into the wire
	// contract as a fake host id.
	presetAddresses := map[string]bool{}
	for _, c := range configs {
		if c.HostID == "" && c.Host != "" {
			presetAddresses[c.Host] = true
		}
	}
	metrics := make([]hostmon.Metrics, 0, len(all))
	for _, v := range all {
		if presetAddresses[v.HostID] {
			v.HostID = ""
		}
		metrics = append(metrics, v)
	}
	return map[string]any{
		"metrics":      metrics,
		"configs":      configs,
		"interval_sec": int(hostMonitorInterval.Seconds()),
	}
}

// handleHostMonitor serves GET /api/v1/hosts/monitor.
func (s *Server) handleHostMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorOrErr(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, monitorPayload(m))
}

type hostMonitorInput struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
}

// handleHostMonitorByID serves PUT/DELETE /api/v1/hosts/{id}/monitor.
func (s *Server) handleHostMonitorByID(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorOrErr(w)
	if !ok {
		return
	}
	id := pathID(r)
	if id == "" {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body hostMonitorInput
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		host := strings.TrimSpace(body.Host)
		if host == "" {
			// The node registry already knows the address; do not make the
			// operator type it twice.
			if reg := s.engine.Hosts(); reg != nil {
				if h, found := reg.Get(id); found {
					host = h.Address
				}
			}
		}
		if host == "" {
			writeErr(w, http.StatusUnprocessableEntity, "请填写 SSH 主机地址")
			return
		}
		port := body.Port
		if port == 0 {
			port = 22
		}
		if port < 1 || port > 65535 {
			writeErr(w, http.StatusUnprocessableEntity, "SSH 端口需在 1..65535")
			return
		}
		user := strings.TrimSpace(body.User)
		if user == "" {
			user = "root"
		}
		if err := m.Set(hostmon.SSHConfig{
			HostID:     id,
			Host:       host,
			Port:       port,
			User:       user,
			Password:   body.Password,
			PrivateKey: body.PrivateKey,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, monitorPayload(m))

	case http.MethodDelete:
		// A preset entry has no HostID. Resolve it by the node's address so
		// 「停用监控」 works for both kinds of entry.
		address := ""
		if reg := s.engine.Hosts(); reg != nil {
			if h, found := reg.Get(id); found {
				address = h.Address
			}
		}
		removedByID := false
		for _, c := range m.Configs() {
			if c.HostID == id {
				removedByID = true
				break
			}
		}
		// DELETE 是幂等的：配置本来就不存在时也算成功。
		if err := m.Remove(id); err != nil && !errors.Is(err, hostmon.ErrNotFound) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !removedByID && address != "" {
			if err := m.Remove(address); err != nil && !errors.Is(err, hostmon.ErrNotFound) {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, monitorPayload(m))

	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// handleHostMonitorRefresh serves POST /api/v1/hosts/{id}/monitor/refresh.
func (s *Server) handleHostMonitorRefresh(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorOrErr(w)
	if !ok {
		return
	}
	id := pathID(r)
	if id == "" {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	key := id
	configured := false
	for _, c := range m.Configs() {
		if c.HostID == id {
			configured = true
			break
		}
	}
	if !configured {
		// Preset entry: fall back to the address the collector keyed it by.
		if reg := s.engine.Hosts(); reg != nil {
			if h, found := reg.Get(id); found && h.Address != "" {
				key = h.Address
			}
		}
	}
	metrics, err := m.Refresh(key)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}
