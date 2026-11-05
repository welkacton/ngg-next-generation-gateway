// Package protocols is the NGG protocol capability center (Stage 4).
//
// It holds the catalog of EVERY protocol family the kernels can handle:
//   - encrypted protocols (VPN / TLS / QUIC / AEAD / post-quantum)
//   - plain unencrypted protocols (DIRECT / SOCKS5 / HTTP / raw forwarding)
//   - ordinary proxy protocols (CONNECT / upstream chains / PAC / listeners)
//
// NGG generates REAL kernel-native configuration fragments and delegates the
// crypto/handshake to the kernels — it never reimplements a kernel's protocol
// stack. Users may edit generated content, save it as an override, add their
// own protocols and enable/disable one, a selection, or everything at once.
package protocols

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Category string

const (
	CatVPN            Category = "vpn"
	CatEncryptedProxy Category = "encrypted-proxy"
	CatPlaintext      Category = "plaintext"
	CatPlainProxy     Category = "plain-proxy"
	CatPQ             Category = "post-quantum"
)

// Group is the top-level taxonomy exposed in the UI.
type Group string

const (
	GroupEncrypted  Group = "encrypted"   // 加密协议
	GroupPlaintext  Group = "plaintext"   // 普通未加密协议
	GroupPlainProxy Group = "plain-proxy" // 普通代理协议
)

// ConfigKind tells the generator how a protocol maps onto a kernel config.
// Outbounds are emitted as kernel-native outbound entries; policies and
// listeners are emitted as valid NGG fragments because kernels implement them
// as rule actions / inbound listeners rather than as outbounds.
type ConfigKind string

const (
	KindOutbound ConfigKind = "outbound"
	KindPolicy   ConfigKind = "policy"
	KindListener ConfigKind = "listener"
	KindHelper   ConfigKind = "helper"
)

type Spec struct {
	ID           string     `json:"id" yaml:"id"`
	Name         string     `json:"name" yaml:"name"`
	Category     Category   `json:"category" yaml:"category"`
	Group        Group      `json:"group" yaml:"group"`
	Security     string     `json:"security" yaml:"security"`
	Transport    string     `json:"transport" yaml:"transport"`
	ConfigKind   ConfigKind `json:"config_kind" yaml:"config_kind"`
	DefaultPort  int        `json:"default_port" yaml:"default_port"`
	Ports        []string   `json:"ports" yaml:"ports"`
	Kernels      []string   `json:"kernels" yaml:"kernels"`
	Features     []string   `json:"features" yaml:"features"`
	PQCapable    bool       `json:"pq_capable" yaml:"pq_capable"`
	RequiresRoot bool       `json:"requires_root" yaml:"requires_root"`
	Description  string     `json:"description" yaml:"description"`
	Enabled      bool       `json:"enabled" yaml:"enabled"`
	Custom       bool       `json:"custom" yaml:"custom"`

	// User-editable state. Params is the default JSON parameter object used by
	// the generator. ConfigContent/ConfigFormat is a saved, hand-edited config
	// override: when present, generation returns it instead of a template.
	Params        map[string]any `json:"params,omitempty" yaml:"params,omitempty"`
	ConfigContent string         `json:"config_content,omitempty" yaml:"config_content,omitempty"`
	ConfigFormat  string         `json:"config_format,omitempty" yaml:"config_format,omitempty"`
}

// --------------------------------------------------------------- registry ---

type Registry struct {
	mu    sync.RWMutex
	specs map[string]Spec
	path  string // optional persistence file; empty = in-memory only
}

// NewRegistry keeps the built-in catalog and optionally persists user state.
// The variadic path keeps NewRegistry() (used by tests) working unchanged.
func NewRegistry(path ...string) *Registry {
	r := &Registry{specs: map[string]Spec{}}
	if len(path) > 0 {
		r.path = path[0]
	}
	for _, s := range builtin() {
		r.specs[s.ID] = s
	}
	return r
}

