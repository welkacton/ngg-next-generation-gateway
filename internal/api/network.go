package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// Stage 2-3 REST surface for the Unified Network Engine. All operations go
// through Core Manager's Network() facade.

func (s *Server) handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	// Keep every field v2.6.3 already reports (running/stage/fail_closed/
	// failover/ingress/tun/dns/...), then add the capability matrix fields.
	status := s.engine.NetworkStatus()
	if ne := s.engine.Network(); ne != nil {
		cfg := ne.Config()
		status["default_core"] = cfg.DefaultCore
		status["direct_on_failure"] = cfg.DirectOnFailure
		status["bypass_tun"] = cfg.Egress.BypassTUN
		status["components"] = ne.Components()
	} else {
		// Honest degradation: no engine means no real running state to report.
		status["default_core"] = ""
		status["direct_on_failure"] = false
		status["bypass_tun"] = false
		status["components"] = []netengine.Component{}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleNetworkStart(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.NetworkStart(r.Context()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w, "NGG unified network engine started")
}

func (s *Server) handleNetworkStop(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.NetworkStop(r.Context()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w, "NGG unified network engine stopped")
}

func (s *Server) handleNetworkConfig(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, ne.Config())
		return
	}
	// Egress is patched field-by-field so a partial update never wipes an
	// unset value with a zero value.
	type egressPatch struct {
		Interface *string `json:"interface"`
		LocalAddr *string `json:"local_addr"`
		Mark      *int    `json:"mark"`
		BypassTUN *bool   `json:"bypass_tun"`
	}
	var body struct {
		FailClosed      *bool                    `json:"fail_closed"`
		Failover        *bool                    `json:"failover"`
		DefaultCore     *string                  `json:"default_core"`
		DirectOnFailure *bool                    `json:"direct_on_failure"`
		EgressMark      *int                     `json:"egress_mark"`
		BypassTUN       *bool                    `json:"bypass_tun"`
		Ingress         *netengine.IngressConfig `json:"ingress"`
		Dns             *netengine.DNSConfig     `json:"dns"`
		Tun             *netengine.TunConfig     `json:"tun"`
		Egress          *egressPatch             `json:"egress"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	// default_core must be empty (auto) or a registered kernel id. Validate
	// before touching cfg so a rejected request changes nothing at runtime.
	if body.DefaultCore != nil {
		core := strings.TrimSpace(*body.DefaultCore)
		if core != "" && !s.isRegisteredCore(core) {
			writeErr(w, http.StatusUnprocessableEntity, "default_core 不是已注册内核: "+core)
			return
		}
	}
	cfg := ne.Config()
	old := *cfg
	changed := []string{}
	needsRestart := []string{}
	warnings := []string{}

	if body.FailClosed != nil {
		cfg.FailClosed = *body.FailClosed
		changed = append(changed, "fail_closed")
	}
	if body.Failover != nil {
		cfg.Failover = *body.Failover
		changed = append(changed, "failover")
	}
	if body.DefaultCore != nil {
		cfg.DefaultCore = *body.DefaultCore
		changed = append(changed, "default_core")
	}
	if body.DirectOnFailure != nil {
		cfg.DirectOnFailure = *body.DirectOnFailure
		changed = append(changed, "direct_on_failure")
	}
	if body.EgressMark != nil {
		cfg.Egress.Mark = *body.EgressMark
		changed = append(changed, "egress.mark")
		needsRestart = append(needsRestart, "egress.mark")
	}
	if body.BypassTUN != nil {
		cfg.Egress.BypassTUN = *body.BypassTUN
		changed = append(changed, "egress.bypass_tun")
		needsRestart = append(needsRestart, "egress.bypass_tun")
	}
	if body.Ingress != nil {
		in := *body.Ingress
		if in.Enabled {
			if in.Port < 1 || in.Port > 65535 {
				writeErr(w, http.StatusBadRequest, "ingress.port must be 1..65535")
				return
			}
			if !isLoopbackListen(in.Listen) && (in.AuthUser == "" || in.AuthPass == "") {
				writeErr(w, http.StatusBadRequest, "non-loopback ingress requires auth_user and auth_pass")
				return
			}
		}
		cfg.Ingress = in
		changed = append(changed, "ingress")
		needsRestart = append(needsRestart, "ingress")
	}
	if body.Dns != nil {
		dns := *body.Dns
		if dns.Enabled && (dns.Port < 1 || dns.Port > 65535) {
			writeErr(w, http.StatusBadRequest, "dns.port must be 1..65535")
			return
		}
		cfg.DNS = dns
		changed = append(changed, "dns")
		needsRestart = append(needsRestart, "dns")
	}
	if body.Tun != nil {
		cfg.TUN = *body.Tun
		changed = append(changed, "tun")
		needsRestart = append(needsRestart, "tun")
		if cfg.TUN.AutoRoute && goruntimeGOOS() != "linux" {
			warnings = append(warnings, "tun.auto_route requires Linux with /dev/net/tun and CAP_NET_ADMIN; it will be ignored on this host")
		}
	}
	if body.Egress != nil {
		if body.Egress.Interface != nil {
			cfg.Egress.Interface = *body.Egress.Interface
			changed = append(changed, "egress.interface")
		}
		if body.Egress.LocalAddr != nil {
			cfg.Egress.LocalAddr = *body.Egress.LocalAddr
			changed = append(changed, "egress.local_addr")
		}
		if body.Egress.Mark != nil {
			cfg.Egress.Mark = *body.Egress.Mark
			changed = append(changed, "egress.mark")
		}
		if body.Egress.BypassTUN != nil {
			cfg.Egress.BypassTUN = *body.Egress.BypassTUN
			changed = append(changed, "egress.bypass_tun")
		}
		needsRestart = append(needsRestart, "egress")
	}

	persisted := false
	persistPath := filepath.Join(s.cfg.Storage.Dir, "network-config.json")
	if len(changed) > 0 {
		if _, err := s.persistNetworkConfig(cfg); err != nil {
			*cfg = old
			writeErr(w, http.StatusInternalServerError, "could not persist network config: "+err.Error())
			return
		}
		persisted = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "changed": changed, "requires_restart": needsRestart,
		"warnings": warnings, "persisted": persisted, "persist_path": persistPath, "config": cfg,
	})
}

func isLoopbackListen(host string) bool {
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func goruntimeGOOS() string { return runtime.GOOS }

// persistNetworkConfig is the ONE writer for the runtime network config.
// Both PUT /network/config and POST /network/components/toggle go through it,
// so there is never a second, divergent write path. Mode is 0600 and the
// replacement is atomic; callers roll back their in-memory cfg on error.
func (s *Server) persistNetworkConfig(cfg *netengine.Config) (string, error) {
	path := filepath.Join(s.cfg.Storage.Dir, "network-config.json")
	if err := atomicfile.WriteJSON(path, cfg); err != nil {
		return path, err
	}
	return path, nil
}

// isRegisteredCore implements the default_core whitelist check.
func (s *Server) isRegisteredCore(id string) bool {
	for _, d := range s.engine.Registry() {
		if string(d.ID) == id {
			return true
		}
	}
	return false
}

// POST /network/rules/delete — batch-delete rules. ids empty and all!=true
// is a 400; all=true clears the entire table. Returns the post-delete table
// plus the defaults so the UI never has to guess.
func (s *Server) handleNetworkRulesDelete(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	var body struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !body.All && len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids or all is required")
		return
	}
	scope := "selected"
	if body.All {
		scope = "all"
	}
	rs := ne.Rules()
	deleted := rs.RemoveMany(body.IDs, body.All)
	action, core := rs.Defaults()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "deleted": deleted, "scope": scope, "rules": rs.List(),
		"default_action": action, "default_core": core,
	})
}

// POST /network/components/toggle — flip one of the eight capability keys.
// Policy keys update the runtime cfg and take effect immediately. Component
// keys only persist the intended Enabled: we do NOT claim the running
// resource was started/stopped, and we return requires_restart=true.
func (s *Server) handleNetworkComponentToggle(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	var body struct {
		Key     string `json:"key"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	key := strings.TrimSpace(body.Key)
	cfg := ne.Config()
	old := *cfg
	strategy, label := false, ""
	switch key {
	case "fail_closed":
		cfg.FailClosed = body.Enabled
		strategy, label = true, "故障保护（断连不泄漏）"
	case "failover":
		cfg.Failover = body.Enabled
		strategy, label = true, "内核故障自动切换"
	case "direct_on_failure":
		cfg.DirectOnFailure = body.Enabled
		strategy, label = true, "故障直连"
	case "bypass_tun":
		cfg.Egress.BypassTUN = body.Enabled
		strategy, label = true, "绕过 TUN"
	case "ingress":
		cfg.Ingress.Enabled = body.Enabled
		label = "统一入口 Ingress"
	case "dns":
		cfg.DNS.Enabled = body.Enabled
		label = "DNS 引擎"
	case "tun":
		cfg.TUN.AutoRoute = body.Enabled
		label = "TUN 全局接管"
	case "egress":
		cfg.Egress.Enabled = body.Enabled
		label = "出口调度 Egress"
	default:
		writeErr(w, http.StatusBadRequest, "unknown component key: "+key)
		return
	}
	if _, err := s.persistNetworkConfig(cfg); err != nil {
		*cfg = old
		writeErr(w, http.StatusInternalServerError, "could not persist network config: "+err.Error())
		return
	}
	running := false
	for _, c := range ne.Components() {
		if c.Key == key {
			running = c.Running
			break
		}
	}
	requiresRestart := !strategy
	message := fmt.Sprintf("%s 的启用状态已设为 %v 并写入 network-config.json", label, body.Enabled)
	if strategy {
		message += "；策略类配置已即时生效，无需重启。"
	} else {
		message += "；该组件在运行时并未被启停，需重启网络引擎后才会按新配置加载。"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "key": key, "enabled": body.Enabled, "running": running,
		"requires_restart": requiresRestart, "message": message,
	})
}

