package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
)

// newGitHubTestServer mirrors newTestServer but hands back the *Server so the
// test can point the GitHub index at a local fake without touching shared
// test helpers or global state.
func newGitHubTestServer(t *testing.T) (*httptest.Server, string, *Server) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Server.Token = "test-token-abcdef"
	cfg.Server.Listen = "127.0.0.1"
	cfg.Server.Port = 19090
	cfg.Server.WebDir = filepath.Join(root, "no-web-dist")
	cfg.Storage.Dir = filepath.Join(root, "data")
	cfg.Logging.Dir = filepath.Join(root, "logs")
	cfg.Cores.DescriptorDir = filepath.Join(root, "cores.d")
	eng, err := engine.New(&cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown(nil) })
	srv := New(&cfg, eng)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, cfg.Server.Token, srv
}

// fakeGitHubAPI serves both the git-trees index and the raw rule files, so
// the /rulesets/github/* endpoints are exercised end-to-end without real
// network access or any mocked "success".
func fakeGitHubAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"truncated":false,"tree":[`+
				`{"path":"rule/Clash/Google/Google.list"},`+
				`{"path":"rule/Clash/YouTube/YouTube.list"},`+
				`{"path":"rule/Clash/Empty/Empty.list"}`+
				`]}`)
		case strings.HasSuffix(r.URL.Path, "/Google/Google.list"):
			fmt.Fprint(w, "# NAME: Google\nDOMAIN-SUFFIX,google.com\nDOMAIN,www.google.com\n")
		case strings.HasSuffix(r.URL.Path, "/YouTube/YouTube.list"):
			fmt.Fprint(w, "DOMAIN-SUFFIX,youtube.com\nIP-CIDR,8.8.8.8/32,no-resolve\n")
		case strings.HasSuffix(r.URL.Path, "/Empty/Empty.list"):
			fmt.Fprint(w, "# this category has no recognisable rules\n")
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestGitHubRuleSetEndpointsEndToEnd(t *testing.T) {
	gh := fakeGitHubAPI(t)
	defer gh.Close()

	ts, token, srv := newGitHubTestServer(t)
	srv.githubIndex.APIBase = gh.URL
	srv.githubIndex.RawBase = gh.URL

	// presets
	resp, body := req(t, "GET", ts.URL+"/api/v1/rulesets/github/presets", token, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "blackmatrix7") {
		t.Fatalf("presets: %d %s", resp.StatusCode, body)
	}

	// catalog refresh (real tree parsing)
	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets/github/catalog/refresh", token,
		`{"repo":"blackmatrix7/ios_rule_script","branch":"master","format":"Clash"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("catalog refresh: %d %s", resp.StatusCode, body)
	}
	var cat struct {
		Total      int `json:"total"`
		Categories []struct {
			Name string `json:"name"`
		} `json:"categories"`
	}
	if err := json.Unmarshal([]byte(body), &cat); err != nil || cat.Total != 3 {
		t.Fatalf("catalog parse: total=%d err=%v body=%s", cat.Total, err, body)
	}

	// mount mode: each category becomes a real URL rule set
	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets/github/mount", token,
		`{"repo":"blackmatrix7/ios_rule_script","branch":"master","format":"Clash",`+
			`"categories":["Google","YouTube"],"mode":"mount"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mount: %d %s", resp.StatusCode, body)
	}
	var mount struct {
		Recognised int `json:"recognised"`
		Mounted    int `json:"mounted"`
		Failed     int `json:"failed"`
		Results    []struct {
			Category   string `json:"category"`
			Recognised int    `json:"recognised"`
			Mounted    bool   `json:"mounted"`
			RuleSetID  string `json:"ruleset_id"`
			Error      string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &mount); err != nil {
		t.Fatal(err)
	}
	if mount.Mounted != 2 || mount.Failed != 0 || mount.Recognised != 4 {
		t.Fatalf("unexpected mount outcome: %+v", mount)
	}
	for _, r := range mount.Results {
		if r.Recognised != 2 || !r.Mounted || r.RuleSetID == "" {
			t.Fatalf("category not really mounted: %+v", r)
		}
	}

	// The mounted sets must be visible through the normal rule-set API.
	resp, listBody := req(t, "GET", ts.URL+"/api/v1/rulesets", token, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(listBody, "gh:blackmatrix7-ios_rule_script:google") {
		t.Fatalf("mounted sets missing from registry: %d %s", resp.StatusCode, listBody)
	}

	// import mode: real rules land in the live rule table
	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets/github/mount", token,
		`{"repo":"blackmatrix7/ios_rule_script","branch":"master","format":"Clash",`+
			`"categories":["Google"],"mode":"import","action":"DIRECT"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	var imp struct {
		RulesAdded int `json:"rules_added"`
	}
	if err := json.Unmarshal([]byte(body), &imp); err != nil || imp.RulesAdded != 2 {
		t.Fatalf("import must add 2 real rules: %+v err=%v body=%s", imp, err, body)
	}
	resp, rulesBody := req(t, "GET", ts.URL+"/api/v1/network/rules", token, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(rulesBody, "google.com") {
		t.Fatalf("imported rules missing from live table: %d %s", resp.StatusCode, rulesBody)
	}

	// Re-import is idempotent (no duplicate ids).
	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets/github/mount", token,
		`{"repo":"blackmatrix7/ios_rule_script","branch":"master","format":"Clash",`+
			`"categories":["Google"],"mode":"import","action":"DIRECT"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-import: %d %s", resp.StatusCode, body)
	}
	var reimp struct {
		RulesAdded int `json:"rules_added"`
	}
	_ = json.Unmarshal([]byte(body), &reimp)
	if reimp.RulesAdded != 0 {
		t.Fatalf("re-import must be idempotent, added %d", reimp.RulesAdded)
	}
}

func TestGitHubMountReportsFailuresHonestly(t *testing.T) {
	gh := fakeGitHubAPI(t)
	defer gh.Close()
	ts, token, srv := newGitHubTestServer(t)
	srv.githubIndex.APIBase, srv.githubIndex.RawBase = gh.URL, gh.URL
	_, _ = req(t, "POST", ts.URL+"/api/v1/rulesets/github/catalog/refresh", token,
		`{"repo":"blackmatrix7/ios_rule_script","branch":"master","format":"Clash"}`)

	// Empty category has no recognisable rules → must be reported as failure.
	resp, body := req(t, "POST", ts.URL+"/api/v1/rulesets/github/mount", token,
		`{"repo":"blackmatrix7/ios_rule_script","branch":"master","format":"Clash",`+
			`"categories":["Empty"],"mode":"mount"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mount: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "解析失败") {
		t.Fatalf("failure must be reported, not hidden: %s", body)
	}

	// Unknown category and over-limit batches must be rejected before any fetch.
	resp, _ = req(t, "POST", ts.URL+"/api/v1/rulesets/github/mount", token,
		`{"repo":"blackmatrix7/ios_rule_script","categories":["Nope"],"mode":"mount"}`)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("unknown category must be rejected")
	}
	many := make([]string, 51)
	for i := range many {
		many[i] = "Google"
	}
	b, _ := json.Marshal(map[string]any{"repo": "blackmatrix7/ios_rule_script", "categories": many, "mode": "mount"})
	resp, _ = req(t, "POST", ts.URL+"/api/v1/rulesets/github/mount", token, string(b))
	if resp.StatusCode == http.StatusOK {
		t.Fatal("over-limit batch must be rejected")
	}
}
