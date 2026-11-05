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

func postUpdate(t *testing.T, url, apiToken string) (*http.Response, string) {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, url+"/api/v1/update/check", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+apiToken)
	r.Header.Set("Content-Type", "application/json")
	// Deliberately no X-GitHub-Token: the public repository must work without
	// any user-supplied credential.
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// The repository is public now, so the update panel must reach GitHub Releases
// automatically with NO token at all. This locks in the removal of the token
// field that used to block every check on a fresh install.
func TestUpdateCheckWorksWithoutAnyToken(t *testing.T) {
	t.Setenv("NGG_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name":"v9.9.9","name":"r","published_at":"2026-11-05T00:00:00Z",`+
			`"body":"notes","html_url":"https://example.invalid/r",`+
			`"assets":[{"name":"NGG-Setup-v9.9.9.exe","size":10,"url":"https://example.invalid/a"}]}`)
	}))
	defer gh.Close()

	ts, apiToken := newUpdateAPIServer(t, gh.URL)

	resp, body := postUpdate(t, ts.URL, apiToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tokenless check: got %d, body=%s", resp.StatusCode, body)
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
		t.Fatal("no token was configured, token_configured must be false")
	}
}
