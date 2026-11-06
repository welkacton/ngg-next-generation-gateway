package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
)

// v2.7 网关总控端点：/gateway/master、/gateway/datapath*、/gateway/master/split-rules。
//
// The routes are registered here on a private mux instead of the shared one so
// this file keeps working whether or not the main server has wired them yet;
// the auth middleware is the real one, so 401 behaviour is covered too.

func newGatewayMasterTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Server.Token = "test-token-master"
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/gateway/master", srv.handleGatewayMasterGet)
	mux.HandleFunc("PUT /api/v1/gateway/master", srv.handleGatewayMasterPut)
	mux.HandleFunc("GET /api/v1/gateway/datapath", srv.handleGatewayDatapath)
	mux.HandleFunc("POST /api/v1/gateway/datapath/apply", srv.handleGatewayDatapathApply)
	mux.HandleFunc("POST /api/v1/gateway/datapath/revert", srv.handleGatewayDatapathRevert)
	mux.HandleFunc("POST /api/v1/gateway/master/split-rules", srv.handleGatewaySplitRules)

	ts := httptest.NewServer(srv.middleware(mux))
	t.Cleanup(ts.Close)
	return ts, cfg.Server.Token
}

type masterStatusPayload struct {
	Master map[string]any `json:"master"`
	Status struct {
		Platform               string `json:"platform"`
		Elevated               bool   `json:"elevated"`
		AdapterPresent         bool   `json:"adapter_present"`
		AdapterName            string `json:"adapter_name"`
		AdapterMetric          int    `json:"adapter_metric"`
		DefaultRouteViaAdapter bool   `json:"default_route_via_adapter"`
		TunSupported           bool   `json:"tun_supported"`
		WintunPresent          bool   `json:"wintun_present"`
		CaptureActive          bool   `json:"capture_active"`
		Reason                 string `json:"reason"`
		CheckedAt              string `json:"checked_at"`
	} `json:"status"`
}

