package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/agent"
)

func newAgentTestMux(t *testing.T) (*http.ServeMux, *agent.Store) {
	t.Helper()
	dir := t.TempDir()
	store := agent.NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	cfg := agent.NewServerStore(dir)
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	newAgentHandlers(store, cfg).Register(mux)
	return mux, store
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func TestAgentRoutesCRUD(t *testing.T) {
	mux, store := newAgentTestMux(t)

	rec, body := doJSON(t, mux, http.MethodGet, "/api/v1/agents", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if body["count"] != float64(0) {
		t.Fatalf("expected an empty registry, got %v", body)
	}
	server, _ := body["server"].(map[string]any)
	if server["port"] != float64(9191) {
		t.Fatalf("server default not returned: %v", body["server"])
	}

	rec, body = doJSON(t, mux, http.MethodPost, "/api/v1/agents",
		`{"name":"HK hk03","address":"203.0.113.7","port":2222,"note":"home","role":"both"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	token, _ := body["token"].(string)
	if len(token) != 64 {
		t.Fatalf("token must be returned exactly once, got %q", token)
	}
	view, _ := body["agent"].(map[string]any)
	id, _ := view["id"].(string)
	if id == "" || view["status"] != "never" || view["online"] != false || view["metrics"] != nil {
		t.Fatalf("unexpected view: %v", view)
	}
	if yamlText, _ := body["config_yaml"].(string); !strings.Contains(yamlText, "agent_id: "+id) || !strings.Contains(yamlText, "token: "+token) {
		t.Fatalf("config_yaml must carry id and token: %s", yamlText)
	}
	install, _ := body["install"].(map[string]any)
	for _, osName := range []string{"windows", "linux", "docker"} {
		if s, _ := install[osName].(string); s == "" {
			t.Fatalf("install.%s missing", osName)
		}
	}

	// /agents/server is a literal route and must never be treated as {id}.
	rec, body = doJSON(t, mux, http.MethodGet, "/api/v1/agents/server", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agents/server must not hit the {id} handler, status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body["listen"] != "0.0.0.0" {
		t.Fatalf("unexpected server body: %v", body)
	}

	rec, body = doJSON(t, mux, http.MethodGet, "/api/v1/agents/"+id, "")
	if rec.Code != http.StatusOK || body["id"] != id {
		t.Fatalf("get status=%d body=%v", rec.Code, body)
	}

	rec, body = doJSON(t, mux, http.MethodPut, "/api/v1/agents/"+id, `{"name":"renamed","port":2223}`)
	if rec.Code != http.StatusOK || body["name"] != "renamed" || body["port"] != float64(2223) {
		t.Fatalf("update status=%d body=%v", rec.Code, body)
	}

	rec, body = doJSON(t, mux, http.MethodPut, "/api/v1/agents/"+id, `{"port":0}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid update must be 422, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec, _ = doJSON(t, mux, http.MethodGet, "/api/v1/agents/"+id+"/metrics?limit=60", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"samples":[]`) {
		t.Fatalf("metrics status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec, _ = doJSON(t, mux, http.MethodDelete, "/api/v1/agents/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec, _ = doJSON(t, mux, http.MethodGet, "/api/v1/agents/"+id, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted agent must 404, got %d", rec.Code)
	}
	if store.Count() != 0 {
		t.Fatalf("store still has %d agents", store.Count())
	}
}

func TestAgentInstallAndRotate(t *testing.T) {
	mux, store := newAgentTestMux(t)
	_, body := doJSON(t, mux, http.MethodPost, "/api/v1/agents",
		`{"name":"n1","address":"203.0.113.8","port":2222}`)
	view, _ := body["agent"].(map[string]any)
	id, _ := view["id"].(string)
	firstToken, _ := body["token"].(string)

	rec, body := doJSON(t, mux, http.MethodGet, "/api/v1/agents/"+id+"/install?os=linux", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("install status = %d body=%s", rec.Code, rec.Body.String())
	}
	script, _ := body["script"].(string)
	if !strings.Contains(script, "systemctl enable --now ngg-agent") {
		t.Fatalf("linux install script incomplete:\n%s", script)
	}
	// The plaintext token is not stored, so later scripts must use the
	// explicit placeholder instead of leaking or inventing one.
	if strings.Contains(script, firstToken) {
		t.Fatal("install endpoint must not re-expose the token")
	}
	if !strings.Contains(script, agent.TokenPlaceholder) {
		t.Fatalf("install script must carry the placeholder:\n%s", script)
	}

	if rec, _ := doJSON(t, mux, http.MethodGet, "/api/v1/agents/"+id+"/install?os=darwin", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown os must be 400, got %d", rec.Code)
	}

	rec, body = doJSON(t, mux, http.MethodPost, "/api/v1/agents/"+id+"/rotate-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d body=%s", rec.Code, rec.Body.String())
	}
	newToken, _ := body["token"].(string)
	if newToken == "" || newToken == firstToken {
		t.Fatal("rotate must issue a fresh token")
	}
	if _, ok := body["agent"].(map[string]any); !ok {
		t.Fatal("rotate response must include the agent view")
	}
	if err := store.Report(id, firstToken, agent.AgentMetrics{}); err == nil {
		t.Fatal("old token must be invalid after rotation")
	}
	if err := store.Report(id, newToken, agent.AgentMetrics{CPUPct: 1}); err != nil {
		t.Fatalf("new token must work: %v", err)
	}
	rec, body = doJSON(t, mux, http.MethodGet, "/api/v1/agents/"+id, "")
	if rec.Code != http.StatusOK || body["status"] != "online" || body["bound"] != true {
		t.Fatalf("agent should now be online: %v", body)
	}
}

func TestAgentServerConfigEndpoint(t *testing.T) {
	mux, _ := newAgentTestMux(t)
	rec, body := doJSON(t, mux, http.MethodPut, "/api/v1/agents/server",
		`{"enabled":true,"listen":"127.0.0.1","port":9292,"public_url":"https://ngg.example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put server status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body["requires_restart"] != true {
		t.Fatalf("changing the port must require a restart: %v", body)
	}
	saved, _ := body["server"].(map[string]any)
	if saved["port"] != float64(9292) || saved["public_url"] != "https://ngg.example.com" {
		t.Fatalf("server not saved: %v", saved)
	}
	warnings, _ := body["warnings"].([]any)
	if len(warnings) != 0 {
		t.Fatalf("loopback + https should not warn: %v", warnings)
	}

	rec, body = doJSON(t, mux, http.MethodPut, "/api/v1/agents/server", `{"listen":"localhost","port":9292}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("non-IP listen must be 422, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec, body = doJSON(t, mux, http.MethodPut, "/api/v1/agents/server", `{"enabled":true,"listen":"0.0.0.0","port":9292}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	warnings, _ = body["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("public listen without public_url must warn")
	}
}
