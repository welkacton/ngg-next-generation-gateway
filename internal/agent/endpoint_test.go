package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestEndpoint(t *testing.T) (*Endpoint, *Store, string, string) {
	t.Helper()
	store := newTestStore(t)
	cfg := NewServerStore(t.TempDir())
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	view, token, err := store.Create("edge", "198.51.100.30", 2222, "", "outbound")
	if err != nil {
		t.Fatal(err)
	}
	return NewEndpoint(store, cfg), store, view.ID, token
}

func TestPingIsPublic(t *testing.T) {
	ep, _, _, _ := newTestEndpoint(t)
	rec := httptest.NewRecorder()
	ep.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/agent/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ping status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["service"] != "ngg-agent-endpoint" {
		t.Fatalf("unexpected ping body: %v", body)
	}
}

func TestReportAuthAndSuccess(t *testing.T) {
	ep, store, id, token := newTestEndpoint(t)
	valid := func() string {
		b, _ := json.Marshal(ReportPayload{
			AgentID: id, Hostname: "h1", OS: "linux", Arch: "amd64", AgentVersion: "2.7.0",
			Metrics: AgentMetrics{CPUPct: 5, MemUsed: 10},
		})
		return string(b)
	}

	cases := []struct {
		name   string
		body   string
		header string
		want   int
	}{
		{"missing token", valid(), "", http.StatusUnauthorized},
		{"wrong token", valid(), "nope", http.StatusUnauthorized},
		{"unknown agent", `{"agent_id":"ag-nope","metrics":{}}`, token, http.StatusUnauthorized},
		{"bad json", `{`, token, http.StatusBadRequest},
		{"missing agent_id", `{"metrics":{}}`, token, http.StatusBadRequest},
		{"ok", valid(), token, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/agent/report", strings.NewReader(tc.body))
			if tc.header != "" {
				req.Header.Set("X-NGG-Agent-Token", tc.header)
			}
			rec := httptest.NewRecorder()
			ep.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Code == http.StatusUnauthorized && strings.Contains(strings.ToLower(rec.Body.String()), "unknown") {
				t.Fatal("401 must not reveal whether the agent id exists")
			}
		})
	}

	got, _ := store.Get(id)
	if got.Metrics == nil || got.Metrics.CPUPct != 5 {
		t.Fatalf("successful report not stored: %+v", got.Metrics)
	}
	if !got.Online {
		t.Fatal("agent should be online after a successful report")
	}
}

func TestReportSuccessBody(t *testing.T) {
	ep, _, id, token := newTestEndpoint(t)
	body, _ := json.Marshal(ReportPayload{AgentID: id, Metrics: AgentMetrics{CPUPct: 1}})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/report", strings.NewReader(string(body)))
	req.Header.Set("X-NGG-Agent-Token", token)
	rec := httptest.NewRecorder()
	ep.Handler().ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["interval_sec"] != float64(DefaultIntervalSec) || out["config_version"] != float64(1) {
		t.Fatalf("unexpected success body: %v", out)
	}
}

func TestSubscriptionPlaceholder(t *testing.T) {
	ep, _, _, _ := newTestEndpoint(t)
	rec := httptest.NewRecorder()
	ep.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/abc123", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("without a renderer /sub must 404, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("404 body must be empty to avoid leaking information, got %q", rec.Body.String())
	}
}

func TestSubscriptionFunc(t *testing.T) {
	ep, _, _, _ := newTestEndpoint(t)
	ep.SetSubscription(func(ctx context.Context, token string) (string, map[string]string, bool) {
		if token != "dev-token" {
			return "", nil, false
		}
		return `{"outbounds":[]}`, map[string]string{
			"subscription-userinfo": "upload=0; download=42; total=100",
			"Content-Type":          "application/json",
		}, true
	})

	rec := httptest.NewRecorder()
	ep.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/dev-token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("known token must return 200, got %d", rec.Code)
	}
	if rec.Header().Get("subscription-userinfo") == "" {
		t.Fatal("subscription-userinfo header missing")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type must stay text/plain, got %q", ct)
	}

	rec = httptest.NewRecorder()
	ep.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/other", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token must 404, got %d", rec.Code)
	}
}

func TestEnabledFollowsConfig(t *testing.T) {
	ep, _, _, _ := newTestEndpoint(t)
	if !ep.Enabled() {
		t.Fatal("endpoint should default to enabled")
	}
	if _, _, err := ep.cfg.Save(ServerConfig{Enabled: false, Listen: "127.0.0.1", Port: 9191}); err != nil {
		t.Fatal(err)
	}
	if ep.Enabled() {
		t.Fatal("Enabled must follow the persisted config")
	}
}
