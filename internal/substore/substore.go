// Package substore speaks the subscription document format of Sub-Store
// (sub-store-org), the advanced subscription manager NGG has to interoperate
// with in both directions:
//
//   - Render produces the document Sub-Store itself ingests: a plain JSON array
//     of proxy objects in Sub-Store's own normalized key space — the exact
//     shape its `target=json` output uses. A Sub-Store subscription can point
//     at an NGG link without any conversion step.
//   - Parse reads what an operator exports from Sub-Store (plain JSON, Clash /
//     mihomo YAML, Clash proxy JSON with JSON5 comments, base64 URI bundles,
//     raw URI lines) and turns every usable entry into one Proxy.
//
// Two rules shape this file:
//
//  1. Never invent a value. A proxy without a server, without a port, or with a
//     type this build cannot express is dropped with a warning that names the
//     entry, instead of being imported as something that only fails later.
//  2. Never fail a whole document for one bad entry. Real exports mix styles: a
//     JSON array where three entries are strings, a YAML document with an extra
//     `rules:` block, base64 that decodes to a URI list with blank lines. Parse
//     reports what it could not use and keeps going.
package substore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Limits. A subscription is a node list, not a file transfer: 4 MiB is already
// tens of thousands of entries, and anything beyond it is a mistake (or an
// attempt to make the console eat memory).
const (
	// MaxBytes caps one document.
	MaxBytes = 4 << 20
	// MaxProxies caps how many entries one document may contribute.
	MaxProxies = 2000
	// maxDepth caps base64 -> text -> base64 recursion.
	maxDepth = 3
)

// Document formats reported in Result.Format.
const (
	FormatJSON   = "json"
	FormatYAML   = "yaml"
	FormatBase64 = "base64"
	FormatURI    = "uri"
)

// Canonical proxy types. They are the names Sub-Store / Clash.Meta use, so a
// parsed proxy keeps the meaning of the source document even when NGG cannot
// import it.
const (
	TypeShadowsocks = "ss"
	TypeSSR         = "ssr"
	TypeVMess       = "vmess"
	TypeVLESS       = "vless"
	TypeTrojan      = "trojan"
	TypeSOCKS5      = "socks5"
	TypeHTTP        = "http"
	TypeHysteria    = "hysteria"
	TypeHysteria2   = "hysteria2"
	TypeTUIC        = "tuic"
	TypeWireGuard   = "wireguard"
	TypeAnyTLS      = "anytls"
	TypeSnell       = "snell"
	TypeSSH         = "ssh"
	TypeDirect      = "direct"
)

// Errors. They are sentinels so the HTTP layer can map them to status codes
// without parsing a message.
var (
	// ErrEmpty means the document had no content at all.
	ErrEmpty = errors.New("订阅内容为空")
	// ErrTooLarge means the document exceeded MaxBytes.
	ErrTooLarge = errors.New("订阅内容超过大小上限")
	// ErrUnrecognized means no known format matched. It is never returned for a
	// document that parsed but contained zero usable proxies — that is a
	// successful parse with a warning.
	ErrUnrecognized = errors.New("无法识别的订阅格式（支持 Sub-Store JSON / Clash YAML / base64 / 节点链接）")
	// ErrUnsupportedProtocol is returned by Render for a protocol Sub-Store's
	// plain JSON cannot express.
	ErrUnsupportedProtocol = errors.New("Sub-Store 不支持该协议")
)

// typeAliases maps every spelling seen in the wild onto one canonical type.
// Unknown spellings are preserved verbatim so a warning can quote them.
var typeAliases = map[string]string{
	"ss": TypeShadowsocks, "shadowsocks": TypeShadowsocks, "ss-aead": TypeShadowsocks,
	"ssr": TypeSSR, "shadowsocksr": TypeSSR,
	"vmess":  TypeVMess,
	"vless":  TypeVLESS,
	"trojan": TypeTrojan, "trojan-go": TypeTrojan,
	"socks5": TypeSOCKS5, "socks": TypeSOCKS5, "socks5h": TypeSOCKS5, "socks4": TypeSOCKS5,
	"http": TypeHTTP, "https": TypeHTTP, "http2": TypeHTTP, "h2": TypeHTTP,
	"hysteria": TypeHysteria, "hy": TypeHysteria,
	"hysteria2": TypeHysteria2, "hy2": TypeHysteria2,
	"tuic":      TypeTUIC,
	"wireguard": TypeWireGuard, "wg": TypeWireGuard,
	"anytls": TypeAnyTLS,
	"snell":  TypeSnell,
	"ssh":    TypeSSH,
	"direct": TypeDirect,
}

