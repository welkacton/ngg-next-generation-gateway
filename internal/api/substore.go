package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/substore"
	"github.com/ngg-project/ngg/internal/version"
)

// 配置引擎 · Sub-Store 导入
//
// The console already renders a Sub-Store link for every user (target=substore).
// This file closes the other direction: an operator who keeps their nodes in
// Sub-Store can pull that document into 节点管理 instead of re-typing servers.
//
// Two endpoints, one shared body:
//
//	POST /api/v1/substore/parse   → what the document contains, nothing saved
//	POST /api/v1/substore/import  → create the hosts it can represent
//
// Nothing is guessed: an entry whose protocol NGG cannot host is reported with
// its real type instead of being coerced into vless, and an entry the registry
// rejects is reported per-item without rolling back the ones that worked.

// substoreRequest is the body both endpoints accept. `text` wins over `url`:
// an operator pasting an export should never have the console fetch anything.
type substoreRequest struct {
	Text   string `json:"text"`
	URL    string `json:"url"`
	Role   string `json:"role"`
	DryRun bool   `json:"dry_run"`
}

// substoreDraft is the preview of one host the import would create. It carries
// no credential: the operator pasted the document, but a preview does not need
// to echo it back.
type substoreDraft struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	SvcPort  int    `json:"svc_port"`
	Protocol string `json:"protocol"`
	Role     string `json:"role"`
	Note     string `json:"note"`
}

// hostDraft pairs the wire preview with the secret the registry needs. Keeping
// the credential out of substoreDraft means it can never be JSON-encoded by
// accident.
type hostDraft struct {
	view       substoreDraft
	credential string
}

// substoreResponse is the shape of both endpoints. `failed` holds one line per
// entry the registry rejected, so a partial import is never silently partial.
type substoreResponse struct {
	DryRun   bool            `json:"dry_run"`
	Format   string          `json:"format"`
	Source   string          `json:"source"`
	Bytes    int             `json:"bytes"`
	Parsed   int             `json:"parsed"`
	Skipped  int             `json:"skipped"`
	Hosts    []substoreDraft `json:"hosts"`
	Failed   []string        `json:"failed"`
	Warnings []string        `json:"warnings"`
}

// defaultNodePort is the NGG agent port written into an imported host. It is
// the same default the 「新增节点」 form uses; the operator can change it in the
// detail drawer, and the value is called out in the response warnings.
const defaultNodePort = 2222

// substoreClient bounds a remote fetch. A subscription URL that hangs must not
// hold an HTTP handler open.
var substoreClient = &http.Client{Timeout: 15 * time.Second}

func (s *Server) handleSubStoreParse(w http.ResponseWriter, r *http.Request) {
	body, source, req, ok := s.substoreInput(w, r)
	if !ok {
		return
	}
	parsed, err := substore.Parse(body)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	drafts, warnings := buildDrafts(parsed.Proxies, hosts.Role(req.Role), nil)
	views := make([]substoreDraft, 0, len(drafts))
	for _, d := range drafts {
		views = append(views, d.view)
	}
	writeJSON(w, http.StatusOK, substoreResponse{
		DryRun:   true,
		Format:   parsed.Format,
		Source:   source,
		Bytes:    parsed.Bytes,
		Parsed:   len(parsed.Proxies),
		Skipped:  parsed.Skipped,
		Hosts:    views,
		Failed:   []string{},
		Warnings: append(parsed.Warnings(), warnings...),
	})
}

