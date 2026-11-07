package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
	if got, want := client.BaseURL(), "https://127.0.0.1:2222"; got != want {
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
