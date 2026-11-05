package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func decodeToggleBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return m
}

func TestNetworkRulesToggleEndpoint(t *testing.T) {
	ts, token := newTestServer(t)
	base := ts.URL + "/api/v1/network/rules"

	resp, body := req(t, http.MethodGet, base, token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET rules: %d %s", resp.StatusCode, body)
	}
	var listed struct {
		Rules []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("parse rules: %v", err)
	}
	if len(listed.Rules) == 0 {
		t.Fatal("expected default rules to be present")
	}
	id := listed.Rules[0].ID

	// 400 when neither ids nor all is supplied.
	resp, body = req(t, http.MethodPost, base+"/toggle", token, `{"enabled":false}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty toggle selection: got %d %s, want 400", resp.StatusCode, body)
	}

	resp, body = req(t, http.MethodPost, base+"/toggle", token,
		`{"ids":["`+id+`"],"enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("single toggle: %d %s", resp.StatusCode, body)
	}
	if got := decodeToggleBody(t, body); got["changed"] != float64(1) || got["enabled"] != false {
		t.Fatalf("unexpected single toggle result: %v", got)
	}

	resp, body = req(t, http.MethodGet, base, token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET after toggle: %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range listed.Rules {
		if r.ID == id {
			found = true
			if r.Enabled {
				t.Fatalf("rule %s is still enabled", id)
			}
		}
	}
	if !found {
		t.Fatalf("rule %s disappeared", id)
	}

	resp, body = req(t, http.MethodPost, base+"/toggle", token, `{"all":true,"enabled":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable all: %d %s", resp.StatusCode, body)
	}
	if got := decodeToggleBody(t, body); got["scope"] != "all" || got["enabled"] != true {
		t.Fatalf("unexpected enable-all result: %v", got)
	}
	_, body = req(t, http.MethodGet, base, token, "")
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	for _, r := range listed.Rules {
		if !r.Enabled {
			t.Fatalf("rule %s still disabled after enable-all", r.ID)
		}
	}

	// Auth must still be enforced on the new route.
	resp, _ = req(t, http.MethodPost, base+"/toggle", "", `{"all":true,"enabled":false}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("toggle without token: got %d, want 401", resp.StatusCode)
	}
}

func TestRuleSetsToggleEndpoint(t *testing.T) {
	ts, token := newTestServer(t)
	base := ts.URL + "/api/v1/rulesets"

	resp, body := req(t, http.MethodPost, base, token,
		`{"id":"rs-toggle-test","name":"Toggle test","source":"inline","content":"DOMAIN-SUFFIX,example.com,DIRECT","format":"auto","enabled":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create rule set: %d %s", resp.StatusCode, body)
	}

	resp, body = req(t, http.MethodPost, base+"/toggle", token,
		`{"ids":["rs-toggle-test"],"enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("single toggle: %d %s", resp.StatusCode, body)
	}
	if got := decodeToggleBody(t, body); got["changed"] != float64(1) {
		t.Fatalf("unexpected toggle result: %v", got)
	}

	resp, body = req(t, http.MethodGet, base, token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET rule sets: %d %s", resp.StatusCode, body)
	}
	var list struct {
		RuleSets []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"rulesets"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.RuleSets) == 0 || list.RuleSets[0].Enabled {
		t.Fatalf("rule set was not disabled: %+v", list.RuleSets)
	}

	resp, body = req(t, http.MethodPost, base+"/toggle", token, `{"all":true,"enabled":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable all: %d %s", resp.StatusCode, body)
	}
	if got := decodeToggleBody(t, body); got["scope"] != "all" {
		t.Fatalf("unexpected enable-all result: %v", got)
	}
	_, body = req(t, http.MethodGet, base, token, "")
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	for _, s := range list.RuleSets {
		if !s.Enabled {
			t.Fatalf("rule set %s still disabled after enable-all", s.ID)
		}
	}

	resp, _ = req(t, http.MethodPost, base+"/toggle", "", `{"all":true,"enabled":false}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("toggle without token: got %d, want 401", resp.StatusCode)
	}
}
