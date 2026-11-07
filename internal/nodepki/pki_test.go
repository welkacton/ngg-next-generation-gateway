package nodepki

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"testing"
	"time"
)

func TestCAIssuesNodeCertWithIdentityAndSAN(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	t.Run("ip address", func(t *testing.T) {
		leaf, err := ca.IssueNode("node-a", "203.0.113.7")
		if err != nil {
			t.Fatalf("IssueNode: %v", err)
		}
		cert := parseLeaf(t, leaf.CertPEM)
		if cert.Subject.CommonName != "node-a" {
			t.Fatalf("CN = %q, want node-a", cert.Subject.CommonName)
		}
		if len(cert.IPAddresses) != 3 {
			t.Fatalf("IP SANs = %v, want the address plus loopback", cert.IPAddresses)
		}
		if err := cert.VerifyHostname("203.0.113.7"); err != nil {
			t.Fatalf("VerifyHostname(address): %v", err)
		}
		// The deploy self-test dials loopback; that must verify too.
		if err := cert.VerifyHostname("127.0.0.1"); err != nil {
			t.Fatalf("VerifyHostname(loopback): %v", err)
		}
	})

	t.Run("dns name", func(t *testing.T) {
		leaf, err := ca.IssueNode("node-b", "jp1.example.com")
		if err != nil {
			t.Fatalf("IssueNode: %v", err)
		}
		cert := parseLeaf(t, leaf.CertPEM)
		if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "jp1.example.com" {
			t.Fatalf("DNS SANs = %v", cert.DNSNames)
		}
	})
}

func TestClientConfigPinsNodeCN(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	client, err := ca.IssueClient()
	if err != nil {
		t.Fatalf("IssueClient: %v", err)
	}
	node, err := ca.IssueNode("node-a", "127.0.0.1")
	if err != nil {
		t.Fatalf("IssueNode: %v", err)
	}

	serverCfg, err := ServerConfig(node.CertPEM, node.KeyPEM, ca.CAPEM(), PanelCN, "node-a")
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	// Dial as the panel would, but expect a different node: the CN pin must
	// reject the connection even though the certificate chains correctly.
	clientCfg, err := ClientConfig(ca.CAPEM(), client.CertPEM, client.KeyPEM, "127.0.0.1", "node-b")
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	if err := handshake(t, serverCfg, clientCfg); err == nil {
		t.Fatal("handshake succeeded with a mismatched node CN, want failure")
	}

	// Same client, correct expectation: must connect.
	goodCfg, err := ClientConfig(ca.CAPEM(), client.CertPEM, client.KeyPEM, "127.0.0.1", "node-a")
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	if err := handshake(t, serverCfg, goodCfg); err != nil {
		t.Fatalf("handshake with correct CN: %v", err)
	}
}

func TestServerConfigRequiresClientCertificate(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	node, err := ca.IssueNode("node-a", "127.0.0.1")
	if err != nil {
		t.Fatalf("IssueNode: %v", err)
	}
	serverCfg, err := ServerConfig(node.CertPEM, node.KeyPEM, ca.CAPEM(), PanelCN)
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CAPEM()) {
		t.Fatal("CA PEM rejected")
	}
	bare := &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	if err := handshake(t, serverCfg, bare); err == nil {
		t.Fatal("handshake succeeded without a client certificate, want failure")
	}
}

// handshake runs a real TLS handshake on loopback, then a write/read round
// trip. The round trip matters: TLS 1.3 lets the client finish its handshake
// before the server has looked at the client certificate, so a rejection only
// shows up on the next read.
func handshake(t *testing.T, serverCfg, clientCfg *tls.Config) error {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	ln = tls.NewListener(ln, serverCfg)

	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.(*tls.Conn).Handshake(); err != nil {
			serverErr <- err
			return
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			serverErr <- err
			return
		}
		_, err = conn.Write(buf)
		serverErr <- err
	}()

	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		<-serverErr
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		<-serverErr
		return err
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		if serr := <-serverErr; serr != nil {
			return serr
		}
		return err
	}
	<-serverErr
	return nil
}

func parseLeaf(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return cert
}