// Proxy is one node from a Sub-Store document, normalized. Secrets live in UUID
// / Password / Username and never reach the wire: MarshalJSON replaces them with
// a has_secret flag so a preview response cannot leak a credential.
type Proxy struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	RawType   string `json:"raw_type,omitempty"`
	Server    string `json:"server"`
	Port      int    `json:"port"`
	Method    string `json:"method,omitempty"`
	Username  string `json:"username,omitempty"`
	TLS       bool   `json:"tls,omitempty"`
	SNI       string `json:"sni,omitempty"`
	Transport string `json:"transport,omitempty"`
	Path      string `json:"path,omitempty"`
	Host      string `json:"host,omitempty"`
	UDP       bool   `json:"udp,omitempty"`

	// Secret half.
	UUID     string `json:"-"`
	Password string `json:"-"`
}

// HasSecret reports whether the entry carries the credential its type needs.
func (p Proxy) HasSecret() bool {
	return p.UUID != "" || p.Password != "" || p.Username != ""
}

// MarshalJSON emits the preview shape: the credential is replaced by a boolean.
func (p Proxy) MarshalJSON() ([]byte, error) {
	type wire struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		RawType   string `json:"raw_type,omitempty"`
		Server    string `json:"server"`
		Port      int    `json:"port"`
		Method    string `json:"method,omitempty"`
		TLS       bool   `json:"tls,omitempty"`
		SNI       string `json:"sni,omitempty"`
		Transport string `json:"transport,omitempty"`
		Path      string `json:"path,omitempty"`
		Host      string `json:"host,omitempty"`
		UDP       bool   `json:"udp,omitempty"`
		HasSecret bool   `json:"has_secret"`
	}
	return json.Marshal(wire{
		Name: p.Name, Type: p.Type, RawType: p.RawType, Server: p.Server, Port: p.Port,
		Method: p.Method, TLS: p.TLS, SNI: p.SNI, Transport: p.Transport,
		Path: p.Path, Host: p.Host, UDP: p.UDP, HasSecret: p.HasSecret(),
	})
}

// Importable reports whether this entry can become an NGG host record as-is.
// Types outside the list still reach the consumer, which reports them as
// "unsupported" together with their real name.
func (p Proxy) Importable() bool { return p.ImportProblem() == "" }

// ImportProblem returns "" when the proxy is importable, otherwise the reason.
func (p Proxy) ImportProblem() string {
	if p.Server == "" {
		return "缺少 server"
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Sprintf("端口 %d 不在 1..65535", p.Port)
	}
	switch p.Type {
	case TypeShadowsocks, TypeVLESS, TypeTrojan:
		if p.UUID == "" && p.Password == "" {
			return "缺少凭据（uuid / password）"
		}
	case TypeSOCKS5, TypeHTTP:
		// An unauthenticated proxy is legal; nothing to check.
	default:
		name := p.RawType
		if name == "" {
			name = p.Type
		}
		return fmt.Sprintf("协议 %s 暂不支持导入 NGG 节点", name)
	}
	return ""
}

// Credential is the NGG credential string for this proxy: "method:password" for
// Shadowsocks, "username:password" for SOCKS5 / HTTP, the bare secret otherwise.
func (p Proxy) Credential() string {
	switch p.Type {
	case TypeShadowsocks:
		if p.Method == "" {
			return p.Password
		}
		return p.Method + ":" + p.Password
	case TypeSOCKS5, TypeHTTP:
		if p.Password == "" {
			return p.Username
		}
		return p.Username + ":" + p.Password
	case TypeVLESS:
		return p.UUID
	default:
		return p.Password
	}
}

