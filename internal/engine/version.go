package engine

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

const githubAPIBase = "https://api.github.com"

var sha256HexRE = regexp.MustCompile(`(?i)\b([a-f0-9]{64})\b`)

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

// versionManager installs, verifies, upgrades and rolls back native kernels.
// Every installed byte is checked against the sha256 digest published by the
// official GitHub Release Assets API; version is confirmed by executing the
// downloaded binary itself. No version is ever assumed from a filename alone.
type versionManager struct {
	registry *core.Registry
	store    interface {
		RecordInstall(contracts.CoreID, string, string, string) error
		ClearInstall(contracts.CoreID) error
		Installed(contracts.CoreID) (string, string, bool, error)
	}
	root     string
	platform contracts.Platform
	client   *http.Client
	bus      core.EventBus
}

var _ core.VersionManager = (*versionManager)(nil)

func newVersionManager(reg *core.Registry, root string, p contracts.Platform, client *http.Client, bus core.EventBus, record interface {
	RecordInstall(contracts.CoreID, string, string, string) error
	ClearInstall(contracts.CoreID) error
	Installed(contracts.CoreID) (string, string, bool, error)
}) *versionManager {
	// Absolute paths are mandatory: Supervisor sets cmd.Dir to the core
	// directory, so a relative binary/config path would resolve twice.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &versionManager{registry: reg, root: root, platform: p, client: client, bus: bus, store: record}
}

func (v *versionManager) coreDir(id contracts.CoreID) string {
	return filepath.Join(v.root, "cores", string(id))
}

func (v *versionManager) BinaryPath(id contracts.CoreID) string {
	a, err := v.registry.Get(id)
	if err != nil {
		return ""
	}
	return filepath.Join(v.coreDir(id), "bin", a.BinaryName(v.platform.OS))
}

