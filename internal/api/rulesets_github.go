package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/internal/rulesets"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// GET /rulesets/github/presets — built-in + user-defined rule repositories.
// "custom" lists the repos the UI is allowed to delete.
func (s *Server) handleGitHubPresets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"presets": rulesets.Presets(),
		"custom":  rulesets.CustomRepos(),
	})
}

// POST /rulesets/github/presets — add a user-defined source. repo must be
// owner/name (validated and scrubbed inside AddCustomPreset).
func (s *Server) handleGitHubPresetCreate(w http.ResponseWriter, r *http.Request) {
	var body rulesets.GitHubPreset
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	preset, err := rulesets.AddCustomPreset(body)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preset": preset})
}

// DELETE /rulesets/github/presets/{id} — id is the repo (owner/name). The
// frontend encodeURIComponent()s it, so PathValue returns the decoded repo.
func (s *Server) handleGitHubPresetDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing preset repo")
		return
	}
	if err := rulesets.RemoveCustomPreset(id); err != nil {
		if errors.Is(err, rulesets.ErrBuiltinPreset) {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": id})
}

// GET /rulesets/github/catalog?repo=&branch=&format=&refresh=true
// POST /rulesets/github/catalog/refresh — force a real GitHub API refresh.
func (s *Server) handleGitHubCatalog(w http.ResponseWriter, r *http.Request) {
	if s.githubIndex == nil {
		writeErr(w, http.StatusInternalServerError, "GitHub 规则集索引未初始化")
		return
	}
	q := r.URL.Query()
	repo := strings.TrimSpace(q.Get("repo"))
	branch := strings.TrimSpace(q.Get("branch"))
	format := strings.TrimSpace(q.Get("format"))
	// POST /catalog/refresh carries the same fields in a JSON body.
	if r.Method == http.MethodPost && repo == "" {
		var body struct {
			Repo   string `json:"repo"`
			Branch string `json:"branch"`
			Format string `json:"format"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		repo, branch, format = strings.TrimSpace(body.Repo), strings.TrimSpace(body.Branch), strings.TrimSpace(body.Format)
	}
	if repo == "" {
		writeErr(w, http.StatusBadRequest, "缺少 repo 参数（owner/name）")
		return
	}
	force := r.Method == http.MethodPost || q.Get("refresh") == "true"
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	c, err := s.githubIndex.GetOrRefresh(ctx, repo, branch, format, force)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// MountResult is the per-category, fully honest outcome of a mount request.
type GitHubMountResult struct {
	Category   string `json:"category"`
	URL        string `json:"url"`
	Recognised int    `json:"recognised"`
	RulesAdded int    `json:"rules_added"`
	Mounted    bool   `json:"mounted"`
	RuleSetID  string `json:"ruleset_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// POST /rulesets/github/mount
//
//	mode = "mount"  → attach each category as a live URL rule set (侧挂载)
//	mode = "import" → parse and add every recognised rule to the live table
//	mode = "both"   → do both
func (s *Server) handleGitHubMount(w http.ResponseWriter, r *http.Request) {
	if s.githubIndex == nil {
		writeErr(w, http.StatusInternalServerError, "GitHub 规则集索引未初始化")
		return
	}
	var body struct {
		Repo       string   `json:"repo"`
		Branch     string   `json:"branch"`
		Format     string   `json:"format"`
		Categories []string `json:"categories"`
		Mode       string   `json:"mode"`
		Action     string   `json:"action"`
		CoreID     string   `json:"core_id"`
		Enabled    *bool    `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.Categories) == 0 {
		writeErr(w, http.StatusBadRequest, "请至少选择一个分流规则分类")
		return
	}
	// A hard cap prevents a single click from issuing hundreds of requests.
	if len(body.Categories) > 50 {
		writeErr(w, http.StatusBadRequest, "单次最多挂载 50 个分类；请分批选择")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode == "" {
		mode = "mount"
	}
	if mode != "mount" && mode != "import" && mode != "both" {
		writeErr(w, http.StatusBadRequest, "mode 只能是 mount / import / both")
		return
	}
	action := netengine.RuleAction(strings.ToUpper(strings.TrimSpace(body.Action)))
	if action == "" {
		action = netengine.ActProxy
	}
	switch action {
	case netengine.ActProxy, netengine.ActDirect, netengine.ActBlock, netengine.ActDNS:
	default:
		writeErr(w, http.StatusBadRequest, "action 只能是 PROXY / DIRECT / REJECT / DNS-LOCAL")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	ne := s.engine.Network()
	reg := s.engine.RuleSets()
	if (mode == "import" || mode == "both") && ne == nil {
		writeErr(w, http.StatusServiceUnavailable, "规则引擎未初始化，无法导入到实时规则表")
		return
	}
	if (mode == "mount" || mode == "both") && reg == nil {
		writeErr(w, http.StatusServiceUnavailable, "规则集注册表未初始化，无法侧挂载")
		return
	}
	if action == netengine.ActProxy && (mode == "import" || mode == "both") {
		if strings.TrimSpace(body.CoreID) == "" && ne != nil {
			_, def := ne.Rules().Defaults()
			body.CoreID = string(def)
		}
		if strings.TrimSpace(body.CoreID) == "" {
			writeErr(w, http.StatusUnprocessableEntity, "PROXY 规则需要执行内核：请提供 core_id 或在规则表设置默认内核")
			return
		}
	}

	// Resolve every category against the cached catalog first; an unknown
	// name is rejected before any network request happens.
	type item struct {
		category, url string
	}
	items := make([]item, 0, len(body.Categories))
	for _, cat := range body.Categories {
		u, err := s.githubIndex.CategoryURL(body.Repo, body.Branch, body.Format, cat)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		items = append(items, item{category: cat, url: u})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()

	results := make([]GitHubMountResult, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // bounded concurrency, be kind to GitHub
	for i, it := range items {
		wg.Add(1)
		go func(i int, it item) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = s.mountOne(ctx, it.category, it.url, body.Repo, mode, action, body.CoreID, enabled, reg)
		}(i, it)
	}
	wg.Wait()

	added, recognised, mounted, failed := 0, 0, 0, 0
	for _, res := range results {
		added += res.RulesAdded
		recognised += res.Recognised
		if res.Mounted {
			mounted++
		}
		if res.Error != "" {
			failed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "mode": mode, "results": results,
		"categories": len(items), "recognised": recognised,
		"rules_added": added, "mounted": mounted, "failed": failed,
		"message": fmt.Sprintf("处理 %d 个分类：识别 %d 条规则，新增 %d 条，挂载 %d 个规则集，失败 %d 个。",
			len(items), recognised, added, mounted, failed),
	})
}

func (s *Server) mountOne(
	ctx context.Context, category, rawURL, repo, mode string,
	action netengine.RuleAction, coreID string, enabled bool,
	reg *rulesets.Registry,
) GitHubMountResult {
	res := GitHubMountResult{Category: category, URL: rawURL}
	text, err := rulesets.FetchURL(ctx, rawURL)
	if err != nil {
		res.Error = "下载失败: " + err.Error()
		return res
	}
	parsed, err := rulesets.Parse(text, "auto")
	if err != nil {
		res.Error = "解析失败: " + err.Error()
		return res
	}
	res.Recognised = parsed.Count

	if mode == "mount" || mode == "both" {
		id := "gh:" + strings.ToLower(strings.ReplaceAll(repo, "/", "-")) + ":" + strings.ToLower(category)
		set := rulesets.RuleSet{
			ID: id, Name: category, Description: fmt.Sprintf("GitHub 规则集 · %s · %s", repo, category),
			Format: parsed.Format, Source: rulesets.SourceURL, URL: rawURL,
			Enabled: enabled, Filename: category,
		}
		// Add() performs another real fetch via materialize(). Update() when
		// it already exists so re-mounting refreshes instead of erroring.
		if _, exists := reg.Get(id); exists {
			_, err = reg.Update(ctx, id, set)
		} else {
			_, err = reg.Add(ctx, set)
		}
		if err != nil {
			res.Error = "挂载失败: " + err.Error()
		} else {
			res.Mounted = true
			res.RuleSetID = id
		}
	}

	if (mode == "import" || mode == "both") && s.engine.Network() != nil {
		rs := s.engine.Network().Rules()
		existing := map[string]bool{}
		for _, r := range rs.List() {
			existing[r.ID] = true
		}
		for i, pr := range parsed.Rules {
			a := action
			// Respect an explicit action written inside the rule file only
			// when the caller did not force one; default is the caller's.
			if strings.EqualFold(pr.Action, "DIRECT") && action == netengine.ActProxy {
				a = netengine.ActDirect
			}
			rule := netengine.Rule{
				ID:   fmt.Sprintf("gh:%s:%d", strings.ToLower(category), i+1),
				Type: netengine.RuleType(pr.Type), Value: pr.Value, Action: a,
				CoreID: contracts.CoreID(coreID), Priority: 50, Enabled: enabled,
				Comment: "GitHub 规则集 " + repo + "/" + category,
			}
			if existing[rule.ID] {
				continue // idempotent re-import
			}
			if err := rs.Add(rule); err != nil {
				continue // invalid individual rules are skipped, not faked
			}
			existing[rule.ID] = true
			res.RulesAdded++
		}
	}
	return res
}
