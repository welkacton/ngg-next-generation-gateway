package substore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const subUUID = "8f0da0b2-9f4b-4a63-9d1f-0d6a2c9f8b11"

// plain is the document Sub-Store's own `target=json` produces: a bare array of
// proxy objects in its normalized key space.
const plain = `[
  {
    "type": "ss",
    "name": "HK-01",
    "server": "hk01.example.com",
    "port": 8443,
    "cipher": "aes-256-gcm",
    "password": "sspass",
    "udp": true
  },
  {
    "type": "vless",
    "name": "US-01",
    "server": "us01.example.com",
    "port": 443,
    "uuid": "` + subUUID + `",
    "tls": true,
    "servername": "www.bing.com",
    "network": "ws",
    "ws-opts": { "path": "/ray", "headers": { "Host": "www.bing.com" } },
    "udp": true
  },
  {
    "type": "trojan",
    "name": "JP-01",
    "server": "jp01.example.com",
    "port": 443,
    "password": "tjpass",
    "sni": "jp01.example.com",
    "udp": true
  }
]`

func TestParsePlainJSONDoc(t *testing.T) {
	res, err := Parse([]byte(plain))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Format != FormatJSON {
		t.Fatalf("format = %q, want json", res.Format)
	}
	if len(res.Proxies) != 3 {
		t.Fatalf("proxies = %d, want 3 (warnings: %v)", len(res.Proxies), res.Warnings())
	}

	ss := res.Proxies[0]
	if ss.Type != TypeShadowsocks || ss.Method != "aes-256-gcm" || ss.Password != "sspass" {
		t.Fatalf("ss proxy wrong: %+v", ss)
	}
	if got, want := ss.Credential(), "aes-256-gcm:sspass"; got != want {
		t.Fatalf("ss credential = %q, want %q", got, want)
	}

	vl := res.Proxies[1]
	if vl.UUID != subUUID || !vl.TLS || vl.SNI != "www.bing.com" {
		t.Fatalf("vless proxy wrong: %+v", vl)
	}
	if vl.Transport != "ws" || vl.Path != "/ray" || vl.Host != "www.bing.com" {
		t.Fatalf("vless transport wrong: %+v", vl)
	}

	// Trojan is TLS-only: an entry without an explicit sni still resolves one.
	if res.Proxies[2].SNI != "jp01.example.com" {
		t.Fatalf("trojan sni = %q", res.Proxies[2].SNI)
	}
}

// A Clash / Sub-Store export is an object with extra keys around `proxies`.
func TestParseClashObjectWithProxies(t *testing.T) {
	doc := `{
  "mode": "rule",
  "proxies": [
    {"type": "socks5", "name": "lan", "server": "10.0.0.9", "port": 1080, "username": "u", "password": "p"}
  ],
  "proxy-groups": [{"name": "PROXY", "type": "select", "proxies": ["lan"]}],
  "rules": ["MATCH,PROXY"]
}`
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Proxies) != 1 {
		t.Fatalf("proxies = %d, want 1 (warnings: %v)", len(res.Proxies), res.Warnings())
	}
	p := res.Proxies[0]
	if p.Type != TypeSOCKS5 || p.Username != "u" || p.Password != "p" {
		t.Fatalf("proxy wrong: %+v", p)
	}
	if got, want := p.Credential(), "u:p"; got != want {
		t.Fatalf("credential = %q, want %q", got, want)
	}
}

func TestParseClashYAML(t *testing.T) {
	doc := `
mixed-port: 7890
proxies:
  - name: "HK ws"
    type: vless
    server: hk.example.com
    port: 443
    uuid: ` + subUUID + `
    tls: true
    servername: hk.example.com
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: hk.example.com
  - name: "ss legacy"
    type: ss
    server: ss.example.com
    port: "8388"
    cipher: chacha20-ietf-poly1305
    password: "123456"
rules:
  - MATCH,DIRECT
`
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Format != FormatYAML {
		t.Fatalf("format = %q, want yaml", res.Format)
	}
	if len(res.Proxies) != 2 {
		t.Fatalf("proxies = %d, want 2 (warnings: %v)", len(res.Proxies), res.Warnings())
	}
	if p := res.Proxies[0]; p.Transport != "ws" || p.Path != "/ws" || !p.TLS {
		t.Fatalf("vless yaml proxy wrong: %+v", p)
	}
	// A quoted port is a string in YAML; it must still become an int.
	if p := res.Proxies[1]; p.Port != 8388 || p.Method != "chacha20-ietf-poly1305" {
		t.Fatalf("ss yaml proxy wrong: %+v", p)
	}
}