func (v *versionManager) IsInstalled(id contracts.CoreID) bool {
	p := v.BinaryPath(id)
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (v *versionManager) InstalledVersion(ctx context.Context, id contracts.CoreID) (contracts.VersionInfo, error) {
	a, err := v.registry.Get(id)
	if err != nil {
		return contracts.VersionInfo{}, err
	}
	bin := v.BinaryPath(id)
	if _, err := os.Stat(bin); err != nil {
		return contracts.VersionInfo{}, fmt.Errorf("kernel %q is not installed", id)
	}
	cmd := exec.CommandContext(ctx, bin, a.VersionArgs()...)
	cmd.Dir = filepath.Dir(bin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return contracts.VersionInfo{}, fmt.Errorf("execute %s %v: %v: %s", bin, a.VersionArgs(), err, truncate(string(out), 400))
	}
	ver, err := a.ParseVersion(string(out))
	if err != nil {
		return contracts.VersionInfo{}, fmt.Errorf("parse %q version: %w", id, err)
	}
	return contracts.VersionInfo{Version: ver, Raw: strings.TrimSpace(string(out)), Channel: "installed", Detected: time.Now().UTC()}, nil
}

func (v *versionManager) AvailableVersions(ctx context.Context, id contracts.CoreID) ([]contracts.VersionInfo, error) {
	a, err := v.registry.Get(id)
	if err != nil {
		return nil, err
	}
	repo := a.Release().Repository
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/releases?per_page=25", githubAPIBase, repo), nil)
	if err != nil {
		return nil, err
	}
	var releases []ghRelease
	if err := v.doJSON(req, &releases); err != nil {
		return nil, fmt.Errorf("query releases of %s: %w", repo, err)
	}
	out := make([]contracts.VersionInfo, 0, len(releases))
	for _, r := range releases {
		when, _ := time.Parse(time.RFC3339, r.PublishedAt)
		asset, _, _ := a.AssetNames(v.platform, r.TagName)
		info := contracts.VersionInfo{
			Version: r.TagName, Channel: "available", Detected: when,
		}
		for _, ga := range r.Assets {
			if ga.Name == asset {
				info.AssetURL = ga.BrowserDownloadURL
				info.DigestURL = fmt.Sprintf("%s/repos/%s/releases/tags/%s (asset digest)", githubAPIBase, repo, url.PathEscape(r.TagName))
				break
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (v *versionManager) Install(ctx context.Context, id contracts.CoreID, version string, progress func(string)) (contracts.VersionInfo, error) {
	a, err := v.registry.Get(id)
	if err != nil {
		return contracts.VersionInfo{}, err
	}
	spec := a.Release()
	if !strings.Contains(spec.Repository, "/") {
		return contracts.VersionInfo{}, fmt.Errorf("kernel %q has no valid release repository", id)
	}
	report := func(msg string) {
		if progress != nil {
			progress(msg)
		}
		v.bus.Publish(contracts.Event{Type: "core.install", CoreID: id, Payload: map[string]any{"message": msg, "version": version}})
	}

	report("查询官方 Release 元数据")
	rel, err := v.fetchRelease(ctx, spec.Repository, version)
	if err != nil {
		return contracts.VersionInfo{}, err
	}
	version = rel.TagName

	assetName, digestName, err := a.AssetNames(v.platform, version)
	if err != nil {
		return contracts.VersionInfo{}, err
	}
	var asset *ghAsset
	for i := range rel.Assets {
		if rel.Assets[i].Name == assetName {
			asset = &rel.Assets[i]
			break
		}
	}
	if asset == nil {
		return contracts.VersionInfo{}, fmt.Errorf("release %s has no asset %q for %s", version, assetName, v.platform.String())
	}
	expected := normalizeDigest(asset.Digest)
	if expected == "" {
		// Fail-Closed: never install an unverifiable binary.
		if digestName == "" {
			return contracts.VersionInfo{}, fmt.Errorf("refusing install: upstream published no sha256 digest for %q", assetName)
		}
		expected, err = v.digestFromAsset(ctx, spec.Repository, version, digestName, assetName)
		if err != nil {
			return contracts.VersionInfo{}, fmt.Errorf("refusing install: %w", err)
		}
	}
	if err := assertTrustedDownloadURL(asset.BrowserDownloadURL); err != nil {
		return contracts.VersionInfo{}, fmt.Errorf("refusing install: %w", err)
	}

	tmp, err := os.MkdirTemp("", "ngg-install-*")
	if err != nil {
		return contracts.VersionInfo{}, err
	}
	defer os.RemoveAll(tmp)
	archivePath := filepath.Join(tmp, assetName)

	report(fmt.Sprintf("下载 %s (%d bytes)", assetName, asset.Size))
	got, err := v.downloadSHA256(ctx, asset.BrowserDownloadURL, archivePath, asset.Size)
	if err != nil {
		return contracts.VersionInfo{}, fmt.Errorf("download %s: %w", assetName, err)
	}
	if !strings.EqualFold(got, expected) {
		return contracts.VersionInfo{}, fmt.Errorf("SHA256 校验失败: 期望 %s, 实际 %s (拒绝安装)", expected, got)
	}
	report("SHA256 摘要校验通过")

	// Optional independent upstream digest file (Xray .dgst) is a second check.
	if digestName != "" && strings.Contains(spec.DigestFormat, "dgst") {
		if upstream, err := v.digestFromAsset(ctx, spec.Repository, version, digestName, assetName); err == nil && upstream != "" {
			if !strings.EqualFold(upstream, got) {
				return contracts.VersionInfo{}, fmt.Errorf("上游 .dgst 摘要与 API 摘要不一致，拒绝安装")
			}
			report("上游 .dgst 二次校验通过")
		}
	}

	report("解包并定位可执行文件")
	binary, err := extractBinary(archivePath, spec, tmp, a.BinaryName(v.platform.OS), v.platform.OS)
	if err != nil {
		return contracts.VersionInfo{}, err
	}

	report("执行原生二进制验证真实版本")
	cmd := exec.CommandContext(ctx, binary, a.VersionArgs()...)
	cmd.Dir = filepath.Dir(binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return contracts.VersionInfo{}, fmt.Errorf("downloaded binary failed version check: %v: %s", err, truncate(string(out), 300))
	}
	detected, err := a.ParseVersion(string(out))
	if err != nil {
		return contracts.VersionInfo{}, fmt.Errorf("downloaded binary version unparsable: %w", err)
	}
	if !versionMatches(version, detected) {
		return contracts.VersionInfo{}, fmt.Errorf("版本校验失败: 请求 %s, 二进制报告 %s (拒绝安装)", version, detected)
	}
	report(fmt.Sprintf("版本校验通过: %s", detected))

	verDir := filepath.Join(v.coreDir(id), "versions", sanitize(version))
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		return contracts.VersionInfo{}, err
	}
	staged := filepath.Join(verDir, a.BinaryName(v.platform.OS))
	if err := copyFileExec(binary, staged); err != nil {
		return contracts.VersionInfo{}, err
	}

	binDir := filepath.Join(v.coreDir(id), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return contracts.VersionInfo{}, err
	}
	active := filepath.Join(binDir, a.BinaryName(v.platform.OS))
	if _, err := os.Stat(active); err == nil {
		_ = copyFileExec(active, active+".previous")
	}
	if err := atomicCopy(staged, active); err != nil {
		return contracts.VersionInfo{}, err
	}
	if err := v.store.RecordInstall(id, detected, active, got); err != nil {
		return contracts.VersionInfo{}, err
	}
	report(fmt.Sprintf("安装完成: %s", detected))
	return contracts.VersionInfo{Version: detected, Raw: strings.TrimSpace(string(out)), Channel: "installed", Detected: time.Now().UTC()}, nil
}

func (v *versionManager) Rollback(ctx context.Context, id contracts.CoreID, version string) error {
	a, err := v.registry.Get(id)
	if err != nil {
		return err
	}
	src := filepath.Join(v.coreDir(id), "versions", sanitize(version), a.BinaryName(v.platform.OS))
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("no stored binary for version %q of %q (available: %v)", version, id, v.storedVersions(id))
	}
	dst := v.BinaryPath(id)
	if err := atomicCopy(src, dst); err != nil {
		return err
	}
	info, err := v.InstalledVersion(ctx, id)
	if err != nil {
		return fmt.Errorf("rollback verification failed: %w", err)
	}
	return v.store.RecordInstall(id, info.Version, dst, "")
}

func (v *versionManager) storedVersions(id contracts.CoreID) []string {
	dir := filepath.Join(v.coreDir(id), "versions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func (v *versionManager) Uninstall(_ context.Context, id contracts.CoreID) error {
	if v.bus != nil {
		v.bus.Publish(contracts.Event{Type: "system.alert", CoreID: id, Payload: map[string]any{"message": "kernel uninstalled"}})
	}
	if err := os.RemoveAll(filepath.Join(v.coreDir(id), "bin")); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(v.coreDir(id), "versions")); err != nil {
		return err
	}
	return v.store.ClearInstall(id)
}

func (v *versionManager) fetchRelease(ctx context.Context, repo, tag string) (ghRelease, error) {
	var rel ghRelease
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBase, repo)
	if tag != "" {
		endpoint = fmt.Sprintf("%s/repos/%s/releases/tags/%s", githubAPIBase, repo, url.PathEscape(tag))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return rel, err
	}
	if err := v.doJSON(req, &rel); err != nil {
		return rel, fmt.Errorf("fetch release %s: %w", endpoint, err)
	}
	if rel.TagName == "" {
		return rel, fmt.Errorf("release %s returned no tag", endpoint)
	}
	return rel, nil
}

func (v *versionManager) digestFromAsset(ctx context.Context, repo, tag, digestName, assetName string) (string, error) {
	rel, err := v.fetchRelease(ctx, repo, tag)
	if err != nil {
		return "", err
	}
	for _, a := range rel.Assets {
		if a.Name != digestName {
			continue
		}
		if err := assertTrustedDownloadURL(a.BrowserDownloadURL); err != nil {
			return "", err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.BrowserDownloadURL, nil)
		resp, err := v.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("digest asset HTTP %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return "", err
		}
		m := sha256HexRE.Find(b)
		if m == nil {
			return "", fmt.Errorf("no sha256 found in %s", digestName)
		}
		return strings.ToLower(string(m)), nil
	}
	return "", fmt.Errorf("digest asset %q not found in release %s", digestName, tag)
}

func (v *versionManager) downloadSHA256(ctx context.Context, rawURL, dest string, expectedSize int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if expectedSize > 0 && n != expectedSize {
		return "", fmt.Errorf("size mismatch: expected %d got %d", expectedSize, n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (v *versionManager) doJSON(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NGG-Gateway/1.0")
	// Anonymous GitHub API allows only 60 requests/hour. A PAT raises that to
	// 5000/hour and also lets private/mirrored installs work. Never logged.
	if t := githubToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("GitHub API 限流 (HTTP %d)：匿名配额 60 次/小时已用完。请在服务端设置 NGG_GITHUB_TOKEN 或 GITHUB_TOKEN 后重试，或稍后再试", resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

// githubToken reads a PAT from the environment on every call so that changing
// it does not require recompiling or embedding credentials in the binary.
func githubToken() string {
	for _, k := range []string{"NGG_GITHUB_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// assertTrustedDownloadURL is the "source verification" leg: only official
// GitHub release delivery hosts are accepted, over HTTPS.
func assertTrustedDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return fmt.Errorf("untrusted download URL %q", raw)
	}
	host := strings.ToLower(u.Hostname())
	allowed := host == "github.com" || host == "objects.githubusercontent.com" ||
		host == "release-assets.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
	if !allowed {
		return fmt.Errorf("untrusted download host %q", host)
	}
	return nil
}

func normalizeDigest(d string) string {
	if i := strings.IndexByte(d, ':'); i >= 0 {
		d = d[i+1:]
	}
	d = strings.TrimSpace(strings.ToLower(d))
	if len(d) == 64 {
		if _, err := hex.DecodeString(d); err == nil {
			return d
		}
	}
	return ""
}

func versionMatches(requested, detected string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
		if i := strings.IndexAny(s, "-+ "); i >= 0 {
			s = s[:i]
		}
		return s
	}
	r, d := norm(requested), norm(detected)
	if r == "" || d == "" {
		return false
	}
	return r == d || strings.HasPrefix(d, r+"-") || strings.HasPrefix(r, d+"-")
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// extractBinary handles zip / single gzip / tar.gz / raw binaries.
func extractBinary(archivePath string, spec core.ReleaseSpec, workDir, binaryName, goos string) (string, error) {
	kind := strings.ToLower(spec.Archive)
	lower := strings.ToLower(archivePath)
	if kind == "" || kind == "auto" {
		switch {
		case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
			kind = "tar.gz"
		case strings.HasSuffix(lower, ".zip"):
			kind = "zip"
		case strings.HasSuffix(lower, ".gz"):
			kind = "gz"
		default:
			kind = "none"
		}
	}
	pattern := spec.BinaryInArchive
	if pattern == "" {
		pattern = "*"
	}
	dest := filepath.Join(workDir, "extracted", binaryName)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	match := func(name string) bool {
		base := filepath.Base(name)
		if strings.EqualFold(base, binaryName) {
			return true
		}
		ok, _ := filepath.Match(pattern, base)
		if !ok {
			return false
		}
		if goos == "windows" {
			return strings.EqualFold(filepath.Ext(base), ".exe")
		}
		return filepath.Ext(base) == "" || strings.EqualFold(filepath.Ext(base), ".bin")
	}

	switch kind {
	case "zip":
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return "", err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || !match(f.Name) {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			err = writeReader(dest, rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			return dest, nil
		}
		return "", fmt.Errorf("no binary matching %q inside zip %s", pattern, filepath.Base(archivePath))
	case "gz":
		f, err := os.Open(archivePath)
		if err != nil {
			return "", err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", err
		}
		defer gz.Close()
		if err := writeReader(dest, gz); err != nil {
			return "", err
		}
		return dest, nil
	case "tar.gz":
		f, err := os.Open(archivePath)
		if err != nil {
			return "", err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if hdr.Typeflag != tar.TypeReg || !match(hdr.Name) {
				continue
			}
			if err := writeReader(dest, tr); err != nil {
				return "", err
			}
			return dest, nil
		}
		return "", fmt.Errorf("no binary matching %q inside tar.gz %s", pattern, filepath.Base(archivePath))
	default:
		if err := copyFileExec(archivePath, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
}

func writeReader(dest string, r io.Reader) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Chmod(dest, 0o755)
}

func copyFileExec(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeReader(dst, in)
}

func atomicCopy(src, dst string) error {
	tmp := dst + ".tmp"
	if err := copyFileExec(src, tmp); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmp, 0o755)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
