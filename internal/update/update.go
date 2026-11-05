// Package update checks the project's GitHub Releases, verifies and stages a
// matching asset, and hands off to a platform script that replaces the running
// binaries. Everything here talks to the real GitHub API; there is no mock
// "update available" state and no silent fallback to stale data.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/version"
)

const defaultAPIBase = "https://api.github.com"

type Status struct {
	Generation      int    `json:"generation"`
	GenerationLabel string `json:"generation_label"`
	GenerationName  string `json:"generation_name"`
	Release         string `json:"release"`
	Repo            string `json:"repo"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	Platform        string `json:"platform"`
	InstallDir      string `json:"install_dir"`
	UpdateDir       string `json:"update_dir"`
	ScriptPath      string `json:"script_path"`
	ScriptReady     bool   `json:"script_ready"`
}

type Asset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	BrowserURL  string `json:"browser_download_url"`
	APIURL      string `json:"url"`
	ContentType string `json:"content_type"`
}

type ReleaseInfo struct {
	Tag         string  `json:"tag_name"`
	Name        string  `json:"name"`
	PublishedAt string  `json:"published_at"`
	Notes       string  `json:"body"`
	HTMLURL     string  `json:"html_url"`
	Prerelease  bool    `json:"prerelease"`
	Draft       bool    `json:"draft"`
	Assets      []Asset `json:"assets"`
}

type CheckResult struct {
	Status          Status       `json:"status"`
	Current         string       `json:"current"`
	Latest          string       `json:"latest"`
	UpdateAvailable bool         `json:"update_available"`
	Release         *ReleaseInfo `json:"release"`
	Asset           *Asset       `json:"asset"`
	CheckedAt       string       `json:"checked_at"`
	Source          string       `json:"source"`
	Note            string       `json:"note,omitempty"`
}

type DownloadResult struct {
	Tag      string `json:"tag"`
	Asset    string `json:"asset"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
	Size     int64  `json:"size"`
}

type ApplyResult struct {
	Started       bool   `json:"started"`
	Tag           string `json:"tag"`
	ScriptPath    string `json:"script_path"`
	LogPath       string `json:"log_path"`
	ManualCommand string `json:"manual_command,omitempty"`
	Note          string `json:"note,omitempty"`
}

type Checker struct {
	mu         sync.Mutex
	DataDir    string
	InstallDir string
	APIBase    string
	Client     *http.Client
	cached     *CheckResult
}

func New(dataDir string) *Checker {
	exe, err := os.Executable()
	installDir := "."
	if err == nil {
		installDir = filepath.Dir(exe)
	}
	return &Checker{
		DataDir:    dataDir,
		InstallDir: installDir,
		APIBase:    defaultAPIBase,
		Client:     &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Checker) Status() Status {
	updateDir := filepath.Join(c.DataDir, "updates")
	script := filepath.Join(c.InstallDir, "scripts", "ngg-update.ps1")
	if runtime.GOOS != "windows" {
		script = filepath.Join(c.InstallDir, "scripts", "ngg-update.sh")
	}
	_, statErr := os.Stat(script)
	return Status{
		Generation:      version.Generation,
		GenerationLabel: version.GenerationLabel,
		GenerationName:  version.GenerationName,
		Release:         version.Release,
		Repo:            version.Repo(),
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
		InstallDir:      c.InstallDir,
		UpdateDir:       updateDir,
		ScriptPath:      script,
		ScriptReady:     statErr == nil,
	}
}

func (c *Checker) token() string {
	for _, k := range []string{"NGG_GITHUB_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func (c *Checker) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NGG-Update-Checker/"+version.Release)
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return c.Client.Do(req)
}

func (c *Checker) fetchRelease(ctx context.Context) (*ReleaseInfo, error) {
	base := strings.TrimRight(c.APIBase, "/")
	repo := version.Repo()
	resp, err := c.do(ctx, base+"/repos/"+repo+"/releases/latest")
	if err != nil {
		return nil, fmt.Errorf("GitHub API unreachable: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		var rel ReleaseInfo
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("parse latest release: %w", err)
		}
		resp.Body.Close()
		return &rel, nil
	}
	resp.Body.Close()

	// No "latest" release (all prereleases, or none yet): list and pick the
	// newest non-draft, preferring a stable release over a prerelease.
	resp, err = c.do(ctx, base+"/repos/"+repo+"/releases?per_page=20")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d (private repo needs NGG_GITHUB_TOKEN)", resp.StatusCode)
	}
	var list []ReleaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, err
	}
	var stable, pre *ReleaseInfo
	for i := range list {
		r := &list[i]
		if r.Draft || r.Tag == "" {
			continue
		}
		if !r.Prerelease && stable == nil {
			stable = r
		}
		if r.Prerelease && pre == nil {
			pre = r
		}
	}
	if stable != nil {
		return stable, nil
	}
	if pre != nil {
		return pre, nil
	}
	return nil, fmt.Errorf("no published release found in %s", repo)
}

