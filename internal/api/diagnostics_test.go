package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
	"github.com/ngg-project/ngg/pkg/contracts"
)

func newDiagnosticsServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Server.Token = "test-token-abcdef"
	cfg.Server.Listen = "127.0.0.1"
	cfg.Server.WebDir = filepath.Join(root, "no-web-dist")
	cfg.Storage.Dir = filepath.Join(root, "data")
	cfg.Logging.Dir = filepath.Join(root, "logs")
	cfg.Cores.DescriptorDir = filepath.Join(root, "cores.d")
	eng, err := engine.New(&cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown(context.Background()) })
	return New(&cfg, eng)
}

func runDiagnostics(t *testing.T, s *Server, id string) (*httptest.ResponseRecorder, coreDiag) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cores/"+id+"/diagnostics", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleCoreDiagnostics(rec, req)
	var d coreDiag
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatalf("decode diagnostics: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec, d
}

func TestCoreDiagnosticsRouteIsRegistered(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, http.MethodGet, ts.URL+"/api/v1/cores/mihomo/diagnostics", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var d coreDiag
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode diagnostics: %v (body %s)", err, body)
	}
	if d.ID != "mihomo" {
		t.Fatalf("id = %q, want mihomo", d.ID)
	}
	if len(d.Checks) != 5 {
		t.Fatalf("checks = %d, want 5", len(d.Checks))
	}
}

func TestCoreDiagnosticsUnknownKernel(t *testing.T) {
	s := newDiagnosticsServer(t)
	rec, _ := runDiagnostics(t, s, "no-such-kernel")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestCoreDiagnosticsReturnsMeasuredFacts(t *testing.T) {
	s := newDiagnosticsServer(t)
	rec, d := runDiagnostics(t, s, string(contracts.CoreSingBox))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// Field names are a public contract with web/src/api/ngg.ts.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, key := range []string{
		"id", "state", "pid", "running", "binary", "version_output", "version_error",
		"config", "ports", "log_tail", "checks", "hint", "checked_at",
	} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing field %q in %s", key, rec.Body.String())
		}
	}
	if d.ID != string(contracts.CoreSingBox) {
		t.Fatalf("id = %q, want %q", d.ID, contracts.CoreSingBox)
	}

	// Nothing is installed in the temp data dir, so the binary checks must be
	// false and the hint must talk about the binary, not about a fake success.
	if d.Binary.Exists {
		t.Fatalf("binary.exists = true in an empty data dir (%+v)", d.Binary)
	}
	if d.Running {
		t.Fatalf("running = true for an unstarted kernel")
	}
	if d.LogTail == nil {
		t.Fatalf("log_tail must be an empty array, not null")
	}
	if len(d.Checks) != 5 {
		t.Fatalf("checks = %d, want 5: %+v", len(d.Checks), d.Checks)
	}
	wantNames := []string{"二进制存在", "二进制可执行", "配置有效", "端口可用", "进程运行"}
	for i, name := range wantNames {
		if d.Checks[i].Name != name {
			t.Fatalf("checks[%d].name = %q, want %q", i, d.Checks[i].Name, name)
		}
		if d.Checks[i].Detail == "" {
			t.Fatalf("checks[%d] has an empty detail", i)
		}
	}
	if d.Checks[0].OK {
		t.Fatalf("二进制存在 must be false without an installed binary")
	}
	if d.Hint == "" {
		t.Fatalf("hint must never be empty")
	}
}

func TestCoreDiagnosticsDetectsOccupiedPort(t *testing.T) {
	s := newDiagnosticsServer(t)
	params, ok := s.engine.PortParams(contracts.CoreXray)
	if !ok || len(params.Ports()) == 0 {
		t.Fatalf("no port block allocated for %q", contracts.CoreXray)
	}
	port := params.Ports()[0]
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("occupy %s: %v", addr, err)
	}
	defer func() { _ = ln.Close() }()

	rec, d := runDiagnostics(t, s, string(contracts.CoreXray))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var found *coreDiagPort
	for i := range d.Ports {
		if d.Ports[i].Port == port {
			found = &d.Ports[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("port %d missing from diagnostics: %+v", port, d.Ports)
	}
	if !found.InUse {
		t.Fatalf("port %d reported free while a listener is bound to it", port)
	}

	var portCheck *coreDiagCheck
	for i := range d.Checks {
		if d.Checks[i].Name == "端口可用" {
			portCheck = &d.Checks[i]
			break
		}
	}
	if portCheck == nil {
		t.Fatalf("端口可用 check missing: %+v", d.Checks)
	}
	if portCheck.OK {
		t.Fatalf("端口可用 must be false while port %d is occupied", port)
	}
	if !strings.Contains(portCheck.Detail, fmt.Sprint(port)) {
		t.Fatalf("port detail %q does not mention port %d", portCheck.Detail, port)
	}
}