func (s *Server) handleNetworkRules(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	rs := ne.Rules()
	switch r.Method {
	case http.MethodGet:
		action, core := rs.Defaults()
		writeJSON(w, http.StatusOK, map[string]any{"rules": rs.List(), "default_action": action, "default_core": core})
	case http.MethodPost:
		var rule netengine.Rule
		if err := decodeJSON(r, &rule); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if err := rs.Add(rule); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rule": rule})
	case http.MethodPut:
		var body struct {
			Rules         []netengine.Rule     `json:"rules"`
			DefaultAction netengine.RuleAction `json:"default_action"`
			DefaultCore   string               `json:"default_core"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if err := rs.Replace(body.Rules); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if body.DefaultAction != "" {
			rs.SetDefaults(body.DefaultAction, coreIDOf(body.DefaultCore))
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(body.Rules)})
	}
}

// handleNetworkRulesToggle enables/disables one rule, a selected batch, or
// the whole rule table. It only flips the Enabled flag; it never replaces or
// re-validates the rest of the table.
func (s *Server) handleNetworkRulesToggle(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	var body struct {
		IDs     []string `json:"ids"`
		All     bool     `json:"all"`
		Enabled bool     `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !body.All && len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids or all is required")
		return
	}
	scope := "selected"
	if body.All {
		scope = "all"
	}
	changed := ne.Rules().SetEnabled(body.IDs, body.All, body.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "changed": changed, "enabled": body.Enabled, "scope": scope,
	})
}