// Result is one parsed document.
type Result struct {
	Format  string   `json:"format"`
	Proxies []Proxy  `json:"proxies"`
	Skipped int      `json:"skipped"`
	Bytes   int      `json:"bytes"`
	Warning []string `json:"warnings"`
}

// Warnings returns the accumulated warnings, never nil, so the JSON shape is
// stable for the console.
func (r Result) Warnings() []string {
	if r.Warning == nil {
		return []string{}
	}
	return r.Warning
}

// Parse reads one document in any format Sub-Store can emit.
func Parse(data []byte) (Result, error) {
	if len(data) > MaxBytes {
		return Result{}, fmt.Errorf("%w：%d 字节 > %d", ErrTooLarge, len(data), MaxBytes)
	}
	text := strings.TrimSpace(stripBOM(string(data)))
	if text == "" {
		return Result{}, ErrEmpty
	}
	res, err := parseText(text, 0)
	if err != nil {
		return Result{}, err
	}
	res.Bytes = len(data)
	return res, nil
}

// ParseURI parses a single share link (ss / vless / trojan / socks5 / http and
// the "known but unsupported" schemes). It exists so the console can explain a
// single line the same way it explains a whole document.
func ParseURI(raw string) (Proxy, error) { return parseURI(strings.TrimSpace(raw)) }

// ------------------------------------------------------------------ *
// Format detection
// ------------------------------------------------------------------ *

func parseText(text string, depth int) (Result, error) {
	if depth > maxDepth {
		return Result{}, fmt.Errorf("%w：编码嵌套超过 %d 层", ErrUnrecognized, maxDepth)
	}
	if r, ok := parseJSONDoc(text); ok {
		return r, nil
	}
	if r, ok := parseYAMLDoc(text); ok {
		return r, nil
	}
	if r, ok := parseBase64Doc(text, depth); ok {
		return r, nil
	}
	if r, ok := parseURIList(text); ok {
		return r, nil
	}
	return Result{}, ErrUnrecognized
}

// parseJSONDoc accepts a bare array, a Clash-style object with `proxies`, and a
// single proxy object. JSON5-ish comments and trailing commas are tolerated
// because Sub-Store's own UI exports them.
func parseJSONDoc(text string) (Result, bool) {
	// Comments are stripped before the first-character check: a document that
	// starts with "// exported by Sub-Store" is still JSON.
	head := strings.TrimSpace(relaxJSON(strings.TrimSpace(text)))
	if head == "" || (head[0] != '{' && head[0] != '[') {
		return Result{}, false
	}
	var doc any
	if err := json.Unmarshal([]byte(head), &doc); err != nil {
		return Result{}, false
	}
	items, single, ok := collect(doc)
	if !ok {
		return Result{}, false
	}
	res := Result{Format: FormatJSON}
	if single {
		// A single proxy object is a complete document with one entry.
		res.Warning = append(res.Warning, "文档是一个单独的节点对象，已按 1 个节点解析")
	}
	res.merge(items)
	return res, true
}

// parseYAMLDoc handles Clash / mihomo / Sub-Store YAML exports. A scalar (which
// is what a base64 blob looks like to YAML) is rejected so the caller can try
// the base64 decoder instead.
func parseYAMLDoc(text string) (Result, bool) {
	var doc any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return Result{}, false
	}
	if _, isScalar := doc.(string); isScalar {
		return Result{}, false
	}
	items, _, ok := collect(doc)
	if !ok {
		return Result{}, false
	}
	res := Result{Format: FormatYAML}
	res.merge(items)
	return res, true
}

