package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
	"github.com/ngg-project/ngg/pkg/contracts"
)

func newTestServer(t *testing.T) (*httptest.Server, string) {
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
	return ts, cfg.Server.Token
}

func req(t *testing.T, method, url, token, body string) (*http.Response, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestAuthIsEnforcedOnEveryAPIRoute(t *testing.T) {
	ts, token := newTestServer(t)
	for _, path := range []string{"/api/v1/system", "/api/v1/cores", "/api/v1/registry", "/api/v1/ports", "/api/v1/audit"} {
		resp, _ := req(t, "GET", ts.URL+path, "", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without token: got %d", path, resp.StatusCode)
		}
		resp, _ = req(t, "GET", ts.URL+path, "wrong-token", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with wrong token: got %d", path, resp.StatusCode)
		}
		resp, body := req(t, "GET", ts.URL+path, token, "")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s with valid token: got %d body=%s", path, resp.StatusCode, body)
		}
	}
}

func TestSystemAndRegistryEndpoints(t *testing.T) {
	ts, token := newTestServer(t)
	_, body := req(t, "GET", ts.URL+"/api/v1/system", token, "")
	var sys contracts.SystemInfo
	if err := json.Unmarshal([]byte(body), &sys); err != nil {
		t.Fatalf("system JSON: %v %s", err, body)
	}
	if sys.Stage != 1 || sys.OS == "" || sys.Arch == "" || sys.Mode != "multi-core-parallel" {
		t.Fatalf("unexpected system info: %+v", sys)
	}

	_, body = req(t, "GET", ts.URL+"/api/v1/registry", token, "")
	var descriptors []contracts.CoreDescriptor
	if err := json.Unmarshal([]byte(body), &descriptors); err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 3 {
		t.Fatalf("expected 3 registered kernels, got %d", len(descriptors))
	}
	for _, d := range descriptors {
		if !d.Capability.WorkerMode {
			t.Errorf("%s not in Worker Mode", d.ID)
		}
	}

	_, body = req(t, "GET", ts.URL+"/api/v1/cores", token, "")
	var cores []contracts.CoreStatus
	if err := json.Unmarshal([]byte(body), &cores); err != nil {
		t.Fatal(err)
	}
	if len(cores) != 3 {
		t.Fatalf("expected 3 cores, got %d", len(cores))
	}
	for _, c := range cores {
		if c.State != contracts.StateNotInstalled {
			t.Errorf("%s should report real not_installed state, got %s", c.ID, c.State)
		}
		if len(c.Ports) != 5 {
			t.Errorf("%s has no isolated port block: %v", c.ID, c.Ports)
		}
	}
}

func TestPortIsolationAcrossRegisteredKernels(t *testing.T) {
	ts, token := newTestServer(t)
	_, body := req(t, "GET", ts.URL+"/api/v1/ports", token, "")
	var allocations []contracts.PortAllocation
	if err := json.Unmarshal([]byte(body), &allocations); err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, a := range allocations {
		for _, p := range a.Ports {
			if other, ok := seen[p]; ok {
				t.Fatalf("port %d shared by %s and %s", p, other, a.CoreID)
			}
			seen[p] = string(a.CoreID)
		}
	}
	if len(seen) != 15 {
		t.Fatalf("expected 15 isolated ports, got %d", len(seen))
	}
}

func TestStartUninstalledKernelReturnsRealError(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, "POST", ts.URL+"/api/v1/cores/mihomo/start", token, "")
	if resp.StatusCode == http.StatusOK {
		t.Fatal("starting an uninstalled kernel must not report success")
	}
	if !strings.Contains(body, "not installed") {
		t.Fatalf("expected an honest not-installed error, got: %s", body)
	}
}