func coreIDOf(v string) contracts.CoreID { return contracts.CoreID(v) }

func (s *Server) handleNetworkRuleByID(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodPut {
		var rule netengine.Rule
		if err := decodeJSON(r, &rule); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		rule.ID = id
		if err := ne.Rules().Update(id, rule); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rule": rule})
		return
	}
	if !ne.Rules().Remove(id) {
		writeErr(w, http.StatusNotFound, "rule "+id+" not found")
		return
	}
	writeOK(w, "rule removed")
}

func (s *Server) handleNetworkRulesLoad(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Path == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	n, err := s.engine.Network().Rules().LoadRuleFile(body.Path)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "loaded": n, "path": body.Path})
}

func (s *Server) handleNetworkDNS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Network().DNS().Status())
}

func (s *Server) handleNetworkDNSResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.Type == "" {
		body.Type = "A"
	}
	ips, err := s.engine.Network().DNS().Resolve(r.Context(), body.Name, body.Type)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": body.Name, "type": body.Type, "answers": out})
}

func (s *Server) handleNetworkDNSHosts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hosts map[string]string `json:"hosts"`
		Block []string          `json:"block"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	s.engine.Network().DNS().SetHosts(body.Hosts, body.Block)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "hosts": len(body.Hosts), "block_added": len(body.Block)})
}

func (s *Server) handleNetworkSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Network().Sessions().List(200))
}

func (s *Server) handleNetworkEndpoints(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Network().Bridge().Endpoints())
}

func (s *Server) handleNetworkSelfTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CoreID string `json:"core_id"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := s.engine.Network().SelfTest(ctx, strings.TrimSpace(body.CoreID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleNetworkPacketTest(w http.ResponseWriter, r *http.Request) {
	// Feeds a real synthetic IPv4/UDP/DNS packet through PacketProcessor.
	pkt := syntheticDNSPacket()
	action, err := s.engine.Network().PacketProcessor().Process(r.Context(), pkt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	parsed, perr := s.engine.Network().PacketProcessor().Parse(pkt)
	writeJSON(w, http.StatusOK, map[string]any{
		"action": action, "parse_error": fmt.Sprint(perr),
		"packet": map[string]any{
			"version": parsed.Version, "protocol": parsed.Protocol,
			"src": parsed.Src.String(), "dst": parsed.Dst.String(),
			"src_port": parsed.SrcPort, "dst_port": parsed.DstPort, "is_dns": parsed.IsDNS,
		},
	})
}

// syntheticDNSPacket builds a real, checksum-free IPv4/UDP/53 packet for
// exercising the PacketProcessor classification path through the API.
func syntheticDNSPacket() []byte {
	b := make([]byte, 20+8+12)
	b[0] = 0x45
	b[2], b[3] = 0, byte(len(b))
	b[8], b[9] = 64, 17
	copy(b[12:16], []byte{10, 89, 0, 2})
	copy(b[16:20], []byte{10, 89, 0, 1})
	b[20], b[21] = 0x30, 0x39 // src 12345
	b[22], b[23] = 0, 53      // dst 53
	b[24], b[25] = 0, byte(8+12)
	// minimal DNS header: ID, flags, QDCOUNT=0
	b[28+4], b[28+5] = 0, 0
	return b
}

// ---------------------------------------------------------------- 接管全局 ---

// GET /network/global/status — real platform capability + current proxy state.
func (s *Server) handleGlobalStatus(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proxy": ne.GlobalProxyStatus(),
		"tun":   ne.TunCapabilities(),
	})
}

// POST /network/global/attach — Windows system-proxy takeover (user scope).
func (s *Server) handleGlobalAttach(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	if err := ne.AttachGlobalProxy(r.Context()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "proxy": ne.GlobalProxyStatus()})
}