// parseBase64Doc decodes a base64 bundle (v2rayN / Shadowrocket style) and
// re-parses the plaintext. Binary payloads are rejected rather than decoded into
// mojibake.
func parseBase64Doc(text string, depth int) (Result, bool) {
	raw, ok := decodeBase64(text)
	if !ok || !utf8.Valid(raw) {
		return Result{}, false
	}
	inner := strings.TrimSpace(stripBOM(string(raw)))
	if inner == "" {
		return Result{}, false
	}
	res, err := parseText(inner, depth+1)
	if err != nil {
		return Result{}, false
	}
	res.Format = FormatBase64
	res.Warning = append(res.Warning, "内容为 base64 编码，已解码后解析")
	return res, true
}

// parseURIList handles one-share-link-per-line documents.
func parseURIList(text string) (Result, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	res := Result{Format: FormatURI}
	found := false
	invalid := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.Contains(line, "://") {
			invalid++
			continue
		}
		found = true
		p, err := parseURI(line)
		if err != nil {
			res.Skipped++
			res.addWarning(fmt.Sprintf("第 %d 行（%s）：%v", len(res.Proxies)+res.Skipped, clip(line), err))
			continue
		}
		if err := res.append(p); err != nil {
			return res, true
		}
	}
	if !found {
		return Result{}, false
	}
	if invalid > 0 {
		res.addWarning(fmt.Sprintf("%d 行不是节点链接，已忽略", invalid))
	}
	return res, true
}

// collect pulls the proxy list out of a decoded document. single reports that
// the document itself was one proxy object rather than a container.
func collect(doc any) (items []any, single bool, ok bool) {
	switch v := doc.(type) {
	case []any:
		return v, false, true
	case map[string]any:
		if raw, has := v["proxies"]; has {
			list, isList := raw.([]any)
			if !isList {
				return nil, false, false
			}
			return list, false, true
		}
		// A Sub-Store "组合订阅" export wraps the list in `list`.
		if raw, has := v["list"]; has {
			if list, isList := raw.([]any); isList {
				return list, false, true
			}
		}
		if looksLikeProxy(v) {
			return []any{v}, true, true
		}
		return nil, false, false
	case nil:
		return nil, false, false
	}
	return nil, false, false
}

func looksLikeProxy(m map[string]any) bool {
	if _, has := m["type"]; !has {
		return false
	}
	_, hasServer := m["server"]
	return hasServer
}

// merge normalizes every collected item into res, counting the ones it had to
// drop. It never returns an error: a partially usable document is more useful
// than a rejection.
func (r *Result) merge(items []any) {
	if len(items) == 0 {
		r.addWarning("文档中没有任何节点条目")
		return
	}
	for i, item := range items {
		if len(r.Proxies) >= MaxProxies {
			r.addWarning(fmt.Sprintf("节点数超过上限 %d，其余条目已忽略", MaxProxies))
			return
		}
		m, isMap := item.(map[string]any)
		if !isMap {
			r.Skipped++
			r.addWarning(fmt.Sprintf("第 %d 条不是对象（%T），已跳过", i+1, item))
			continue
		}
		p, err := normalize(m)
		if err != nil {
			r.Skipped++
			r.addWarning(fmt.Sprintf("第 %d 条：%v", i+1, err))
			continue
		}
		_ = r.append(p)
	}
}

// append adds one proxy, de-duplicating an identical server:port:type entry
// (Sub-Store collections often repeat one node across sources).
func (r *Result) append(p Proxy) error {
	for _, seen := range r.Proxies {
		if seen.Server == p.Server && seen.Port == p.Port && seen.Type == p.Type {
			r.Skipped++
			r.addWarning(fmt.Sprintf("%s 与前面的条目重复（%s:%d），已跳过", p.Name, p.Server, p.Port))
			return nil
		}
	}
	r.Proxies = append(r.Proxies, p)
	return nil
}

func (r *Result) addWarning(msg string) {
	if len(r.Warning) < 200 {
		r.Warning = append(r.Warning, msg)
	}
}

// ------------------------------------------------------------------ *
// Normalization
// ------------------------------------------------------------------ *

