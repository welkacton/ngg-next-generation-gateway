package protocols

// Catalog is the read-only "what protocols does each kernel support" directory.
//
// It is fetched from the OFFICIAL documentation repositories that back each
// kernel's website (every doc page links "Edit this page on GitHub" to these
// repos). Results are cached to disk, refreshed on a timer, and never faked:
// when the network fails we keep the last good snapshot and mark it stale, or
// fall back to a clearly-labelled built-in baseline.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CatalogProtocol is one protocol name. Text only — no editing, no toggles.
type CatalogProtocol struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	DocURL string `json:"doc_url"`
}

// CatalogKernel is the protocol list of one kernel plus honest sync metadata.
type CatalogKernel struct {
	Kernel    string            `json:"kernel"`
	Label     string            `json:"label"`
	SiteURL   string            `json:"site_url"`
	SourceURL string            `json:"source_url"`
	Protocols []CatalogProtocol `json:"protocols"`
	Count     int               `json:"count"`
	SyncedAt  string            `json:"synced_at"`
	FromCache bool              `json:"from_cache"`
	Error     string            `json:"error,omitempty"`
}

// Catalog is the payload returned by GET /protocols/catalog.
type Catalog struct {
	Kernels         []CatalogKernel `json:"kernels"`
	SyncedAt        string          `json:"synced_at"`
	NextSyncAt      string          `json:"next_sync_at"`
	IntervalMinutes int             `json:"interval_minutes"`
	Source          string          `json:"source"` // official-docs | cache | builtin
	Stale           bool            `json:"stale"`
	Message         string          `json:"message"`
}

// catalogSource describes one official documentation source.
type catalogSource struct {
	Kernel    string
	Label     string
	SiteURL   string
	APIURL    string
	DocPrefix string
	Filter    func(name string) bool
}

var catalogSources = []catalogSource{
	{
		Kernel:    "mihomo",
		Label:     "Mihomo",
		SiteURL:   "https://wiki.metacubex.one/config/proxies/",
		APIURL:    "https://api.github.com/repos/MetaCubeX/Meta-Docs/contents/docs/config/proxies",
		DocPrefix: "https://wiki.metacubex.one/config/proxies/%s/",
		// The repo carries .en.md / .ru.md translations; keep the Chinese
		// canonical file only, and drop the index page.
		Filter: func(n string) bool {
			return strings.HasSuffix(n, ".md") &&
				!strings.Contains(n, ".en.md") && !strings.Contains(n, ".ru.md") &&
				!strings.HasPrefix(n, "index.")
		},
	},
	{
		Kernel:    "sing-box",
		Label:     "sing-box",
		SiteURL:   "https://sing-box.sagernet.org/configuration/outbound/",
		APIURL:    "https://api.github.com/repos/SagerNet/sing-box/contents/docs/configuration/outbound",
		DocPrefix: "https://sing-box.sagernet.org/configuration/outbound/%s/",
		Filter: func(n string) bool {
			return strings.HasSuffix(n, ".md") && !strings.Contains(n, ".zh.md") &&
				!strings.HasPrefix(n, "index.")
		},
	},
	{
		Kernel:    "xray-core",
		Label:     "Xray-core",
		SiteURL:   "https://xtls.github.io/config/outbounds/",
		APIURL:    "https://api.github.com/repos/XTLS/Xray-docs-next/contents/docs/config/outbounds",
		DocPrefix: "https://xtls.github.io/config/outbounds/%s.html",
		Filter: func(n string) bool {
			return strings.HasSuffix(n, ".md") && !strings.HasPrefix(n, "index.")
		},
	},
}

// Friendly display names for slugs that are abbreviations or have unusual
// capitalisation. Anything not listed gets a readable title-cased name.
var catalogNames = map[string]string{
	"ss": "Shadowsocks (SS)", "ssr": "ShadowsocksR (SSR)",
	"wg": "WireGuard", "vmess": "VMess", "vless": "VLESS",
	"tuic": "TUIC", "hysteria2": "Hysteria2", "hysteria": "Hysteria",
	"http": "HTTP", "socks": "SOCKS", "socks5": "SOCKS5",
	"ssh": "SSH", "dns": "DNS", "tls": "TLS", "tor": "Tor",
	"anytls": "AnyTLS", "mieru": "Mieru", "snell": "Snell",
	"shadowtls": "ShadowTLS", "shadowquic": "ShadowQuic", "masque": "MASQUE",
	"naive": "NaiveProxy", "openvpn": "OpenVPN", "wireguard": "WireGuard",
	"easytier": "EasyTier", "tailscale": "Tailscale", "zerotier": "ZeroTier",
	"trusttunnel": "TrustTunnel", "sudoku": "Sudoku", "rematch": "ReMatch",
	"built-in": "Built-in", "dialer-proxy": "Dialer Proxy",
	"freedom": "Freedom", "blackhole": "Blackhole", "loopback": "Loopback",
	"selector": "Selector", "urltest": "URLTest", "block": "Block",
	"direct": "DIRECT", "bridge": "Bridge", "tailcat": "Tailcat",
	"transport": "Transport", "trojan": "Trojan", "v2ray": "V2Ray",
}