// CompareVersions parses vX.Y.Z(-suffix) and returns -1/0/1. Unparsable
// strings fall back to a plain lexical comparison so nothing is faked.
func CompareVersions(a, b string) int {
	parse := func(v string) ([]int, bool) {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			v = v[:i]
		}
		parts := strings.Split(v, ".")
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || len(parts) == 0 {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	}
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := 0, 0
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// PreferredAsset returns the asset matching the running platform, preferring
// the Windows GUI installer over the portable ZIP.
func PreferredAsset(rel *ReleaseInfo, tag, goos, goarch string) *Asset {
	if rel == nil {
		return nil
	}
	candidates := []string{}
	switch goos {
	case "windows":
		candidates = append(candidates,
			"NGG-Setup-"+tag+".exe",
			"NGG-"+tag+"-windows-"+goarch+".zip",
			"ngg-"+tag+"-windows-"+goarch+".tar.gz")
	default:
		candidates = append(candidates,
			"ngg-"+tag+"-"+goos+"-"+goarch+".tar.gz",
			"ngg-"+tag+"-"+goos+"-"+goarch+".zip")
	}
	for _, want := range candidates {
		for i := range rel.Assets {
			if rel.Assets[i].Name == want {
				return &rel.Assets[i]
			}
		}
	}
	return nil
}

func (c *Checker) Check(ctx context.Context) (*CheckResult, error) {
	rel, err := c.fetchRelease(ctx)
	if err != nil {
		return nil, err
	}
	st := c.Status()
	asset := PreferredAsset(rel, rel.Tag, runtime.GOOS, runtime.GOARCH)
	note := ""
	if asset == nil {
		note = "No release asset matches " + st.Platform + "; check the release page manually."
	}
	res := &CheckResult{
		Status:          st,
		Current:         version.Release,
		Latest:          rel.Tag,
		UpdateAvailable: CompareVersions(rel.Tag, version.Release) > 0,
		Release:         rel,
		Asset:           asset,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		Source:          "github-api",
		Note:            note,
	}
	c.mu.Lock()
	c.cached = res
	c.mu.Unlock()
	return res, nil
}

func (c *Checker) Cached() *CheckResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cached
}