func normalize(m map[string]any) (Proxy, error) {
	rawType := strings.ToLower(str(m, "type", "protocol"))
	if rawType == "" {
		return Proxy{}, errors.New("缺少 type 字段")
	}
	canonical, known := typeAliases[rawType]
	if !known {
		canonical = rawType
	}

	server := str(m, "server", "address", "host", "addr")
	port := num(m, "port", "server_port", "server-port")
	p := Proxy{
		Type:    canonical,
		RawType: rawType,
		Server:  server,
		Port:    port,
		Method:  str(m, "cipher", "method", "encrypt-method"),
		UDP:     boolean(m, "udp"),
		// Trojan is TLS-only: a document that omits `tls` still means TLS.
		TLS: canonical == TypeTrojan,
	}

	p.Name = str(m, "name", "tag", "remark", "remarks")
	if p.Name == "" {
		p.Name = fmt.Sprintf("%s:%d", server, port)
	}

	// Credentials: the same field is spelled differently per type and per app.
	p.UUID = str(m, "uuid", "id", "user-id")
	p.Password = secret(m, "password", "pass", "auth", "auth_str", "auth-str", "psk")
	p.Username = str(m, "username", "user")
	if p.Type == TypeVMess && p.UUID == "" {
		// Shadowrocket intermittently writes the VMess uuid into `password`.
		p.UUID = p.Password
	}
	if p.Type == TypeTUIC {
		// tuic://uuid:password@host — the uuid is the username half.
		if p.UUID == "" && p.Username != "" {
			p.UUID, p.Username = p.Username, ""
		}
	}

	// TLS / transport are described by a mix of flat keys and sub-objects.
	security := strings.ToLower(str(m, "security", "tls-security"))
	p.TLS = p.TLS || boolean(m, "tls") || security == "tls" || security == "reality" || security == "xtls"
	p.SNI = str(m, "sni", "servername", "server-name", "peer", "servername-verify")
	p.Transport = strings.ToLower(str(m, "network", "net", "transport", "type-transport"))
	if p.Transport == "" {
		if obfs := strings.ToLower(str(m, "obfs")); obfs == "ws" || obfs == "websocket" {
			p.Transport = "ws"
		}
	}
	switch p.Transport {
	case "", "tcp", "raw", "none":
		p.Transport = ""
	case "websocket":
		p.Transport = "ws"
	case "http":
		if p.Type != TypeHTTP {
			p.Transport = "h2"
		}
	case "grpc":
		p.Transport = "grpc"
	}

	// ws-opts / grpc-opts carry the request path and the Host header.
	if ws := obj(m, "ws-opts", "ws_opts", "ws-options"); ws != nil {
		p.Path = str(ws, "path")
		if hdrs := obj(ws, "headers", "header"); hdrs != nil {
			p.Host = str(hdrs, "Host", "host")
		}
	}
	if p.Path == "" {
		p.Path = str(m, "ws-path", "ws_path", "path")
	}
	if p.Host == "" {
		p.Host = str(m, "ws-headers", "host-header", "obfs-host")
	}
	if g := obj(m, "grpc-opts", "grpc_opts"); g != nil {
		if p.Path == "" {
			p.Path = str(g, "grpc-service-name", "grpc-service-name")
		}
	}

	// A SOCKS5 / HTTP entry that only carries an authority in `username` is
	// still usable; nothing else to reconstruct here.

	if p.Server == "" {
		return Proxy{}, errors.New("缺少 server（地址）")
	}
	if p.Port < 1 || p.Port > 65535 {
		return Proxy{}, fmt.Errorf("端口 %d 不在 1..65535", p.Port)
	}
	return p, nil
}

// ------------------------------------------------------------------ *
// URI parsing
// ------------------------------------------------------------------ *

