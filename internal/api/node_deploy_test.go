package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/nodeapi"
	"github.com/ngg-project/ngg/internal/nodedeploy"
	"github.com/ngg-project/ngg/internal/nodepki"
)

// A record loaded from disk has no node certificate bytes (they live only in
// the rendered script). Connecting — including the 30 s metrics poller — must
// still work after a panel restart. See the regression this guards: an earlier
// nodeClient() demanded spec.NodeCertPEM and therefore refused every persisted
// plan with "该方案没有节点证书".
func TestNodeClientWorksAfterStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pki, err := nodepki.Open(filepath.Join(dir, "nodepki"))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := pki.IssueNode("vps-main", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	store := nodedeploy.NewStore(dir)
	spec := nodedeploy.Spec{
		HostID: "vps-main", HostName: "主 VPS", Address: "127.0.0.1",
		Port: 3372, NodePort: nodedeploy.DefaultNodePort,
		NodeCertPEM: leaf.CertPEM, NodeKeyPEM: leaf.KeyPEM, CAPEM: pki.CAPEM(),
	}
	if err := store.Save(nodedeploy.Record{HostID: spec.HostID, Spec: spec, Script: "#!/bin/bash\n"}); err != nil {
		t.Fatal(err)
	}

	rec, err := store.Load(spec.HostID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Spec.NodeCertPEM) != 0 || len(rec.Spec.NodeKeyPEM) != 0 || len(rec.Spec.CAPEM) != 0 {
		t.Fatal("certificate bytes leaked into the record file")
	}

	s := &Server{pki: pki}
	client, err := s.nodeClient(rec.Spec)
	if err != nil {
		t.Fatalf("connecting a persisted plan must work: %v", err)
	}
	if got, want := client.BaseURL(), "https://127.0.0.1:2244"; got != want {
		t.Fatalf("base URL = %q, want %q", got, want)
	}
}

func TestNodeClientRejectsLegacyPlan(t *testing.T) {
	pki, err := nodepki.Open(filepath.Join(t.TempDir(), "nodepki"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{pki: pki}
	// v2.12 probe/SSH records have no NODE_PORT. They must be refused with an
	// actionable message, not silently dialled on port 0.
	_, err = s.nodeClient(nodedeploy.Spec{
		HostID: "vps-main", Address: "127.0.0.1", Port: 3372,
	})
	if err == nil || !strings.Contains(err.Error(), "旧版本") {
		t.Fatalf("expected an actionable legacy-plan error, got %v", err)
	}
}

// The download endpoint must never hand out an obsolete probe-based script:
// installing it would produce a node this panel can never connect to.
func TestLegacyScriptIsNotDownloadable(t *testing.T) {
	dir := t.TempDir()
	store := nodedeploy.NewStore(dir)
	if err := store.Save(nodedeploy.Record{
		HostID: "vps-main",
		Spec:   nodedeploy.Spec{HostID: "vps-main", Address: "127.0.0.1", Port: 3372},
		Script: "#!/bin/bash\necho install ngg-probe.service\n",
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{deploy: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hosts/vps-main/deploy/script", nil)
	req.SetPathValue("id", "vps-main")
	w := httptest.NewRecorder()
	s.handleNodeDeployScript(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if strings.Contains(w.Body.String(), "ngg-probe.service") {
		t.Fatal("legacy script leaked through the download endpoint")
	}
}

// The advice must match the diagnosis: a foreign certificate means "change the
// management port", not "regenerate the plan" (which cannot free a port).
func TestFriendlyNodeErrorPointsAtPortCollision(t *testing.T) {
	got := friendlyNodeError(&nodeapi.Error{
		Kind:   nodeapi.KindTLS,
		Reason: nodeapi.ReasonForeignCA,
		Msg:    "该端口上的服务不是本面板签发的节点",
	})
	for _, want := range []string{"2244", "remnanode", "重新执行"} {
		if !strings.Contains(got, want) {
			t.Fatalf("advice %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "重新生成方案并在服务器上重新执行脚本即可换发证书") {
		t.Fatal("generic 'regenerate the plan' advice must not be used for a port collision")
	}
}

// A profile that points at TLS files on the node must be flagged BEFORE the
// script is shipped: Xray only complains at step 7, by which time the operator
// has already copied 28 KB and run it on the server.
func TestProfileFileWarningListsMissingCertificates(t *testing.T) {
	content := `{
  "inbounds": [{
    "tag": "DIRECT_Trojan",
    "streamSettings": {"tlsSettings": {"certificates": [{
      "certificateFile": "/etc/xray-certs/example.com/example.com.crt",
      "keyFile": "/etc/xray-certs/example.com/example.com.key"
    }]}}
  }]
}`
	got := profileFileWarning(content)
	for _, want := range []string{
		"/etc/xray-certs/example.com/example.com.crt",
		"/etc/xray-certs/example.com/example.com.key",
		"自动签发同域名的自签证书",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning %q missing %q", got, want)
		}
	}
	if profileFileWarning(`{"inbounds":[]}`) != "" {
		t.Fatal("a config without external files must not produce a warning")
	}
	// /etc/ngg is created by our own script, so it is not an external dep.
	if profileFileWarning(`{"inbounds":[{"certificateFile":"/etc/ngg/tls/server.crt"}]}`) != "" {
		t.Fatal("paths owned by the deploy script must not be flagged")
	}
}

func TestJoinWarnings(t *testing.T) {
	if got := joinWarnings("", "a", "", "b"); got != "a b" {
		t.Fatalf("joinWarnings = %q", got)
	}
}
