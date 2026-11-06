package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/clients"
)

func newClientsTestMux(t *testing.T) (*http.ServeMux, *clients.Store) {
	t.Helper()
	st := clients.NewStore(t.TempDir())
	if err := st.Load(); err != nil {
		t.Fatalf("clients.Load: %v", err)
	}
	mux := http.NewServeMux()
	newClientHandlers(st).Register(mux)
	return mux, st
}

// do issues one request against mux and returns the recorder.
func doClientReq(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
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

func decodeClientBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

func TestClientsEndpointFlow(t *testing.T) {
	mux, st := newClientsTestMux(t)

	// ---- list: empty, default access -------------------------------------
	rec := doClientReq(t, mux, http.MethodGet, "/api/v1/clients", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rec.Code, rec.Body)
	}
	list := decodeClientBody[struct {
		Clients []clients.ClientView `json:"clients"`
		Count   int                  `json:"count"`
		Access  clients.AccessConfig `json:"access"`
	}](t, rec)
	if list.Count != 0 || len(list.Clients) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
	if list.Access.PublicHost != "" || list.Access.PublicPort != 1080 || list.Access.Protocol != "socks5" {
		t.Fatalf("unexpected default access %+v", list.Access)
	}

	// ---- create with no public host: config must be refused --------------
	rec = doClientReq(t, mux, http.MethodPost, "/api/v1/clients", map[string]any{"name": "iPhone 15", "platform": "ios"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}
	created := decodeClientBody[struct {
		Client     clients.ClientView `json:"client"`
		Credential string             `json:"credential"`
	}](t, rec)
	if created.Credential == "" || len(created.Credential) != 32 {
		t.Fatalf("credential = %q, want 32 chars", created.Credential)
	}
	if created.Client.ID == "" || !created.Client.CredentialSet || !created.Client.Enabled {
		t.Fatalf("unexpected client %+v", created.Client)
	}
	id := created.Client.ID
	if got := strings.Join(created.Client.Formats, ","); got != "singbox,mihomo,socks" {
		t.Fatalf("formats = %q", got)
	}

	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+id+"/config?format=singbox", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("config without public_host status = %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "未设置对外接入地址") {
		t.Fatalf("error must name the missing address: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "CONFIG_INVALID") {
		t.Fatalf("error envelope missing CONFIG_INVALID: %s", rec.Body)
	}

	// ---- access: invalid host rejected -----------------------------------
	rec = doClientReq(t, mux, http.MethodPut, "/api/v1/clients/access", map[string]any{
		"public_host": "http://vpn.example.com", "public_port": 1080, "protocol": "socks5",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("scheme in host status = %d, body %s", rec.Code, rec.Body)
	}

	// ---- access: valid save ----------------------------------------------
	rec = doClientReq(t, mux, http.MethodPut, "/api/v1/clients/access", map[string]any{
		"public_host": "vpn.example.com", "public_port": 1080, "protocol": "socks5", "note": "HK",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save access status = %d, body %s", rec.Code, rec.Body)
	}
	saved := decodeClientBody[struct {
		Access  clients.AccessConfig `json:"access"`
		Changed []string             `json:"changed"`
	}](t, rec)
	if saved.Access.PublicHost != "vpn.example.com" || saved.Access.PublicPort != 1080 {
		t.Fatalf("saved access = %+v", saved.Access)
	}
	if strings.Join(saved.Changed, ",") != "public_host,note" {
		t.Fatalf("changed = %v, want public_host,note", saved.Changed)
	}

	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/access", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get access status = %d", rec.Code)
	}
	acc := decodeClientBody[clients.AccessConfig](t, rec)
	if acc.PublicHost != "vpn.example.com" {
		t.Fatalf("get access = %+v", acc)
	}

	// ---- config: every format the client advertises ----------------------
	type configResult struct {
		Format          string `json:"format"`
		Filename        string `json:"filename"`
		Content         string `json:"content"`
		SubscriptionURL string `json:"subscription_url"`
	}
	for _, format := range []string{"singbox", "mihomo", "socks"} {
		rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+id+"/config?format="+format, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s config status = %d, body %s", format, rec.Code, rec.Body)
		}
		res := decodeClientBody[configResult](t, rec)
		if res.Format != format || res.Filename == "" || res.Content == "" {
			t.Fatalf("%s result = %+v", format, res)
		}
		if !strings.HasPrefix(res.SubscriptionURL, "/sub/") {
			t.Fatalf("%s subscription_url = %q, want relative /sub/...", format, res.SubscriptionURL)
		}
		switch format {
		case "singbox":
			var doc map[string]any
			if err := json.Unmarshal([]byte(res.Content), &doc); err != nil {
				t.Fatalf("singbox body is not JSON: %v", err)
			}
			if !strings.HasSuffix(res.Filename, ".json") {
				t.Fatalf("singbox filename = %q", res.Filename)
			}
		case "socks":
			if !strings.Contains(res.Content, "socks5://"+id+":") {
				t.Fatalf("socks body missing credential URI: %s", res.Content)
			}
			if !strings.HasSuffix(res.Filename, ".txt") {
				t.Fatalf("socks filename = %q", res.Filename)
			}
		case "mihomo":
			if !strings.Contains(res.Content, "proxy-groups:") || !strings.Contains(res.Content, "MATCH,NGG") {
				t.Fatalf("mihomo body incomplete: %s", res.Content)
			}
		}
	}

	// default format is singbox.
	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+id+"/config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default format status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decodeClientBody[configResult](t, rec).Format; got != "singbox" {
		t.Fatalf("default format = %q, want singbox", got)
	}

	// wireguard config on a socks5 device is refused with a reason.
	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+id+"/config?format=wireguard", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wireguard-on-socks status = %d, body %s", rec.Code, rec.Body)
	}

	// unknown format is a 400, not a 422: the request itself is malformed.
	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+id+"/config?format=openvpn", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown format status = %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "INVALID_ARGUMENT") {
		t.Fatalf("unknown format envelope = %s", rec.Body)
	}

	// ---- a wireguard device gets a real .conf ----------------------------
	rec = doClientReq(t, mux, http.MethodPost, "/api/v1/clients", map[string]any{
		"name": "Phone WG", "platform": "android", "protocol": "wireguard",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create wg status = %d, body %s", rec.Code, rec.Body)
	}
	wg := decodeClientBody[struct {
		Client     clients.ClientView `json:"client"`
		Credential string             `json:"credential"`
	}](t, rec)
	if got := strings.Join(wg.Client.Formats, ","); got != "singbox,mihomo,wireguard" {
		t.Fatalf("wireguard formats = %q", got)
	}
	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+wg.Client.ID+"/config?format=wireguard", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("wireguard config status = %d, body %s", rec.Code, rec.Body)
	}
	wgRes := decodeClientBody[configResult](t, rec)
	if !strings.HasSuffix(wgRes.Filename, ".conf") || !strings.Contains(wgRes.Content, "[Interface]") || !strings.Contains(wgRes.Content, "[Peer]") {
		t.Fatalf("wireguard conf = %q / %s", wgRes.Filename, wgRes.Content)
	}

	// ---- disabled client: config still exportable, subscription dead -----
	rec = doClientReq(t, mux, http.MethodPut, "/api/v1/clients/"+id, map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body %s", rec.Code, rec.Body)
	}
	updated := decodeClientBody[clients.ClientView](t, rec)
	if updated.Enabled {
		t.Fatalf("client still enabled: %+v", updated)
	}
	if _, ok := st.Subscription(created.Credential); ok {
		t.Fatalf("disabled client still served a subscription")
	}
	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+id+"/config?format=socks", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("config for disabled client status = %d (admin export must still work), body %s", rec.Code, rec.Body)
	}

	// ---- rotate ----------------------------------------------------------
	rec = doClientReq(t, mux, http.MethodPost, "/api/v1/clients/"+id+"/rotate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body %s", rec.Code, rec.Body)
	}
	rotated := decodeClientBody[struct {
		Client     clients.ClientView `json:"client"`
		Credential string             `json:"credential"`
	}](t, rec)
	if rotated.Credential == created.Credential || rotated.Credential == "" {
		t.Fatalf("rotate did not change the credential")
	}
	if _, ok := st.Subscription(created.Credential); ok {
		t.Fatalf("old credential still valid after rotate")
	}
	// Re-enable so the new credential can be exercised end to end.
	rec = doClientReq(t, mux, http.MethodPut, "/api/v1/clients/"+id, map[string]any{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d", rec.Code)
	}
	if _, ok := st.Subscription(rotated.Credential); !ok {
		t.Fatalf("rotated credential not served")
	}

	// ---- delete + 404s ---------------------------------------------------
	rec = doClientReq(t, mux, http.MethodDelete, "/api/v1/clients/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", rec.Code, rec.Body)
	}
	if body := decodeClientBody[map[string]bool](t, rec); !body["ok"] {
		t.Fatalf("delete body = %v", body)
	}
	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+id+"/config?format=singbox", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("config of deleted client status = %d, body %s", rec.Code, rec.Body)
	}
	rec = doClientReq(t, mux, http.MethodDelete, "/api/v1/clients/"+id, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete status = %d", rec.Code)
	}
	rec = doClientReq(t, mux, http.MethodPost, "/api/v1/clients/"+id+"/rotate", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rotate missing status = %d", rec.Code)
	}

	// ---- validation failures are 422 and persist nothing ------------------
	before := len(st.List())
	bad := []map[string]any{
		{"name": "", "platform": "ios"},
		{"name": strings.Repeat("n", 65), "platform": "ios"},
		{"name": "x", "platform": "symbian"},
		{"name": "x", "platform": "ios", "protocol": "vless"},
		{"name": "x", "platform": "ios", "expires_at": "2020-01-01T00:00:00Z"},
		{"name": "x", "platform": "ios", "expires_at": "not-a-time"},
	}
	for _, payload := range bad {
		rec = doClientReq(t, mux, http.MethodPost, "/api/v1/clients", payload)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("payload %v status = %d, body %s", payload, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "CONFIG_INVALID") {
			t.Fatalf("payload %v envelope = %s", payload, rec.Body)
		}
	}
	if after := len(st.List()); after != before {
		t.Fatalf("rejected payloads changed the store: %d -> %d", before, after)
	}

	// Malformed JSON is a 400.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clients", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d, body %s", rec.Code, rec.Body)
	}

	// A dotted id cannot escape the registry.
	rec = doClientReq(t, mux, http.MethodGet, "/api/v1/clients/%2E/config?format=singbox", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dot id status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestClientsSubscriptionBase(t *testing.T) {
	st := clients.NewStore(t.TempDir())
	if err := st.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	h := newClientHandlers(st)
	h.SetSubscriptionBase(func() string { return "https://vpn.example.com:9191/" })
	mux := http.NewServeMux()
	h.Register(mux)

	if _, _, err := st.SaveAccess(clients.AccessConfig{PublicHost: "vpn.example.com", PublicPort: 1080, Protocol: "socks5"}); err != nil {
		t.Fatalf("save access: %v", err)
	}
	view, cred, err := st.Create(clients.CreateRequest{Name: "pc", Platform: "windows"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rec := doClientReq(t, mux, http.MethodGet, "/api/v1/clients/"+view.ID+"/config?format=singbox", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("config status = %d, body %s", rec.Code, rec.Body)
	}
	got := decodeClientBody[struct {
		SubscriptionURL string `json:"subscription_url"`
	}](t, rec)
	want := "https://vpn.example.com:9191/sub/" + cred
	if got.SubscriptionURL != want {
		t.Fatalf("subscription_url = %q, want %q", got.SubscriptionURL, want)
	}
}