func parseURI(raw string) (Proxy, error) {
	if raw == "" {
		return Proxy{}, errors.New("空链接")
	}
	if strings.HasPrefix(strings.ToLower(raw), "vmess://") {
		return parseVMessURI(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Proxy{}, fmt.Errorf("链接无法解析: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	canonical, known := typeAliases[scheme]
	if !known {
		// socks5+tls / http+tls style suffixes.
		base, _, hasSuffix := strings.Cut(scheme, "+")
		if hasSuffix {
			canonical, known = typeAliases[base]
		}
	}
	if !known {
		return Proxy{}, fmt.Errorf("未知的链接协议 %q", scheme)
	}

	host := u.Hostname()
	port := 0
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	if port == 0 {
		// Documented defaults, only where the protocol defines one.
		switch canonical {
		case TypeHTTP:
			if strings.HasPrefix(scheme, "https") {
				port = 443
			} else {
				port = 80
			}
		case TypeSOCKS5:
			port = 1080
		}
	}

	p := Proxy{Type: canonical, RawType: scheme, Server: host, Port: port}
	p.Name = uriName(u, host, port)
	if u.User != nil {
		user := u.User.Username()
		pass, hasPass := u.User.Password()
		switch canonical {
		case TypeShadowsocks:
			// SIP002: ss://base64(method:password)@host:port, or plain
			// method:password when the app did not encode it.
			method, password, ok := splitSS(user)
			if !ok && hasPass {
				method, password = user, pass
			}
			p.Method, p.Password = method, password
		case TypeSOCKS5, TypeHTTP:
			p.Username, p.Password = user, pass
		case TypeVLESS:
			p.UUID = user
		case TypeTUIC:
			p.UUID, p.Password = user, pass
		default:
			p.Password = user
		}
	}

	q := u.Query()
	if sec := strings.ToLower(q.Get("security")); sec != "" {
		p.TLS = sec == "tls" || sec == "reality" || sec == "xtls"
	}
	if strings.HasPrefix(scheme, "https") || strings.HasSuffix(scheme, "+tls") {
		p.TLS = true
	}
	p.SNI = firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("servername"))
	p.Transport = strings.ToLower(firstNonEmpty(q.Get("type"), q.Get("network"), q.Get("obfs")))
	if p.Transport == "none" || p.Transport == "tcp" || p.Transport == "raw" {
		p.Transport = ""
	}
	if p.Transport == "websocket" {
		p.Transport = "ws"
	}
	p.Path = firstNonEmpty(q.Get("path"), q.Get("serviceName"), q.Get("servicename"))
	p.Host = q.Get("host")

	if p.Server == "" {
		return Proxy{}, errors.New("链接里没有主机地址")
	}
	if p.Port < 1 || p.Port > 65535 {
		return Proxy{}, fmt.Errorf("端口 %d 不在 1..65535", p.Port)
	}
	return p, nil
}

// parseVMessURI handles both spellings v2rayN / Shadowrocket produce:
// vmess://base64(json) and vmess://uuid@host:port?...#name.
func parseVMessURI(raw string) (Proxy, error) {
	body := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		body = raw[i+3:]
	}
	if strings.Contains(body, "@") {
		p, err := parseURIAs(raw, "vmess", TypeVMess)
		return p, err
	}
	bin, ok := decodeBase64(body)
	if !ok {
		return Proxy{}, errors.New("vmess 链接的 payload 不是合法 base64")
	}
	var doc map[string]any
	if err := json.Unmarshal(bin, &doc); err != nil {
		return Proxy{}, fmt.Errorf("vmess 链接的 JSON 无法解析: %w", err)
	}
	// v2rayN's vmess payload has its own vocabulary: ps = name, add = server,
	// id = uuid, net = transport, tls = "tls"/"" (a string, not a bool).
	server := str(doc, "add", "server", "address")
	port := num(doc, "port")
	if server == "" {
		return Proxy{}, errors.New("vmess 链接缺少 add（地址）")
	}
	p := Proxy{
		Type:      TypeVMess,
		RawType:   "vmess",
		Name:      firstNonEmpty(str(doc, "ps", "remarks", "name"), fmt.Sprintf("%s:%d", server, port)),
		Server:    server,
		Port:      port,
		UUID:      str(doc, "id", "uuid"),
		Transport: strings.ToLower(str(doc, "net", "network")),
		Path:      str(doc, "path"),
		Host:      str(doc, "host"),
	}
	if p.UUID == "" && p.Password == "" {
		p.UUID = str(doc, "password", "pass")
	}
	if security := strings.ToLower(str(doc, "tls", "security", "sni")); security == "tls" || security == "reality" || security == "xtls" {
		p.TLS = true
	}
	p.SNI = firstNonEmpty(str(doc, "sni"), str(doc, "host"))
	if p.Transport == "tcp" || p.Transport == "none" {
		p.Transport = ""
	}
	if p.Port < 1 || p.Port > 65535 {
		return Proxy{}, fmt.Errorf("端口 %d 不在 1..65535", p.Port)
	}
	return p, nil
}

// parseURIAs re-parses a link whose scheme is not one url.Parse knows.
func parseURIAs(raw, scheme, canonical string) (Proxy, error) {
	p, err := parseURI(strings.Replace(raw, scheme+"://", "http://", 1))
	if err != nil {
		return Proxy{}, err
	}
	p.Type, p.RawType = canonical, scheme
	return p, nil
}

func uriName(u *url.URL, host string, port int) string {
	if f := strings.TrimSpace(u.Fragment); f != "" {
		return f
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// splitSS accepts "method:password" (optionally base64 encoded by SIP002).
func splitSS(userinfo string) (method, password string, ok bool) {
	candidates := []string{userinfo}
	if bin, decoded := decodeBase64(userinfo); decoded {
		candidates = append([]string{string(bin)}, candidates...)
	}
	for _, c := range candidates {
		if i := strings.Index(c, ":"); i > 0 && i < len(c)-1 {
			return c[:i], c[i+1:], true
		}
	}
	return "", "", false
}

// ------------------------------------------------------------------ *
// Small helpers
// ------------------------------------------------------------------ *

func stripBOM(s string) string { return strings.TrimPrefix(s, "\ufeff") }

// relaxJSON strips // and /* */ comments outside strings and drops trailing
// commas, so the JSON5 documents Sub-Store's UI exports still parse with the
// standard library.
func relaxJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	return dropTrailingCommas(b.String())
}

func dropTrailingCommas(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue // drop the comma
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func decodeBase64(s string) ([]byte, bool) {
	compact := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', ' ':
			return -1
		}
		return r
	}, s)
	if len(compact) < 8 {
		return nil, false
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if bin, err := enc.DecodeString(compact); err == nil {
			return bin, true
		}
	}
	return nil, false
}

