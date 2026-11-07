package nodesvc

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/nodeapi"
	"github.com/ngg-project/ngg/internal/nodepki"
)

// fixture writes the certificate material to a temp dir and returns a service
// plus the panel's client material.
type fixture struct {
	svc     *Service
	srv     *httptest.Server
	ca      *nodepki.CA
	panel   nodepki.Leaf
	nodeID  string
	address string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	ca, err := nodepki.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	panel, err := ca.IssueClient()
	if err != nil {
		t.Fatalf("IssueClient: %v", err)
	}
	node, err := ca.IssueNode("node-test", "127.0.0.1")
	if err != nil {
		t.Fatalf("IssueNode: %v", err)
	}
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	certFile := write("node.crt", node.CertPEM)
	keyFile := write("node.key", node.KeyPEM)
	caFile := write("ca.crt", ca.CAPEM())
	manifest := write("node.json", []byte(`{"node_id":"node-test"}`))
	coreConfig := filepath.Join(dir, "config.json")

	svc, err := New(Config{
		Listen:       "127.0.0.1:0",
		CertFile:     certFile,
		KeyFile:      keyFile,
		CAFile:       caFile,
		ManifestPath: manifest,
		KernelBin:    filepath.Join(dir, "no-such-kernel"),
		CoreConfig:   coreConfig,
		CoreUnit:     "ngg-core.service",
		Version:      "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tlsCfg, err := nodepki.ServerConfig(node.CertPEM, node.KeyPEM, ca.CAPEM(), nodepki.PanelCN, "node-test")
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	srv := httptest.NewUnstartedServer(svc.Handler())
	srv.TLS = tlsCfg
	srv.StartTLS()
	t.Cleanup(srv.Close)

	return &fixture{svc: svc, srv: srv, ca: ca, panel: panel, nodeID: "node-test", address: "127.0.0.1"}
}

// endpoint builds the client endpoint for the test server.
func (f *fixture) endpoint(t *testing.T) nodeapi.Endpoint {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return nodeapi.Endpoint{
		Address: f.address,
		Port:    port,
		NodeID:  f.nodeID,
		CAPEM:   f.ca.CAPEM(),
		CertPEM: f.panel.CertPEM,
		KeyPEM:  f.panel.KeyPEM,
	}
}

func TestHealthcheckOverMutualTLS(t *testing.T) {
	f := newFixture(t)
	client, err := nodeapi.New(f.endpoint(t))
	if err != nil {
		t.Fatalf("nodeapi.New: %v", err)
	}
	h, _, err := client.Health(t.Context())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.IsAlive {
		t.Fatal("isAlive = false")
	}
	if h.NodeID != "node-test" {
		t.Fatalf("nodeId = %q", h.NodeID)
	}
	if h.NodeVersion != "test" {
		t.Fatalf("nodeVersion = %q", h.NodeVersion)
	}
}

func TestStatsReturnsRealHostData(t *testing.T) {
	f := newFixture(t)
	client, err := nodeapi.New(f.endpoint(t))
	if err != nil {
		t.Fatalf("nodeapi.New: %v", err)
	}
	sys, _, err := client.SystemStats(t.Context())
	if err != nil {
		t.Fatalf("SystemStats: %v", err)
	}
	if sys.Info.Hostname == "" {
		t.Fatal("hostname is empty: stats are not read from the host")
	}
	if sys.Info.CPUs == 0 {
		t.Fatal("cpus = 0")
	}
	if sys.Stats.MemoryTotal == 0 {
		t.Fatal("memoryTotal = 0")
	}
}

func TestUnknownClientCertificateIsRejected(t *testing.T) {
	f := newFixture(t)
	// A certificate signed by a DIFFERENT CA: chains to nothing we trust.
	otherCA, err := nodepki.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	rogue, err := otherCA.IssueClient()
	if err != nil {
		t.Fatalf("IssueClient: %v", err)
	}
	ep := f.endpoint(t)
	ep.CertPEM, ep.KeyPEM = rogue.CertPEM, rogue.KeyPEM
	client, err := nodeapi.New(ep)
	if err != nil {
		t.Fatalf("nodeapi.New: %v", err)
	}
	if _, _, err := client.Health(t.Context()); err == nil {
		t.Fatal("healthcheck succeeded with a foreign client certificate")
	}
}

func TestNodeCertCannotActAsPanel(t *testing.T) {
	f := newFixture(t)
	// Another node's certificate is signed by the same CA, so the chain is
	// valid — only the CN allow-list stops it.
	other, err := f.ca.IssueNode("node-other", "127.0.0.1")
	if err != nil {
		t.Fatalf("IssueNode: %v", err)
	}
	ep := f.endpoint(t)
	ep.CertPEM, ep.KeyPEM = other.CertPEM, other.KeyPEM
	client, err := nodeapi.New(ep)
	if err != nil {
		t.Fatalf("nodeapi.New: %v", err)
	}
	if _, _, err := client.Health(t.Context()); err == nil {
		t.Fatal("a sibling node's certificate was accepted as the panel")
	}
}

func TestStartRejectsMissingConfig(t *testing.T) {
	f := newFixture(t)
	code, envErr := postRaw(t, f, `{"internals":{"forceRestart":true}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if !strings.Contains(envErr, "xrayConfig") {
		t.Fatalf("error = %q, want a message mentioning xrayConfig", envErr)
	}
}

func TestHealthcheckWithoutClientCertFails(t *testing.T) {
	f := newFixture(t)
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(f.ca.CAPEM()) {
		t.Fatal("CA PEM rejected")
	}
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12,
	}}}
	resp, err := hc.Get(f.srv.URL + "/node/xray/healthcheck")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("request without a client certificate succeeded: HTTP %d", resp.StatusCode)
	}
}

func TestStartWritesConfigAndReportsKernelFailure(t *testing.T) {
	f := newFixture(t)
	// The fake kernel binary does not exist, so the validator cannot run; the
	// node must answer isStarted=false with the reason, and must leave the
	// config path untouched rather than installing an unvalidated file.
	cfg := `{"inbounds":[],"outbounds":[]}`
	st, _, err := startViaAPI(t, f, cfg)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if st.IsStarted {
		t.Fatal("isStarted = true with a missing kernel binary")
	}
	if st.Error == nil || strings.TrimSpace(*st.Error) == "" {
		t.Fatal("no error message returned")
	}
	if _, err := os.Stat(f.svc.cfg.CoreConfig); err == nil {
		t.Fatal("config was written even though validation failed")
	}
}

// startViaAPI posts a start request through the mTLS client and decodes the
// raw envelope (the typed client turns a failed start into an error).
func startViaAPI(t *testing.T, f *fixture, configJSON string) (nodeapi.StartResult, int, error) {
	t.Helper()
	code, _, st := postEnvelope(t, f, `{"internals":{"forceRestart":true},"xrayConfig":`+configJSON+`}`)
	if code != http.StatusOK {
		return st, code, os.ErrInvalid
	}
	return st, code, nil
}

// postRaw sends a body and returns the status plus the envelope's error field.
func postRaw(t *testing.T, f *fixture, body string) (int, string) {
	t.Helper()
	code, envErr, _ := postEnvelope(t, f, body)
	return code, envErr
}

func postEnvelope(t *testing.T, f *fixture, body string) (int, string, nodeapi.StartResult) {
	t.Helper()
	ep := f.endpoint(t)
	tlsCfg, err := nodepki.ClientConfig(ep.CAPEM, ep.CertPEM, ep.KeyPEM, ep.Address, ep.NodeID)
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}
	resp, err := hc.Post(f.srv.URL+"/node/xray/start", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Response nodeapi.StartResult `json:"response"`
		Error    string              `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env.Error, env.Response
}
