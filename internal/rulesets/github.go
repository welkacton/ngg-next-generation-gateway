package rulesets

// GitHub rule-set discovery and mounting.
//
// Repositories such as blackmatrix7/ios_rule_script publish hundreds of
// ready-made 分流规则分类 under rule/<Format>/<Category>/<Category>.list.
// This package discovers them with ONE real GitHub git-trees API call,
// caches the index to disk, and can either:
//   - mount a category as a live URL rule set (侧挂载), or
//   - import its rules straight into the live NGG rule table (自动添加).
// Nothing is faked: every count below comes from parsing real downloaded
// content, and any failure is reported per category.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// GitHubPreset is a well-known rule repository offered in the UI.
type GitHubPreset struct {
	Repo     string   `json:"repo"`
	Label    string   `json:"label"`
	Branch   string   `json:"branch"`
	Formats  []string `json:"formats"`
	Homepage string   `json:"homepage"`
	Note     string   `json:"note"`
}

var githubPresets = []GitHubPreset{
	{
		Repo: "blackmatrix7/ios_rule_script", Label: "blackmatrix7 · ios_rule_script",
		Branch: "master", Formats: []string{"Clash", "Surge", "QuantumultX", "Loon", "Shadowrocket", "AdGuard"},
		Homepage: "https://github.com/blackmatrix7/ios_rule_script",
		Note:     "668+ 个分流规则分类（Google / YouTube / OpenAI / 国内直连 …），Clash 目录最适合 NGG 解析器",
	},
	{
		Repo: "Loyalsoldier/clash-rules", Label: "Loyalsoldier · clash-rules",
		Branch: "release", Formats: []string{"Clash"},
		Homepage: "https://github.com/Loyalsoldier/clash-rules",
		Note:     "自动生成的大陆/海外域名与 IP 规则集（release 分支为编译产物）",
	},
}

func Presets() []GitHubPreset { return append([]GitHubPreset(nil), githubPresets...) }

// GitHubCategory is one discovered 分流规则分类.
type GitHubCategory struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	File   string `json:"file"`
	URL    string `json:"url"`
	Format string `json:"format"`
	Ext    string `json:"ext"`
}

// GitHubCatalog is the cached index of one repo/branch/format combination.
type GitHubCatalog struct {
	Repo       string           `json:"repo"`
	Branch     string           `json:"branch"`
	Format     string           `json:"format"`
	Categories []GitHubCategory `json:"categories"`
	Total      int              `json:"total"`
	SyncedAt   string           `json:"synced_at"`
	Source     string           `json:"source"` // github-api | cache
	Message    string           `json:"message"`
	Truncated  bool             `json:"truncated"`
}

// GitHubIndex caches catalogs on disk. Safe for concurrent use.
type GitHubIndex struct {
	mu      sync.RWMutex
	dir     string
	client  *http.Client
	tokenFn func() string
	catalog map[string]GitHubCatalog

	// APIBase / RawBase are overridable for tests and for GitHub Enterprise
	// or a corporate mirror. Empty means the public endpoints.
	APIBase string
	RawBase string
}

func NewGitHubIndex(dataDir string, tokenFn func() string) *GitHubIndex {
	if tokenFn == nil {
		tokenFn = func() string { return "" }
	}
	return &GitHubIndex{
		dir:     filepath.Join(dataDir, "ruleset-github"),
		client:  &http.Client{Timeout: 30 * time.Second},
		tokenFn: tokenFn,
		catalog: map[string]GitHubCatalog{},
		APIBase: "https://api.github.com",
		RawBase: "https://raw.githubusercontent.com",
	}
}

func (g *GitHubIndex) apiBase() string {
	if g.APIBase != "" {
		return strings.TrimSuffix(g.APIBase, "/")
	}
	return "https://api.github.com"
}

func (g *GitHubIndex) rawBase() string {
	if g.RawBase != "" {
		return strings.TrimSuffix(g.RawBase, "/")
	}
	return "https://raw.githubusercontent.com"
}

func catalogKey(repo, branch, format string) string {
	safe := func(s string) string {
		return strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return '_'
		}, s)
	}
	return safe(repo) + "_" + safe(branch) + "_" + safe(format)
}

func (g *GitHubIndex) Catalog(repo, branch, format string) (GitHubCatalog, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	c, ok := g.catalog[catalogKey(repo, branch, format)]
	return c, ok
}

func (g *GitHubIndex) loadFromDisk(repo, branch, format string) (GitHubCatalog, bool) {
	b, err := os.ReadFile(filepath.Join(g.dir, catalogKey(repo, branch, format)+".json"))
	if err != nil {
		return GitHubCatalog{}, false
	}
	var c GitHubCatalog
	if err := json.Unmarshal(b, &c); err != nil || len(c.Categories) == 0 {
		return GitHubCatalog{}, false
	}
	c.Source = "cache"
	c.Message = "已加载本地缓存目录；点「刷新」可从 GitHub 获取最新分类。"
	g.mu.Lock()
	g.catalog[catalogKey(repo, branch, format)] = c
	g.mu.Unlock()
	return c, true
}

func (g *GitHubIndex) save(c GitHubCatalog) error {
	if err := os.MkdirAll(g.dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(g.dir, catalogKey(c.Repo, c.Branch, c.Format)+".json"), b, 0o600)
}

// Get returns the cached catalog, loading it from disk if needed. It never
// performs a network request; call Refresh for that.
func (g *GitHubIndex) Get(repo, branch, format string) (GitHubCatalog, bool) {
	if c, ok := g.Catalog(repo, branch, format); ok {
		return c, true
	}
	return g.loadFromDisk(repo, branch, format)
}

