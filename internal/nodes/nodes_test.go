package nodes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryCRUDAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes-config.json")
	r := NewRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if n := r.List(""); len(n) != 0 {
		t.Fatalf("fresh registry not empty: %+v", n)
	}
	in := Node{ID: "in-wg", Name: "WG 入口", Kind: KindInbound, Protocol: "wireguard",
		Address: "0.0.0.0", Enabled: true, Params: map[string]any{"private_key": "abc"}}
	if _, err := r.Add(in); err != nil {
		t.Fatalf("add inbound: %v", err)
	}
	if _, err := r.Add(in); err == nil {
		t.Fatal("duplicate id must be rejected")
	}
	out := Node{ID: "out-vless", Name: "VLESS 出口", Kind: KindOutbound, Protocol: "vless-reality",
		Address: "example.com", Enabled: true, Params: map[string]any{"uuid": "u1"}}
	if _, err := r.Add(out); err != nil {
		t.Fatalf("add outbound: %v", err)
	}
	if len(r.List(KindInbound)) != 1 || len(r.List(KindOutbound)) != 1 {
		t.Fatal("kind filtering broken")
	}
	stored, ok := r.Get("in-wg")
	if !ok || stored.Port != 51820 {
		t.Fatalf("default port not applied: %+v", stored)
	}
	stored.Name = "WG 入口（已改）"
	stored.Params["mtu"] = 1420
	if _, err := r.Update("in-wg", stored); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := r.Get("in-wg")
	if got.Name != "WG 入口（已改）" || fmt.Sprint(got.Params["mtu"]) != "1420" {
		t.Fatalf("update not applied: %+v", got)
	}
	// Persisted file is real JSON and reloadable.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var list []Node
	if err := json.Unmarshal(b, &list); err != nil || len(list) != 2 {
		t.Fatalf("persisted file invalid: %v %d", err, len(list))
	}
	r2 := NewRegistry(path)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	if len(r2.List("")) != 2 {
		t.Fatal("reload lost nodes")
	}
	if err := r.Remove("in-wg"); err != nil {
		t.Fatal(err)
	}
	if len(r.List("")) != 1 {
		t.Fatal("remove failed")
	}
}

func TestValidationAndFailClosedImport(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "nodes.json"))
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(Node{ID: "x", Name: "x", Kind: KindInbound, Protocol: "unknown-protocol"}); err == nil {
		t.Fatal("unknown protocol must be rejected")
	}
	if _, err := r.Add(Node{ID: "x", Name: "x", Kind: KindInbound, Protocol: "openvpn"}); err == nil {
		t.Fatal("outbound-only protocol must not be allowed on inbound")
	}
	if _, err := r.Add(Node{ID: "x", Name: "x", Kind: KindInbound, Protocol: "socks5", Port: 70000}); err == nil {
		t.Fatal("out-of-range port must be rejected")
	}
	good := []Node{
		{ID: "a", Name: "A", Kind: KindInbound, Protocol: "socks5", Port: 1080},
		{ID: "b", Name: "B", Kind: KindOutbound, Protocol: "trojan", Address: "example.com", Port: 443},
	}
	if n, err := r.Replace(good); err != nil || n != 2 {
		t.Fatalf("valid import rejected: %d %v", n, err)
	}
	bad := append(good, Node{ID: "c", Name: "C", Kind: KindOutbound, Protocol: "nope"})
	if _, err := r.Replace(bad); err == nil {
		t.Fatal("invalid import must be rejected")
	}
	if len(r.List("")) != 2 {
		t.Fatal("failed import must leave the previous set intact")
	}
	if len(r.Protocols(KindInbound)) < 10 {
		t.Fatal("inbound protocol catalog too small")
	}
}

func TestGenerateKernelConfigForEncryptedProtocols(t *testing.T) {
	for _, kernel := range []string{"mihomo", "xray-core", "sing-box"} {
		for _, kind := range []Kind{KindInbound, KindOutbound} {
			n := Node{ID: "n1", Name: "N1", Kind: kind, Protocol: "shadowsocks",
				Address: "198.51.100.1", Port: 8388,
				Params: map[string]any{"cipher": "aes-256-gcm", "password": "secret"}}
			b, format, err := GenerateKernelConfig(n, kernel)
			if err != nil {
				t.Errorf("%s/%s: %v", kernel, kind, err)
				continue
			}
			if len(b) == 0 || !strings.Contains(string(b), "shadowsocks") {
				t.Errorf("%s/%s: generated config missing protocol: %s", kernel, kind, b)
			}
			if format == "" {
				t.Errorf("%s/%s: empty format", kernel, kind)
			}
		}
	}
	if _, _, err := GenerateKernelConfig(Node{ID: "x", Kind: KindOutbound, Protocol: "direct"}, "no-kernel"); err == nil {
		t.Fatal("unknown kernel must be reported honestly")
	}
}
