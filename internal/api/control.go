package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/ngg-project/ngg/internal/gateway"
	"github.com/ngg-project/ngg/internal/netengine"
)

// The Control Center merges what used to be two pages (网关总控 + 接管全局)
// into one ordered flow:
//
//	① 启动 NGG 虚拟网卡 → ② 把全部网卡的全部流量强制接管进 NGG 虚拟网络
//	→ ③ 在 WAN 出口里选择物理网卡，让流量经本地运营商出去
//
// Every step reports what really happened on this host; nothing is inferred
// from "saved" state alone.

type controlStep struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Order  int    `json:"order"`
	Status string `json:"status"` // ok | warn | fail | skip
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

func step(key, title string, order int, status, detail string) controlStep {
	return controlStep{Key: key, Title: title, Order: order, Status: status, Detail: detail}
}

// controlState assembles the live picture for the console.
func (s *Server) controlState() map[string]any {
	ne := s.engine.Network()
	st := s.gateway
	if ne == nil || st == nil {
		return nil
	}
	cfg := st.Get()
	m := cfg.Master
	ncfg := ne.Config()
	tun := ne.TUNStatus()
	dp := st.Datapath()
	adapters := netengine.ListAdapters(ncfg.TUN.Name)

	open, _ := tun["open"].(bool)
	adapterName := m.AdapterName
	if adapterName == "" {
		adapterName = ncfg.TUN.Name
	}
	adapterUp := false
	for _, a := range adapters {
		if strings.EqualFold(a.Name, adapterName) && a.Up {
			adapterUp = true
			break
		}
	}

	steps := []controlStep{}
	// ① 虚拟网卡
	switch {
	case open && adapterUp:
		steps = append(steps, step("adapter", "NGG 虚拟网卡", 1, "ok",
			"已创建 "+adapterName+" 并处于启用状态"))
	case open:
		steps = append(steps, step("adapter", "NGG 虚拟网卡", 1, "warn",
			"网卡 "+adapterName+" 已创建但系统未报告为启用"))
	default:
		detail := "尚未创建；需要已签名的 wintun.dll 与管理员权限"
		if caps, ok := ne.TunCapabilities()["tun_supported"].(bool); ok && !caps {
			if reason, ok := ne.TunCapabilities()["reason"].(string); ok && reason != "" {
				detail = reason
			}
		}
		steps = append(steps, step("adapter", "NGG 虚拟网卡", 1, "warn", detail))
	}

	// ② 流量接管
	if dp.CaptureActive {
		steps = append(steps, step("capture", "全量流量接管", 2, "ok",
			"默认路由已指向 "+m.AdapterName+"，全部网卡流量进入 NGG 虚拟网络"))
	} else if dp.AdapterPresent {
		hint := dp.Reason
		if hint == "" {
			hint = "网卡已就绪，尚未接管默认路由"
		}
		steps = append(steps, step("capture", "全量流量接管", 2, "warn", hint))
	} else {
		steps = append(steps, step("capture", "全量流量接管", 2, "skip",
			"等待第 ① 步：虚拟网卡尚未创建"))
	}

	// ③ WAN 出口
	egressIface := ncfg.Egress.Interface
	if egressIface == "" {
		steps = append(steps, step("egress", "WAN 出口", 3, "warn",
			"未指定物理出口，NGG 自身的出站将使用系统默认路由"))
	} else {
		up := false
		label := egressIface
		for _, a := range adapters {
			if strings.EqualFold(a.Name, egressIface) {
				up = a.Up
				if len(a.IPv4) > 0 {
					label = egressIface + " (" + a.IPv4[0] + ")"
				}
				break
			}
		}
		status := "ok"
		detail := "流量从 " + label + " 出本地运营商"
		if !up {
			status = "warn"
			detail = "所选出口 " + label + " 当前未启用"
		}
		steps = append(steps, step("egress", "WAN 出口", 3, status, detail))
	}

	stale := staleTunAdapters(adapters, m.AdapterName, ncfg.TUN.IPv4)
	return map[string]any{
		"stale_adapters": stale,
		"master":         m,
		"datapath":       dp,
		"tun": map[string]any{
			"adapter":      tun,
			"capabilities": ne.TunCapabilities(),
			"config_name":  ncfg.TUN.Name,
		},
		"adapters": adapters,
		"egress": map[string]any{
			"interface":  ncfg.Egress.Interface,
			"local_addr": ncfg.Egress.LocalAddr,
			"mark":       ncfg.Egress.Mark,
		},
		"wans":    cfg.WANs,
		"steps":   steps,
		"running": ne.IsRunning(),
	}
}