func catalogDisplayName(slug string) string {
	if n, ok := catalogNames[slug]; ok {
		return n
	}
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// Baseline is used only when both the network and the on-disk cache are
// unavailable. It is explicitly labelled "builtin" in the API response.
var catalogBaseline = map[string][]string{
	"mihomo": {"anytls", "direct", "dns", "hysteria", "hysteria2", "http", "mieru",
		"openvpn", "shadowquic", "shadowsocks", "snell", "socks", "ssh", "ssr",
		"trojan", "tuic", "vless", "vmess", "wg", "wireguard"},
	"sing-box": {"anytls", "direct", "http", "hysteria", "hysteria2", "naive",
		"selector", "shadowsocks", "shadowtls", "snell", "socks", "ssh", "tor",
		"trojan", "tuic", "urltest", "vless", "vmess", "wireguard"},
	"xray-core": {"blackhole", "dns", "freedom", "http", "hysteria", "loopback",
		"shadowsocks", "socks", "trojan", "vless", "vmess", "wireguard"},
}

func baselineKernels() []CatalogKernel {
	out := make([]CatalogKernel, 0, len(catalogSources))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, src := range catalogSources {
		k := CatalogKernel{
			Kernel: src.Kernel, Label: src.Label, SiteURL: src.SiteURL,
			SourceURL: src.APIURL, SyncedAt: now, FromCache: false,
			Protocols: make([]CatalogProtocol, 0, len(catalogBaseline[src.Kernel])),
		}
		for _, slug := range catalogBaseline[src.Kernel] {
			k.Protocols = append(k.Protocols, CatalogProtocol{
				Slug: slug, Name: catalogDisplayName(slug),
				DocURL: fmt.Sprintf(src.DocPrefix, slug),
			})
		}
		sort.Slice(k.Protocols, func(i, j int) bool { return k.Protocols[i].Name < k.Protocols[j].Name })
		k.Count = len(k.Protocols)
		out = append(out, k)
	}
	return out
}

// CatalogSyncer keeps the directory fresh. It is safe for concurrent use.
type CatalogSyncer struct {
	path     string
	interval time.Duration
	client   *http.Client
	tokenFn  func() string

	mu         sync.RWMutex
	current    Catalog
	lastTry    time.Time
	refreshing bool
}

// NewCatalogSyncer loads the cached snapshot (or the labelled baseline).
// interval is clamped to at least 15 minutes to avoid hammering GitHub.
func NewCatalogSyncer(dir string, interval time.Duration, tokenFn func() string) *CatalogSyncer {
	if interval < 15*time.Minute {
		interval = 6 * time.Hour
	}
	if tokenFn == nil {
		tokenFn = func() string { return "" }
	}
	c := &CatalogSyncer{
		path:     filepath.Join(dir, "protocol-catalog.json"),
		interval: interval,
		client:   &http.Client{Timeout: 20 * time.Second},
		tokenFn:  tokenFn,
	}
	if snap, err := c.readCache(); err == nil && snap != nil {
		c.current = *snap
		c.current.Source = "cache"
		c.current.Stale = true // until the next successful refresh
		c.current.Message = "已加载本地缓存，正在从内核官网核对最新协议清单…"
	} else {
		c.current = Catalog{
			Kernels: baselineKernels(), Source: "builtin", Stale: true,
			SyncedAt: time.Now().UTC().Format(time.RFC3339),
			Message:  "暂无缓存且官网暂不可达，当前显示内置基线清单；联网后会自动更新。",
		}
	}
	c.current.IntervalMinutes = int(c.interval / time.Minute)
	c.current.NextSyncAt = time.Now().UTC().Add(c.interval).Format(time.RFC3339)
	return c
}

func (c *CatalogSyncer) readCache() (*Catalog, error) {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	var snap Catalog
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	if len(snap.Kernels) == 0 {
		return nil, fmt.Errorf("empty snapshot")
	}
	return &snap, nil
}

func (c *CatalogSyncer) writeCache(snap Catalog) error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Current returns a copy of the current snapshot.
func (c *CatalogSyncer) Current() Catalog {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// Start refreshes once immediately (in the background) and then on a timer
// until ctx is cancelled. It never blocks the caller.
func (c *CatalogSyncer) Start(ctx context.Context) {
	go c.Refresh(ctx)
	go func() {
		t := time.NewTicker(c.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.Refresh(ctx)
			}
		}
	}()
}

// Refresh fetches every kernel's official documentation directory. A failed
// kernel keeps its previous list (or the baseline) and is marked with an error;
// a fully successful run is persisted and marked fresh. Concurrent callers
// share one in-flight refresh.
func (c *CatalogSyncer) Refresh(ctx context.Context) Catalog {
	c.mu.Lock()
	if c.refreshing {
		c.mu.Unlock()
		return c.Current()
	}
	if time.Since(c.lastTry) < 30*time.Second {
		c.mu.Unlock()
		return c.Current()
	}
	c.refreshing = true
	c.lastTry = time.Now()
	prev := c.current
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
	}()

	type result struct {
		idx int
		k   CatalogKernel
		err error
	}
	ch := make(chan result, len(catalogSources))
	for i, src := range catalogSources {
		go func(i int, src catalogSource) {
			k, err := c.fetchOne(ctx, src)
			ch <- result{idx: i, k: k, err: err}
		}(i, src)
	}

	kernels := make([]CatalogKernel, len(catalogSources))
	errs := make([]string, len(catalogSources))
	for range catalogSources {
		res := <-ch
		kernels[res.idx] = res.k
		if res.err != nil {
			errs[res.idx] = res.err.Error()
		}
	}

	// Merge: on failure keep the previous (or baseline) list, but record the
	// error so the UI can tell the user the list is not fresh.
	base := baselineKernels()
	success, failed := 0, 0
	for i := range catalogSources {
		if errs[i] != "" {
			failed++
			kept := base[i]
			if i < len(prev.Kernels) && len(prev.Kernels[i].Protocols) > 0 {
				kept = prev.Kernels[i]
			}
			kept.FromCache = true
			kept.Error = errs[i]
			kernels[i] = kept
		} else {
			success++
		}
	}

	now := time.Now().UTC()
	snap := Catalog{
		Kernels:         kernels,
		SyncedAt:        now.Format(time.RFC3339),
		NextSyncAt:      now.Add(c.interval).Format(time.RFC3339),
		IntervalMinutes: int(c.interval / time.Minute),
		Stale:           failed > 0,
	}
	switch {
	case success == 0:
		snap.Source = "builtin"
		snap.Message = "三个内核官网均无法访问，显示缓存/内置基线清单；请检查网络后点「刷新目录」。"
	case failed > 0:
		snap.Source = "official-docs"
		snap.Message = fmt.Sprintf("%d 个内核已从官方文档更新，%d 个沿用上次结果（见各项错误）。", success, failed)
	default:
		snap.Source = "official-docs"
		snap.Message = "已从三个内核官方文档同步最新协议清单。"
	}
	if err := c.writeCache(snap); err != nil {
		log.Printf("protocol catalog: persist snapshot: %v", err)
	}

	c.mu.Lock()
	c.current = snap
	c.mu.Unlock()
	return snap
}

