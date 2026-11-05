package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func decodeObj(t *testing.T, body string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, body)
	}
	return v
}

func TestProtocolCenterCRUDConfigAndBatchToggle(t *testing.T) {
	ts, token := newTestServer(t)
	base := ts.URL + "/api/v1"

	// Metadata is a literal path and must not be swallowed by /protocols/{id}.
	resp, body := req(t, "GET", base+"/protocols/meta", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("meta status=%d body=%s", resp.StatusCode, body)
	}
	meta := decodeObj(t, body)
	if _, ok := meta["groups"]; !ok {
		t.Fatal("meta missing groups")
	}

	// Group filtering really splits the catalog into the three families.
	resp, body = req(t, "GET", base+"/protocols?group=plaintext", token, "")
	var plain []map[string]any
	if err := json.Unmarshal([]byte(body), &plain); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("list plaintext: status=%d err=%v body=%s", resp.StatusCode, err, body)
	}
	if len(plain) < 5 {
		t.Fatalf("plaintext group too small: %d", len(plain))
	}
	for _, p := range plain {
		if p["group"] != "plaintext" {
			t.Fatalf("group filter leaked %v", p["id"])
		}
	}

	// Create a custom protocol from a raw JSON body.
	custom := `{"id":"corp-relay","name":"Corp Relay","category":"plain-proxy","group":"plain-proxy",` +
		`"security":"自定义","transport":"TCP","default_port":9999,"config_kind":"outbound",` +
		`"kernels":["mihomo"],"params":{"server":"10.10.0.1","port":9999}}`
	resp, body = req(t, "POST", base+"/protocols", token, custom)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = req(t, "GET", base+"/protocols/corp-relay", token, "")
	if resp.StatusCode != http.StatusOK || !json.Valid([]byte(body)) {
		t.Fatalf("get custom: status=%d body=%s", resp.StatusCode, body)
	}
	if got := decodeObj(t, body); got["custom"] != true || got["enabled"] != true {
		t.Fatalf("custom flag missing or default enabled wrong: %+v", got)
	}

	// Generated config uses the spec params.
	resp, body = req(t, "POST", base+"/protocols/corp-relay/config", token,
		`{"kernel":"mihomo","params":{}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config: status=%d body=%s", resp.StatusCode, body)
	}
	cfg := decodeObj(t, body)
	if cfg["validated"] != true || cfg["format"] != "yaml" {
		t.Fatalf("unexpected config result: %+v", cfg)
	}
	content, _ := cfg["content"].(string)
	if content == "" {
		t.Fatal("empty generated content")
	}

	// Validate hand-edited content, both good and bad.
	resp, body = req(t, "POST", base+"/protocols/validate", token,
		`{"content":"{\"a\":1}","format":"json"}`)
	if v := decodeObj(t, body); resp.StatusCode != http.StatusOK || v["valid"] != true {
		t.Fatalf("validate good: %d %+v", resp.StatusCode, v)
	}
	resp, body = req(t, "POST", base+"/protocols/validate", token,
		`{"content":"{bad","format":"json"}`)
	if v := decodeObj(t, body); v["valid"] != false || v["error"] == "" {
		t.Fatalf("validate bad: %+v", v)
	}

	// Save an edited config, then confirm generation returns the override.
	edited := `{"type":"http","tag":"hand-edited","server":"10.10.0.1"}`
	payload, _ := json.Marshal(map[string]string{"content": edited, "format": "json"})
	resp, body = req(t, "PUT", base+"/protocols/corp-relay/config", token, string(payload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save config: status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = req(t, "POST", base+"/protocols/corp-relay/config", token,
		`{"kernel":"mihomo","params":{}}`)
	cfg = decodeObj(t, body)
	if cfg["content"] != edited || cfg["customized"] != true {
		t.Fatalf("override not returned: %+v", cfg)
	}
	resp, body = req(t, "DELETE", base+"/protocols/corp-relay/config", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear config: status=%d body=%s", resp.StatusCode, body)
	}

	// Batch disable/enable: selected and all.
	resp, body = req(t, "POST", base+"/protocols/toggle", token,
		`{"ids":["wireguard","trojan"],"enabled":false}`)
	if v := decodeObj(t, body); resp.StatusCode != http.StatusOK || v["changed"] != float64(2) {
		t.Fatalf("toggle selected: %d %+v", resp.StatusCode, v)
	}
	resp, body = req(t, "POST", base+"/protocols/toggle", token, `{"all":true,"enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle all: status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = req(t, "GET", base+"/protocols?enabled=true", token, "")
	var enabled []map[string]any
	_ = json.Unmarshal([]byte(body), &enabled)
	if len(enabled) != 0 {
		t.Fatalf("disable-all left %d enabled protocols", len(enabled))
	}
	// A disabled protocol must not generate config.
	resp, _ = req(t, "POST", base+"/protocols/wireguard/config", token,
		`{"kernel":"sing-box","params":{},"regenerate":true}`)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("disabled protocol should not generate config")
	}
	resp, body = req(t, "POST", base+"/protocols/toggle", token, `{"all":true,"enabled":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-enable all: status=%d body=%s", resp.StatusCode, body)
	}

	// Update + delete custom; built-in deletion is refused.
	updated := `{"id":"corp-relay","name":"Corp Relay v2","category":"plain-proxy",` +
		`"group":"plain-proxy","kernels":["mihomo","sing-box"],"enabled":true}`
	resp, body = req(t, "PUT", base+"/protocols/corp-relay", token, updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", resp.StatusCode, body)
	}
	resp, _ = req(t, "DELETE", base+"/protocols/wireguard", token, "")
	if resp.StatusCode == http.StatusOK {
		t.Fatal("built-in protocol must not be deletable")
	}
	resp, body = req(t, "DELETE", base+"/protocols/corp-relay", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete custom: status=%d body=%s", resp.StatusCode, body)
	}
}