func (s *Server) handleSubStoreImport(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	body, source, req, ok := s.substoreInput(w, r)
	if !ok {
		return
	}
	parsed, err := substore.Parse(body)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Drafts are built against the ids already in the registry so a second
	// import never collides with the first.
	used := map[string]bool{}
	for _, h := range reg.List("") {
		used[h.ID] = true
	}
	drafts, warnings := buildDrafts(parsed.Proxies, hosts.Role(req.Role), used)

	resp := substoreResponse{
		DryRun:   req.DryRun,
		Format:   parsed.Format,
		Source:   source,
		Bytes:    parsed.Bytes,
		Parsed:   len(parsed.Proxies),
		Skipped:  parsed.Skipped,
		Hosts:    []substoreDraft{},
		Failed:   []string{},
		Warnings: append(parsed.Warnings(), warnings...),
	}
	if req.DryRun {
		for _, d := range drafts {
			resp.Hosts = append(resp.Hosts, d.view)
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	for _, d := range drafts {
		h := hosts.Host{
			ID:        d.view.ID,
			Name:      d.view.Name,
			Address:   d.view.Address,
			NodePort:  defaultNodePort,
			SvcPort:   d.view.SvcPort,
			Role:      hosts.Role(d.view.Role),
			Enabled:   true,
			Protocol:  d.view.Protocol,
			SecretKey: d.credential,
			Note:      d.view.Note,
			Tags:      []string{"substore"},
			Track:     true,
		}
		saved, err := reg.Add(r.Context(), h)
		if err != nil {
			resp.Failed = append(resp.Failed, fmt.Sprintf("%s：%v", d.view.Name, err))
			continue
		}
		d.view.ID = saved.ID
		resp.Hosts = append(resp.Hosts, d.view)
	}
	if len(resp.Hosts) == 0 && len(resp.Failed) > 0 {
		writeErr(w, http.StatusUnprocessableEntity, "一个节点都没能导入："+strings.Join(resp.Failed, "；"))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// substoreInput decodes the request and resolves the document, either from the
// pasted text or from the URL. ok=false means a response was already written.
func (s *Server) substoreInput(w http.ResponseWriter, r *http.Request) (body []byte, source string, req substoreRequest, ok bool) {
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return nil, "", req, false
	}
	text := strings.TrimSpace(req.Text)
	rawURL := strings.TrimSpace(req.URL)
	switch {
	case text != "":
		return []byte(text), "text", req, true
	case rawURL != "":
		fetched, err := fetchSubscription(r.Context(), rawURL)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return nil, "", req, false
		}
		return fetched, "url", req, true
	default:
		writeErr(w, http.StatusBadRequest, "请粘贴 Sub-Store 配置内容，或填写订阅地址")
		return nil, "", req, false
	}
}

// fetchSubscription downloads a subscription document. Only http/https is
// accepted: file:// and gopher:// would turn this endpoint into a local file
// reader. Private addresses are deliberately allowed — Sub-Store is usually
// self-hosted on the LAN the console runs in.
func fetchSubscription(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("订阅地址无法解析: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("订阅地址必须是 http:// 或 https://")
	}
	if u.Host == "" {
		return nil, errors.New("订阅地址缺少主机名")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("订阅请求构造失败: %w", err)
	}
	req.Header.Set("User-Agent", "NGG/"+version.Release+" (substore-import)")
	req.Header.Set("Accept", "application/json, text/yaml, text/plain, */*")
	resp, err := substoreClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取订阅失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("拉取订阅失败: HTTP %d", resp.StatusCode)
	}
	// One byte more than the limit so Parse can report "too large" instead of
	// silently truncating the document into a half-parsed one.
	data, err := io.ReadAll(io.LimitReader(resp.Body, substore.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取订阅内容失败: %w", err)
	}
	return data, nil
}

// buildDrafts turns parsed proxies into host drafts. Entries that cannot be
// imported are reported in the second return value, never dropped silently.
func buildDrafts(proxies []substore.Proxy, role hosts.Role, used map[string]bool) ([]hostDraft, []string) {
	if !hosts.ValidRole(role) {
		role = hosts.RoleOutbound
	}
	seen := used
	if seen == nil {
		seen = map[string]bool{}
	}
	drafts := make([]hostDraft, 0, len(proxies))
	var warnings []string
	for _, p := range proxies {
		if problem := p.ImportProblem(); problem != "" {
			warnings = append(warnings, fmt.Sprintf("%s：%s，已跳过", p.Name, problem))
			continue
		}
		id := uniqueHostID(hostIDBase(p), seen)
		note := "从 Sub-Store 导入"
		if name := strings.TrimSpace(p.Name); name != "" {
			note += " · " + name
		}
		drafts = append(drafts, hostDraft{
			view: substoreDraft{
				ID:       id,
				Name:     p.Name,
				Address:  p.Server,
				SvcPort:  p.Port,
				Protocol: p.Type,
				Role:     string(role),
				Note:     note,
			},
			credential: p.Credential(),
		})
	}
	if len(drafts) > 0 {
		warnings = append(warnings, fmt.Sprintf("导入的节点管理端口统一为 %d（NGG Agent 默认端口），如需探测可在节点详情中修改", defaultNodePort))
	}
	return drafts, warnings
}

// hostIDBase builds an id that satisfies hosts' id rule (letters, digits, dot,
// dash, underscore) from the entry's name, falling back to its address. CJK
// names collapse to nothing, which is why the address fallback exists.
func hostIDBase(p substore.Proxy) string {
	base := slugify(p.Name, 40)
	if base == "" {
		base = slugify(strings.ReplaceAll(p.Server, ":", "-"), 40)
	}
	if base == "" {
		base = "node"
	}
	if p.Port > 0 {
		base = fmt.Sprintf("%s-%d", base, p.Port)
	}
	return base
}

func slugify(in string, max int) string {
	var b strings.Builder
	lastDash := true // a leading dash is illegal in an id
	for _, r := range strings.ToLower(in) {
		isWord := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isWord || r == '.' || r == '_' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-._")
	if len(out) > max {
		out = strings.Trim(out[:max], "-._")
	}
	return out
}

// uniqueHostID appends -2, -3, ... until the id is free, so importing the same
// Sub-Store collection twice creates a second batch instead of failing.
func uniqueHostID(base string, used map[string]bool) string {
	id := base
	for i := 2; used[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
		if i > 9999 {
			id = fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
			break
		}
	}
	used[id] = true
	return id
}
