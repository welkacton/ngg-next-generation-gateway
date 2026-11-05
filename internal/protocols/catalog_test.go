package protocols

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// swapCatalogSources points the syncer at a local fake official-doc API so
// the parser is tested for real without depending on network availability.
func swapCatalogSources(t *testing.T, base string) {
	t.Helper()
	old := append([]catalogSource(nil), catalogSources...)
	t.Cleanup(func() { catalogSources = old })
	for i := range catalogSources {
		catalogSources[i].APIURL = base + "/" + catalogSources[i].Kernel
	}
}

func fakeOfficialDocs(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/mihomo"):
			_, _ = w.Write([]byte(`[
				{"name":"vmess.md","type":"file"},
				{"name":"vmess.en.md","type":"file"},
				{"name":"ss.md","type":"file"},
				{"name":"hysteria2.md","type":"file"},
				{"name":"index.md","type":"file"}
			]`))
		case strings.HasSuffix(r.URL.Path, "/sing-box"):
			_, _ = w.Write([]byte(`[
				{"name":"vless.md","type":"file"},
				{"name":"vless.zh.md","type":"file"},
				{"name":"wireguard.md","type":"file"},
				{"name":"index.zh.md","type":"file"}
			]`))
		case strings.HasSuffix(r.URL.Path, "/xray-core"):
			_, _ = w.Write([]byte(`[
				{"name":"trojan.md","type":"file"},
				{"name":"freedom.md","type":"file"},
				{"name":"index.md","type":"file"}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCatalogRefreshParsesOfficialDocs(t *testing.T) {
	ts := fakeOfficialDocs(t)
	defer ts.Close()
	swapCatalogSources(t, ts.URL)

	dir := t.TempDir()
	c := NewCatalogSyncer(dir, time.Hour, nil)
	if !c.Current().Stale {
		t.Fatal("a fresh syncer must be stale until the first successful refresh")
	}
	snap := c.Refresh(context.Background())
	if snap.Source != "official-docs" || snap.Stale {
		t.Fatalf("expected a successful official-docs sync, got %+v", snap)
	}
	if len(snap.Kernels) != 3 {
		t.Fatalf("expected 3 kernels, got %d", len(snap.Kernels))
	}
	want := map[string][]string{
		"mihomo":    {"Hysteria2", "Shadowsocks (SS)", "VMess"},
		"sing-box":  {"VLESS", "WireGuard"},
		"xray-core": {"Freedom", "Trojan"},
	}
	for _, k := range snap.Kernels {
		var names []string
		for _, p := range k.Protocols {
			names = append(names, p.Name)
			if !strings.HasPrefix(p.DocURL, "https://") {
				t.Errorf("%s/%s has no official doc URL", k.Kernel, p.Slug)
			}
		}
		exp := strings.Join(want[k.Kernel], ", ")
		if strings.Join(names, ", ") != exp {
			t.Errorf("%s: got [%s], want [%s] (translations/index must be filtered)", k.Kernel, strings.Join(names, ", "), exp)
		}
		if k.Count != len(k.Protocols) {
			t.Errorf("%s count mismatch", k.Kernel)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "protocol-catalog.json")); err != nil {
		t.Fatalf("snapshot must be cached to disk: %v", err)
	}

	// Reopening loads the cache and marks it stale until the next sync.
	c2 := NewCatalogSyncer(dir, time.Hour, nil)
	if c2.Current().Source != "cache" || len(c2.Current().Kernels[0].Protocols) == 0 {
		t.Fatalf("cache was not loaded: %+v", c2.Current())
	}
}

func TestCatalogKeepsLastGoodDataWhenOneKernelFails(t *testing.T) {
	ts := fakeOfficialDocs(t)
	defer ts.Close()
	swapCatalogSources(t, ts.URL)

	c := NewCatalogSyncer(t.TempDir(), time.Hour, nil)
	first := c.Refresh(context.Background())
	goodSingBox := first.Kernels[1]

	// Break only sing-box; the other two still succeed.
	catalogSources[1].APIURL = ts.URL + "/broken"
	c.lastTry = time.Time{} // bypass the 30s refresh throttle inside the test
	second := c.Refresh(context.Background())

	if second.Source != "official-docs" || !second.Stale {
		t.Fatalf("partial failure must be reported as stale: %+v", second)
	}
	if second.Kernels[1].Error == "" {
		t.Fatal("the failing kernel must carry an error")
	}
	if !second.Kernels[1].FromCache {
		t.Fatal("the failing kernel must be marked as cached data")
	}
	if len(second.Kernels[1].Protocols) != len(goodSingBox.Protocols) {
		t.Fatal("the failing kernel must keep its last good protocol list")
	}
	if second.Kernels[0].Error != "" || second.Kernels[2].Error != "" {
		t.Fatal("successful kernels must not be marked as errors")
	}
}

func TestCatalogBaselineIsHonestWhenEverythingFails(t *testing.T) {
	ts := fakeOfficialDocs(t)
	ts.Close() // nothing is reachable
	swapCatalogSources(t, ts.URL)
	// Use a client pointing at a closed port with a tiny timeout.
	c := NewCatalogSyncer(t.TempDir(), time.Hour, nil)
	snap := c.Refresh(context.Background())
	if snap.Source != "builtin" || !snap.Stale {
		t.Fatalf("total failure must fall back to a labelled baseline: %+v", snap)
	}
	for _, k := range snap.Kernels {
		if k.Error == "" || len(k.Protocols) == 0 || !k.FromCache {
			t.Fatalf("baseline kernel %s must be labelled and non-empty: %+v", k.Kernel, k)
		}
	}
}

func TestCatalogDisplayName(t *testing.T) {
	for slug, want := range map[string]string{
		"vmess": "VMess", "ss": "Shadowsocks (SS)", "dialer-proxy": "Dialer Proxy",
		"anything-new": "Anything New",
	} {
		if got := catalogDisplayName(slug); got != want {
			t.Errorf("%s: got %q want %q", slug, got, want)
		}
	}
}