// str returns the first non-empty scalar among keys, as text. Numbers are
// rendered without an exponent so a port never becomes "4.43e+02".
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, has := m[k]
		if !has || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case int:
			return strconv.Itoa(t)
		case int64:
			return strconv.FormatInt(t, 10)
		case uint64:
			return strconv.FormatUint(t, 10)
		case bool:
			return strconv.FormatBool(t)
		}
	}
	return ""
}

// secret is like str but does not trim the value: a password may legitimately
// start or end with a space.
func secret(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, has := m[k]; has {
			if s, isString := v.(string); isString && s != "" {
				return s
			}
		}
	}
	return ""
}

func num(m map[string]any, keys ...string) int {
	for _, k := range keys {
		v, has := m[k]
		if !has || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case int64:
			return int(t)
		case uint64:
			return int(t)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
			if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
				return int(f)
			}
		}
	}
	return 0
}

// boolean accepts true, "true", 1, "1", "yes" — exports disagree on all four.
func boolean(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		v, has := m[k]
		if !has || v == nil {
			continue
		}
		switch t := v.(type) {
		case bool:
			return t
		case float64:
			return t != 0
		case int:
			return t != 0
		case string:
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "true", "1", "yes", "on":
				return true
			case "false", "0", "no", "off":
				return false
			}
		}
	}
	return false
}

func obj(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		if v, has := m[k]; has {
			if o, isMap := v.(map[string]any); isMap {
				return o
			}
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// clip shortens a line for a warning so one huge entry cannot flood the UI.
func clip(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// hostPort is shared by the URI parser and the tests.
func hostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