// POST /network/global/detach — restore the operator's original proxy.
func (s *Server) handleGlobalDetach(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	if err := ne.DetachGlobalProxy(r.Context()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "proxy": ne.GlobalProxyStatus()})
}

// ------------------------------------------------------------ TUN 虚拟网卡 ---

// POST /network/tun/driver/install — downloads + verifies the official signed
// wintun build and places wintun.dll next to nggd.exe.
func (s *Server) handleTunDriverInstall(w http.ResponseWriter, r *http.Request) {
	res, err := netengine.InstallWintun(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := map[string]any{"ok": true, "driver": res}
	if ne := s.engine.Network(); ne != nil {
		out["capabilities"] = ne.TunCapabilities()
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /network/tun/status
func (s *Server) handleTunStatus(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adapter":      ne.TUNStatus(),
		"capabilities": ne.TunCapabilities(),
		"running":      ne.IsRunning(),
	})
}

// POST /network/tun/attach {"route_all":bool}
func (s *Server) handleTunAttach(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	var body struct {
		RouteAll bool `json:"route_all"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	if err := ne.AttachTUN(r.Context(), body.RouteAll); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "adapter": ne.TUNStatus(), "route_all": body.RouteAll,
		"message": "TUN 虚拟网卡已由 NGG 创建，TCP 流量进入统一调度器",
	})
}

// POST /network/tun/detach
func (s *Server) handleTunDetach(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	if err := ne.DetachTUN(r.Context()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "adapter": ne.TUNStatus()})
}