func TestGatewayMasterEndpoints(t *testing.T) {
	ts, token := newGatewayMasterTestServer(t)

	getMaster := func() masterStatusPayload {
		t.Helper()
		resp, body := req(t, "GET", ts.URL+"/api/v1/gateway/master", token, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET master: %d %s", resp.StatusCode, body)
		}
		var got masterStatusPayload
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		return got
	}

	// 1) 默认值必须与契约逐字一致，status 来自真实主机。
	first := getMaster()
	if first.Master["adapter_name"] != "NGG" {
		t.Errorf("adapter_name = %v, want NGG", first.Master["adapter_name"])
	}
	if first.Master["adapter_metric"] != float64(0) {
		t.Errorf("adapter_metric = %v, want 0", first.Master["adapter_metric"])
	}
	if first.Master["capture_all"] != true || first.Master["bypass_private"] != true {
		t.Errorf("default booleans = %v / %v, want true", first.Master["capture_all"], first.Master["bypass_private"])
	}
	if first.Master["enabled"] != false {
		t.Errorf("enabled = %v, want false", first.Master["enabled"])
	}
	split, _ := first.Master["split"].(map[string]any)
	if split["mode"] != "smart" || split["default"] != "local" {
		t.Errorf("split = %v", split)
	}
	uplink, _ := first.Master["uplink"].(map[string]any)
	if uplink["mode"] != "gateway-wan" || uplink["wan_id"] != "wan1" {
		t.Errorf("uplink = %v", uplink)
	}
	if first.Status.Platform != runtime.GOOS {
		t.Errorf("status.platform = %q, want %q", first.Status.Platform, runtime.GOOS)
	}
	if first.Status.CheckedAt == "" {
		t.Error("status.checked_at must be filled")
	}
	if first.Status.Reason == "" {
		t.Error("status.reason must explain the state")
	}
	if first.Status.AdapterMetric < -1 {
		t.Errorf("adapter_metric = %d, want -1 or a real metric", first.Status.AdapterMetric)
	}
	if first.Status.CaptureActive && !first.Status.DefaultRouteViaAdapter {
		t.Error("capture_active requires default_route_via_adapter")
	}

	// 2) 合法 PUT：返回 changed / persisted / 诚实 warnings。
	master := map[string]any{}
	for k, v := range first.Master {
		master[k] = v
	}
	master["enabled"] = true
	master["adapter_name"] = "NGG-Test"
	master["adapter_metric"] = 100
	blob, _ := json.Marshal(master)
	resp, body := req(t, "PUT", ts.URL+"/api/v1/gateway/master", token, string(blob))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT master: %d %s", resp.StatusCode, body)
	}
	var saved struct {
		Master    map[string]any `json:"master"`
		Changed   []string       `json:"changed"`
		Persisted bool           `json:"persisted"`
		Warnings  []string       `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(body), &saved); err != nil {
		t.Fatalf("decode PUT: %v (%s)", err, body)
	}
	if !saved.Persisted {
		t.Error("a valid PUT must report persisted=true")
	}
	if saved.Master["adapter_metric"] != float64(100) || saved.Master["enabled"] != true {
		t.Errorf("saved master = %v", saved.Master)
	}
	wantPaths := map[string]bool{"master.enabled": false, "master.adapter_metric": false, "master.adapter_name": false}
	for _, c := range saved.Changed {
		if _, ok := wantPaths[c]; ok {
			wantPaths[c] = true
		}
	}
	for path, seen := range wantPaths {
		if !seen {
			t.Errorf("changed %v is missing %q", saved.Changed, path)
		}
	}
	joined := strings.Join(saved.Warnings, " | ")
	if !strings.Contains(joined, "管理员") {
		t.Errorf("warnings must mention the privilege requirement: %v", saved.Warnings)
	}
	if !strings.Contains(joined, "内核") {
		t.Errorf("warnings must mention the kernel reload requirement: %v", saved.Warnings)
	}

	// 3) 保存后 GET 必须读回同一份配置。
	again := getMaster()
	if again.Master["adapter_metric"] != float64(100) || again.Master["adapter_name"] != "NGG-Test" {
		t.Fatalf("saved master not returned: %v", again.Master)
	}

	// 4) 非法 metric：422 且不落盘。
	bad := map[string]any{}
	for k, v := range again.Master {
		bad[k] = v
	}
	bad["adapter_metric"] = 10000
	blob, _ = json.Marshal(bad)
	resp, body = req(t, "PUT", ts.URL+"/api/v1/gateway/master", token, string(blob))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid metric: got %d, want 422 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(body, "CONFIG_INVALID") {
		t.Errorf("422 must carry CONFIG_INVALID: %s", body)
	}

	// 5) 非法 split.mode：422。
	badMode := map[string]any{}
	for k, v := range again.Master {
		badMode[k] = v
	}
	badMode["split"] = map[string]any{
		"mode": "auto", "default": "local",
		"proxy_domains": []string{}, "local_domains": []string{},
		"proxy_cidrs": []string{}, "local_cidrs": []string{},
	}
	blob, _ = json.Marshal(badMode)
	resp, body = req(t, "PUT", ts.URL+"/api/v1/gateway/master", token, string(blob))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid split.mode: got %d, want 422 (%s)", resp.StatusCode, body)
	}

	after := getMaster()
	if after.Master["adapter_metric"] != float64(100) || after.Master["adapter_name"] != "NGG-Test" {
		t.Fatalf("a rejected PUT changed the stored master: %v", after.Master)
	}

	// 6) datapath 状态：结构固定，数值真实。
	resp, body = req(t, "GET", ts.URL+"/api/v1/gateway/datapath", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET datapath: %d %s", resp.StatusCode, body)
	}
	var dp map[string]any
	if err := json.Unmarshal([]byte(body), &dp); err != nil {
		t.Fatalf("decode datapath: %v (%s)", err, body)
	}
	for _, key := range []string{
		"platform", "elevated", "adapter_present", "adapter_name", "adapter_metric",
		"default_route_via_adapter", "tun_supported", "wintun_present", "capture_active",
		"reason", "checked_at",
	} {
		if _, ok := dp[key]; !ok {
			t.Errorf("datapath is missing %q: %s", key, body)
		}
	}
	if dp["platform"] != runtime.GOOS {
		t.Errorf("datapath.platform = %v, want %q", dp["platform"], runtime.GOOS)
	}

	// 7) apply / revert：非 Windows 只能生成命令，永远不能自称已生效。
	for _, path := range []string{"/api/v1/gateway/datapath/apply", "/api/v1/gateway/datapath/revert"} {
		resp, body = req(t, "POST", ts.URL+path, token, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s: %d %s", path, resp.StatusCode, body)
		}
		var res struct {
			Applied  bool `json:"applied"`
			Executed bool `json:"executed"`
			Commands []string
			Reason   string
			Status   map[string]any
		}
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, body)
		}
		if res.Applied {
			t.Errorf("%s must not claim applied=true here: %s", path, body)
		}
		if runtime.GOOS != "windows" && res.Executed {
			t.Errorf("%s must not execute on %s: %s", path, runtime.GOOS, body)
		}
		if len(res.Commands) == 0 {
			t.Errorf("%s must return the plan for manual execution: %s", path, body)
		}
		if res.Reason == "" {
			t.Errorf("%s must explain why nothing was applied", path)
		}
		for _, c := range res.Commands {
			if strings.Contains(c, `""`) {
				t.Errorf("%s contains an empty argument: %q", path, c)
			}
		}
	}
}

func TestGatewaySplitRulesEndpoint(t *testing.T) {
	ts, token := newGatewayMasterTestServer(t)

	// A direct-only policy needs no proxy kernel, so this test is deterministic
	// on any host: the rules are plain DIRECT entries.
	master := map[string]any{
		"enabled": true, "adapter_name": "NGG", "adapter_metric": 0,
		"capture_all": true, "auto_start": false, "bypass_private": true,
		"bypass_cidrs": []string{},
		"split": map[string]any{
			"mode": "smart", "default": "local",
			"proxy_domains": []string{}, "local_domains": []string{"intranet.lan", "*.corp.example.com"},
			"proxy_cidrs": []string{}, "local_cidrs": []string{"10.1.0.0/16"},
		},
		"uplink": map[string]any{"mode": "gateway-wan", "wan_id": "wan1", "pipeline_id": ""},
	}
	blob, _ := json.Marshal(master)
	resp, body := req(t, "PUT", ts.URL+"/api/v1/gateway/master", token, string(blob))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT master: %d %s", resp.StatusCode, body)
	}

	type splitResult struct {
		Added         int    `json:"added"`
		Skipped       int    `json:"skipped"`
		DefaultAction string `json:"default_action"`
		Rules         []struct {
			ID      string   `json:"id"`
			Type    string   `json:"type"`
			Value   string   `json:"value"`
			Action  string   `json:"action"`
			Tag     string   `json:"tag"`
			Enabled bool     `json:"enabled"`
			Comment string   `json:"comment"`
			Chain   []string `json:"chain"`
		} `json:"rules"`
		Warnings []string `json:"warnings"`
	}

	resp, body = req(t, "POST", ts.URL+"/api/v1/gateway/master/split-rules", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST split-rules: %d %s", resp.StatusCode, body)
	}
	var first splitResult
	if err := json.Unmarshal([]byte(body), &first); err != nil {
		t.Fatalf("decode split-rules: %v (%s)", err, body)
	}
	if first.Added != 3 || first.Skipped != 0 {
		t.Fatalf("added/skipped = %d/%d, want 3/0 (%s)", first.Added, first.Skipped, body)
	}
	if len(first.Rules) != 3 {
		t.Fatalf("rules = %d, want 3", len(first.Rules))
	}
	seen := map[string]bool{}
	for _, r := range first.Rules {
		if r.Tag != "gateway-master" {
			t.Errorf("rule %s tag = %q, want gateway-master", r.ID, r.Tag)
		}
		if r.Action != "DIRECT" {
			t.Errorf("rule %s action = %q, want DIRECT", r.ID, r.Action)
		}
		if !r.Enabled {
			t.Errorf("rule %s must be enabled", r.ID)
		}
		switch r.Type {
		case "DOMAIN-SUFFIX":
			seen["domain:"+r.Value] = true
		case "IP-CIDR":
			seen["cidr:"+r.Value] = true
		default:
			t.Errorf("rule %s type = %q", r.ID, r.Type)
		}
	}
	for _, want := range []string{"domain:intranet.lan", "domain:corp.example.com", "cidr:10.1.0.0/16"} {
		if !seen[want] {
			t.Errorf("split rules %v are missing %q", seen, want)
		}
	}
	if first.DefaultAction != "DIRECT" {
		t.Errorf("default_action = %q, want DIRECT for mode=smart/default=local", first.DefaultAction)
	}
	if len(first.Warnings) == 0 || !strings.Contains(strings.Join(first.Warnings, ""), "内核") {
		t.Errorf("split-rules must warn about the kernel reload: %v", first.Warnings)
	}

	// Idempotent: a second call adds nothing and reports the duplicates.
	resp, body = req(t, "POST", ts.URL+"/api/v1/gateway/master/split-rules", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST split-rules (2): %d %s", resp.StatusCode, body)
	}
	var second splitResult
	if err := json.Unmarshal([]byte(body), &second); err != nil {
		t.Fatalf("decode split-rules 2: %v (%s)", err, body)
	}
	if second.Added != 0 || second.Skipped != 3 {
		t.Fatalf("second call added/skipped = %d/%d, want 0/3 (%s)", second.Added, second.Skipped, body)
	}
	if len(second.Rules) != 0 {
		t.Errorf("second call must not return new rules: %v", second.Rules)
	}

	// An invalid CIDR must be rejected before it can reach the rule engine.
	master["split"] = map[string]any{
		"mode": "smart", "default": "local",
		"proxy_domains": []string{}, "local_domains": []string{},
		"proxy_cidrs": []string{}, "local_cidrs": []string{"10.0.0.0/64"},
	}
	blob, _ = json.Marshal(master)
	resp, body = req(t, "PUT", ts.URL+"/api/v1/gateway/master", token, string(blob))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid cidr PUT: got %d, want 422 (%s)", resp.StatusCode, body)
	}
}

func TestGatewayMasterRequiresAuth(t *testing.T) {
	ts, _ := newGatewayMasterTestServer(t)
	cases := []struct{ method, path string }{
		{"GET", "/api/v1/gateway/master"},
		{"PUT", "/api/v1/gateway/master"},
		{"GET", "/api/v1/gateway/datapath"},
		{"POST", "/api/v1/gateway/datapath/apply"},
		{"POST", "/api/v1/gateway/datapath/revert"},
		{"POST", "/api/v1/gateway/master/split-rules"},
	}
	for _, c := range cases {
		resp, _ := req(t, c.method, ts.URL+c.path, "", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without token: got %d, want 401", c.method, c.path, resp.StatusCode)
		}
	}
}