// GET /api/v1/gateway/control
func (s *Server) handleControlGet(w http.ResponseWriter, r *http.Request) {
	st := s.controlState()
	if st == nil {
		writeErr(w, http.StatusServiceUnavailable, "网关或网络引擎未初始化")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// GET /api/v1/network/adapters — real interface list for the adapter picker.
func (s *Server) handleAdapters(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	name := ne.Config().TUN.Name
	list := netengine.ListAdapters(name)
	def := ""
	for _, a := range list {
		if a.Default && len(a.IPv4) > 0 {
			def = a.IPv4[0]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adapters":          list,
		"tun_name":          name,
		"default_source_ip": def,
	})
}

type controlStartBody struct {
	AdapterName     string  `json:"adapter_name"`
	AdapterMetric   *int    `json:"adapter_metric"`
	CaptureAll      *bool   `json:"capture_all"`
	BypassPrivate   *bool   `json:"bypass_private"`
	AutoStart       *bool   `json:"auto_start"`
	EgressInterface string  `json:"egress_interface"`
	WANID           *string `json:"wan_id"`
}

// POST /api/v1/gateway/control/start — the ordered one-click flow.
func (s *Server) handleControlStart(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	st := s.gateway
	if ne == nil || st == nil {
		writeErr(w, http.StatusServiceUnavailable, "网关或网络引擎未初始化")
		return
	}
	var body controlStartBody
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}

	// 1) Persist the master switch first: "start" always means "on".
	cfg := st.Get()
	m := cfg.Master
	if name := strings.TrimSpace(body.AdapterName); name != "" {
		m.AdapterName = name
	}
	if strings.TrimSpace(m.AdapterName) == "" {
		m.AdapterName = "NGG"
	}
	if body.AdapterMetric != nil {
		m.AdapterMetric = *body.AdapterMetric
	}
	if body.CaptureAll != nil {
		m.CaptureAll = *body.CaptureAll
	}
	m.CaptureAll = true // the Control Center flow is a full-takeover flow
	if body.BypassPrivate != nil {
		m.BypassPrivate = *body.BypassPrivate
	}
	if body.AutoStart != nil {
		m.AutoStart = *body.AutoStart
	}
	if body.WANID != nil {
		m.Uplink.WANID = strings.TrimSpace(*body.WANID)
	}
	m.Enabled = true
	saved, _, warnings, err := st.SaveMaster(m)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	results := []controlStep{}
	failed := false

	// 2) The TUN adapter must exist and carry the configured name.
	ncfg := ne.Config()
	if !strings.EqualFold(ncfg.TUN.Name, saved.Master.AdapterName) {
		// Renaming requires recreating the adapter.
		if open, _ := ne.TUNStatus()["open"].(bool); open {
			_ = ne.DetachTUN(r.Context())
		}
		ncfg.TUN.Name = saved.Master.AdapterName
		if _, err := s.persistNetworkConfig(ncfg); err != nil {
			results = append(results, step("adapter", "NGG 虚拟网卡", 1, "fail", "保存网卡名失败："+err.Error()))
			failed = true
		}
	}

	// A renamed adapter leaves the previous capture behind (e.g. ngg0 with a
	// default route). Clean it before creating the new one, otherwise traffic
	// keeps flowing into a dead interface.
	if stale := staleTunAdapters(netengine.ListAdapters(ncfg.TUN.Name), saved.Master.AdapterName, ncfg.TUN.IPv4); len(stale) > 0 {
		for _, name := range stale {
			executed, msg := st.RevertStaleAdapter(name)
			status := "warn"
			if executed {
				status = "ok"
			}
			results = append(results, step("cleanup", "清理旧虚拟网卡", 0, status, msg))
		}
	}

	if open, _ := ne.TUNStatus()["open"].(bool); !open {
		if err := ne.AttachTUN(r.Context(), false); err != nil {
			results = append(results, step("adapter", "NGG 虚拟网卡", 1, "fail", err.Error()))
			failed = true
		} else {
			results = append(results, step("adapter", "NGG 虚拟网卡", 1, "ok",
				"已创建 "+saved.Master.AdapterName))
		}
	} else {
		results = append(results, step("adapter", "NGG 虚拟网卡", 1, "ok",
			saved.Master.AdapterName+" 已存在，无需重建"))
	}
	if failed {
		// Do not leave master.enabled=true behind when nothing was applied:
		// the switch must describe reality, not intent.
		m.Enabled = false
		if again, _, _, err := st.SaveMaster(m); err == nil {
			saved = again
		}
	}

	// 3) Capture all traffic into that adapter.
	var dp gateway.DatapathResult
	if failed {
		results = append(results, step("capture", "全量流量接管", 2, "skip", "网卡未就绪，已跳过"))
	} else {
		dp = st.ApplyDatapath()
		switch {
		case dp.Applied:
			results = append(results, step("capture", "全量流量接管", 2, "ok", dp.Reason))
		case !dp.Executed:
			results = append(results, step("capture", "全量流量接管", 2, "fail", dp.Reason))
			failed = true
		default:
			results = append(results, step("capture", "全量流量接管", 2, "warn", dp.Reason))
		}
	}

	// 4) Physical uplink for the captured traffic.
	if iface := strings.TrimSpace(body.EgressInterface); iface != "" {
		ncfg.Egress.Interface = iface
		if _, err := s.persistNetworkConfig(ncfg); err != nil {
			results = append(results, step("egress", "WAN 出口", 3, "warn", "保存出口失败："+err.Error()))
		} else if err := ne.ApplyEgress(); err != nil {
			results = append(results, step("egress", "WAN 出口", 3, "warn",
				"出口已保存，但运行时绑定失败："+err.Error()))
		} else {
			detail := "出站已绑定到 " + iface
			// Keep the WAN entry in sync so the WAN console and the control
			// center describe the same uplink.
			if body.WANID != nil {
				if id := strings.TrimSpace(*body.WANID); id != "" {
					gcfg := st.Get()
					hit := false
					for i := range gcfg.WANs {
						if gcfg.WANs[i].ID == id {
							gcfg.WANs[i].Interface = iface
							hit = true
						}
					}
					if gcfg.WAN.ID == id {
						gcfg.WAN.Interface = iface
					}
					if hit {
						if _, _, _, err := st.Save(gcfg); err == nil {
							detail += "，并写入 WAN " + strings.ToUpper(id) + " 的接口"
						}
					}
				}
			}
			results = append(results, step("egress", "WAN 出口", 3, "ok", detail))
		}
	} else if ncfg.Egress.Interface == "" {
		results = append(results, step("egress", "WAN 出口", 3, "warn",
			"未选择物理出口，将使用系统默认路由"))
	} else {
		_ = ne.ApplyEgress()
		results = append(results, step("egress", "WAN 出口", 3, "ok",
			"沿用已保存的出口 "+ncfg.Egress.Interface))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       !failed,
		"steps":    results,
		"master":   saved.Master,
		"datapath": dp,
		"tun":      ne.TUNStatus(),
		"adapters": netengine.ListAdapters(ncfg.TUN.Name),
		"warnings": warnings,
		"egress":   ncfg.Egress.Interface,
		"control":  s.controlState(),
	})
}

