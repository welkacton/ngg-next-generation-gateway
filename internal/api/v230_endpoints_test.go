package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGitHubPresetCreateDeleteAndCustomField(t *testing.T) {
	ts, token := newTestServer(t)

	resp, body := req(t, "GET", ts.URL+"/api/v1/rulesets/github/presets", token, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"custom"`) {
		t.Fatalf("presets must expose custom list: %d %s", resp.StatusCode, body)
	}

	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets/github/presets", token,
		`{"repo":"Acme/MyRules","label":"Acme rules","formats":["Clash"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add preset: %d %s", resp.StatusCode, body)
	}
	var created struct {
		OK     bool `json:"ok"`
		Preset struct {
			Repo   string `json:"repo"`
			Custom bool   `json:"custom"`
		} `json:"preset"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.Preset.Repo != "Acme/MyRules" || !created.Preset.Custom {
		t.Fatalf("created preset wrong: %s", body)
	}

	_, listBody := req(t, "GET", ts.URL+"/api/v1/rulesets/github/presets", token, "")
	if !strings.Contains(listBody, "Acme/MyRules") || !strings.Contains(listBody, `"custom":["Acme/MyRules"]`) {
		t.Fatalf("custom preset not listed/deletable: %s", listBody)
	}

	// A built-in preset cannot be deleted (422).
	resp, body = req(t, "DELETE", ts.URL+"/api/v1/rulesets/github/presets/"+
		url.PathEscape("blackmatrix7/ios_rule_script"), token, "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("built-in delete: got %d %s", resp.StatusCode, body)
	}

	// The repo contains '/', so the client URL-encodes it; PathValue must
	// hand back the decoded "owner/name".
	resp, body = req(t, "DELETE", ts.URL+"/api/v1/rulesets/github/presets/"+
		url.PathEscape("Acme/MyRules"), token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete preset: %d %s", resp.StatusCode, body)
	}
	var deleted struct {
		OK      bool   `json:"ok"`
		Deleted string `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(body), &deleted); err != nil {
		t.Fatal(err)
	}
	if !deleted.OK || deleted.Deleted != "Acme/MyRules" {
		t.Fatalf("deleted result wrong (path traversal / decoding issue): %s", body)
	}

	_, listBody = req(t, "GET", ts.URL+"/api/v1/rulesets/github/presets", token, "")
	if strings.Contains(listBody, "Acme/MyRules") {
		t.Fatalf("deleted preset still listed: %s", listBody)
	}
}

func TestRuleSetSyntaxAndKernelEcho(t *testing.T) {
	ts, token := newTestServer(t)

	resp, body := req(t, "GET", ts.URL+"/api/v1/rulesets/syntax", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rulesets/syntax: %d %s", resp.StatusCode, body)
	}
	var syntax struct {
		Schemas []struct {
			Kernel string `json:"kernel"`
			Fields []struct {
				Key string `json:"key"`
			} `json:"fields"`
			Example string `json:"example"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal([]byte(body), &syntax); err != nil {
		t.Fatal(err)
	}
	if len(syntax.Schemas) != 3 {
		t.Fatalf("expected 3 kernels, got %d", len(syntax.Schemas))
	}
	seen := map[string]bool{}
	for _, s := range syntax.Schemas {
		seen[s.Kernel] = true
		if len(s.Fields) == 0 || s.Example == "" {
			t.Errorf("kernel %s has empty fields/example", s.Kernel)
		}
	}
	for _, k := range []string{"mihomo", "sing-box", "xray-core"} {
		if !seen[k] {
			t.Errorf("missing schema for %s", k)
		}
	}

	// POST /rulesets accepts an optional kernel and GET echoes it back.
	resp, body = req(t, "POST", ts.URL+"/api/v1/rulesets", token,
		`{"id":"rs-kernel","name":"sing-box set","source":"inline","enabled":true,`+
			`"content":"DOMAIN-SUFFIX,example.com,PROXY\n","kernel":"sing-box"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create ruleset with kernel: %d %s", resp.StatusCode, body)
	}
	_, listBody := req(t, "GET", ts.URL+"/api/v1/rulesets", token, "")
	if !strings.Contains(listBody, `"kernel":"sing-box"`) {
		t.Fatalf("kernel not echoed by GET /rulesets: %s", listBody)
	}
	_, oneBody := req(t, "GET", ts.URL+"/api/v1/rulesets/rs-kernel", token, "")
	if !strings.Contains(oneBody, `"kernel":"sing-box"`) {
		t.Fatalf("kernel not echoed by GET /rulesets/{id}: %s", oneBody)
	}
}

func TestDnsSchemaEndpoint(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, "GET", ts.URL+"/api/v1/profiles/dns-schema", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profiles/dns-schema: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Schemas []struct {
			Kernel string `json:"kernel"`
			Format string `json:"format"`
			Fields []struct {
				Key string `json:"key"`
			} `json:"fields"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Schemas) != 3 {
		t.Fatalf("expected 3 DNS schemas, got %d", len(out.Schemas))
	}
	for i, k := range []string{"mihomo", "sing-box", "xray-core"} {
		if out.Schemas[i].Kernel != k || len(out.Schemas[i].Fields) == 0 {
			t.Errorf("DNS schema[%d] = %+v, want kernel %s with fields", i, out.Schemas[i], k)
		}
	}
}

func TestNetworkRulesDeleteEndpoint(t *testing.T) {
	ts, token := newTestServer(t)

	resp, body := req(t, "POST", ts.URL+"/api/v1/network/rules/delete", token, `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty delete must be 400: %d %s", resp.StatusCode, body)
	}

	resp, body = req(t, "POST", ts.URL+"/api/v1/network/rules/delete", token, `{"all":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete all: %d %s", resp.StatusCode, body)
	}
	var out struct {
		OK            bool   `json:"ok"`
		Deleted       int    `json:"deleted"`
		Scope         string `json:"scope"`
		Rules         []any  `json:"rules"`
		DefaultAction string `json:"default_action"`
		DefaultCore   string `json:"default_core"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Deleted == 0 || out.Scope != "all" || len(out.Rules) != 0 {
		t.Fatalf("delete all result wrong: %+v", out)
	}
	if out.DefaultAction == "" {
		t.Fatalf("default_action must be returned: %s", body)
	}

	// Selective delete only removes the named ids.
	for _, id := range []string{"r1", "r2"} {
		resp, _ = req(t, "POST", ts.URL+"/api/v1/network/rules", token,
			fmt.Sprintf(`{"id":"%s","type":"DOMAIN","value":"%s.test","action":"DIRECT","enabled":true}`, id, id))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("add rule %s: %d", id, resp.StatusCode)
		}
	}
	resp, body = req(t, "POST", ts.URL+"/api/v1/network/rules/delete", token, `{"ids":["r1"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("selective delete: %d %s", resp.StatusCode, body)
	}
	var out2 struct {
		OK            bool   `json:"ok"`
		Deleted       int    `json:"deleted"`
		Scope         string `json:"scope"`
		Rules         []any  `json:"rules"`
		DefaultAction string `json:"default_action"`
		DefaultCore   string `json:"default_core"`
	}
	if err := json.Unmarshal([]byte(body), &out2); err != nil {
		t.Fatal(err)
	}
	if out2.Deleted != 1 || out2.Scope != "selected" || len(out2.Rules) != 1 {
		t.Fatalf("selective delete result wrong: %+v", out2)
	}
}

func TestNodeRuntimeEndpoints(t *testing.T) {
	ts, token := newTestServer(t)

	// Real key generation.
	resp, body := req(t, "POST", ts.URL+"/api/v1/nodes/keys/generate", token, `{"kind":"wireguard"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("keys/generate: %d %s", resp.StatusCode, body)
	}
	var kp struct {
		OK         bool   `json:"ok"`
		Kind       string `json:"kind"`
		PrivateKey string `json:"private_key"`
		PublicKey  string `json:"public_key"`
	}
	if err := json.Unmarshal([]byte(body), &kp); err != nil {
		t.Fatal(err)
	}
	if !kp.OK || kp.Kind != "wireguard" || kp.PrivateKey == "" || kp.PublicKey == "" {
		t.Fatalf("keypair incomplete: %s", body)
	}
	resp, _ = req(t, "POST", ts.URL+"/api/v1/nodes/keys/generate", token, `{"kind":"bogus"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatal("invalid key kind must be rejected")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot open local listener: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	defer ln.Close()

	mkNode := func(id, kind string) string {
		return fmt.Sprintf(`{"id":"%s","name":"%s","kind":"%s","protocol":"socks5",`+
			`"address":"127.0.0.1","port":%d,"enabled":true}`, id, id, kind, port)
	}
	for _, n := range []struct{ id, kind string }{{"in-1", "inbound"}, {"out-1", "outbound"}} {
		resp, b := req(t, "POST", ts.URL+"/api/v1/nodes", token, mkNode(n.id, n.kind))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create node %s: %d %s", n.id, resp.StatusCode, b)
		}
	}

	// Pairing writes real material into both nodes' Params and persists it.
	resp, body = req(t, "POST", ts.URL+"/api/v1/nodes/keys/pair", token,
		`{"inbound_id":"in-1","outbound_id":"out-1","kind":"wireguard"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("keys/pair: %d %s", resp.StatusCode, body)
	}
	var pair struct {
		OK        bool `json:"ok"`
		Paired    bool `json:"paired"`
		Persisted bool `json:"persisted"`
	}
	if err := json.Unmarshal([]byte(body), &pair); err != nil {
		t.Fatal(err)
	}
	if !pair.OK || !pair.Paired || !pair.Persisted {
		t.Fatalf("pair not persisted: %s", body)
	}
	_, nodeBody := req(t, "GET", ts.URL+"/api/v1/nodes/in-1", token, "")
	if !strings.Contains(nodeBody, "private_key") {
		t.Fatalf("inbound Params missing private_key: %s", nodeBody)
	}
	_, nodeBody = req(t, "GET", ts.URL+"/api/v1/nodes/out-1", token, "")
	if !strings.Contains(nodeBody, "peer_public_key") {
		t.Fatalf("outbound Params missing peer_public_key: %s", nodeBody)
	}

	// Config file is written with 0600 and recorded on the node.
	resp, body = req(t, "PUT", ts.URL+"/api/v1/nodes/in-1/config-file", token,
		`{"content":"{\"log\":{}}","format":"json"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config-file: %d %s", resp.StatusCode, body)
	}
	var cf struct {
		OK        bool   `json:"ok"`
		Path      string `json:"path"`
		Bytes     int    `json:"bytes"`
		Validated bool   `json:"validated"`
	}
	if err := json.Unmarshal([]byte(body), &cf); err != nil {
		t.Fatal(err)
	}
	if !cf.OK || cf.Path == "" || cf.Bytes == 0 || !cf.Validated {
		t.Fatalf("config-file result wrong: %s", body)
	}
	_, nodeBody = req(t, "GET", ts.URL+"/api/v1/nodes/in-1", token, "")
	if !strings.Contains(nodeBody, "config_file") {
		t.Fatalf("node does not echo config_file: %s", nodeBody)
	}

	// Metrics must honestly report unavailability instead of faking numbers.
	resp, body = req(t, "GET", ts.URL+"/api/v1/nodes/in-1/metrics", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics: %d %s", resp.StatusCode, body)
	}
	var metrics struct {
		OK         bool    `json:"ok"`
		CPUPercent float64 `json:"cpu_percent"`
		MemoryRSS  uint64  `json:"memory_rss"`
		Source     string  `json:"source"`
		Note       string  `json:"note"`
	}
	if err := json.Unmarshal([]byte(body), &metrics); err != nil {
		t.Fatal(err)
	}
	if !metrics.OK || metrics.Source != "unavailable" || metrics.Note == "" {
		t.Fatalf("metrics must report unavailable honestly: %s", body)
	}
	if metrics.CPUPercent != 0 || metrics.MemoryRSS != 0 {
		t.Fatalf("metrics must not fabricate values: %s", body)
	}

	// Probe performs a real TCP dial and writes status/last_seen back.
	resp, body = req(t, "POST", ts.URL+"/api/v1/nodes/in-1/probe", token, `{"timeout_ms":2000}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("probe: %d %s", resp.StatusCode, body)
	}
	var probe struct {
		OK        bool   `json:"ok"`
		Reachable bool   `json:"reachable"`
		CheckedAt string `json:"checked_at"`
	}
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.OK || !probe.Reachable || probe.CheckedAt == "" {
		t.Fatalf("real probe against a live listener should succeed: %s", body)
	}
	_, nodeBody = req(t, "GET", ts.URL+"/api/v1/nodes/in-1", token, "")
	if !strings.Contains(nodeBody, `"status":"online"`) || !strings.Contains(nodeBody, "last_seen") {
		t.Fatalf("probe did not persist online status: %s", nodeBody)
	}
}
