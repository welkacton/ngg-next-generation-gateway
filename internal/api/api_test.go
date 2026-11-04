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