// POST /api/v1/gateway/control/stop — revert in reverse order.
func (s *Server) handleControlStop(w http.ResponseWriter, r *http.Request) {
	ne := s.engine.Network()
	st := s.gateway
	if ne == nil || st == nil {
		writeErr(w, http.StatusServiceUnavailable, "网关或网络引擎未初始化")
		return
	}
	results := []controlStep{}

	dp := st.RevertDatapath()
	switch {
	case dp.Applied || !dp.Status.DefaultRouteViaAdapter:
		results = append(results, step("capture", "全量流量接管", 2, "ok", dp.Reason))
	default:
		results = append(results, step("capture", "全量流量接管", 2, "warn", dp.Reason))
	}

	if err := ne.DetachTUN(r.Context()); err != nil {
		results = append(results, step("adapter", "NGG 虚拟网卡", 1, "warn", err.Error()))
	} else {
		results = append(results, step("adapter", "NGG 虚拟网卡", 1, "ok", "已销毁虚拟网卡并恢复路由"))
	}

	cfg := st.Get()
	m := cfg.Master
	m.Enabled = false
	saved, _, _, err := st.SaveMaster(m)
	if err != nil {
		results = append(results, step("master", "总控开关", 0, "warn", err.Error()))
		saved = cfg
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"steps":    results,
		"master":   saved.Master,
		"datapath": dp,
		"tun":      ne.TUNStatus(),
		"control":  s.controlState(),
	})
}

// staleTunAdapters finds interfaces that still carry the TUN subnet but are not
// the adapter we are about to use. Only the subnet is trusted: a random adapter
// that happens to have a 10.89.0.x address would have to be created by us.
func staleTunAdapters(list []netengine.AdapterInfo, keepName, tunCIDR string) []string {
	ip, ipnet, err := net.ParseCIDR(strings.TrimSpace(tunCIDR))
	if err != nil || ip == nil {
		return nil
	}
	out := []string{}
	for _, a := range list {
		if strings.EqualFold(a.Name, keepName) {
			continue
		}
		for _, raw := range a.IPv4 {
			candidate := net.ParseIP(raw)
			if candidate == nil {
				continue
			}
			if ipnet.Contains(candidate) {
				out = append(out, a.Name)
				break
			}
		}
	}
	return out
}