// Refresh performs a real GitHub git-trees API call and rebuilds the index.
func (g *GitHubIndex) Refresh(ctx context.Context, repo, branch, format string) (GitHubCatalog, error) {
	repoParts := strings.Split(strings.Trim(strings.TrimSpace(repo), "/"), "/")
	if len(repoParts) != 2 || repoParts[0] == "" || repoParts[1] == "" {
		return GitHubCatalog{}, fmt.Errorf("仓库格式应为 owner/name，例如 blackmatrix7/ios_rule_script")
	}
	if branch == "" {
		branch = "master"
	}
	if format == "" {
		format = "Clash"
	}

	apiURL := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1",
		g.apiBase(), repoParts[0], repoParts[1], branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return GitHubCatalog{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NGG-RuleSet-GitHub/2.2")
	if t := strings.TrimSpace(g.tokenFn()); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return GitHubCatalog{}, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return GitHubCatalog{}, fmt.Errorf("仓库或分支不存在（%s@%s），或该仓库为私有需配置 GITHUB_TOKEN", repo, branch)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return GitHubCatalog{}, fmt.Errorf("GitHub API 限流（HTTP %d）；配置 GITHUB_TOKEN 可提高限额，或稍后再试", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return GitHubCatalog{}, fmt.Errorf("GitHub API 返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return GitHubCatalog{}, err
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &tree); err != nil {
		return GitHubCatalog{}, fmt.Errorf("解析 GitHub 文件树失败: %w", err)
	}

	prefix := "rule/" + format + "/"
	byCategory := map[string][]GitHubCategory{}
	for _, entry := range tree.Tree {
		p := entry.Path
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		parts := strings.Split(rest, "/")
		if len(parts) != 2 {
			continue
		}
		cat, file := parts[0], parts[1]
		ext := strings.ToLower(filepath.Ext(file))
		if ext != ".list" && ext != ".yaml" && ext != ".yml" && ext != ".txt" && ext != ".conf" {
			continue
		}
		base := strings.TrimSuffix(file, filepath.Ext(file))
		if strings.HasSuffix(base, "_No_Resolve") || strings.HasSuffix(base, "_Classical") {
			continue // duplicates / variants, keep the canonical file
		}
		byCategory[cat] = append(byCategory[cat], GitHubCategory{
			Name: cat, Path: cat, File: p, Format: format, Ext: ext,
			URL: fmt.Sprintf("%s/%s/%s/%s/%s",
				g.rawBase(), repoParts[0], repoParts[1], branch, p),
		})
	}

	cats := make([]GitHubCategory, 0, len(byCategory))
	for cat, files := range byCategory {
		// Prefer a .list (plain rule lines) over payload YAML; NGG's parser
		// handles both, but .list is the canonical, smallest form.
		sort.Slice(files, func(i, j int) bool {
			rank := func(e string) int {
				if e == ".list" {
					return 0
				}
				if e == ".yaml" || e == ".yml" {
					return 1
				}
				return 2
			}
			if rank(files[i].Ext) != rank(files[j].Ext) {
				return rank(files[i].Ext) < rank(files[j].Ext)
			}
			return files[i].File < files[j].File
		})
		chosen := files[0]
		// Prefer a file whose base name matches the category (avoids
		// picking unrelated extras inside the folder).
		for _, f := range files {
			if strings.EqualFold(strings.TrimSuffix(filepath.Base(f.File), filepath.Ext(f.File)), cat) {
				chosen = f
				break
			}
		}
		cats = append(cats, chosen)
	}
	sort.Slice(cats, func(i, j int) bool { return strings.ToLower(cats[i].Name) < strings.ToLower(cats[j].Name) })

	c := GitHubCatalog{
		Repo: repoParts[0] + "/" + repoParts[1], Branch: branch, Format: format,
		Categories: cats, Total: len(cats),
		SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Source:   "github-api", Truncated: tree.Truncated,
		Message: fmt.Sprintf("已从 GitHub 识别 %d 个分流规则分类（%s）", len(cats), format),
	}
	if len(cats) == 0 {
		return GitHubCatalog{}, fmt.Errorf("在 %s 的 rule/%s/ 下未找到可识别的规则文件；请检查格式目录", repo, format)
	}
	if err := g.save(c); err != nil {
		return GitHubCatalog{}, fmt.Errorf("缓存目录失败: %w", err)
	}
	g.mu.Lock()
	g.catalog[catalogKey(c.Repo, c.Branch, c.Format)] = c
	g.mu.Unlock()
	return c, nil
}

// GetOrRefresh returns the cached catalog; when force is true or nothing is
// cached it performs a real refresh. Stale data is never silently presented
// as fresh — the Source field always says where it came from.
func (g *GitHubIndex) GetOrRefresh(ctx context.Context, repo, branch, format string, force bool) (GitHubCatalog, error) {
	if !force {
		if c, ok := g.Get(repo, branch, format); ok {
			return c, nil
		}
	}
	return g.Refresh(ctx, repo, branch, format)
}

// CategoryURL resolves a category name to its raw download URL. Only names
// present in the cached catalog are accepted (no arbitrary URL injection).
func (g *GitHubIndex) CategoryURL(repo, branch, format, category string) (string, error) {
	c, ok := g.Get(repo, branch, format)
	if !ok {
		return "", fmt.Errorf("目录尚未加载，请先刷新 GitHub 规则集目录")
	}
	for _, cat := range c.Categories {
		if cat.Name == category {
			return cat.URL, nil
		}
	}
	return "", fmt.Errorf("分类 %q 不在已识别目录中", category)
}
