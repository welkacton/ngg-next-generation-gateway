package nodes

import (
	"path/filepath"
	"strings"
	"testing"
)

// NOTE: only placeholder credentials are used in this fixture. Real operator
// secrets must never be committed to the repository.
const sampleXray = `{
  "log": { "loglevel": "info" },
  "inbounds": [
    {
      "tag": "HK_VlessEncryption", "port": 33927, "listen": "0.0.0.0",
      "protocol": "vless",
      "settings": { "clients": [], "decryption": "mlkem768x25519plus.native.600s.PLACEHOLDER_KEY" },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] },
      "streamSettings": { "network": "raw", "security": "none" }
    },
    {
      "tag": "DIRECT_Trojan", "port": 43804, "listen": "0.0.0.0",
      "protocol": "trojan", "settings": { "clients": [] },
      "streamSettings": { "network": "raw", "security": "tls" }
    }
  ],
  "outbounds": [
    {
      "tag": "HK_Socks5_Encryption", "protocol": "socks",
      "settings": { "servers": [ { "address": "203.0.113.10", "port": 40723 } ] }
    },
    { "tag": "US_DIRECT_Encryption", "protocol": "freedom" }
  ],
  "routing": {
    "domainStrategy": "AsIs",
    "rules": [
      { "type": "field", "inboundTag": ["HK_VlessEncryption"], "outboundTag": "HK_Socks5_Encryption" },
      { "type": "field", "inboundTag": ["DIRECT_Trojan"], "outboundTag": "US_DIRECT_Encryption" }
    ]
  }
}`

func TestParseNativeXrayConfig(t *testing.T) {
	res, err := ParseNative("xray-core", sampleXray)
	if err != nil {
		t.Fatal(err)
	}
	if res.Detected["inbounds"] != 2 || res.Detected["outbounds"] != 2 {
		t.Fatalf("wrong counts: %+v", res.Detected)
	}
	if len(res.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d: %+v", len(res.Nodes), res.Nodes)
	}
	if len(res.Routing) != 2 {
		t.Fatalf("expected 2 routing bindings, got %+v", res.Routing)
	}
	byID := map[string]Node{}
	for _, n := range res.Nodes {
		byID[n.ID] = n
	}
	if byID["HK_VlessEncryption"].Protocol != "mlkem-hybrid" {
		t.Fatalf("ML-KEM VLESS not recognised: %+v", byID["HK_VlessEncryption"])
	}
	if byID["HK_VlessEncryption"].Port != 33927 || byID["HK_VlessEncryption"].Kind != KindInbound {
		t.Fatalf("inbound fields wrong: %+v", byID["HK_VlessEncryption"])
	}
	if byID["HK_Socks5_Encryption"].Protocol != "socks5" || byID["HK_Socks5_Encryption"].Kind != KindOutbound {
		t.Fatalf("socks outbound not mapped: %+v", byID["HK_Socks5_Encryption"])
	}
	if byID["US_DIRECT_Encryption"].Protocol != "direct" {
		t.Fatalf("freedom not mapped to direct: %+v", byID["US_DIRECT_Encryption"])
	}
	if byID["DIRECT_Trojan"].Protocol != "trojan" {
		t.Fatalf("trojan not mapped: %+v", byID["DIRECT_Trojan"])
	}
	if _, ok := byID["HK_VlessEncryption"].Params["settings"]; !ok {
		t.Fatal("native settings were not preserved")
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", res.Warnings)
	}
}

func TestCleanNativeTextWrappers(t *testing.T) {
	wrapped := "@long-text:" + string(rune(34)) + sampleXray + string(rune(34)) + " <long_text_quote>" + sampleXray + "</long_text_quote>"
	clean := CleanNativeText(wrapped)
	if !strings.HasPrefix(strings.TrimSpace(clean), "{") || !strings.Contains(clean, "HK_VlessEncryption") {
		t.Fatalf("wrapper not stripped: %q", clean[:min(80, len(clean))])
	}
	if _, err := ParseNative("xray-core", "<long_text_quote>"+sampleXray+"</long_text_quote>"); err != nil {
		t.Fatalf("wrapped config must parse: %v", err)
	}
}

func TestParseRejectsInvalidNativeConfig(t *testing.T) {
	if _, err := ParseNative("xray-core", "{not json"); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
	if _, err := ParseNative("unknown-kernel", sampleXray); err == nil {
		t.Fatal("unknown kernel must be reported")
	}
	if _, err := ParseNative("xray-core", ""); err == nil {
		t.Fatal("empty config must be rejected")
	}
}

