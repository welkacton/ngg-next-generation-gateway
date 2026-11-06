package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/subscription"
	"github.com/ngg-project/ngg/internal/users"
)

func newSubscriptionTestMux(t *testing.T) (*http.ServeMux, *users.Registry, string) {
	t.Helper()
	dir := t.TempDir()

	ur := users.NewRegistry(filepath.Join(dir, "users-config.json"))
	if err := ur.Load(); err != nil {
		t.Fatal(err)
	}
	u, err := ur.Add(users.User{Username: "US-01", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	hr := hosts.NewRegistry(filepath.Join(dir, "hosts.json"))
	if err := hr.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := hr.Add(context.Background(), hosts.Host{
		ID: "us01", Name: "US us01", Address: "127.0.0.1",
		NodePort: 1, SvcPort: 8443, Role: hosts.RoleOutbound, Enabled: true,
		Protocol: "trojan", SecretKey: "pass-1",
	}); err != nil {
		t.Fatal(err)
	}
	renderer := subscription.New(subscription.Options{
		Users: ur, Hosts: hr, DataDir: dir,
		BaseURL: func() string { return "https://vpn.example.com:9191" },
	})
	mux := http.NewServeMux()
	newSubscriptionHandlers(ur, renderer).Register(mux)
	return mux, ur, u.ID
}

func doSubReq(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewReader(blob)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestSubscriptionAPIUnknownUser(t *testing.T) {
	mux, _, _ := newSubscriptionTestMux(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users/usr_missing/subscription"},
		{http.MethodPut, "/api/v1/users/usr_missing/subscription"},
		{http.MethodPost, "/api/v1/users/usr_missing/subscription/rotate"},
	} {
		rec := doSubReq(t, mux, tc.method, tc.path, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404 (%s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestSubscriptionAPIUnboundIs422(t *testing.T) {
	mux, _, id := newSubscriptionTestMux(t)
	rec := doSubReq(t, mux, http.MethodGet, "/api/v1/users/"+id+"/subscription", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unbound GET = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("未绑定节点")) {
		t.Fatalf("422 must name the real reason: %s", rec.Body.String())
	}
}

func TestSubscriptionAPILifecycle(t *testing.T) {
	mux, ur, id := newSubscriptionTestMux(t)
	path := "/api/v1/users/" + id + "/subscription"

	// Bind: node + protocol + dns.
	rec := doSubReq(t, mux, http.MethodPut, path, map[string]any{
		"node_id": "us01", "protocol": "trojan", "dns_profile": "gateway",
		"rule_set_ids": []string{"rs-ads"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	var payload subscription.Payload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if payload.UserID != id || payload.NodeID != "us01" || payload.Protocol != "trojan" || payload.DNSProfile != "gateway" {
		t.Fatalf("payload wrong: %+v", payload)
	}
	if payload.Token == "" {
		t.Fatal("PUT must mint the token on first use")
	}
	if want := "https://vpn.example.com:9191/sub/u/" + payload.Token + "?target=clash"; payload.Links.Clash != want {
		t.Fatalf("clash link = %q, want %q", payload.Links.Clash, want)
	}
	for _, link := range []string{payload.Links.Clash, payload.Links.Surge, payload.Links.Singbox, payload.Links.V2ray, payload.Links.Socks} {
		if link == "" {
			t.Fatalf("every target must have a link: %+v", payload.Links)
		}
	}

	// GET returns the same binding and the same token.
	rec = doSubReq(t, mux, http.MethodGet, path, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d (%s)", rec.Code, rec.Body.String())
	}
	var again subscription.Payload
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.Token != payload.Token || again.NodeID != "us01" {
		t.Fatalf("GET drifted from PUT: %+v", again)
	}

	// Rotate invalidates the old token.
	rec = doSubReq(t, mux, http.MethodPost, path+"/rotate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d (%s)", rec.Code, rec.Body.String())
	}
	var rotated subscription.Payload
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.Token == payload.Token {
		t.Fatal("rotate must change the token")
	}
	if _, ok := ur.UserBySubscriptionToken(payload.Token); ok {
		t.Fatal("the rotated-away token must stop resolving")
	}
	if _, ok := ur.UserBySubscriptionToken(rotated.Token); !ok {
		t.Fatal("the new token must resolve")
	}
}

func TestSubscriptionAPIRejectsBadBinding(t *testing.T) {
	mux, ur, id := newSubscriptionTestMux(t)
	path := "/api/v1/users/" + id + "/subscription"

	rec := doSubReq(t, mux, http.MethodPut, path, map[string]any{
		"node_id": "us01", "protocol": "wireguard", "dns_profile": "system",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad protocol = %d, want 422", rec.Code)
	}

	rec = doSubReq(t, mux, http.MethodPut, path, map[string]any{
		"node_id": "us01", "protocol": "trojan", "dns_profile": "quad9",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad dns profile = %d, want 422", rec.Code)
	}

	// A node id that does not exist is a 422 with the real reason, and nothing
	// is left behind that would make the user look bound to a working node.
	rec = doSubReq(t, mux, http.MethodPut, path, map[string]any{
		"node_id": "ghost", "protocol": "trojan", "dns_profile": "system",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown node = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("ghost")) {
		t.Fatalf("422 must name the node: %s", rec.Body.String())
	}
	if u, _ := ur.Get(id); u.NodeID != "ghost" {
		t.Fatalf("the requested binding is stored verbatim for the operator to fix: %+v", u)
	}
}