func (c *CatalogSyncer) fetchOne(ctx context.Context, src catalogSource) (CatalogKernel, error) {
	k := CatalogKernel{
		Kernel: src.Kernel, Label: src.Label, SiteURL: src.SiteURL,
		SourceURL: src.APIURL, Protocols: []CatalogProtocol{},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.APIURL, nil)
	if err != nil {
		return k, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NGG-Protocol-Catalog/2.2")
	if t := strings.TrimSpace(c.tokenFn()); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return k, fmt.Errorf("请求官方文档失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return k, fmt.Errorf("官方文档返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return k, err
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return k, fmt.Errorf("解析官方文档目录失败: %w", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Type != "" && e.Type != "file" {
			continue
		}
		if !src.Filter(e.Name) {
			continue
		}
		slug := strings.TrimSuffix(e.Name, ".md")
		if seen[slug] || slug == "" {
			continue
		}
		seen[slug] = true
		k.Protocols = append(k.Protocols, CatalogProtocol{
			Slug: slug, Name: catalogDisplayName(slug),
			DocURL: fmt.Sprintf(src.DocPrefix, slug),
		})
	}
	if len(k.Protocols) == 0 {
		return k, fmt.Errorf("官方文档目录未解析到协议条目")
	}
	sort.Slice(k.Protocols, func(i, j int) bool { return k.Protocols[i].Name < k.Protocols[j].Name })
	k.Count = len(k.Protocols)
	k.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	return k, nil
}