func TestMergeFromNativeIsFailClosed(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "nodes.json"))
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(Node{ID: "existing", Name: "E", Kind: KindInbound, Protocol: "socks5", Port: 1080}); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseNative("xray-core", sampleXray)
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Merge(parsed.Nodes)
	if err != nil || n != 4 {
		t.Fatalf("merge failed: %d %v", n, err)
	}
	if len(r.List("")) != 5 {
		t.Fatal("existing node was lost during merge")
	}
	bad := append(parsed.Nodes, Node{ID: "broken", Name: "B", Kind: KindOutbound, Protocol: "unknown"})
	if _, err := r.Merge(bad); err == nil {
		t.Fatal("invalid merge must be rejected")
	}
	if len(r.List("")) != 5 {
		t.Fatal("failed merge changed the registry")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// A config exported by the 3X-UI panel (MHSanaei/3x-ui) must be accepted
// verbatim: one Shadowsocks inbound, freedom + blackhole outbounds, no rules.
func TestParse3XUIDefaultConfig(t *testing.T) {
	const uiDefault = `{
  "log": { "loglevel": "info" },
  "inbounds": [
    {
      "tag": "Shadowsocks_924883", "port": 1234, "protocol": "shadowsocks",
      "settings": { "method": "chacha20-ietf-poly1305", "clients": [], "network": "tcp,udp" },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] }
    }
  ],
  "outbounds": [
    { "tag": "DIRECT", "protocol": "freedom" },
    { "tag": "BLOCK", "protocol": "blackhole" }
  ],
  "routing": { "rules": [] }
}`
	res, err := ParseNative("xray-core", uiDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 3 || len(res.Routing) != 0 {
		t.Fatalf("3X-UI default should yield 1 inbound + 2 outbounds and no routing: %+v", res.Detected)
	}
	byID := map[string]Node{}
	for _, n := range res.Nodes {
		byID[n.ID] = n
	}
	ss := byID["Shadowsocks_924883"]
	if ss.Protocol != "shadowsocks" || ss.Kind != KindInbound || ss.Port != 1234 {
		t.Fatalf("Shadowsocks inbound parsed wrong: %+v", ss)
	}
	settings, _ := ss.Params["settings"].(map[string]any)
	if settings == nil || settings["method"] != "chacha20-ietf-poly1305" {
		t.Fatalf("native settings not preserved: %+v", ss.Params)
	}
	if byID["DIRECT"].Protocol != "direct" || byID["BLOCK"].Protocol != "reject" {
		t.Fatalf("outbound aliases wrong: %+v / %+v", byID["DIRECT"], byID["BLOCK"])
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("3X-UI default should parse without warnings: %+v", res.Warnings)
	}
}

func TestTemplatesAvailable(t *testing.T) {
	list := Templates()
	if len(list) == 0 {
		t.Fatal("no templates returned")
	}
	found := false
	for _, tpl := range list {
		if tpl.ID == "3x-ui-default" {
			found = true
			if _, err := ParseNative(tpl.Kernel, tpl.Content); err != nil {
				t.Fatalf("shipped template does not parse: %v", err)
			}
		}
	}
	if !found {
		t.Fatal("3X-UI sample template missing")
	}
}

// Remnawave Node ships Xray-core configs with ext: geosite/geoip rules,
// VLESS REALITY inbounds and file-based logging. Parsing must preserve the
// rule matchers instead of silently dropping them.
func TestParseRemnawaveStyleConfig(t *testing.T) {
	const cfg = `{
  "log": { "loglevel": "warning", "error": "/var/log/remnanode/error.log" },
  "inbounds": [
    { "tag": "VLESS_TCP_REALITY", "port": 443, "protocol": "vless", "settings": { "clients": [], "decryption": "none" },
      "streamSettings": { "network": "raw", "security": "reality" } },
    { "tag": "Shadowsocks_20221101", "port": 1234, "protocol": "shadowsocks",
      "settings": { "method": "chacha20-ietf-poly1305", "clients": [] } }
  ],
  "outbounds": [
    { "tag": "NOT_RU_OUTBOUND", "protocol": "freedom" },
    { "tag": "BLOCK", "protocol": "blackhole" }
  ],
  "routing": { "rules": [
    { "type": "field", "domain": ["ext:geo-zapret.dat:zapret"], "inboundTag": ["VLESS_TCP_REALITY"], "outboundTag": "NOT_RU_OUTBOUND" },
    { "type": "field", "ip": ["ext:ip-zapret.dat:zapret"], "outboundTag": "NOT_RU_OUTBOUND" }
  ] }
}`
	res, err := ParseNative("xray-core", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 4 {
		t.Fatalf("expected 2 inbounds + 2 outbounds, got %d: %+v", len(res.Nodes), res.Detected)
	}
	if len(res.Routing) != 2 {
		t.Fatalf("routing rules lost: %+v", res.Routing)
	}
	if len(res.Routing[0].Matchers) == 0 || res.Routing[0].Matchers[0] != "domain=ext:geo-zapret.dat:zapret" {
		t.Fatalf("geosite matcher not preserved: %+v", res.Routing[0])
	}
	if len(res.Routing[1].Matchers) == 0 || res.Routing[1].Matchers[0] != "ip=ext:ip-zapret.dat:zapret" {
		t.Fatalf("geoip matcher not preserved: %+v", res.Routing[1])
	}
	if res.Routing[0].InboundTag != "VLESS_TCP_REALITY" || res.Routing[0].OutboundTag != "NOT_RU_OUTBOUND" {
		t.Fatalf("inbound/outbound binding lost: %+v", res.Routing[0])
	}
}
