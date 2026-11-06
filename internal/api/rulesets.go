package api

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/internal/rulesets"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// Config Engine rule sets: inline / URL / uploaded documents. Uploading a file
// auto-detects its format, parses the rules and immediately appends the set to
// the rule-set list.

func (s *Server) handleRuleSets(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.RuleSets()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "rule set registry not initialized")
		return
	}
	if r.Method == http.MethodPost {
		var set rulesets.RuleSet
		if err := decodeJSON(r, &set); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Add(r.Context(), set)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ruleset": saved})
		return
	}
	list := reg.List()
	if list == nil {
		list = []rulesets.RuleSet{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rulesets": list, "count": len(list)})
}

// handleRuleSetsToggle enables/disables one rule set, a selected batch, or
// all rule sets. It only flips Enabled and persists; no URL fetch / re-parse.
func (s *Server) handleRuleSetsToggle(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.RuleSets()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "rule set registry not initialized")
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
	changed, err := reg.SetEnabled(body.IDs, body.All, body.Enabled)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "changed": changed, "enabled": body.Enabled, "scope": scope,
	})
}

func (s *Server) handleRuleSetFormats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, rulesets.Formats())
}

// GET /rulesets/syntax — official rule-set syntax schemas for all three
// kernels. POST /rulesets accepts an optional "kernel" field, which is stored
// on RuleSet and echoed back by GET /rulesets and GET /rulesets/{id}.
func (s *Server) handleRuleSetSyntax(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"schemas": rulesets.SyntaxSchemas()})
}

func (s *Server) handleRuleSetByID(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.RuleSets()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "rule set registry not initialized")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		set, ok := reg.Get(id)
		if !ok {
			writeErr(w, http.StatusNotFound, "rule set "+id+" not found")
			return
		}
		writeJSON(w, http.StatusOK, set)
	case http.MethodPut:
		var set rulesets.RuleSet
		if err := decodeJSON(r, &set); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		saved, err := reg.Update(r.Context(), id, set)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ruleset": saved})
	case http.MethodDelete:
		if err := reg.Remove(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, "rule set removed")
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "upload"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// handleRuleSetUpload: multipart form field "file"; optional "name", "id",
// "description", "format". Returns the parse preview AND the created set.
func (s *Server) handleRuleSetUpload(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.RuleSets()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "rule set registry not initialized")
		return
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "multipart parse: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "form field 'file' is required")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	base := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = base
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		id = "rs-" + sanitizeID(base) + "-" + fmt.Sprint(time.Now().UTC().UnixNano())
	}
	parsed, err := rulesets.Parse(string(content), r.FormValue("format"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	saved, err := reg.Add(r.Context(), rulesets.RuleSet{
		ID: id, Name: name, Description: strings.TrimSpace(r.FormValue("description")),
		Source: rulesets.SourceUpload, Filename: header.Filename,
		Content: string(content), Enabled: true, Format: parsed.Format,
	})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	sample := parsed.Rules
	if len(sample) > 8 {
		sample = sample[:8]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "ruleset": saved,
		"parse": map[string]any{
			"format": parsed.Format, "count": parsed.Count,
			"warnings": parsed.Warnings, "sample": sample,
		},
	})
}

func (s *Server) handleRuleSetFetch(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.RuleSets()
	if reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "rule set registry not initialized")
		return
	}
	saved, err := reg.Refresh(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ruleset": saved})
}

// handleRuleSetApply merges a rule set into the live NGG rule table.
// PROXY rules need an execution kernel; it comes from the body or the current
// default core, and an explicit error is returned when neither is available.
func (s *Server) handleRuleSetApply(w http.ResponseWriter, r *http.Request) {
	reg := s.engine.RuleSets()
	ne := s.engine.Network()
	if reg == nil || ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "rule engine not initialized")
		return
	}
	set, ok := reg.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "rule set not found")
		return
	}
	var body struct {
		CoreID string `json:"core_id"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	core := strings.TrimSpace(body.CoreID)
	if core == "" {
		_, def := ne.Rules().Defaults()
		core = string(def)
	}
	if core == "" {
		writeErr(w, http.StatusUnprocessableEntity, "PROXY 规则需要执行内核：请在请求体提供 core_id 或在规则表设置默认内核")
		return
	}
	rs := ne.Rules()
	applied, skipped := 0, 0
	for i, pr := range set.Rules {
		action := netengine.RuleAction(strings.ToUpper(pr.Action))
		switch action {
		case netengine.ActProxy, netengine.ActDirect, netengine.ActBlock, netengine.ActDNS:
		default:
			action = netengine.ActProxy
		}
		rule := netengine.Rule{
			ID:   fmt.Sprintf("rs:%s:%d", set.ID, i+1),
			Type: netengine.RuleType(pr.Type), Value: pr.Value, Action: action,
			CoreID: contracts.CoreID(core), Priority: 50, Enabled: true,
			Comment: "from ruleset " + set.ID,
		}
		if err := rs.Update(rule.ID, rule); err != nil {
			if err := rs.Add(rule); err != nil {
				skipped++
				continue
			}
		}
		applied++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "ruleset_id": set.ID, "applied": applied,
		"skipped": skipped, "core_id": core,
	})
}