func TestStaticFallbackIsHonest(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := req(t, "GET", ts.URL+"/", "", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "尚未构建") {
		t.Fatalf("expected honest 'UI not built' page, got %d %s", resp.StatusCode, body[:min(len(body), 200)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestNetworkConfigIngressEgressPatch verifies the config API accepts real
// nested ingress/tun/egress patches, reports restart requirements, and
// refuses an insecure non-loopback ingress (Fail-Closed).
func TestNetworkConfigIngressEgressPatch(t *testing.T) {
	ts, token := newTestServer(t)

	resp, body := req(t, "GET", ts.URL+"/api/v1/network/config", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET network/config: %d %s", resp.StatusCode, body)
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(body), &before); err != nil {
		t.Fatal(err)
	}
	ing, _ := before["ingress"].(map[string]any)
	if ing == nil || ing["port"].(float64) != 18080 {
		t.Fatalf("unexpected default ingress: %+v", ing)
	}

	resp, body = req(t, "PUT", ts.URL+"/api/v1/network/config", token,
		`{"ingress":{"enabled":true,"listen":"127.0.0.1","port":18099,"auth_user":"","auth_pass":""}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT ingress: %d %s", resp.StatusCode, body)
	}
	var saved map[string]any
	_ = json.Unmarshal([]byte(body), &saved)
	changed := fmt.Sprint(saved["changed"])
	if !strings.Contains(changed, "ingress") || !strings.Contains(fmt.Sprint(saved["requires_restart"]), "ingress") {
		t.Fatalf("ingress change not reported: %s", body)
	}
	if saved["persisted"] != true {
		t.Fatal("network config must be persisted to the data dir")
	}

	_, body = req(t, "GET", ts.URL+"/api/v1/network/config", token, "")
	var after map[string]any
	_ = json.Unmarshal([]byte(body), &after)
	ing2 := after["ingress"].(map[string]any)
	if ing2["port"].(float64) != 18099 {
		t.Fatalf("ingress port not applied: %+v", ing2)
	}

	resp, body = req(t, "PUT", ts.URL+"/api/v1/network/config", token,
		`{"ingress":{"enabled":true,"listen":"0.0.0.0","port":18099,"auth_user":"","auth_pass":""}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("insecure ingress must be rejected, got %d %s", resp.StatusCode, body)
	}

	resp, body = req(t, "PUT", ts.URL+"/api/v1/network/config", token,
		`{"egress":{"interface":"eth0","mark":4321,"bypass_tun":false}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT egress: %d %s", resp.StatusCode, body)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/network/config", token, "")
	var final map[string]any
	_ = json.Unmarshal([]byte(body), &final)
	eg := final["egress"].(map[string]any)
	if eg["interface"] != "eth0" || eg["mark"].(float64) != 4321 || eg["bypass_tun"] != false {
		t.Fatalf("egress patch not applied: %+v", eg)
	}
	if final["fail_closed"] != true {
		t.Fatal("unrelated fields must be preserved")
	}
}

// TestNodeRegistryAndRuleUpdate verifies the Config Engine's editable node
// registry (CRUD + JSON import + kernel config generation) and single-rule
// editing through PUT /network/rules/{id}.
func TestNodeRegistryAndRuleUpdate(t *testing.T) {
	ts, token := newTestServer(t)

	resp, body := req(t, "GET", ts.URL+"/api/v1/nodes/protocols?kind=inbound", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("protocol catalog: %d %s", resp.StatusCode, body)
	}
	var catalog []map[string]any
	if err := json.Unmarshal([]byte(body), &catalog); err != nil || len(catalog) < 10 {
		t.Fatalf("inbound protocol catalog too small: %v %d", err, len(catalog))
	}

	node := `{"id":"in-ss","name":"SS 入口","kind":"inbound","protocol":"shadowsocks","address":"0.0.0.0","port":8388,"enabled":true,"params":{"cipher":"aes-256-gcm","password":"pw"}}`
	resp, body = req(t, "POST", ts.URL+"/api/v1/nodes", token, node)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add node: %d %s", resp.StatusCode, body)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/nodes?kind=inbound", token, "")
	var listed struct {
		Nodes []map[string]any `json:"nodes"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(body), &listed); err != nil || listed.Count != 1 {
		t.Fatalf("list nodes: %v %s", err, body)
	}

	updated := `{"id":"in-ss","name":"SS 入口（已编辑）","kind":"inbound","protocol":"trojan","address":"0.0.0.0","port":443,"enabled":true,"params":{"password":"pw2"}}`
	resp, body = req(t, "PUT", ts.URL+"/api/v1/nodes/in-ss", token, updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update node: %d %s", resp.StatusCode, body)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/nodes/in-ss", token, "")
	var one map[string]any
	_ = json.Unmarshal([]byte(body), &one)
	if one["name"] != "SS 入口（已编辑）" || one["protocol"] != "trojan" {
		t.Fatalf("node edit not applied: %s", body)
	}

	resp, body = req(t, "POST", ts.URL+"/api/v1/nodes/in-ss/config", token, `{"kernel":"sing-box"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "trojan") {
		t.Fatalf("kernel config generation: %d %s", resp.StatusCode, body)
	}

	// JSON import is all-or-nothing.
	goodImport := `{"nodes":[{"id":"a","name":"A","kind":"inbound","protocol":"socks5","port":1080},{"id":"b","name":"B","kind":"outbound","protocol":"wireguard","port":51820,"params":{"private_key":"x"}}]}`
	resp, body = req(t, "PUT", ts.URL+"/api/v1/nodes/import", token, goodImport)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid JSON import rejected: %d %s", resp.StatusCode, body)
	}
	badImport := `{"nodes":[{"id":"c","name":"C","kind":"outbound","protocol":"not-a-protocol"}]}`
	resp, _ = req(t, "PUT", ts.URL+"/api/v1/nodes/import", token, badImport)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid JSON import must be rejected, got %d", resp.StatusCode)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/nodes", token, "")
	var after struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal([]byte(body), &after)
	if after.Count != 2 {
		t.Fatalf("failed import changed the node set: %s", body)
	}

	resp, body = req(t, "DELETE", ts.URL+"/api/v1/nodes/a", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete node: %d %s", resp.StatusCode, body)
	}

	// Single-rule editing.
	_, body = req(t, "GET", ts.URL+"/api/v1/network/rules", token, "")
	var rules struct {
		Rules []map[string]any `json:"rules"`
	}
	_ = json.Unmarshal([]byte(body), &rules)
	if len(rules.Rules) == 0 {
		t.Fatal("expected built-in default rules")
	}
	ruleID := fmt.Sprint(rules.Rules[0]["id"])
	ruleEdit := `{"id":"` + ruleID + `","type":"DOMAIN-SUFFIX","value":".edited.test","action":"PROXY","core_id":"mihomo","priority":77,"enabled":true,"comment":"edited via API"}`
	resp, body = req(t, "PUT", ts.URL+"/api/v1/network/rules/"+ruleID, token, ruleEdit)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update rule: %d %s", resp.StatusCode, body)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/network/rules", token, "")
	if !strings.Contains(body, ".edited.test") || !strings.Contains(body, "edited via API") {
		t.Fatalf("rule edit not applied: %s", body)
	}
}

// TestRuleSets verifies custom rule sets: inline/URL creation, edit, upload
// auto-detection, apply into the live rule table, and delete.
func TestRuleSets(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, "GET", ts.URL+"/api/v1/rulesets", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list rule sets: %d %s", resp.StatusCode, body)
	}

	created := `{"id":"rs-test","name":"测试规则集","source":"inline","enabled":true,"content":"DOMAIN-SUFFIX,example.com,PROXY\nIP-CIDR,10.0.0.0/8,DIRECT\n"}`
	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets", token, created)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create rule set: %d %s", resp.StatusCode, body)
	}
	var saved struct {
		RuleSet struct {
			RuleCount int    `json:"rule_count"`
			Format    string `json:"format"`
		} `json:"ruleset"`
	}
	_ = json.Unmarshal([]byte(body), &saved)
	if saved.RuleSet.RuleCount != 2 || saved.RuleSet.Format != "rule-lines" {
		t.Fatalf("auto-detect failed: %s", body)
	}

	edited := `{"id":"rs-test","name":"已修改","source":"inline","enabled":true,"content":"DOMAIN,edited.example,PROXY\n"}`
	resp, body = req(t, "PUT", ts.URL+"/api/v1/rulesets/rs-test", token, edited)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit rule set: %d %s", resp.StatusCode, body)
	}

	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets/rs-test/apply", token, `{"core_id":"mihomo"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply rule set: %d %s", resp.StatusCode, body)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/network/rules", token, "")
	if !strings.Contains(body, "edited.example") {
		t.Fatalf("applied rule missing from live table: %s", body)
	}

	// Multipart upload must auto-detect a Clash YAML payload.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "my-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`payload:
  - 'DOMAIN-SUFFIX,uploaded.example'
`))
	_ = mw.Close()
	upReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/rulesets/upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	upReq.Header.Set("Authorization", "Bearer "+token)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		t.Fatal(err)
	}
	defer upResp.Body.Close()
	upBody, _ := io.ReadAll(upResp.Body)
	if upResp.StatusCode != http.StatusOK || !strings.Contains(string(upBody), "clash-yaml") {
		t.Fatalf("upload auto-detect: %d %s", upResp.StatusCode, upBody)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/rulesets", token, "")
	if !strings.Contains(body, "uploaded.example") || !strings.Contains(body, "rs-my-rules") {
		t.Fatalf("uploaded set not appended to list: %s", body)
	}

	// URL-backed set performs a real HTTP fetch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("DOMAIN,remote.example,PROXY\n"))
	}))
	defer srv.Close()
	urlSet := `{"id":"rs-url","name":"远程","source":"url","url":"` + srv.URL + `","enabled":true}`
	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets", token, urlSet)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("url rule set: %d %s", resp.StatusCode, body)
	}

	resp, _ = req(t, "DELETE", ts.URL+"/api/v1/rulesets/rs-test", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete rule set: %d", resp.StatusCode)
	}
}