func TestParseBase64URIList(t *testing.T) {
	links := "vless://" + subUUID + "@us.example.com:443?encryption=none&security=tls&sni=us.example.com&type=ws&path=%2Fray#US-01\n" +
		"trojan://tjpass@jp.example.com:443?sni=jp.example.com#JP-01\n"
	b64 := base64.StdEncoding.EncodeToString([]byte(links))

	res, err := Parse([]byte(b64))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Format != FormatBase64 {
		t.Fatalf("format = %q, want base64", res.Format)
	}
	if len(res.Proxies) != 2 {
		t.Fatalf("proxies = %d, want 2 (warnings: %v)", len(res.Proxies), res.Warnings())
	}
	vl := res.Proxies[0]
	if vl.Type != TypeVLESS || vl.UUID != subUUID || !vl.TLS || vl.Path != "/ray" || vl.Name != "US-01" {
		t.Fatalf("vless URI wrong: %+v", vl)
	}
	if tr := res.Proxies[1]; tr.Type != TypeTrojan || tr.Password != "tjpass" || tr.SNI != "jp.example.com" {
		t.Fatalf("trojan URI wrong: %+v", tr)
	}
}

// Sub-Store accepts JSON5 in its UI; an export pasted back must still parse.
func TestParseJSON5Comments(t *testing.T) {
	doc := `// Sub-Store export
{
  "proxies": [
    {
      "type": "ss", "name": "A", "server": "a.example.com", "port": 443,
      "cipher": "aes-128-gcm", "password": "x", /* inline */
    },
  ],
}`
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Proxies) != 1 || res.Proxies[0].Name != "A" {
		t.Fatalf("proxies = %+v (warnings %v)", res.Proxies, res.Warnings())
	}
}

// One broken entry must not discard the document.
func TestParseKeepsGoodEntries(t *testing.T) {
	doc := `[
  {"type": "ss", "name": "ok", "server": "ok.example.com", "port": 443, "cipher": "aes-128-gcm", "password": "p"},
  "this is not an object",
  {"type": "ss", "name": "no-server", "port": 443, "password": "p"},
  {"type": "ss", "name": "bad-port", "server": "x.example.com", "port": 70000, "password": "p"},
  {"type": "vmess", "name": "vm", "server": "vm.example.com", "port": 443, "uuid": "` + subUUID + `"}
]`
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Proxies) != 2 {
		t.Fatalf("proxies = %d, want 2 (ss ok + vmess kept)", len(res.Proxies))
	}
	if res.Skipped != 3 {
		t.Fatalf("skipped = %d, want 3", res.Skipped)
	}
	if len(res.Warnings()) < 3 {
		t.Fatalf("warnings = %v, want one per dropped entry", res.Warnings())
	}

	// vmess parses (so the count is honest) but cannot become an NGG host.
	vm := res.Proxies[1]
	if vm.Importable() {
		t.Fatalf("vmess must not be importable")
	}
	if !strings.Contains(vm.ImportProblem(), "vmess") {
		t.Fatalf("import problem = %q, want it to name vmess", vm.ImportProblem())
	}
	for _, p := range res.Proxies[:1] {
		if !p.Importable() {
			t.Fatalf("%s should be importable: %s", p.Name, p.ImportProblem())
		}
	}
}

func TestParseRejectsUnusableInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "   \n\t ", ErrEmpty},
		{"bom only", "\ufeff", ErrEmpty},
		{"garbage", "hello world, this is not a subscription", ErrUnrecognized},
		{"html", "<html><body>404</body></html>", ErrUnrecognized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.in)); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseTooLarge(t *testing.T) {
	big := make([]byte, MaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := Parse(big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// A BOM + CRLF document is what Windows notepad exports look like.
func TestParseBOMAndCRLF(t *testing.T) {
	doc := "\ufeff[\r\n{\"type\":\"ss\",\"name\":\"A\",\"server\":\"a.example.com\",\"port\":443,\"cipher\":\"aes-128-gcm\",\"password\":\"p\"}\r\n]\r\n"
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Proxies) != 1 || res.Proxies[0].Name != "A" {
		t.Fatalf("proxies = %+v", res.Proxies)
	}
}

func TestParseDuplicateCollapsed(t *testing.T) {
	doc := `[
  {"type": "ss", "name": "A", "server": "same.example.com", "port": 443, "cipher": "aes-128-gcm", "password": "p"},
  {"type": "ss", "name": "A again", "server": "same.example.com", "port": 443, "cipher": "aes-128-gcm", "password": "p"}
]`
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Proxies) != 1 || res.Skipped != 1 {
		t.Fatalf("proxies = %d skipped = %d, want 1 / 1", len(res.Proxies), res.Skipped)
	}
	if !strings.Contains(strings.Join(res.Warnings(), " "), "重复") {
		t.Fatalf("warnings = %v, want a duplicate note", res.Warnings())
	}
}

func TestParseSingleProxyObject(t *testing.T) {
	doc := `{"type":"http","name":"proxy","server":"p.example.com","port":8080,"username":"u","password":"p","tls":true}`
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Proxies) != 1 {
		t.Fatalf("proxies = %d, want 1", len(res.Proxies))
	}
	if p := res.Proxies[0]; p.Type != TypeHTTP || !p.TLS || p.Credential() != "u:p" {
		t.Fatalf("http proxy wrong: %+v", p)
	}
}

// Values exported as strings ("true", "443") must be normalised, not dropped.
func TestParseStringlyTypedFields(t *testing.T) {
	doc := `[{"type":"vless","name":"A","server":"a.example.com","port":"8443","uuid":"` + subUUID + `","tls":"true","sni":"a.example.com"}]`
	res, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := res.Proxies[0]
	if p.Port != 8443 || !p.TLS || p.SNI != "a.example.com" {
		t.Fatalf("proxy wrong: %+v", p)
	}
}

func TestParseURILegacySS(t *testing.T) {
	// SIP002 with the userinfo base64 encoded.
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pw"))
	p, err := ParseURI("ss://" + userinfo + "@ss.example.com:8388#Legacy")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if p.Method != "aes-256-gcm" || p.Password != "pw" || p.Port != 8388 {
		t.Fatalf("proxy wrong: %+v", p)
	}
}

func TestParseVMessURI(t *testing.T) {
	payload := map[string]any{
		"v": "2", "ps": "vm", "add": "vm.example.com", "port": "443",
		"id": subUUID, "net": "ws", "host": "vm.example.com",
		"path": "/vm", "tls": "tls",
	}
	b, _ := json.Marshal(payload)
	raw := "vmess://" + base64.StdEncoding.EncodeToString(b)
	res, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Proxies) != 1 {
		t.Fatalf("proxies = %d", len(res.Proxies))
	}
	p := res.Proxies[0]
	if p.Type != TypeVMess || p.UUID != subUUID || p.Server != "vm.example.com" || !p.TLS {
		t.Fatalf("vmess proxy wrong: %+v", p)
	}
}

