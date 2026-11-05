package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/profiles"
)

// POST /profiles/split is the endpoint the "完整配置" editor calls while the
// user types. It must be authenticated, return six sections, and reject
// malformed configs without touching the store.
func TestProfileSplitEndpoint(t *testing.T) {
	ts, token := newTestServer(t)
	p := profiles.Template("mihomo", "Split-API")
	full, _, err := profiles.Render(p)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(full, "port: 7890", "port: 7891", 1)
	payload, _ := json.Marshal(map[string]any{"profile": p, "content": edited})

	resp, body := req(t, "POST", ts.URL+"/api/v1/profiles/split", token, string(payload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("split: got %d %s", resp.StatusCode, body)
	}
	var out struct {
		OK       bool               `json:"ok"`
		Sections []profiles.Section `json:"sections"`
		Format   string             `json:"format"`
		Changed  []string           `json:"changed"`
		Warnings []string           `json:"warnings"`
		Message  string             `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if !out.OK || len(out.Sections) != 6 || out.Format != "yaml" {
		t.Fatalf("unexpected result: %+v", out)
	}
	found := false
	for _, s := range out.Sections {
		if s.Key == "inbounds" && strings.Contains(s.Content, "7891") {
			found = true
		}
	}
	if !found {
		t.Fatalf("edited port missing from inbounds: %s", body)
	}
	if !containsStr(out.Changed, "inbounds") {
		t.Fatalf("changed sections should include inbounds: %v", out.Changed)
	}

	// Malformed config -> 422, no partial section update.
	bad, _ := json.Marshal(map[string]any{"profile": p, "content": "proxies: ["})
	resp2, body2 := req(t, "POST", ts.URL+"/api/v1/profiles/split", token, string(bad))
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("broken config: got %d %s", resp2.StatusCode, body2)
	}

	// Missing kernel -> 400.
	resp3, _ := req(t, "POST", ts.URL+"/api/v1/profiles/split", token, `{"content":"mode: rule"}`)
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing kernel: got %d", resp3.StatusCode)
	}

	// The route is behind the same auth gate as every other API route.
	resp4, _ := req(t, "POST", ts.URL+"/api/v1/profiles/split", "", string(payload))
	if resp4.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated split: got %d", resp4.StatusCode)
	}
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
