package api

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/gateway"
	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/pkg/contracts"
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

// ---------------------------------------------------------------- 网关总控 ---
//
// v2.7 网关总控：虚拟网卡 + 智能分流。所有端点都复用同一个 gateway.Store，
// 因此 PUT 与落盘的 gateway-config.json 不会漂移；状态字段全部来自真实主机。

// splitRuleTag marks every rule this API generates, so a re-apply of the same
// policy is idempotent and cannot be confused with hand-written rules.
const splitRuleTag = "gateway-master"

// handleGatewayMasterGet returns the master switch plus the live datapath
// status of this host.
func (s *Server) handleGatewayMasterGet(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"master": st.Get().Master,
		"status": st.Datapath(),
	})
}

// handleGatewayMasterPut replaces only the master switch. A rejected payload
// returns 422 and leaves both the file and the in-memory config untouched.
func (s *Server) handleGatewayMasterPut(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	var body gateway.Master
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	saved, changed, warnings, err := st.SaveMaster(body)
	if err != nil {
		var verr *gateway.ValidationError
		if errors.As(err, &verr) {
			writeErr(w, http.StatusUnprocessableEntity, verr.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 固定追加的诚实提示：保存不等于生效。
	warnings = append(warnings,
		"数据面接管（创建虚拟网卡 / 改写默认路由）需要管理员权限；权限不足时 NGG 只返回命令，不会执行",
		"分流规则只有写入规则引擎并重新加载内核后才会作用于真实流量",
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"master":    saved.Master,
		"changed":   changed,
		"persisted": true,
		"warnings":  warnings,
	})
}

// handleGatewayDatapath reports the live data-path capability.
func (s *Server) handleGatewayDatapath(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	writeJSON(w, http.StatusOK, st.Datapath())
}

// handleGatewayDatapathApply tries to take the data path over. It only really
// executes on Windows with an elevated token and a present adapter; every
// other case returns the plan with executed=false / applied=false.
//
// The body is optional. When it carries a master config the handler persists
// it first, so "toggle the switch and hit apply" works in one step instead of
// failing with "master.enabled=false" because the form was never saved.
func (s *Server) handleGatewayDatapathApply(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	var body struct {
		Master *gateway.Master `json:"master"`
	}
	if r.Body != nil {
		// An empty body is the normal case; only malformed JSON is an error.
		if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	payload := map[string]any{}
	if body.Master != nil {
		saved, _, _, err := st.SaveMaster(*body.Master)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "CONFIG_INVALID: "+err.Error())
			return
		}
		// SaveMaster returns the whole gateway config; the caller wants master.
		payload["master"] = saved.Master
	}
	res := st.ApplyDatapath()
	payload["applied"] = res.Applied
	payload["executed"] = res.Executed
	payload["commands"] = res.Commands
	payload["reason"] = res.Reason
	payload["status"] = res.Status
	writeJSON(w, http.StatusOK, payload)
}

// handleGatewayDatapathRevert undoes a takeover under the same conditions.
func (s *Server) handleGatewayDatapathRevert(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	writeJSON(w, http.StatusOK, st.RevertDatapath())
}

// handleGatewaySplitRules turns the saved split policy into netengine rules.
// It writes the rule table only — it never restarts a kernel, so the response
// says so explicitly.
func (s *Server) handleGatewaySplitRules(w http.ResponseWriter, r *http.Request) {
	st := s.gatewayOrFail(w)
	if st == nil {
		return
	}
	ne := s.engine.Network()
	if ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "network engine not initialized")
		return
	}
	cfg := st.Get()
	entries := cfg.Master.SplitEntries()
	core := s.gatewayProxyCore(ne)
	for _, e := range entries {
		if e.Action == gateway.SplitProxy && core == "" {
			writeErr(w, http.StatusUnprocessableEntity,
				"没有可用的代理内核（network default_core 为空且未注册任何内核），无法为 proxy 列表生成 PROXY 规则")
			return
		}
	}

	rules := make([]netengine.Rule, 0, len(entries))
	for _, e := range entries {
		rule := netengine.Rule{
			ID:      splitRuleID(e),
			Value:   e.Value,
			Enabled: true,
			Comment: "gateway-master split (" + cfg.Master.Split.Mode + ")",
			Tag:     splitRuleTag,
		}
		switch e.Kind {
		case gateway.SplitDomain:
			rule.Type = netengine.RuleDomainSuffix
		default:
			rule.Type = netengine.RuleIPCIDR
		}
		switch e.Action {
		case gateway.SplitProxy:
			rule.Action = netengine.ActProxy
			rule.CoreID = contracts.CoreID(core)
			rule.Priority = 70
		default:
			rule.Action = netengine.ActDirect
			rule.Priority = 75
		}
		rules = append(rules, rule)
	}

	rs := ne.Rules()
	added, skipped, err := rs.AddMany(rules)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "规则引擎拒绝了分流规则: "+err.Error())
		return
	}

	// The policy's "default" decides what happens to everything that matches
	// no rule. Setting it is part of writing the policy; it is skipped when a
	// PROXY default would have no core to send traffic to.
	warnings := []string{
		"分流规则只写入 NGG 规则引擎，未重启内核；需重新加载内核（或重启网络引擎）后才会作用于真实流量",
	}
	defaultAction, defaultCore := rs.Defaults()
	want := cfg.Master.DefaultSplitAction()
	applied := ""
	switch {
	case want == "proxy" && core != "":
		rs.SetDefaults(netengine.ActProxy, contracts.CoreID(core))
		defaultAction, defaultCore = netengine.ActProxy, contracts.CoreID(core)
		applied = "proxy"
	case want == "proxy":
		warnings = append(warnings, "没有可用的代理内核，规则引擎默认动作保持原样（未设为 PROXY）")
	case want == "local":
		rs.SetDefaults(netengine.ActDirect, defaultCore)
		defaultAction = netengine.ActDirect
		applied = "local"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"added":          len(added),
		"skipped":        skipped,
		"rules":          added,
		"default_action": defaultAction,
		"default_core":   defaultCore,
		"default_set":    applied,
		"warnings":       warnings,
	})
}

// gatewayProxyCore picks the core that generated PROXY rules point at: the
// network engine's default core, else the first registered kernel. Empty means
// the host has no proxy kernel at all and PROXY rules cannot be honoured.
func (s *Server) gatewayProxyCore(ne *netengine.Engine) string {
	if c := strings.TrimSpace(ne.Config().DefaultCore); c != "" {
		return c
	}
	for _, d := range s.engine.Registry() {
		if id := strings.TrimSpace(string(d.ID)); id != "" {
			return id
		}
	}
	return ""
}

// splitRuleID derives a stable id from the payload, so re-applying an
// unchanged policy produces the same ids and AddMany can skip them.
func splitRuleID(e gateway.SplitEntry) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s|%s|%s", e.Kind, e.Action, e.Value)
	kind, action := "d", "x"
	if e.Kind == gateway.SplitCIDR {
		kind = "c"
	}
	if e.Action == gateway.SplitProxy {
		action = "p"
	}
	return fmt.Sprintf("gm-%s%s-%08x", kind, action, h.Sum32())
}
