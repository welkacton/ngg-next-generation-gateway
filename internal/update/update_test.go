package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v2.0.0", "2.0.0", 0},
		{"v2.1.0", "v2.0.9", 1},
		{"v1.9.9", "v2.0.0", -1},
		{"v3.0.0-rc1", "v2.9.9", 1},
		{"nonsense", "v2.0.0", -1}, // unparsable -> lexical fallback
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPreferredAsset(t *testing.T) {
	rel := &ReleaseInfo{Tag: "v9.9.9", Assets: []Asset{
		{Name: "ngg-v9.9.9-linux-amd64.tar.gz"},
		{Name: "NGG-v9.9.9-windows-amd64.zip"},
		{Name: "NGG-Setup-v9.9.9.exe"},
	}}
	if a := PreferredAsset(rel, "v9.9.9", "windows", "amd64"); a == nil || a.Name != "NGG-Setup-v9.9.9.exe" {
		t.Fatalf("windows preferred asset = %+v", a)
	}
	if a := PreferredAsset(rel, "v9.9.9", "linux", "amd64"); a == nil || a.Name != "ngg-v9.9.9-linux-amd64.tar.gz" {
		t.Fatalf("linux preferred asset = %+v", a)
	}
	if a := PreferredAsset(rel, "v9.9.9", "darwin", "arm64"); a != nil {
		t.Fatalf("unexpected darwin asset: %+v", a)
	}
}

// A real local HTTP server stands in for api.github.com. No "update
// available" boolean is fabricated: it is derived from the served tag.
// platformAsset mirrors PreferredAsset's naming so the same fake release
// works on Windows (CI windows-scripts job) and Linux (CI backend job).
func platformAsset(tag string) string {
	if runtime.GOOS == "windows" {
		return "NGG-Setup-" + tag + ".exe"
	}
	return "ngg-" + tag + "-linux-amd64.tar.gz"
}

func newFakeGitHub(t *testing.T, exe []byte, tag, sumsOverride string) *httptest.Server {
	t.Helper()
	h := sha256.Sum256(exe)
	digest := hex.EncodeToString(h[:])
	names := []string{
		"NGG-Setup-" + tag + ".exe",
		"ngg-" + tag + "-linux-amd64.tar.gz",
		"NGG-" + tag + "-windows-amd64.zip",
	}
	sumLine := ""
	for _, n := range names {
		sumLine += fmt.Sprintf("%s  %s\n", digest, n)
	}
	if sumsOverride != "" {
		sumLine = sumsOverride
	}
	var ts *httptest.Server
	mux := http.NewServeMux()
	for _, p := range []string{"/assets/exe", "/assets/linux", "/assets/zip"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(exe)
		})
	}
	mux.HandleFunc("/assets/sums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sumLine))
	})
	mux.HandleFunc("/repos/welkacton/ngg-next-generation-gateway/releases/latest",
		func(w http.ResponseWriter, _ *http.Request) {
			body := fmt.Sprintf(`{
				"tag_name": %q, "name": %q, "published_at": "2026-11-05T00:00:00Z",
				"body": "release notes", "html_url": "https://example.invalid/release",
				"prerelease": false, "draft": false,
				"assets": [
					{"name": "NGG-Setup-%s.exe", "size": %d, "url": %q, "browser_download_url": %q},
					{"name": "ngg-%s-linux-amd64.tar.gz", "size": %d, "url": %q, "browser_download_url": %q},
					{"name": "NGG-%s-windows-amd64.zip", "size": %d, "url": %q, "browser_download_url": %q},
					{"name": "SHA256SUMS", "size": %d, "url": %q, "browser_download_url": %q}
				]}`, tag, tag, tag, len(exe), ts.URL+"/assets/exe", ts.URL+"/assets/exe",
				tag, len(exe), ts.URL+"/assets/linux", ts.URL+"/assets/linux",
				tag, len(exe), ts.URL+"/assets/zip", ts.URL+"/assets/zip",
				len(sumLine), ts.URL+"/assets/sums", ts.URL+"/assets/sums")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
	ts = httptest.NewServer(mux)
	return ts
}

func TestCheckAndDownloadWithRealHashVerification(t *testing.T) {
	exe := []byte("NGG-FAKE-BINARY-FOR-TEST-ONLY")
	ts := newFakeGitHub(t, exe, "v9.9.9", "")
	defer ts.Close()

	c := New(t.TempDir())
	c.APIBase = ts.URL
	st := c.Status()
	if st.Repo != "welkacton/ngg-next-generation-gateway" || st.Generation != 2 {
		t.Fatalf("unexpected status: %+v", st)
	}

	res, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Latest != "v9.9.9" || !res.UpdateAvailable || res.Asset == nil {
		t.Fatalf("unexpected check result: %+v", res)
	}
	wantAsset := platformAsset("v9.9.9")
	if res.Asset.Name != wantAsset {
		t.Fatalf("wrong asset for %s: %+v", runtime.GOOS, res.Asset)
	}
	if c.Cached() == nil {
		t.Fatal("check result should be cached")
	}

	dl, err := c.Download(context.Background(), "v9.9.9", wantAsset)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !dl.Verified || dl.SHA256 != fmt.Sprintf("%x", sha256.Sum256(exe)) {
		t.Fatalf("download not verified: %+v", dl)
	}
	got, err := os.ReadFile(dl.Path)
	if err != nil || string(got) != string(exe) {
		t.Fatalf("staged bytes mismatch: %v", err)
	}
}

func TestDownloadRefusesBadChecksum(t *testing.T) {
	exe := []byte("REAL-BYTES")
	// Serve a wrong digest for every platform asset while keeping real bytes.
	wrong := ""
	for _, n := range []string{
		"NGG-Setup-v9.9.9.exe", "ngg-v9.9.9-linux-amd64.tar.gz", "NGG-v9.9.9-windows-amd64.zip",
	} {
		wrong += "0000000000000000000000000000000000000000000000000000000000000000  " + n + "\n"
	}
	ts := newFakeGitHub(t, exe, "v9.9.9", wrong)
	defer ts.Close()

	c := New(t.TempDir())
	c.APIBase = ts.URL
	_, err := c.Download(context.Background(), "v9.9.9", platformAsset("v9.9.9"))
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected SHA256 mismatch error, got %v", err)
	}
}

func TestDownloadRefusesMissingChecksumFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","assets":[{"name":"only.exe","url":"http://127.0.0.1/only"}]}`))
	}))
	defer ts.Close()
	c := New(t.TempDir())
	c.APIBase = ts.URL
	_, err := c.Download(context.Background(), "v9.9.9", "only.exe")
	if err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Fatalf("expected missing-checksum refusal, got %v", err)
	}
}

func TestApplyRequiresStagedAndVerifiedUpdate(t *testing.T) {
	c := New(t.TempDir())
	if _, err := c.Apply(context.Background(), "v9.9.9", "a.exe"); err == nil {
		t.Fatal("apply without a staged download must fail")
	}
	// Stage a fake asset + checksum just to reach the script-readiness gate.
	dir := filepath.Join(c.DataDir, "updates", "v9.9.9")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.exe"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("x  a.exe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := c.Apply(context.Background(), "v9.9.9", "a.exe")
	if err == nil {
		t.Fatal("apply must fail when the platform update script is missing")
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(err.Error(), "update script missing") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