// Load merges persisted user state on top of the current built-in catalog, so
// built-ins added in a newer binary still appear after an upgrade.
func (r *Registry) Load() error {
	if r.path == "" {
		return nil
	}
	b, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var saved []Spec
	if err := json.Unmarshal(b, &saved); err != nil {
		return fmt.Errorf("parse %s: %w", r.path, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range saved {
		if strings.TrimSpace(s.ID) == "" {
			continue
		}
		if s.Custom {
			r.specs[s.ID] = s
			continue
		}
		base, ok := r.specs[s.ID]
		if !ok {
			continue // stale built-in from an older release
		}
		base.Enabled = s.Enabled
		base.Params = s.Params
		base.ConfigContent = s.ConfigContent
		base.ConfigFormat = s.ConfigFormat
		r.specs[s.ID] = base
	}
	return nil
}

func (r *Registry) persistLocked() error {
	if r.path == "" {
		return nil
	}
	list := make([]Spec, 0, len(r.specs))
	for _, s := range r.specs {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o600)
}

// snapshot returns a shallow copy used to roll back a failed write.
func (r *Registry) snapshot() map[string]Spec {
	out := make(map[string]Spec, len(r.specs))
	for k, v := range r.specs {
		out[k] = v
	}
	return out
}

type Filter struct {
	Category      string
	Group         string
	Search        string
	Enabled       string // "", "true", "false"
	Kernel        string // only protocols whose Kernels contain this value
	ExcludeGroups []Group
}

func (r *Registry) List(category string) []Spec { return r.Query(Filter{Category: category}) }

func (r *Registry) Query(f Filter) []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(f.Search))
	out := make([]Spec, 0, len(r.specs))
	for _, s := range r.specs {
		if f.Category != "" && string(s.Category) != f.Category {
			continue
		}
		if f.Group != "" && string(s.Group) != f.Group {
			continue
		}
		excluded := false
		for _, g := range f.ExcludeGroups {
			if s.Group == g {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		if f.Kernel != "" {
			found := false
			for _, k := range s.Kernels {
				if k == f.Kernel {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if f.Enabled == "true" && !s.Enabled {
			continue
		}
		if f.Enabled == "false" && s.Enabled {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(s.ID+" "+s.Name+" "+s.Description+" "+strings.Join(s.Kernels, " ")), q) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Get(id string) (Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.specs[id]
	return s, ok
}

func (r *Registry) SetEnabled(id string, enabled bool) (Spec, error) {
	changed, err := r.SetEnabledBatch([]string{id}, false, enabled)
	if err != nil {
		return Spec{}, err
	}
	_ = changed
	s, ok := r.Get(id)
	if !ok {
		return Spec{}, fmt.Errorf("protocol %q not registered", id)
	}
	return s, nil
}

// SetEnabledBatch flips one protocol, a selection, or all of them with a
// single persistence write. On a write failure the in-memory map is restored.
func (r *Registry) SetEnabledBatch(ids []string, all, enabled bool) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	before := r.snapshot()
	targets := map[string]bool{}
	if all {
		for id := range r.specs {
			targets[id] = true
		}
	} else {
		for _, id := range ids {
			targets[id] = true
		}
	}
	missing := 0
	for id := range targets {
		if _, ok := r.specs[id]; !ok {
			missing++
		}
	}
	if len(targets) == 0 || missing == len(targets) {
		return 0, fmt.Errorf("no matching protocols")
	}
	changed := 0
	for id := range targets {
		s, ok := r.specs[id]
		if !ok {
			continue
		}
		if s.Enabled != enabled {
			s.Enabled = enabled
			r.specs[id] = s
			changed++
		}
	}
	if err := r.persistLocked(); err != nil {
		r.specs = before
		return 0, fmt.Errorf("persist enabled state: %w", err)
	}
	return changed, nil
}

func (r *Registry) Register(s Spec) error {
	if err := validateSpec(&s, true); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[s.ID]; exists {
		return fmt.Errorf("protocol %q already exists", s.ID)
	}
	before := r.snapshot()
	s.Custom = true
	r.specs[s.ID] = s
	if err := r.persistLocked(); err != nil {
		r.specs = before
		return fmt.Errorf("persist custom protocol: %w", err)
	}
	return nil
}

// Update replaces a custom protocol in full. Built-in catalog entries keep
// their code-defined structure (so a binary upgrade can fix catalog data);
// the JSON editor may still change their enabled state, params, display text
// and saved config override. To change structure, create a custom protocol.
func (r *Registry) Update(in Spec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.specs[in.ID]
	if !ok {
		return fmt.Errorf("protocol %q not registered", in.ID)
	}
	s := old
	if old.Custom {
		if err := validateSpec(&in, false); err != nil {
			return err
		}
		s = in
		s.Custom = true
	} else {
		s.Enabled = in.Enabled
		if strings.TrimSpace(in.Name) != "" {
			s.Name = in.Name
		}
		if strings.TrimSpace(in.Description) != "" {
			s.Description = in.Description
		}
		if in.Params != nil {
			s.Params = in.Params
		}
		s.ConfigContent = in.ConfigContent
		s.ConfigFormat = in.ConfigFormat
		if s.ConfigContent != "" {
			if s.ConfigFormat == "" {
				s.ConfigFormat = "json"
			}
			if err := ValidateConfig([]byte(s.ConfigContent), s.ConfigFormat); err != nil {
				return fmt.Errorf("invalid config_content (%s): %w", s.ConfigFormat, err)
			}
		}
	}
	before := r.snapshot()
	r.specs[s.ID] = s
	if err := r.persistLocked(); err != nil {
		r.specs = before
		return fmt.Errorf("persist protocol: %w", err)
	}
	return nil
}

func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.specs[id]
	if !ok {
		return fmt.Errorf("protocol %q not registered", id)
	}
	if !s.Custom {
		return fmt.Errorf("built-in protocol %q cannot be deleted; disable it instead", id)
	}
	before := r.snapshot()
	delete(r.specs, id)
	if err := r.persistLocked(); err != nil {
		r.specs = before
		return fmt.Errorf("persist deletion: %w", err)
	}
	return nil
}

func (r *Registry) SaveConfig(id, content, format string) (Spec, error) {
	// The editor shows JSONC-style comments; persist only clean config text.
	clean := string(StripComments([]byte(content), format))
	if err := ValidateConfig([]byte(clean), format); err != nil {
		return Spec{}, fmt.Errorf("invalid %s config: %w", format, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.specs[id]
	if !ok {
		return Spec{}, fmt.Errorf("protocol %q not registered", id)
	}
	before := r.snapshot()
	s.ConfigContent = clean
	s.ConfigFormat = format
	r.specs[id] = s
	if err := r.persistLocked(); err != nil {
		r.specs = before
		return Spec{}, fmt.Errorf("persist config: %w", err)
	}
	return s, nil
}

func (r *Registry) ClearConfig(id string) (Spec, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.specs[id]
	if !ok {
		return Spec{}, fmt.Errorf("protocol %q not registered", id)
	}
	before := r.snapshot()
	s.ConfigContent = ""
	s.ConfigFormat = ""
	r.specs[id] = s
	if err := r.persistLocked(); err != nil {
		r.specs = before
		return Spec{}, fmt.Errorf("persist config reset: %w", err)
	}
	return s, nil
}

func validateSpec(s *Spec, creating bool) error {
	s.ID = strings.TrimSpace(s.ID)
	if s.ID == "" {
		return fmt.Errorf("protocol needs an id")
	}
	if strings.TrimSpace(s.Name) == "" {
		s.Name = s.ID
	}
	if len(s.Kernels) == 0 {
		return fmt.Errorf("protocol needs at least one kernel")
	}
	if s.Category == "" {
		s.Category = CatPlainProxy
	}
	if s.Group == "" {
		switch s.Category {
		case CatVPN, CatEncryptedProxy, CatPQ:
			s.Group = GroupEncrypted
		case CatPlaintext:
			s.Group = GroupPlaintext
		default:
			s.Group = GroupPlainProxy
		}
	}
	if s.ConfigKind == "" {
		s.ConfigKind = KindOutbound
	}
	if s.ConfigContent != "" {
		if s.ConfigFormat == "" {
			s.ConfigFormat = "json"
		}
		if err := ValidateConfig([]byte(s.ConfigContent), s.ConfigFormat); err != nil {
			return fmt.Errorf("invalid config_content (%s): %w", s.ConfigFormat, err)
		}
	}
	if creating && s.Params == nil {
		s.Params = map[string]any{}
	}
	return nil
}

// GenerateFor is the registry-aware generator used by the API. A disabled
// protocol cannot be used, and a saved hand-edited config wins unless the
// caller explicitly asks to regenerate from the built-in template.
func (r *Registry) GenerateFor(id, kernel string, params map[string]any, regenerate bool) ([]byte, string, error) {
	s, ok := r.Get(id)
	if !ok {
		return nil, "", fmt.Errorf("protocol %q not registered", id)
	}
	if !s.Enabled {
		return nil, "", fmt.Errorf("protocol %q is disabled; enable it first", id)
	}
	merged := map[string]any{}
	for k, v := range s.Params {
		merged[k] = v
	}
	for k, v := range params {
		merged[k] = v
	}
	if !regenerate && s.ConfigContent != "" {
		format := s.ConfigFormat
		if format == "" {
			format = "json"
		}
		return []byte(s.ConfigContent), format, nil
	}
	return generate(s, kernel, merged)
}

// -------------------------------------------------------------- generator ---

var builtinOnce sync.Once
var builtinIndex map[string]Spec

func builtinSpec(id string) (Spec, bool) {
	builtinOnce.Do(func() {
		builtinIndex = map[string]Spec{}
		for _, s := range builtin() {
			builtinIndex[s.ID] = s
		}
	})
	s, ok := builtinIndex[strings.ToLower(id)]
	return s, ok
}

// GeneratePreview renders a config for a Spec that has NOT been registered
// yet. The visual editor uses it while the user is still defining a new
// custom protocol, so no fake "already saved" state is ever shown.
func GeneratePreview(s Spec, kernel string, params map[string]any) ([]byte, string, error) {
	if params == nil {
		params = map[string]any{}
	}
	for k, v := range s.Params {
		if _, exists := params[k]; !exists {
			params[k] = v
		}
	}
	if s.ConfigKind == "" {
		s.ConfigKind = KindOutbound
	}
	return generate(s, kernel, params)
}

// GenerateConfig is the low-level, registry-free generator kept for callers
// and tests that only know a protocol id.
func GenerateConfig(protocolID, kernel string, params map[string]any) ([]byte, string, error) {
	s, ok := builtinSpec(strings.ToLower(protocolID))
	if !ok {
		return nil, "", fmt.Errorf("no config generator for protocol %q on kernel %q", protocolID, kernel)
	}
	return generate(s, kernel, params)
}

func str(params map[string]any, k, def string) string {
	if v, ok := params[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return def
}

func port(params map[string]any, def int) int {
	// Different kernels name the same field differently: Mihomo/Xray use
	// "port", sing-box uses "server_port". Accept both from the visual editor.
	for _, k := range []string{"port", "server_port"} {
		if v, ok := params[k]; ok {
			n := 0
			if _, err := fmt.Sscanf(fmt.Sprint(v), "%d", &n); err == nil && n > 0 {
				return n
			}
		}
	}
	return def
}

// extras collects user params that are not consumed as top-level fields so a
// hand-written JSON object is never silently dropped.
func extras(params map[string]any, skip ...string) map[string]any {
	skipMap := map[string]bool{}
	for _, k := range skip {
		skipMap[k] = true
	}
	out := map[string]any{}
	for k, v := range params {
		if !skipMap[k] {
			out[k] = v
		}
	}
	return out
}

func jsonBytes(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
func yamlBytes(v any) ([]byte, error) { return yaml.Marshal(v) }

// alias maps protocol ids to the native type name each kernel expects.
var aliases = map[string]struct{ m, s, x string }{
	"shadowsocks":     {"ss", "shadowsocks", "shadowsocks"},
	"vless-reality":   {"vless", "vless", "vless"},
	"hysteria":        {"hysteria", "hysteria", "hysteria"},
	"hysteria2":       {"hysteria2", "hysteria2", "hysteria2"},
	"tuic":            {"tuic", "tuic", "tuic"},
	"naive":           {"naive", "naive", "naive"},
	"snell":           {"snell", "snell", "snell"},
	"shadowtls":       {"shadowtls", "shadowtls", "shadowtls"},
	"http-proxy":      {"http", "http", "http"},
	"http-connect":    {"http", "http", "http"},
	"upstream-http":   {"http", "http", "http"},
	"socks4":          {"socks5", "socks", "socks"},
	"upstream-socks5": {"socks5", "socks", "socks"},
	"ssh-tunnel":      {"ssh", "ssh", "ssh"},
	"mieru":           {"mieru", "mieru", "mieru"},
	"anytls":          {"anytls", "anytls", "anytls"},
}

func kernelTypes(id string) (m, s, x string) {
	if a, ok := aliases[id]; ok {
		return a.m, a.s, a.x
	}
	return id, id, id
}

func generate(s Spec, kernel string, params map[string]any) ([]byte, string, error) {
	if params == nil {
		params = map[string]any{}
	}
	server := str(params, "server", "127.0.0.1")
	p := port(params, s.DefaultPort)
	if p == 0 {
		p = 443
	}

	// Preserve the historically exact, kernel-specific outputs.
	switch s.ID {
	case "wireguard":
		switch kernel {
		case "mihomo":
			cfg := map[string]any{
				"proxies": []any{map[string]any{
					"name": "wg", "type": "wireguard", "server": server, "port": p,
					"ip":          str(params, "local_address", "10.0.0.2/32"),
					"private-key": str(params, "private_key", ""),
					"public-key":  str(params, "peer_public_key", ""),
				}},
			}
			b, err := yamlBytes(cfg)
			return b, "yaml", err
		case "xray-core":
			cfg := map[string]any{"protocol": "wireguard", "tag": "wg-out", "settings": map[string]any{
				"address":   []string{str(params, "local_address", "10.0.0.2/32")},
				"secretKey": str(params, "private_key", ""),
				"peers":     []any{map[string]any{"endpoint": fmt.Sprintf("%s:%d", server, p), "publicKey": str(params, "peer_public_key", "")}},
			}}
			b, err := jsonBytes(cfg)
			return b, "json", err
		default: // sing-box and anything else
			cfg := map[string]any{
				"type": "wireguard", "tag": "wg-out", "server": server, "server_port": p,
				"local_address":   []string{str(params, "local_address", "10.0.0.2/32")},
				"private_key":     str(params, "private_key", ""),
				"peer_public_key": str(params, "peer_public_key", ""),
				"reserved":        []int{0, 0, 0},
			}
			b, err := jsonBytes(cfg)
			return b, "json", err
		}
	case "openvpn":
		cfg := fmt.Sprintf("# OpenVPN is executed by an external helper; NGG manages its lifecycle.\nclient\ndev tun\nproto %s\nremote %s %d\nresolv-retry infinite\nnobind\npersist-key\npersist-tun\n", str(params, "proto", "udp"), server, p)
		return []byte(cfg), "conf", nil
	case "mlkem-hybrid":
		cfg := map[string]any{
			"type": "ngg-pq-control", "suite": "X25519+ML-KEM-768+HKDF-SHA256+AES-256-GCM",
			"server": server, "port": p, "note": "post-quantum control channel, see internal/pq",
		}
		b, err := jsonBytes(cfg)
		return b, "json", err
	}

	mType, sType, xType := kernelTypes(s.ID)
	rest := extras(params, "server", "port", "kernel", "protocol")

	switch s.ConfigKind {
	case KindPolicy:
		// DIRECT / REJECT / DNS / forwarding are rule actions, not outbounds.
		cfg := map[string]any{
			"ngg_policy": map[string]any{
				"id": s.ID, "name": s.Name, "action": strings.ToUpper(strings.ReplaceAll(s.ID, "-", "_")),
				"kernel": kernel, "params": rest,
			},
		}
		b, err := yamlBytes(cfg)
		return b, "yaml", err
	case KindListener:
		cfg := map[string]any{
			"type": sType, "tag": s.ID + "-in",
			"listen":      str(params, "listen", "127.0.0.1"),
			"listen_port": p,
			"ngg_note":    "inbound listener fragment; wire it into the kernel config",
		}
		if len(rest) > 0 {
			cfg["params"] = rest
		}
		b, err := jsonBytes(cfg)
		return b, "json", err
	case KindHelper:
		lines := []string{
			fmt.Sprintf("# %s — executed by an external helper; NGG manages the lifecycle.", s.Name),
			fmt.Sprintf("protocol=%s", s.ID),
			fmt.Sprintf("server=%s", server),
			fmt.Sprintf("port=%d", p),
		}
		keys := make([]string, 0, len(rest))
		for k := range rest {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("%s=%v", k, rest[k]))
		}
		return []byte(strings.Join(lines, "\n") + "\n"), "conf", nil
	}

	// Default: a real kernel-native outbound fragment. The SAME visual fields
	// are mapped onto each kernel's official syntax; see schema.go.
	return outboundConfig(s, kernel, params, mType, sType, xType, server, p, rest)
}

func hasValue(params map[string]any, key string) (any, bool) {
	v, ok := params[key]
	if !ok || v == nil {
		return nil, false
	}
	if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
		return nil, false
	}
	return v, true
}

// outboundConfig emits the three genuinely different kernel grammars.
// Every key used below matches that kernel's official documentation.
func outboundConfig(s Spec, kernel string, params map[string]any, mType, sType, xType, server string, p int, rest map[string]any) ([]byte, string, error) {
	switch kernel {
	case "mihomo":
		// YAML: proxies: [ { name, type, server, port, ... } ]
		entry := map[string]any{
			"name":   str(params, "name", s.ID),
			"type":   str(params, "type", mType),
			"server": str(params, "server", server),
			"port":   p,
		}
		for _, k := range []string{"cipher", "password", "uuid", "alterId", "network", "tls", "sni", "skip-cert-verify", "udp", "client-fingerprint"} {
			if v, ok := hasValue(params, k); ok {
				entry[k] = v
			}
		}
		// Keep any extra user-supplied keys instead of silently dropping them.
		for k, v := range extras(params, "server", "port", "name", "type", "kernel", "protocol") {
			if _, exists := entry[k]; !exists {
				entry[k] = v
			}
		}
		b, err := yamlBytes(map[string]any{"proxies": []any{entry}})
		return b, "yaml", err

	case "xray-core":
		// JSON: { protocol, tag, settings.vnext[].users[], streamSettings }
		user := map[string]any{}
		if v, ok := hasValue(params, "id"); ok {
			user["id"] = v
		} else if v, ok := hasValue(params, "uuid"); ok {
			user["id"] = v
		}
		for _, k := range []string{"alterId", "security", "flow"} {
			if v, ok := hasValue(params, k); ok {
				user[k] = v
			}
		}
		vnext := map[string]any{
			"address": str(params, "address", server),
			"port":    p,
			"users":   []any{user},
		}
		cfg := map[string]any{
			"protocol": str(params, "protocol", xType),
			"tag":      str(params, "tag", s.ID+"-out"),
			"settings": map[string]any{"vnext": []any{vnext}},
		}
		stream := map[string]any{}
		if v, ok := hasValue(params, "network"); ok {
			stream["network"] = v
		}
		if v, ok := hasValue(params, "stream_security"); ok {
			stream["security"] = v
		}
		if v, ok := hasValue(params, "serverName"); ok {
			stream["tlsSettings"] = map[string]any{"serverName": v}
		}
		ws := map[string]any{}
		if v, ok := hasValue(params, "path"); ok {
			ws["path"] = v
		}
		if v, ok := hasValue(params, "Host"); ok {
			ws["headers"] = map[string]any{"Host": v}
		}
		if len(ws) > 0 {
			stream["wsSettings"] = ws
		}
		if len(stream) > 0 {
			cfg["streamSettings"] = stream
		}
		b, err := jsonBytes(cfg)
		return b, "json", err

	case "external-helper":
		b := []byte(fmt.Sprintf("# %s (%s) handled by an external helper\nserver=%s\nport=%d\n", s.Name, s.ID, server, p))
		return b, "conf", nil
	case "ngg-native", "":
		cfg := map[string]any{"type": s.ID, "tag": s.ID + "-out", "server": server, "server_port": p, "ngg_note": "generated by NGG protocol center"}
		if len(rest) > 0 {
			cfg["params"] = rest
		}
		b, err := jsonBytes(cfg)
		return b, "json", err

	default: // sing-box and any other kernel
		// JSON: { type, tag, server, server_port, tls:{}, transport:{} }
		cfg := map[string]any{
			"type":        str(params, "type", sType),
			"tag":         str(params, "tag", s.ID+"-out"),
			"server":      str(params, "server", server),
			"server_port": p,
		}
		for _, k := range []string{"uuid", "password", "method", "flow", "udp_over_tcp"} {
			if v, ok := hasValue(params, k); ok {
				cfg[k] = v
			}
		}
		tls := map[string]any{}
		if v, ok := hasValue(params, "tls"); ok {
			tls["enabled"] = v
		}
		if v, ok := hasValue(params, "server_name"); ok {
			tls["server_name"] = v
		} else if v, ok := hasValue(params, "sni"); ok {
			tls["server_name"] = v
		}
		if len(tls) > 0 {
			cfg["tls"] = tls
		}
		if v, ok := hasValue(params, "transport"); ok {
			tr := map[string]any{"type": v}
			if w, ok := hasValue(params, "path"); ok {
				tr["path"] = w
			}
			cfg["transport"] = tr
		}
		b, err := jsonBytes(cfg)
		return b, "json", err
	}
}

// ValidateConfig parses generated or hand-edited content with a format-aware
// parser so malformed JSON/YAML never reaches a kernel.
func ValidateConfig(content []byte, format string) error {
	switch strings.ToLower(format) {
	case "json":
		// The visual code editor shows JSONC-style line comments for every
		// field. Strip whole-line comments before parsing (URLs inside
		// string values are never touched).
		var v any
		return json.Unmarshal(StripComments(content, "json"), &v)
	case "yaml", "yml":
		var v any
		return yaml.Unmarshal(content, &v)
	case "conf", "text", "raw", "":
		if len(strings.TrimSpace(string(content))) == 0 {
			return fmt.Errorf("empty config")
		}
		return nil
	}
	return fmt.Errorf("unknown config format %q (use json / yaml / conf)", format)
}

// Formats lists the config formats the editor accepts.
func Formats() []string { return []string{"json", "yaml", "conf"} }

// --------------------------------------------------------------- catalog ---

// mk builds a Spec compactly. kernels is a space separated list.
func mk(id, name string, cat Category, g Group, security, transport string, p int, kind ConfigKind, kernels, description string, features ...string) Spec {
	return Spec{
		ID: id, Name: name, Category: cat, Group: g, Security: security,
		Transport: transport, DefaultPort: p, ConfigKind: kind,
		Kernels: strings.Fields(kernels), Features: features,
		Description: description, Enabled: true,
	}
}

func builtin() []Spec {
	out := []Spec{
		// ---------------- 加密协议 · VPN ----------------
		mk("wireguard", "WireGuard", CatVPN, GroupEncrypted, "NoiseIK / ChaCha20-Poly1305", "UDP", 51820, KindOutbound, "sing-box mihomo xray-core", "现代轻量 VPN；可用 NGG 后量子控制信道做混合密钥保护", "NoiseIK", "Curve25519", "ChaCha20-Poly1305"),
		mk("openvpn", "OpenVPN", CatVPN, GroupEncrypted, "TLS 1.2/1.3", "UDP/TCP", 1194, KindHelper, "external-helper", "传统 SSL VPN，由外部 helper 执行，NGG 管理生命周期", "TLS", "tun/tap"),
		mk("ipsec-ikev2", "IPsec / IKEv2", CatVPN, GroupEncrypted, "ESP / AES-GCM", "UDP", 500, KindHelper, "external-helper", "企业级站点到站点 / 远程接入 VPN", "IKEv2", "ESP"),
		mk("l2tp-ipsec", "L2TP / IPsec", CatVPN, GroupEncrypted, "IPsec ESP", "UDP", 1701, KindHelper, "external-helper", "传统路由器常见 VPN", "L2TP", "IPsec"),
		mk("pptp", "PPTP", CatVPN, GroupEncrypted, "MPPE（已不安全的遗留加密）", "TCP/GRE", 1723, KindHelper, "external-helper", "遗留协议，仅兼容用途，不推荐", "legacy"),
		mk("sstp", "SSTP", CatVPN, GroupEncrypted, "TLS / HTTPS 隧道", "TCP", 443, KindHelper, "external-helper", "基于 HTTPS 的微软 VPN", "TLS"),
		mk("openconnect", "OpenConnect / AnyConnect", CatVPN, GroupEncrypted, "TLS / DTLS", "TCP/UDP", 443, KindHelper, "sing-box external-helper", "Cisco AnyConnect 兼容 VPN", "DTLS"),
		mk("ssh-tunnel", "SSH 端口转发 / 隧道", CatVPN, GroupEncrypted, "SSH / AES-GCM / ChaCha20", "TCP", 22, KindOutbound, "sing-box external-helper", "基于 SSH 的加密隧道与动态端口转发", "SSH", "port-forward"),

		// ---------------- 加密协议 · 加密代理 ----------------
		mk("shadowsocks", "Shadowsocks (AEAD / 2022)", CatEncryptedProxy, GroupEncrypted, "AEAD-2022 / AES-GCM / ChaCha20", "TCP/UDP", 8388, KindOutbound, "mihomo xray-core sing-box", "最常见的加密代理协议", "AEAD", "SIP022"),
		mk("vmess", "VMess", CatEncryptedProxy, GroupEncrypted, "AES-128-GCM / ChaCha20-Poly1305", "TCP/WS/gRPC", 443, KindOutbound, "mihomo xray-core sing-box", "带认证与混淆的加密代理", "AEAD", "alterId"),
		mk("vless", "VLESS", CatEncryptedProxy, GroupEncrypted, "TLS 1.3 / XTLS-Vision", "TCP", 443, KindOutbound, "xray-core sing-box mihomo", "无内建加密、依赖 TLS/REALITY 传输层加密的轻量协议", "XTLS", "TLS"),
		mk("vless-reality", "VLESS + REALITY", CatEncryptedProxy, GroupEncrypted, "REALITY / uTLS / XTLS-Vision", "TCP", 443, KindOutbound, "xray-core sing-box mihomo", "无需自有证书的 TLS 伪装，抗主动探测", "REALITY", "uTLS"),
		mk("trojan", "Trojan", CatEncryptedProxy, GroupEncrypted, "TLS 1.3", "TCP", 443, KindOutbound, "mihomo xray-core sing-box", "伪装成 HTTPS 流量的加密代理", "TLS"),
		mk("hysteria2", "Hysteria2", CatEncryptedProxy, GroupEncrypted, "QUIC / TLS 1.3", "UDP/QUIC", 443, KindOutbound, "mihomo sing-box", "基于 QUIC 的暴力拥塞控制加速协议", "QUIC", "BBR"),
		mk("hysteria", "Hysteria (v1)", CatEncryptedProxy, GroupEncrypted, "QUIC / TLS 1.3", "UDP/QUIC", 443, KindOutbound, "mihomo sing-box", "Hysteria2 的前一代，仅兼容旧节点", "QUIC", "legacy"),
		mk("tuic", "TUIC v5", CatEncryptedProxy, GroupEncrypted, "QUIC / TLS 1.3 / 0-RTT", "UDP/QUIC", 443, KindOutbound, "mihomo sing-box", "低延迟 QUIC 代理，支持 0-RTT", "QUIC", "0-RTT"),
		mk("anytls", "AnyTLS", CatEncryptedProxy, GroupEncrypted, "TLS / 会话复用", "TCP", 443, KindOutbound, "mihomo sing-box", "以会话复用降低握手开销的 TLS 代理", "TLS", "session-reuse"),
		mk("mieru", "Mieru", CatEncryptedProxy, GroupEncrypted, "自研加密 + 流量混淆", "TCP/UDP", 2999, KindOutbound, "mihomo", "强调抗封锁的混淆代理", "obfuscation"),
		mk("snell", "Snell v3", CatEncryptedProxy, GroupEncrypted, "AES-GCM / ChaCha20-Poly1305", "TCP/UDP", 443, KindOutbound, "mihomo", "Surge 生态的轻量加密代理", "AEAD"),
		mk("shadowtls", "ShadowTLS v3", CatEncryptedProxy, GroupEncrypted, "TLS 1.3 伪装", "TCP", 443, KindOutbound, "sing-box", "借用真实 TLS 握手隐藏代理特征", "uTLS"),
		mk("naive", "NaïveProxy", CatEncryptedProxy, GroupEncrypted, "Chrome TLS 栈 / HTTP2", "TCP", 443, KindOutbound, "external-helper", "复用 Chrome 网络栈以规避 TLS 指纹，需外部 helper", "HTTP2", "TLS"),

		// ---------------- 加密协议 · 后量子 ----------------
		mk("mlkem-hybrid", "ML-KEM-768 混合后量子控制信道", CatPQ, GroupEncrypted, "X25519 + ML-KEM-768 + HKDF-SHA256 + AES-256-GCM", "TCP", 18443, KindOutbound, "ngg-native", "NGG 原生实现：混合密钥交换，抗量子计算攻击", "FIPS-203", "X25519"),

		// ---------------- 普通未加密协议 ----------------
		mk("direct", "DIRECT 直连", CatPlaintext, GroupPlaintext, "无加密", "TCP/UDP", 0, KindPolicy, "mihomo sing-box xray-core", "不匹配任何代理，直接连接目标；内核中是规则动作而非 outbound", "policy"),
		mk("reject", "REJECT / BLOCK", CatPlaintext, GroupPlaintext, "无加密", "TCP/UDP", 0, KindPolicy, "mihomo sing-box xray-core", "拒绝连接；内核中以 rule 动作实现", "policy"),
		mk("http-proxy", "HTTP 明文代理", CatPlaintext, GroupPlaintext, "无加密", "TCP", 8080, KindOutbound, "mihomo sing-box xray-core", "标准 HTTP 正向代理，明文传输，仅限可信内网", "plaintext"),
		mk("socks5", "SOCKS5（明文 / 可选认证）", CatPlaintext, GroupPlaintext, "无加密（可选用户名密码认证）", "TCP/UDP", 1080, KindOutbound, "mihomo sing-box xray-core", "通用 SOCKS5 代理，支持 UDP Associate", "UDP-Associate"),
		mk("socks4", "SOCKS4 / 4a", CatPlaintext, GroupPlaintext, "无加密", "TCP", 1080, KindOutbound, "mihomo sing-box", "遗留 SOCKS 版本，仅 TCP、无认证", "legacy"),
		mk("dns-udp", "DNS（UDP/53 明文）", CatPlaintext, GroupPlaintext, "无加密", "UDP", 53, KindListener, "mihomo sing-box xray-core", "明文 DNS 监听 / 转发，用于内网 DNS 分流", "DNS"),
		mk("tcp-forward", "TCP 端口转发", CatPlaintext, GroupPlaintext, "无加密", "TCP", 0, KindListener, "sing-box xray-core", "纯 TCP 透传与端口映射", "port-forward"),
		mk("udp-forward", "UDP 端口转发", CatPlaintext, GroupPlaintext, "无加密", "UDP", 0, KindListener, "sing-box xray-core", "纯 UDP 透传与端口映射", "port-forward"),

		// ---------------- 普通代理协议 ----------------
		mk("http-connect", "HTTP CONNECT 隧道", CatPlainProxy, GroupPlainProxy, "依赖上游代理，自身不加密", "TCP", 3128, KindOutbound, "mihomo sing-box xray-core", "通过 CONNECT 建立任意 TCP 隧道的标准代理方式", "CONNECT"),
		mk("upstream-http", "上游 HTTP 代理", CatPlainProxy, GroupPlainProxy, "依赖上游代理，自身不加密", "TCP", 8080, KindOutbound, "mihomo sing-box xray-core", "把流量交给另一台标准 HTTP 代理服务器", "chain"),
		mk("upstream-socks5", "上游 SOCKS5 代理", CatPlainProxy, GroupPlainProxy, "依赖上游代理，自身不加密", "TCP", 1080, KindOutbound, "mihomo sing-box xray-core", "把流量交给另一台标准 SOCKS5 代理服务器", "chain"),
		mk("relay-chain", "代理链 / Relay", CatPlainProxy, GroupPlainProxy, "依赖链路上各跳，自身不加密", "TCP", 0, KindOutbound, "sing-box mihomo", "多跳普通代理串联，用于出口编排", "chain", "relay"),
		mk("pac", "PAC 自动代理脚本", CatPlainProxy, GroupPlainProxy, "无加密", "HTTP", 80, KindListener, "ngg-native mihomo", "按 URL 规则决定走直连还是某个普通代理", "proxy-auto-config"),
		mk("system-proxy", "系统代理入口", CatPlainProxy, GroupPlainProxy, "无加密", "TCP", 7890, KindListener, "ngg-native", "操作系统级 HTTP/SOCKS 代理设置入口（NGG 下发）", "system"),
		mk("websocket-plain", "WebSocket 明文 (ws://)", CatPlainProxy, GroupPlainProxy, "无加密", "TCP", 80, KindOutbound, "mihomo sing-box xray-core", "明文 WebSocket 承载，仅用于内网或配合上层 TLS", "ws"),
		mk("grpc-plain", "gRPC h2c 明文传输", CatPlainProxy, GroupPlainProxy, "无加密", "TCP", 80, KindOutbound, "xray-core sing-box", "明文 HTTP/2 gRPC 承载，生产环境应改用 TLS", "h2c"),
	}

	// A handful of legacy VPN helpers genuinely need root / TUN.
	for i := range out {
		switch out[i].ID {
		case "openvpn", "ipsec-ikev2", "l2tp-ipsec", "pptp":
			out[i].RequiresRoot = true
		case "mlkem-hybrid":
			out[i].PQCapable = true
		}
		out[i].Ports = []string{}
		if out[i].DefaultPort > 0 {
			out[i].Ports = []string{fmt.Sprintf("%s/%d", strings.ToLower(out[i].Transport), out[i].DefaultPort)}
		}
	}
	return out
}
