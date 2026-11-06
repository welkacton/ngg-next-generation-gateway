// Package clients implements the client-access registry for phones, PCs and
// Macs (v2.7.0 section C).
//
// The package owns three things and nothing else:
//   - the persisted document <data>/clients.json (0600, atomic replace);
//   - validation of every field that can end up in a client config;
//   - rendering of client configs (sing-box JSON, mihomo YAML, WireGuard
//     .conf, plain SOCKS5 text).
//
// It never invents a public address: when AccessConfig.PublicHost is empty the
// renderer refuses with ErrNoPublicHost instead of guessing a domain, because a
// guessed host is a config that silently fails on the device.
package clients

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Error categories. The API layer maps them to status codes; keeping them as
// sentinels means the HTTP layer never has to parse an error string.
var (
	// ErrNoPublicHost is returned when a config is requested but the operator
	// has not configured a public address yet.
	ErrNoPublicHost = errors.New("未设置对外接入地址：请先在「客户端接入」中填写公网地址与端口")
	// ErrNotFound is returned for unknown client IDs.
	ErrNotFound = errors.New("客户端不存在")
	// ErrUnknownFormat is returned for a format that is not one of the four
	// documented renderers.
	ErrUnknownFormat = errors.New("不支持的配置格式")
)

// ValidationError marks a payload that must be rejected with 422 and must not
// be persisted.
type ValidationError struct {
	Field  string
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return "客户端配置不合法: " + e.Detail
	}
	return "客户端配置不合法: " + e.Field + " " + e.Detail
}

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Detail: fmt.Sprintf(format, args...)}
}

// Supported enums. They mirror web/src/api/ngg.ts byte for byte.
const (
	ProtocolSocks5    = "socks5"
	ProtocolHTTP      = "http"
	ProtocolWireGuard = "wireguard"

	PlatformIOS     = "ios"
	PlatformAndroid = "android"
	PlatformWindows = "windows"
	PlatformMacOS   = "macos"
	PlatformLinux   = "linux"
)

// Config formats.
const (
	FormatSingBox   = "singbox"
	FormatMihomo    = "mihomo"
	FormatWireGuard = "wireguard"
	FormatSocks     = "socks"
)

// onlineWindow is how long a client is still shown as online after its last
// successful subscription fetch. A device that never fetched a config is
// offline, not "probably online".
const onlineWindow = 30 * time.Minute

var protocols = []string{ProtocolSocks5, ProtocolHTTP, ProtocolWireGuard}

var platforms = []string{PlatformIOS, PlatformAndroid, PlatformWindows, PlatformMacOS, PlatformLinux}

// AccessConfig is the shared listener every client config points at.
type AccessConfig struct {
	PublicHost string `json:"public_host"`
	PublicPort int    `json:"public_port"`
	Protocol   string `json:"protocol"`
	TLS        bool   `json:"tls"`
	Note       string `json:"note"`
}

// DefaultAccess matches the contract: no public host yet, so every render will
// 422 until the operator fills it in.
func DefaultAccess() AccessConfig {
	return AccessConfig{PublicHost: "", PublicPort: 1080, Protocol: ProtocolSocks5, TLS: false, Note: ""}
}

// Client is the persisted record. Credential is the subscription/secret token
// of this device; it is written to clients.json (0600) and returned exactly
// once, on create or rotate.
type Client struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Protocol   string `json:"protocol"`
	Enabled    bool   `json:"enabled"`
	Credential string `json:"credential"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
	LastSeen   string `json:"last_seen"`
	QuotaBytes uint64 `json:"quota_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
}

// ClientView is the wire shape. Credential is replaced by CredentialSet so the
// secret can never leak through GET /api/v1/clients.
type ClientView struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Platform      string   `json:"platform"`
	Protocol      string   `json:"protocol"`
	Enabled       bool     `json:"enabled"`
	CredentialSet bool     `json:"credential_set"`
	CreatedAt     string   `json:"created_at"`
	ExpiresAt     string   `json:"expires_at"`
	LastSeen      string   `json:"last_seen"`
	QuotaBytes    uint64   `json:"quota_bytes"`
	UsedBytes     uint64   `json:"used_bytes"`
	Online        bool     `json:"online"`
	Formats       []string `json:"formats"`
}

// Config is the root document stored in clients.json.
type Config struct {
	Access  AccessConfig `json:"access"`
	Clients []Client     `json:"clients"`
}

// View converts a stored client to its public shape at the given instant.
func (c Client) View(now time.Time) ClientView {
	return ClientView{
		ID:            c.ID,
		Name:          c.Name,
		Platform:      c.Platform,
		Protocol:      c.Protocol,
		Enabled:       c.Enabled,
		CredentialSet: c.Credential != "",
		CreatedAt:     c.CreatedAt,
		ExpiresAt:     c.ExpiresAt,
		LastSeen:      c.LastSeen,
		QuotaBytes:    c.QuotaBytes,
		UsedBytes:     c.UsedBytes,
		Online:        isOnline(c, now),
		Formats:       Formats(c),
	}
}

