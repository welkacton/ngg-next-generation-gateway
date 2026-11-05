package rulesets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// A small stand-in for blackmatrix7's tree, including the noise the real
// repo contains (README, _No_Resolve variants, other formats, nested dirs).
func fakeGitHub(t *testing.T) (*httptest.Server, *GitHubIndex) {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"truncated":false,"tree":[`+
				`{"path":"rule/Clash/Google/Google.list"},`+
				`{"path":"rule/Clash/Google/Google.yaml"},`+
				`{"path":"rule/Clash/Google/Google_No_Resolve.yaml"},`+
				`{"path":"rule/Clash/Google/README.md"},`+
				`{"path":"rule/Clash/YouTube/YouTube.list"},`+
				`{"path":"rule/Clash/OpenAI/OpenAI.list"},`+
				`{"path":"rule/Surge/Google/Google.list"},`+
				`{"path":"rule/Clash/Broken/Broken.list"},`+
				`{"path":"README.md"},`+
				`{"path":"rule/Clash/Weird/nested/deep.list"}`+
				`]}`)
		case strings.HasSuffix(r.URL.Path, "/Google/Google.list"):
			fmt.Fprint(w, "# NAME: Google\n# TOTAL: 3\nDOMAIN-SUFFIX,google.com\nDOMAIN,www.google.com\nIP-CIDR,8.8.8.8/32\n")
		case strings.HasSuffix(r.URL.Path, "/YouTube/YouTube.list"):
			fmt.Fprint(w, "DOMAIN-SUFFIX,youtube.com\nDOMAIN-SUFFIX,ytimg.com\n")
		case strings.HasSuffix(r.URL.Path, "/OpenAI/OpenAI.list"):
			fmt.Fprint(w, "DOMAIN-SUFFIX,openai.com\n")
		case strings.HasSuffix(r.URL.Path, "/Broken/Broken.list"):
			fmt.Fprint(w, "# no rules here\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	idx := NewGitHubIndex(t.TempDir(), nil)
	idx.APIBase = ts.URL
	idx.RawBase = ts.URL
	return ts, idx
}

func TestGitHubRefreshDiscoversRealCategories(t *testing.T) {
	_, idx := fakeGitHub(t)
	c, err := idx.Refresh(context.Background(), "blackmatrix7/ios_rule_script", "master", "Clash")
	if err != nil {
		t.Fatal(err)
	}
	if c.Total != 4 {
		t.Fatalf("expected 4 Clash categories, got %d: %+v", c.Total, c.Categories)
	}
	want := []string{"Broken", "Google", "OpenAI", "YouTube"}
	for i, name := range want {
		if c.Categories[i].Name != name {
			t.Fatalf("category %d: got %s want %s", i, c.Categories[i].Name, name)
		}
	}
	// Google has both .list and .yaml; the .list must win.
	for _, cat := range c.Categories {
		if cat.Name == "Google" {
			if !strings.HasSuffix(cat.File, "Google.list") {
				t.Fatalf("Google should prefer .list, got %s", cat.File)
			}
			if !strings.HasPrefix(cat.URL, "http") {
				t.Fatalf("missing raw URL: %+v", cat)
			}
		}
		if strings.Contains(cat.File, "Surge") {
			t.Fatal("other formats must not leak into the Clash catalog")
		}
	}
	if c.Source != "github-api" || c.Truncated {
		t.Fatalf("unexpected metadata: %+v", c)
	}

	got, ok := idx.Get("blackmatrix7/ios_rule_script", "master", "Clash")
	if !ok || got.Total != 4 {
		t.Fatalf("catalog not cached in memory: %+v", got)
	}
	// A brand-new index over the same data dir must load the snapshot from
	// disk without any network call, and label it as cache (not fresh).
	// idx.dir is already <dataDir>/ruleset-github, so pass its parent here.
	reopened := NewGitHubIndex(filepath.Dir(idx.dir), nil)
	fromDisk, ok := reopened.Get("blackmatrix7/ios_rule_script", "master", "Clash")
	if !ok || fromDisk.Total != 4 || fromDisk.Source != "cache" {
		t.Fatalf("disk cache not loaded/labelled correctly: %+v", fromDisk)
	}
	if u, err := idx.CategoryURL("blackmatrix7/ios_rule_script", "master", "Clash", "Google"); err != nil || !strings.HasSuffix(u, "Google.list") {
		t.Fatalf("category resolution: u=%q err=%v", u, err)
	}
	if _, err := idx.CategoryURL("blackmatrix7/ios_rule_script", "master", "Clash", "NotThere"); err == nil {
		t.Fatal("unknown category must be rejected (no arbitrary URL injection)")
	}
}

func TestGitHubDownloadsAndParsesRealRules(t *testing.T) {
	_, idx := fakeGitHub(t)
	if _, err := idx.Refresh(context.Background(), "blackmatrix7/ios_rule_script", "master", "Clash"); err != nil {
		t.Fatal(err)
	}
	u, _ := idx.CategoryURL("blackmatrix7/ios_rule_script", "master", "Clash", "Google")
	text, err := FetchURL(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(text, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Count != 3 {
		t.Fatalf("expected 3 real rules, got %d: %+v", parsed.Count, parsed.Rules)
	}
	if parsed.Rules[0].Type != "DOMAIN-SUFFIX" || parsed.Rules[0].Value != "google.com" {
		t.Fatalf("unexpected first rule: %+v", parsed.Rules[0])
	}
}

func TestGitHubRefreshRejectsBadRepoAndErrors(t *testing.T) {
	_, idx := fakeGitHub(t)
	if _, err := idx.Refresh(context.Background(), "not-a-repo", "", ""); err == nil {
		t.Fatal("malformed repo must be rejected")
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()
	idx.APIBase = ts.URL
	_, err := idx.Refresh(context.Background(), "owner/repo", "main", "Clash")
	if err == nil || !strings.Contains(err.Error(), "限流") {
		t.Fatalf("rate limit must give actionable error, got %v", err)
	}
}

func TestGitHubPresetsAreWellFormed(t *testing.T) {
	for _, p := range Presets() {
		if !strings.Contains(p.Repo, "/") || p.Branch == "" || len(p.Formats) == 0 {
			t.Fatalf("bad preset: %+v", p)
		}
	}
}

func TestGitHubCatalogJSONRoundTrip(t *testing.T) {
	_, idx := fakeGitHub(t)
	c, err := idx.Refresh(context.Background(), "blackmatrix7/ios_rule_script", "master", "Clash")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back GitHubCatalog
	if err := json.Unmarshal(b, &back); err != nil || back.Total != c.Total {
		t.Fatalf("round trip failed: %v %+v", err, back)
	}
}
