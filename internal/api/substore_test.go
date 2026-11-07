package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/hosts"
)

const subStoreDoc = `[
  {"type":"ss","name":"HK-01","server":"127.0.0.1","port":8388,"cipher":"aes-256-gcm","password":"sspass"},
  {"type":"vless","name":"US-01","server":"127.0.0.1","port":443,"uuid":"8f0da0b2-9f4b-4a63-9d1f-0d6a2c9f8b11","tls":true,"servername":"www.bing.com"},
  {"type":"vmess","name":"JP-vmess","server":"127.0.0.1","port":8443,"uuid":"8f0da0b2-9f4b-4a63-9d1f-0d6a2c9f8b11"}
]`

type substoreBody struct {
	Text   string `json:"text"`
	URL    string `json:"url"`
	Role   string `json:"role"`
	DryRun bool   `json:"dry_run"`
}

type substoreParsedHost struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	SvcPort  int    `json:"svc_port"`
	Protocol string `json:"protocol"`
	Role     string `json:"role"`
}

type substoreParsed struct {
	DryRun   bool                 `json:"dry_run"`
	Format   string               `json:"format"`
	Source   string               `json:"source"`
	Parsed   int                  `json:"parsed"`
	Skipped  int                  `json:"skipped"`
	Hosts    []substoreParsedHost `json:"hosts"`
	Failed   []string             `json:"failed"`
	Warnings []string             `json:"warnings"`
}

func postSubStore(t *testing.T, ts *httptest.Server, token, path string, body substoreBody) (*http.Response, string) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return req(t, http.MethodPost, ts.URL+path, token, string(b))
}

func decodeSubStore(t *testing.T, raw string) substoreParsed {
	t.Helper()
	var out substoreParsed
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func TestSubStoreParsePreview(t *testing.T) {
	ts, token := newTestServer(t)

	resp, raw := postSubStore(t, ts, token, "/api/v1/substore/parse", substoreBody{
		Text: subStoreDoc, Role: "inbound",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("parse: %d %s", resp.StatusCode, raw)
	}
	got := decodeSubStore(t, raw)
	if got.Format != "json" || got.Source != "text" || got.Parsed != 3 {
		t.Fatalf("unexpected parse result: %s", raw)
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %d, want 2 (vmess must be rejected): %s", len(got.Hosts), raw)
	}
	// The unsupported entry is named, not silently dropped.
	joined := strings.Join(got.Warnings, " ")
	if !strings.Contains(joined, "vmess") {
		t.Fatalf("warnings must name the unsupported protocol: %v", got.Warnings)
	}
	// A preview must not echo the credentials back.
	if strings.Contains(raw, "sspass") || strings.Contains(raw, "8f0da0b2") {
		t.Fatalf("preview leaked a credential: %s", raw)
	}

	// Nothing was saved by a parse.
	listResp, listBody := req(t, http.MethodGet, ts.URL+"/api/v1/hosts", token, "")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("hosts: %d %s", listResp.StatusCode, listBody)
	}
	if !strings.Contains(listBody, `"count":0`) {
		t.Fatalf("parse must not create hosts: %s", listBody)
	}
}

func TestSubStoreImportCreatesHosts(t *testing.T) {
	ts, token := newTestServer(t)

	// A dry run reports the same preview and writes nothing.
	resp, raw := postSubStore(t, ts, token, "/api/v1/substore/import", substoreBody{
		Text: subStoreDoc, Role: "outbound", DryRun: true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry run: %d %s", resp.StatusCode, raw)
	}
	if got := decodeSubStore(t, raw); !got.DryRun || len(got.Hosts) != 2 {
		t.Fatalf("dry run wrong: %s", raw)
	}
	if _, body := req(t, http.MethodGet, ts.URL+"/api/v1/hosts", token, ""); !strings.Contains(body, `"count":0`) {
		t.Fatalf("dry run must not persist: %s", body)
	}

	// The real import creates one host per importable entry.
	resp, raw = postSubStore(t, ts, token, "/api/v1/substore/import", substoreBody{
		Text: subStoreDoc, Role: "outbound",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", resp.StatusCode, raw)
	}
	got := decodeSubStore(t, raw)
	if len(got.Hosts) != 2 || len(got.Failed) != 0 {
		t.Fatalf("import result wrong: %s", raw)
	}
	byProto := map[string]substoreParsedHost{}
	for _, h := range got.Hosts {
		byProto[h.Protocol] = h
	}
	if ss, ok := byProto["ss"]; !ok || ss.SvcPort != 8388 || ss.Address != "127.0.0.1" || ss.Role != "outbound" {
		t.Fatalf("shadowsocks host wrong: %+v", got.Hosts)
	}

	// The host really is in the registry, with the credential the renderer needs.
	_, body := req(t, http.MethodGet, ts.URL+"/api/v1/hosts", token, "")
	if !strings.Contains(body, `"count":2`) {
		t.Fatalf("registry count: %s", body)
	}
	if !strings.Contains(body, "aes-256-gcm:sspass") {
		t.Fatalf("shadowsocks credential must carry the cipher: %s", body)
	}

	// Importing the same document twice must not collide on ids.
	resp, raw = postSubStore(t, ts, token, "/api/v1/substore/import", substoreBody{Text: subStoreDoc})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second import: %d %s", resp.StatusCode, raw)
	}
	if second := decodeSubStore(t, raw); len(second.Hosts) != 2 || len(second.Failed) != 0 {
		t.Fatalf("second import must reuse ids with a suffix: %s", raw)
	}
	if _, body := req(t, http.MethodGet, ts.URL+"/api/v1/hosts", token, ""); !strings.Contains(body, `"count":4`) {
		t.Fatalf("registry count after second import: %s", body)
	}
}

func TestSubStoreImportRejectsBadInput(t *testing.T) {
	ts, token := newTestServer(t)

	cases := []struct {
		name string
		body substoreBody
		want int
	}{
		{"nothing to read", substoreBody{}, http.StatusBadRequest},
		{"non-http scheme", substoreBody{URL: "file:///etc/passwd"}, http.StatusBadRequest},
		{"garbage", substoreBody{Text: "这不是订阅内容"}, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := postSubStore(t, ts, token, "/api/v1/substore/import", tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("got %d, want %d: %s", resp.StatusCode, tc.want, raw)
			}
		})
	}
}

func TestSubStoreImportFromURL(t *testing.T) {
	ts, token := newTestServer(t)

	// A stub Sub-Store server, so the fetch path is exercised end to end.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.Contains(got, "NGG/") {
			t.Errorf("user agent = %q, want an NGG one", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(subStoreDoc))
	}))
	defer stub.Close()

	resp, raw := postSubStore(t, ts, token, "/api/v1/substore/parse", substoreBody{URL: stub.URL + "/sub/u/x?target=json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("parse from url: %d %s", resp.StatusCode, raw)
	}
	got := decodeSubStore(t, raw)
	if got.Source != "url" || len(got.Hosts) != 2 {
		t.Fatalf("url parse wrong: %s", raw)
	}
}

func TestSubStoreRoleDefaultsToOutbound(t *testing.T) {
	ts, token := newTestServer(t)
	resp, raw := postSubStore(t, ts, token, "/api/v1/substore/import", substoreBody{Text: subStoreDoc, Role: "nonsense"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", resp.StatusCode, raw)
	}
	for _, h := range decodeSubStore(t, raw).Hosts {
		if h.Role != string(hosts.RoleOutbound) {
			t.Fatalf("role = %q, want outbound", h.Role)
		}
	}
}
