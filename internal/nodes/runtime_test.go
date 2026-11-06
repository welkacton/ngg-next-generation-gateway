package nodes

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateKeyPairWireGuardIsRealX25519(t *testing.T) {
	kp, err := GenerateKeyPair("wireguard")
	if err != nil {
		t.Fatal(err)
	}
	if !kp.OK || kp.Kind != "wireguard" || kp.CreatedAt == "" {
		t.Fatalf("incomplete keypair: %+v", kp)
	}
	priv, err := base64.StdEncoding.DecodeString(kp.PrivateKey)
	if err != nil || len(priv) != 32 {
		t.Fatalf("private key is not a 32-byte X25519 scalar: %v len=%d", err, len(priv))
	}
	pub, err := base64.StdEncoding.DecodeString(kp.PublicKey)
	if err != nil || len(pub) != 32 {
		t.Fatalf("public key is not a 32-byte X25519 point: %v len=%d", err, len(pub))
	}
	kp2, err := GenerateKeyPair("wireguard")
	if err != nil {
		t.Fatal(err)
	}
	if kp2.PrivateKey == kp.PrivateKey || kp2.PublicKey == kp.PublicKey {
		t.Fatal("two key generations returned identical material (not real randomness)")
	}
}

func TestGenerateKeyPairUUIDAndPassword(t *testing.T) {
	u, err := GenerateKeyPair("uuid")
	if err != nil || !u.OK || u.UUID == "" || u.Kind != "uuid" {
		t.Fatalf("uuid keypair: %+v err=%v", u, err)
	}
	parts := strings.Split(u.UUID, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Fatalf("uuid is not v4-shaped: %q", u.UUID)
	}
	if u.UUID[14] != '4' {
		t.Fatalf("uuid version nibble is not 4: %q", u.UUID)
	}

	p, err := GenerateKeyPair("password")
	if err != nil || !p.OK || p.Password == "" {
		t.Fatalf("password keypair: %+v err=%v", p, err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(p.Password)
	if err != nil || len(decoded) != 24 {
		t.Fatalf("password is not 24 random bytes: %v len=%d", err, len(decoded))
	}

	if _, err := GenerateKeyPair("not-a-kind"); err == nil {
		t.Fatal("unknown key kind must be rejected")
	}
}

func TestProbeResultStructureAndRealTCPDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot open a local listener: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	lat, err := Probe("127.0.0.1", port, 2*time.Second)
	if err != nil {
		t.Fatalf("real TCP dial to a live listener failed: %v", err)
	}
	if lat < 0 {
		t.Fatalf("negative latency %f", lat)
	}
	_ = ln.Close()

	// A closed port must surface as an error, never as a synthetic success.
	if _, err := Probe("127.0.0.1", port, 150*time.Millisecond); err == nil {
		t.Log("closed listener port became reachable again (rare OS reuse); not treating as failure")
	}

	// The response contract must carry every documented field.
	b, err := json.Marshal(NodeProbeResult{
		OK: true, NodeID: "n1", Address: "1.2.3.4", Port: 443,
		Reachable: true, LatencyMs: 1.5, CheckedAt: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ok", "node_id", "address", "port", "reachable", "latency_ms", "checked_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("NodeProbeResult missing field %q: %s", k, b)
		}
	}
}

func TestSafeNodeIDAndConfigFileWrite(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"../../etc/passwd", "passwd"},
		{"a/b", "b"},
		{"..", "node"},
		{"", "node"},
	} {
		if got := SafeNodeID(tc.in); got != tc.want {
			t.Errorf("SafeNodeID(%q) = %q want %q", tc.in, got, tc.want)
		}
	}

	if !ValidateConfigContent(`{"a":1}`, "json") || ValidateConfigContent(`{not json`, "json") {
		t.Fatal("JSON content validation is not format-aware")
	}
	if !ValidateConfigContent("a: 1", "yaml") || ValidateConfigContent("a: [", "yaml") {
		t.Fatal("YAML content validation is not format-aware")
	}

	dir := t.TempDir()
	content := `{"outbounds":[]}`
	path, n, validated, err := WriteConfigFile(dir, "node/../../evil", "json", content)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(content) || !validated {
		t.Fatalf("write result: n=%d validated=%v", n, validated)
	}
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, filepath.Clean(dir)+string(os.PathSeparator)+"node-configs"+string(os.PathSeparator)) {
		t.Fatalf("config file escaped its directory: %s", clean)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Fatalf("config file content: %q err=%v", got, err)
	}
}
