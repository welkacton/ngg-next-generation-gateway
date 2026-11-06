package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The Control Center must always describe three ordered stages, and the
// adapter list must come from the real host, never a hard-coded table.
func TestControlCenterReportsOrderedStages(t *testing.T) {
	ts, token := newTestServer(t)

	resp, body := req(t, http.MethodGet, ts.URL+"/api/v1/gateway/control", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /gateway/control: %d %s", resp.StatusCode, body)
	}
	var state struct {
		Master struct {
			AdapterName string `json:"adapter_name"`
		} `json:"master"`
		Steps []struct {
			Key    string `json:"key"`
			Order  int    `json:"order"`
			Status string `json:"status"`
		} `json:"steps"`
		Adapters []struct {
			Name string `json:"name"`
		} `json:"adapters"`
		Egress struct {
			Interface string `json:"interface"`
		} `json:"egress"`
	}
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	wantOrder := []string{"adapter", "capture", "egress"}
	if len(state.Steps) != len(wantOrder) {
		t.Fatalf("expected %d steps, got %d: %s", len(wantOrder), len(state.Steps), body)
	}
	for i, key := range wantOrder {
		if state.Steps[i].Key != key {
			t.Fatalf("step %d = %q, want %q", i, state.Steps[i].Key, key)
		}
		if state.Steps[i].Order != i+1 {
			t.Fatalf("step %q order = %d, want %d", key, state.Steps[i].Order, i+1)
		}
		switch state.Steps[i].Status {
		case "ok", "warn", "fail", "skip":
		default:
			t.Fatalf("step %q has an unknown status %q", key, state.Steps[i].Status)
		}
	}
	if state.Master.AdapterName != "NGG" {
		t.Fatalf("default adapter name = %q, want NGG", state.Master.AdapterName)
	}
	// A gateway test host has at least the loopback interface.
	if len(state.Adapters) == 0 {
		t.Fatal("adapter list is empty: the console would have nothing to pick from")
	}
}

func TestAdapterListFlagsVirtualAndTun(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, http.MethodGet, ts.URL+"/api/v1/network/adapters", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /network/adapters: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Adapters []struct {
			Name     string   `json:"name"`
			Loopback bool     `json:"loopback"`
			IsNGG    bool     `json:"is_ngg"`
			IPv4     []string `json:"ipv4"`
		} `json:"adapters"`
		TunName string `json:"tun_name"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TunName != "NGG" {
		t.Fatalf("tun_name = %q, want NGG", out.TunName)
	}
	if len(out.Adapters) == 0 {
		t.Fatal("no adapters reported")
	}
	for _, a := range out.Adapters {
		if a.Name == "" {
			t.Fatal("adapter without a name")
		}
		if strings.EqualFold(a.Name, "NGG") && !a.IsNGG {
			t.Fatalf("adapter named NGG must be flagged is_ngg: %+v", a)
		}
	}
}

// start must answer with per-step results even when the host cannot elevate;
// a failed adapter step must never be reported as success.
func TestControlStartIsHonestWithoutPrivileges(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := req(t, http.MethodPost, ts.URL+"/api/v1/gateway/control/start", token,
		`{"adapter_name":"NGG","capture_all":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %s", resp.StatusCode, body)
	}
	var out struct {
		OK    bool `json:"ok"`
		Steps []struct {
			Key    string `json:"key"`
			Status string `json:"status"`
		} `json:"steps"`
		Master struct {
			Enabled    bool `json:"enabled"`
			CaptureAll bool `json:"capture_all"`
		} `json:"master"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Steps) == 0 {
		t.Fatalf("start returned no steps: %s", body)
	}
	if !out.Master.CaptureAll {
		t.Fatalf("start must record capture_all=true: %+v", out.Master)
	}
	// enabled must describe reality: true only when the takeover actually ran.
	if out.Master.Enabled != out.OK {
		t.Fatalf("enabled=%v but ok=%v: the switch must not claim an unapplied takeover", out.Master.Enabled, out.OK)
	}
	// On a test host the TUN cannot be created (no elevation / no driver), so
	// the adapter step must be "fail" and ok must be false — never a fake pass.
	for _, s := range out.Steps {
		if s.Key != "adapter" {
			continue
		}
		if s.Status == "ok" {
			t.Skip("host can create TUN adapters; the failure path is not exercised here")
		}
		if s.Status != "fail" {
			t.Fatalf("adapter step = %q, want fail on an unprivileged host", s.Status)
		}
		if out.OK {
			t.Fatal("ok=true although the adapter step failed")
		}
		return
	}
	t.Fatal("no adapter step in the response")
}