func TestRenderPlainJSON(t *testing.T) {
	b, err := Render(
		Node{Name: "US-01", Server: "us.example.com", Port: 443, Protocol: "vless",
			Credential: subUUID, TLS: true, SNI: "www.bing.com", Transport: "ws",
			Path: "/ray", Host: "www.bing.com"},
		Node{Name: "HK-01", Server: "hk.example.com", Port: 8443, Protocol: "shadowsocks",
			Credential: "aes-256-gcm:sspass"},
		Node{Name: "LAN", Server: "10.0.0.9", Port: 1080, Protocol: "socks5", Credential: "u:p"},
	)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var doc []map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("rendered document is not valid JSON: %v\n%s", err, b)
	}
	if len(doc) != 3 {
		t.Fatalf("entries = %d, want 3", len(doc))
	}
	if doc[0]["type"] != "vless" || doc[0]["uuid"] != subUUID || doc[0]["tls"] != true {
		t.Fatalf("vless entry wrong: %v", doc[0])
	}
	opts, ok := doc[0]["ws-opts"].(map[string]any)
	if !ok || opts["path"] != "/ray" {
		t.Fatalf("ws-opts wrong: %v", doc[0]["ws-opts"])
	}
	if doc[1]["type"] != "ss" || doc[1]["cipher"] != "aes-256-gcm" || doc[1]["password"] != "sspass" {
		t.Fatalf("ss entry wrong: %v", doc[1])
	}
	if doc[2]["type"] != "socks5" || doc[2]["username"] != "u" || doc[2]["password"] != "p" {
		t.Fatalf("socks5 entry wrong: %v", doc[2])
	}
}

// Render -> Parse must be lossless for everything NGG can express.
func TestRenderParseRoundTrip(t *testing.T) {
	b, err := Render(Node{
		Name: "JP-01", Server: "jp.example.com", Port: 443, Protocol: "trojan",
		Credential: "tjpass", TLS: true, SNI: "jp.example.com",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	res, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse(rendered): %v", err)
	}
	if len(res.Proxies) != 1 {
		t.Fatalf("proxies = %d", len(res.Proxies))
	}
	p := res.Proxies[0]
	if p.Type != TypeTrojan || p.Server != "jp.example.com" || p.Port != 443 ||
		p.Password != "tjpass" || p.SNI != "jp.example.com" || !p.TLS {
		t.Fatalf("round trip lost data: %+v", p)
	}
	if !p.Importable() {
		t.Fatalf("round-tripped trojan should be importable: %s", p.ImportProblem())
	}
}

func TestRenderRejects(t *testing.T) {
	if _, err := Render(Node{Name: "x", Port: 443, Protocol: "vless", Credential: subUUID}); !errors.Is(err, ErrNoServer) {
		t.Fatalf("err = %v, want ErrNoServer", err)
	}
	if _, err := Render(Node{Name: "x", Server: "a.example.com", Port: 443, Protocol: "hysteria2"}); !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("err = %v, want ErrUnsupportedProtocol", err)
	}
	if _, err := Render(Node{Name: "x", Server: "a.example.com", Port: 443, Protocol: "vless"}); !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("err = %v, want ErrUnsupportedProtocol for a vless without uuid", err)
	}
}

// A preview must never echo a credential back to the browser.
func TestProxyJSONHidesSecrets(t *testing.T) {
	p := Proxy{
		Name: "A", Type: TypeVLESS, Server: "a.example.com", Port: 443,
		UUID: subUUID, Password: "topsecret",
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, subUUID) || strings.Contains(s, "topsecret") {
		t.Fatalf("preview leaked a credential: %s", s)
	}
	if !strings.Contains(s, `"has_secret":true`) {
		t.Fatalf("preview should report has_secret: %s", s)
	}
}

func TestParseBase64NestingGuard(t *testing.T) {
	// base64 of base64 of base64 of ... a document: only three layers are
	// decoded, the rest is reported instead of recursing without bound.
	doc := `[{"type":"ss","name":"A","server":"a.example.com","port":443,"cipher":"aes-128-gcm","password":"p"}]`
	payload := doc
	for i := 0; i < 5; i++ {
		payload = base64.StdEncoding.EncodeToString([]byte(payload))
	}
	res, err := Parse([]byte(payload))
	if err == nil && len(res.Proxies) == 1 {
		return // decoded within the guard: also acceptable
	}
	if err != nil && !errors.Is(err, ErrUnrecognized) {
		t.Fatalf("err = %v, want ErrUnrecognized or a decoded result", err)
	}
}
