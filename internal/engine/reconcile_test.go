package engine

import (
	"net"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

func testParams() core.RuntimeParams {
	return core.RuntimeParams{
		MixedPort: 28001,
		SocksPort: 28002,
		HTTPPort:  28003,
		APIPort:   28004,
		DNSPort:   28005,
	}
}

func TestDeclaredPortsMihomo(t *testing.T) {
	cfg := []byte("mixed-port: 17890\nexternal-controller: 127.0.0.1:17894\nsocks-port: 17892\n")
	refs := declaredPorts(contracts.CoreMihomo, cfg)
	if len(refs) != 3 {
		t.Fatalf("want 3 refs, got %d (%+v)", len(refs), refs)
	}
	if refs[0].key != "mixed-port" || refs[0].port != 17890 {
		t.Fatalf("unexpected first ref: %+v", refs[0])
	}
	if refs[1].key != "external-controller" || refs[1].port != 17894 {
		t.Fatalf("external-controller port not parsed: %+v", refs[1])
	}
}

func TestDeclaredPortsSingBox(t *testing.T) {
	cfg := []byte(`{"inbounds":[{"type":"mixed","tag":"ngg-mixed","listen":"127.0.0.1","listen_port":17920}]}`)
	refs := declaredPorts(contracts.CoreSingBox, cfg)
	if len(refs) != 1 || refs[0].port != 17920 || refs[0].key != "ngg-mixed" {
		t.Fatalf("unexpected refs: %+v", refs)
	}
}

func TestDeclaredPortsXray(t *testing.T) {
	cfg := []byte(`{"inbounds":[{"tag":"ngg-socks","port":17930,"protocol":"socks"},{"tag":"ngg-http","port":17931,"protocol":"http"}]}`)
	refs := declaredPorts(contracts.CoreXray, cfg)
	if len(refs) != 2 || refs[0].port != 17930 || refs[1].port != 17931 {
		t.Fatalf("unexpected refs: %+v", refs)
	}
}

func TestRewritePortsMihomo(t *testing.T) {
	cfg := []byte("# comment stays\nmixed-port: 17890\nexternal-controller: 127.0.0.1:17894\n")
	out, notes, ok := rewritePorts(contracts.CoreMihomo, cfg, testParams())
	if !ok || len(notes) == 0 {
		t.Fatalf("rewrite reported no change: ok=%v notes=%v", ok, notes)
	}
	s := string(out)
	if !strings.Contains(s, "mixed-port: 28001") {
		t.Fatalf("mixed-port not rewritten:\n%s", s)
	}
	if !strings.Contains(s, "external-controller: 127.0.0.1:28004") {
		t.Fatalf("external-controller not rewritten:\n%s", s)
	}
	if !strings.Contains(s, "# comment stays") {
		t.Fatalf("comment was dropped:\n%s", s)
	}
}

func TestRewritePortsSingBoxKeepsOtherFields(t *testing.T) {
	cfg := []byte(`{"log":{"level":"info"},"inbounds":[{"type":"mixed","tag":"ngg-mixed","listen":"127.0.0.1","listen_port":17920}],"outbounds":[{"type":"direct","tag":"direct"}]}`)
	out, _, ok := rewritePorts(contracts.CoreSingBox, cfg, testParams())
	if !ok {
		t.Fatal("expected rewrite")
	}
	refs := declaredPorts(contracts.CoreSingBox, out)
	if len(refs) != 1 || refs[0].port != 28001 {
		t.Fatalf("listen_port not rewritten: %+v (%s)", refs, out)
	}
	if !strings.Contains(string(out), `"log"`) || !strings.Contains(string(out), `"direct"`) {
		t.Fatalf("other fields lost:\n%s", out)
	}
}

func TestRewritePortsXrayMapsSocksAndHTTP(t *testing.T) {
	cfg := []byte(`{"inbounds":[{"tag":"ngg-socks","port":17930,"protocol":"socks"},{"tag":"ngg-http","port":17931,"protocol":"http"}]}`)
	out, _, ok := rewritePorts(contracts.CoreXray, cfg, testParams())
	if !ok {
		t.Fatal("expected rewrite")
	}
	refs := declaredPorts(contracts.CoreXray, out)
	if len(refs) != 2 || refs[0].port != 28002 || refs[1].port != 28003 {
		t.Fatalf("xray ports mapped wrong: %+v", refs)
	}
}

func TestRewritePortsUnknownFormatIsRefused(t *testing.T) {
	if _, _, ok := rewritePorts(contracts.CoreSingBox, []byte("not json"), testParams()); ok {
		t.Fatal("broken JSON must not be rewritten")
	}
}

func TestPortAcceptsConnectionsDetectsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if portAcceptsConnections(port) {
		t.Fatalf("port %d is bound but reported free", port)
	}
}
