package protocols

import "testing"

func TestProtocolRegistryAndConfigGeneration(t *testing.T) {
	r := NewRegistry()
	all := r.List("")
	if len(all) < 15 {
		t.Fatalf("expected a real protocol catalog, got %d", len(all))
	}
	if len(r.List("vpn")) < 5 || len(r.List("post-quantum")) < 1 {
		t.Fatal("missing VPN or post-quantum protocols")
	}
	pq, ok := r.Get("mlkem-hybrid")
	if !ok || !pq.PQCapable || !pq.Enabled {
		t.Fatalf("post-quantum protocol not registered correctly: %+v", pq)
	}
	for _, tc := range []struct{ proto, kernel, format string }{
		{"wireguard", "sing-box", "json"},
		{"wireguard", "mihomo", "yaml"},
		{"wireguard", "xray-core", "json"},
		{"openvpn", "external-helper", "conf"},
		{"mlkem-hybrid", "ngg-native", "json"},
	} {
		b, format, err := GenerateConfig(tc.proto, tc.kernel, map[string]any{"server": "198.51.100.1", "port": 51820, "private_key": "test"})
		if err != nil || format != tc.format {
			t.Errorf("%s/%s: format=%s err=%v", tc.proto, tc.kernel, format, err)
			continue
		}
		if err := ValidateConfig(b, format); err != nil {
			t.Errorf("%s/%s generated invalid %s: %v", tc.proto, tc.kernel, format, err)
		}
	}
	if _, _, err := GenerateConfig("unknown", "mihomo", nil); err == nil {
		t.Fatal("unknown protocol must error")
	}
	if err := ValidateConfig([]byte("{bad json"), "json"); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}
