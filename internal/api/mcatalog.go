package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/engine"
	"github.com/ngg-project/ngg/internal/mcatalog"
	"github.com/ngg-project/ngg/internal/module"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// 规划 2~10 的统一 HTTP 入口。所有数据都在 module-catalog.json 与
// modules.json 两个独立 0600 文件里，不触碰任何既有域。

func (s *Server) mcatOrErr(w http.ResponseWriter) (*mcatalog.Store, bool) {
	if s.mcat == nil {
		writeErr(w, http.StatusServiceUnavailable, "模块库不可用")
		return nil, false
	}
	return s.mcat, true
}

// handleMCat returns the whole catalog plus the module summaries in one call.
func (s *Server) handleMCat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	cat, ok := s.mcatOrErr(w)
	if !ok {
		return
	}
	c := cat.Get()
	mcatalog.SortForDisplay(&c)
	summaries := []module.Summary{}
	if s.modules != nil {
		for _, m := range s.modules.List() {
			summaries = append(summaries, m.Summarize())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"catalog": c, "modules": summaries,
		"clients": mcatalog.Clients,
		"kinds":   mcatalog.Kinds,
	})
}

// handleMCatItem upserts (PUT) or removes (DELETE) one library entry.
func (s *Server) handleMCatItem(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.mcatOrErr(w)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	switch r.Method {
	case http.MethodPut:
		body, err := decodeRaw(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := cat.Upsert(kind, body)
		if err != nil {
			if errors.Is(err, mcatalog.ErrUnknownKind) {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := cat.Remove(kind, r.PathValue("id")); err != nil {
			if errors.Is(err, mcatalog.ErrNotFound) || errors.Is(err, mcatalog.ErrUnknownKind) {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeOK(w, "已删除")
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

func decodeRaw(r *http.Request) ([]byte, error) {
	var raw json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("请求体不能为空")
	}
	return raw, nil
}

// 规划 2：真实启停三个内核（single / multiple / all）。
func (s *Server) handleMCatKernels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	cat, ok := s.mcatOrErr(w)
	if !ok {
		return
	}
	action := r.PathValue("action")
	var body struct {
		Mode      string   `json:"mode"`
		IDs       []string `json:"ids"`
		Exclusive bool     `json:"exclusive"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ids := body.IDs
	if action == "start" {
		resolved, err := mcatalog.ResolveStart(cat.Get(), body.Mode, body.IDs)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		ids = resolved
	} else if action != "stop" {
		writeErr(w, http.StatusNotFound, "未知操作: "+action)
		return
	} else if len(ids) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "stop 需要指定内核列表")
		return
	}

	results := []map[string]any{}
	if s.engine == nil {
		for _, id := range ids {
			results = append(results, map[string]any{"id": id, "ok": false, "message": "内核引擎未初始化"})
		}
	} else {
		coreIDs := make([]contracts.CoreID, 0, len(ids))
		for _, id := range ids {
			coreIDs = append(coreIDs, contracts.CoreID(id))
		}
		var ops []engine.OpResult
		if action == "start" {
			ops = s.engine.BatchStart(r.Context(), coreIDs, body.Exclusive)
		} else {
			ops = s.engine.BatchStop(r.Context(), coreIDs)
		}
		state := "running"
		if action == "stop" {
			state = "stopped"
		}
		for _, op := range ops {
			if op.OK {
				_ = cat.SetKernelStates([]string{string(op.ID)}, state)
			} else {
				_ = cat.SetKernelStates([]string{string(op.ID)}, "error")
			}
			results = append(results, map[string]any{
				"id": string(op.ID), "ok": op.OK, "message": op.Message,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action": action, "mode": body.Mode, "results": results,
		"kernels": cat.Get().Kernels,
	})
}

// 规划 5：DNS 分流试算（前端可输入域名/IP 看会走哪个上游）。
func (s *Server) handleMCatDNSResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	cat, ok := s.mcatOrErr(w)
	if !ok {
		return
	}
	var body struct {
		DNSID  string `json:"dns_id"`
		Domain string `json:"domain"`
		IP     string `json:"ip"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	var policy mcatalog.DNSPolicy
	found := false
	for _, d := range cat.Get().DNS {
		if d.ID == body.DNSID || (body.DNSID == "" && d.Enabled) {
			policy, found = d, true
			break
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "没有可用的 DNS 策略，请先在 DNS 管理里新建")
		return
	}
	writeJSON(w, http.StatusOK, mcatalog.ResolveDNS(policy, body.Domain, body.IP))
}

// 规划 7：模块列表 / 新建 / 校验 / 删除。
func (s *Server) handleMCatModules(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.mcatOrErr(w)
	if !ok || s.modules == nil {
		writeErr(w, http.StatusServiceUnavailable, "模块存储不可用")
		return
	}
	switch r.Method {
	case http.MethodGet:
		out := []module.Summary{}
		for _, m := range s.modules.List() {
			out = append(out, m.Summarize())
		}
		writeJSON(w, http.StatusOK, map[string]any{"modules": out})
	case http.MethodPost:
		var sel mcatalog.Selection
		if err := decodeJSON(r, &sel); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		// 节点以 ID 引用，从规划 1 的节点库取快照内容填进模块。
		if s.mnodes != nil {
			for _, id := range sel.NodeIDs {
				view, found := s.mnodes.Get(id)
				if !found {
					writeErr(w, http.StatusUnprocessableEntity, "节点不存在: "+id)
					return
				}
				sel.Nodes = append(sel.Nodes, view.Node)
			}
		}
		m, err := mcatalog.BuildModule(cat.Get(), sel)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err := s.modules.Save(m); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, m)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

func (s *Server) handleMCatModule(w http.ResponseWriter, r *http.Request) {
	if s.modules == nil {
		writeErr(w, http.StatusServiceUnavailable, "模块存储不可用")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodDelete:
		if err := s.modules.Delete(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, "模块已删除")
	case http.MethodPost: // .../validate
		m, found := s.modules.Get(id)
		if !found {
			writeErr(w, http.StatusNotFound, "模块不存在: "+id)
			return
		}
		preview, err := m.BuildPreview()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, preview)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// 规划 8 + 9：用户保存后签发订阅 token，并派生客户端专属链接。
func (s *Server) handleMCatUserLinks(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.mcatOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var user mcatalog.User
	for _, u := range cat.Get().Users {
		if u.ID == id {
			user = u
		}
	}
	if user.ID == "" {
		writeErr(w, http.StatusNotFound, "用户不存在: "+id)
		return
	}
	switch r.Method {
	case http.MethodPost:
		if s.modules != nil {
			if _, found := s.modules.Get(user.ModuleID); !found {
				writeErr(w, http.StatusUnprocessableEntity, "用户绑定的模块不存在，请重新选择模块")
				return
			}
		}
		var body struct {
			BaseURL string   `json:"base_url"`
			Clients []string `json:"clients"`
		}
		if r.ContentLength > 0 {
			_ = decodeJSON(r, &body)
		}
		if strings.TrimSpace(body.BaseURL) == "" {
			body.BaseURL = "http://" + r.Host + "/api/v1/sub"
		}
		links := mcatalog.BuildLinks(user, body.BaseURL, body.Clients)
		if err := cat.Update(func(c *mcatalog.Catalog) error {
			keep := make([]mcatalog.Link, 0, len(c.Links))
			for _, l := range c.Links {
				if l.UserID != id {
					keep = append(keep, l)
				}
			}
			c.Links = append(keep, links...)
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id": id, "token": user.Token,
			"subscription": strings.TrimSuffix(body.BaseURL, "/") + "/" + user.Token,
			"links":        links,
		})
	case http.MethodGet:
		out := []mcatalog.Link{}
		for _, l := range cat.Get().Links {
			if l.UserID == id {
				out = append(out, l)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"links": out})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// 规划 10：主内核总控 —— 读取模块并真实调控内核，记录状态。
func (s *Server) handleMCatMaster(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.mcatOrErr(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, cat.Get().Master)
	case http.MethodPost:
		if s.modules == nil {
			writeErr(w, http.StatusServiceUnavailable, "模块存储不可用")
			return
		}
		var body struct {
			ModuleID     string `json:"module_id"`
			AutoRegulate bool   `json:"auto_regulate"`
		}
		if r.ContentLength > 0 {
			_ = decodeJSON(r, &body)
		}
		if body.ModuleID == "" {
			body.ModuleID = cat.Get().Master.ModuleID
		}
		m, found := s.modules.Get(body.ModuleID)
		if !found {
			writeErr(w, http.StatusUnprocessableEntity, "请指定一个已存在的模块")
			return
		}
		if err := m.Apply(); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		c := cat.Get()
		plan := mcatalog.Regulate(m, c.RunningKernels())
		// 真正执行启停（规划 2 的能力在这里被总控调用）
		if s.engine != nil {
			if len(plan.StartKernels) > 0 {
				ids := make([]contracts.CoreID, 0, len(plan.StartKernels))
				for _, id := range plan.StartKernels {
					ids = append(ids, contracts.CoreID(id))
				}
				for _, op := range s.engine.BatchStart(r.Context(), ids, false) {
					state := "error"
					if op.OK {
						state = "running"
					}
					_ = cat.SetKernelStates([]string{string(op.ID)}, state)
				}
			}
			if len(plan.StopKernels) > 0 {
				ids := make([]contracts.CoreID, 0, len(plan.StopKernels))
				for _, id := range plan.StopKernels {
					ids = append(ids, contracts.CoreID(id))
				}
				for _, op := range s.engine.BatchStop(r.Context(), ids) {
					state := "error"
					if op.OK {
						state = "stopped"
					}
					_ = cat.SetKernelStates([]string{string(op.ID)}, state)
				}
			}
		}
		if err := cat.Update(func(c *mcatalog.Catalog) error {
			c.Master = mcatalog.MasterState{
				Enabled: true, AutoRegulate: body.AutoRegulate, ModuleID: m.ID,
				ActiveKernels: append([]string{}, plan.StartKernels...),
				EgressNodeID:  plan.EgressNodeID,
				LastApply:     time.Now().UTC(), LastResult: strings.Join(plan.Actions, "；"),
				RegulateCount: c.Master.RegulateCount + 1,
			}
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"module_id": m.ID, "plan": plan, "master": cat.Get().Master,
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}
