package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestNetworkComponentToggleRejectsUnknownKey(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, "POST", ts.URL+"/api/v1/network/components/toggle", token,
		`{"key":"not_a_component","enabled":true}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown component key: got %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "unknown component key") {
		t.Fatalf("error should name the unknown key: %s", body)
	}
}

func TestNetworkComponentTogglePolicyVsComponent(t *testing.T) {
	ts, token := newTestServer(t)

	// A policy key takes effect immediately and must not require a restart.
	resp, body := req(t, "POST", ts.URL+"/api/v1/network/components/toggle", token,
		`{"key":"fail_closed","enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("policy toggle: %d %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["key"] != "fail_closed" || out["enabled"] != false {
		t.Fatalf("policy toggle result wrong: %s", body)
	}
	if out["requires_restart"] != false {
		t.Fatalf("policy toggle must not require restart: %s", body)
	}
	if !strings.Contains(fmt.Sprint(out["message"]), "即时生效") {
		t.Fatalf("policy toggle message should say it is immediate: %s", body)
	}

	// A component key only persists the intent; it must not claim runtime start/stop.
	resp, body = req(t, "POST", ts.URL+"/api/v1/network/components/toggle", token,
		`{"key":"dns","enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("component toggle: %d %s", resp.StatusCode, body)
	}
	out = nil
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out["requires_restart"] != true {
		t.Fatalf("component toggle must require restart: %s", body)
	}
	msg := fmt.Sprint(out["message"])
	if !strings.Contains(msg, "并未被启停") {
		t.Fatalf("component toggle message must be honest about runtime state: %s", body)
	}
	if strings.Contains(msg, "已启停") {
		t.Fatalf("component toggle falsely claims a runtime start/stop: %s", body)
	}
}

func TestNetworkStatusReportsCapabilityMatrix(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, "GET", ts.URL+"/api/v1/network/status", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("network status: %d %s", resp.StatusCode, body)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatal(err)
	}
	// v2.6.3 fields must be preserved.
	for _, k := range []string{"running", "stage", "fail_closed", "failover", "ingress", "tun", "dns"} {
		if _, ok := status[k]; !ok {
			t.Errorf("status lost existing field %q", k)
		}
	}
	// New capability fields.
	for _, k := range []string{"default_core", "direct_on_failure", "bypass_tun", "components"} {
		if _, ok := status[k]; !ok {
			t.Errorf("status missing new field %q", k)
		}
	}
	// v2.7.5: only real, independently observable resources are listed. The
	// four policy keys (fail_closed / failover / direct_on_failure / bypass_tun)
	// were removed: they cannot be started or stopped on their own and are
	// already reported as 安全策略 by the engine card.
	components, ok := status["components"].([]any)
	if !ok || len(components) != 4 {
		t.Fatalf("components should be 4 rows, got %T %v", status["components"], status["components"])
	}
	wantKeys := map[string]bool{"ingress": true, "dns": true, "tun": true, "egress": true}
	for _, raw := range components {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("component row is not an object: %v", raw)
		}
		for _, k := range []string{"key", "label", "enabled", "running", "description"} {
			if _, ok := row[k]; !ok {
				t.Errorf("component row missing %q: %v", k, row)
			}
		}
		key, _ := row["key"].(string)
		if !wantKeys[key] {
			t.Errorf("unexpected component key %q", key)
		}
		delete(wantKeys, key)
	}
	if len(wantKeys) != 0 {
		t.Fatalf("missing component keys: %v", wantKeys)
	}
}

func TestNetworkConfigRejectsUnregisteredDefaultCore(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, "PUT", ts.URL+"/api/v1/network/config", token,
		`{"default_core":"not-a-kernel"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unregistered default_core: got %d %s", resp.StatusCode, body)
	}
	// Empty (auto) and registered kernels are accepted.
	for _, core := range []string{"", "mihomo", "sing-box", "xray-core"} {
		resp, body = req(t, "PUT", ts.URL+"/api/v1/network/config", token,
			`{"default_core":"`+core+`"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("default_core %q rejected: %d %s", core, resp.StatusCode, body)
		}
	}
}
