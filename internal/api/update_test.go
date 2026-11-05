package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
)

// newUpdateAPIServer mirrors newTestServer but lets a test point the updater
// at a fake GitHub API.
func newUpdateAPIServer(t *testing.T, apiBase string) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Server.Token = "test-token-abcdef"
	cfg.Server.Listen = "127.0.0.1"
	cfg.Server.Port = 19091
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
	srv.updater.APIBase = apiBase
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, cfg.Server.Token
}

func postUpdate(t *testing.T, url, apiToken, githubToken string) (*http.Response, string) {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, url+"/api/v1/update/check", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+apiToken)
	r.Header.Set("Content-Type", "application/json")
	if githubToken != "" {
		r.Header.Set(githubTokenHeader, githubToken)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// Regression for the real bug: a private repo returns 404 to anonymous
// requests, and the Web UI (browser) cannot set server environment variables.
// The PAT must arrive via X-GitHub-Token and be usable in the same request.
func TestUpdateCheckAcceptsTokenFromWebUI(t *testing.T) {
	t.Setenv("NGG_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	const pat = "ui-pat-abc"

	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+pat {
			w.WriteHeader(http.StatusNotFound) // GitHub hides private repos as 404
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name":"v9.9.9","name":"r","published_at":"2026-11-05T00:00:00Z",`+
			`"body":"notes","html_url":"https://example.invalid/r",`+
			`"assets":[{"name":"NGG-Setup-v9.9.9.exe","size":10,"url":"https://example.invalid/a"}]}`)
	}))
	defer gh.Close()

	ts, apiToken := newUpdateAPIServer(t, gh.URL)

	// No PAT -> the same 502 the user saw, with an actionable message.
	resp, body := postUpdate(t, ts.URL, apiToken, "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("anonymous check: got %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "GitHub Token") {
		t.Fatalf("error must tell the user where to enter a token: %s", body)
	}

	// PAT from the Web UI -> real release data.
	resp, body = postUpdate(t, ts.URL, apiToken, pat)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check with UI token: got %d, body=%s", resp.StatusCode, body)
	}
	var res struct {
		Latest          string `json:"latest"`
		UpdateAvailable bool   `json:"update_available"`
		Status          struct {
			TokenConfigured bool `json:"token_configured"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("parse response: %v\n%s", err, body)
	}
	if res.Latest != "v9.9.9" || !res.UpdateAvailable {
		t.Fatalf("unexpected check result: %+v", res)
	}
	if res.Status.TokenConfigured {
		t.Fatal("token_configured describes the SERVER env only; a UI token must not be reported as server config")
	}

	// The status endpoint must never echo the token value.
	resp, body = req(t, "GET", ts.URL+"/api/v1/update/status", apiToken, "")
	if resp.StatusCode != http.StatusOK || strings.Contains(body, pat) {
		t.Fatalf("status leaked the token or failed: %d %s", resp.StatusCode, body)
	}
}
