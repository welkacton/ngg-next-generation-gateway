package mobile

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMobileCoreCallsRealHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t0ken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/network/status":
			_, _ = w.Write([]byte(`{"running":true,"ingress":{"address":"127.0.0.1:18080"}}`))
		case "/api/v1/cores/mihomo/start":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	g := New(srv.URL, "t0ken")
	if !g.Connectable() {
		t.Fatal("health check failed")
	}
	addr, err := g.ProxyURL()
	if err != nil || addr != "127.0.0.1:18080" {
		t.Fatalf("ProxyURL %q err %v", addr, err)
	}
	out, err := g.StartCore("mihomo")
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("StartCore %q err %v", out, err)
	}
	bad := New(srv.URL, "wrong")
	if bad.Connectable() {
		t.Fatal("wrong token must not report connected")
	}
}