func (c *Checker) downloadAsset(ctx context.Context, asset Asset, dest string) (string, error) {
	url := asset.APIURL
	accept := "application/octet-stream"
	if url == "" {
		url = asset.BrowserURL
		accept = "*/*"
	}
	if url == "" {
		return "", fmt.Errorf("asset %s has no download URL", asset.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "NGG-Update-Checker/"+version.Release)
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", asset.Name, resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 512<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("downloaded %s is empty", asset.Name)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Download fetches a release asset and its SHA256SUMS, verifies the digest and
// stages the file under <data>/updates/<tag>/. Verification is mandatory.
func (c *Checker) Download(ctx context.Context, tag, assetName string) (*DownloadResult, error) {
	if tag == "" || assetName == "" {
		return nil, fmt.Errorf("tag and asset are required")
	}
	rel, err := c.fetchRelease(ctx)
	if err != nil {
		return nil, err
	}
	// Only assets published in this repository's release may be downloaded.
	var target *Asset
	var sums *Asset
	for i := range rel.Assets {
		a := &rel.Assets[i]
		if a.Name == assetName {
			target = a
		}
		if a.Name == "SHA256SUMS" {
			sums = a
		}
	}
	if target == nil {
		return nil, fmt.Errorf("asset %q not found in release %s", assetName, rel.Tag)
	}
	if sums == nil {
		return nil, fmt.Errorf("release %s has no SHA256SUMS; refusing to download unverified binaries", rel.Tag)
	}

	dir := filepath.Join(c.DataDir, "updates", tag)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sumPath := filepath.Join(dir, "SHA256SUMS")
	if _, err := c.downloadAsset(ctx, *sums, sumPath); err != nil {
		return nil, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	raw, err := os.ReadFile(sumPath)
	if err != nil {
		return nil, err
	}
	expected := ""
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			name := strings.TrimPrefix(f[1], "*")
			name = strings.TrimPrefix(name, "./")
			if name == assetName {
				expected = strings.ToLower(f[0])
				break
			}
		}
	}
	if expected == "" {
		return nil, fmt.Errorf("SHA256SUMS has no entry for %s", assetName)
	}
	dest := filepath.Join(dir, assetName)
	got, err := c.downloadAsset(ctx, *target, dest)
	if err != nil {
		return nil, err
	}
	if got != expected {
		_ = os.Remove(dest)
		return nil, fmt.Errorf("SHA256 mismatch for %s (got %s, want %s)", assetName, got, expected)
	}
	fi, _ := os.Stat(dest)
	size := int64(0)
	if fi != nil {
		size = fi.Size()
	}
	return &DownloadResult{
		Tag: rel.Tag, Asset: assetName, Path: dest,
		SHA256: got, Verified: true, Size: size,
	}, nil
}

// Apply launches the platform update script detached. The script backs up,
// stops the service, replaces binaries, restarts, health-checks and rolls back
// on failure. On non-Windows the service manager may kill children in its
// cgroup, so an exact manual command is returned instead of pretending success.
func (c *Checker) Apply(ctx context.Context, tag, assetName string) (*ApplyResult, error) {
	st := c.Status()
	stageDir := filepath.Join(c.DataDir, "updates", tag)
	assetPath := filepath.Join(stageDir, assetName)
	if _, err := os.Stat(assetPath); err != nil {
		return nil, fmt.Errorf("staged update not found at %s; run download first", assetPath)
	}
	if _, err := os.Stat(filepath.Join(stageDir, "SHA256SUMS")); err != nil {
		return nil, fmt.Errorf("missing SHA256SUMS in staged update; refusing to apply")
	}
	if !st.ScriptReady {
		return nil, fmt.Errorf("update script missing: %s", st.ScriptPath)
	}
	logPath := filepath.Join(c.DataDir, "updates", "apply-"+tag+".log")
	_ = ctx // the script must outlive this HTTP request; do not bind it to ctx

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Separate argv elements: Go quotes paths containing spaces for us.
		cmd = exec.Command("powershell.exe",
			"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", st.ScriptPath,
			"-StageDir", stageDir, "-Asset", assetName,
			"-InstallDir", st.InstallDir, "-ServiceName", "NGG", "-LogPath", logPath)
	} else {
		manual := fmt.Sprintf("sudo bash %s --stage %s --asset %s --prefix %s --service ngg",
			st.ScriptPath, stageDir, assetName, st.InstallDir)
		return &ApplyResult{
			Started: false, Tag: tag, ScriptPath: st.ScriptPath, LogPath: logPath,
			ManualCommand: manual,
			Note:          "systemd may kill child processes in the service cgroup; run this command from a root shell instead of applying in-place.",
		}, nil
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start update script: %w", err)
	}
	// Detached on purpose: the script must survive this service being stopped.
	go func() { _ = cmd.Wait() }()
	return &ApplyResult{
		Started: true, Tag: tag, ScriptPath: st.ScriptPath, LogPath: logPath,
		Note: "Update started in the background. The service will restart shortly; refresh in ~30 seconds.",
	}, nil
}