// Formats lists the downloadable configs for a client. "wireguard" is only
// offered for a WireGuard device, and "socks" only for a SOCKS5/HTTP device:
// a WireGuard tunnel has no SOCKS5 endpoint behind it, so offering one would be
// a button that always fails.
func Formats(c Client) []string {
	out := make([]string, 0, 4)
	out = append(out, FormatSingBox, FormatMihomo)
	if c.Protocol == ProtocolWireGuard {
		out = append(out, FormatWireGuard)
	} else {
		out = append(out, FormatSocks)
	}
	return out
}

// ValidFormat reports whether format is one of the four renderers.
func ValidFormat(format string) bool {
	switch format {
	case FormatSingBox, FormatMihomo, FormatWireGuard, FormatSocks:
		return true
	}
	return false
}

// Expired reports whether the client's expiry has passed. An empty ExpiresAt
// means "never expires".
func (c Client) Expired(now time.Time) bool {
	if c.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		// A corrupted timestamp must not silently grant permanent access.
		return true
	}
	return !t.After(now)
}

// Active reports whether the client may be used right now.
func (c Client) Active(now time.Time) bool { return c.Enabled && !c.Expired(now) }

func isOnline(c Client, now time.Time) bool {
	if c.LastSeen == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, c.LastSeen)
	if err != nil {
		return false
	}
	return now.Sub(t) < onlineWindow
}

// ------------------------------------------------------------- validation ---

func normalizeAccess(a AccessConfig) AccessConfig {
	a.Protocol = strings.ToLower(strings.TrimSpace(a.Protocol))
	a.PublicHost = strings.ToLower(strings.TrimSpace(a.PublicHost))
	a.Note = strings.TrimSpace(a.Note)
	if a.Protocol == "" {
		a.Protocol = ProtocolSocks5
	}
	if a.PublicPort == 0 {
		a.PublicPort = 1080
	}
	return a
}

func validateAccess(a AccessConfig) error {
	if !contains(protocols, a.Protocol) {
		return invalid("protocol", "必须是 socks5 / http / wireguard 之一，收到 %q", a.Protocol)
	}
	if a.PublicPort < 1 || a.PublicPort > 65535 {
		return invalid("public_port", "必须在 1..65535 之间，收到 %d", a.PublicPort)
	}
	if a.Note != "" && utf8.RuneCountInString(a.Note) > 200 {
		return invalid("note", "不能超过 200 个字符")
	}
	if a.PublicHost == "" {
		// Legal: the operator may fill the address in later. Rendering is what
		// fails, not saving.
		return nil
	}
	return validateHost(a.PublicHost)
}

// validateHost accepts a bare host name or IPv4 literal. A scheme, a path or an
// embedded port is rejected: the address and the port are separate fields, and
// accepting "host:1080" would produce "host:1080:1080" in every config.
func validateHost(host string) error {
	if strings.Contains(host, "://") {
		return invalid("public_host", "只填主机名或 IP，不要带 http:// 或 socks5:// 前缀")
	}
	if strings.ContainsAny(host, "/\\?#@ \t") {
		return invalid("public_host", "含有非法字符（不允许路径、查询串、@ 或空白）")
	}
	if strings.Contains(host, ":") {
		return invalid("public_host", "不要包含端口；端口使用 public_port 字段（暂不支持 IPv6 字面量）")
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if l == "" {
			return invalid("public_host", "域名格式不合法（存在空标签）")
		}
		if len(l) > 63 {
			return invalid("public_host", "域名标签过长")
		}
		for _, r := range l {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				continue
			}
			return invalid("public_host", "含有非法字符 %q", string(r))
		}
	}
	return nil
}

func validateName(name string) error {
	if name == "" {
		return invalid("name", "不能为空")
	}
	if utf8.RuneCountInString(name) > 64 {
		return invalid("name", "不能超过 64 个字符，收到 %d", utf8.RuneCountInString(name))
	}
	return nil
}

func validatePlatform(p string) error {
	if !contains(platforms, p) {
		return invalid("platform", "必须是 ios / android / windows / macos / linux 之一，收到 %q", p)
	}
	return nil
}

func validateProtocol(p string) error {
	if !contains(protocols, p) {
		return invalid("protocol", "必须是 socks5 / http / wireguard 之一，收到 %q", p)
	}
	return nil
}

// validateExpires accepts "" (never) or an RFC3339 timestamp in the future.
func validateExpires(s string, now time.Time) error {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return invalid("expires_at", "必须是 RFC3339 时间（如 2026-12-31T23:59:59Z），收到 %q", s)
	}
	if !t.After(now) {
		return invalid("expires_at", "必须晚于当前时间")
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
