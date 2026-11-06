package nodes

// Node runtime helpers: real key generation, real TCP probing and on-disk
// config-file persistence. No value here is fabricated; a caller that cannot
// obtain real data must report source:"unavailable" with a note (see the
// metrics handler in internal/api/nodes.go).

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

// NodeKeyPair is returned ONLY in the HTTP response. WireGuard private key
// material is generated here and never logged; it is written to a node's
// Params only when the operator explicitly calls the pair endpoint.
type NodeKeyPair struct {
	OK         bool   `json:"ok"`
	Kind       string `json:"kind"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	UUID       string `json:"uuid,omitempty"`
	Password   string `json:"password,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// GenerateKeyPair produces a REAL key pair / credential:
//   - wireguard: crypto/ecdh X25519, base64-encoded
//   - uuid:      16 crypto/rand bytes formatted as a v4 UUID
//   - password:  24 crypto/rand bytes, base64url-encoded
func GenerateKeyPair(kind string) (NodeKeyPair, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	switch kind {
	case "wireguard":
		curve := ecdh.X25519()
		priv, err := curve.GenerateKey(rand.Reader)
		if err != nil {
			return NodeKeyPair{}, fmt.Errorf("generate X25519 key: %w", err)
		}
		return NodeKeyPair{
			OK: true, Kind: kind,
			PrivateKey: base64.StdEncoding.EncodeToString(priv.Bytes()),
			PublicKey:  base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()),
			CreatedAt:  now,
		}, nil
	case "uuid":
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return NodeKeyPair{}, fmt.Errorf("generate uuid: %w", err)
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		u := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		return NodeKeyPair{OK: true, Kind: kind, UUID: u, CreatedAt: now}, nil
	case "password":
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return NodeKeyPair{}, fmt.Errorf("generate password: %w", err)
		}
		return NodeKeyPair{OK: true, Kind: kind, Password: base64.RawURLEncoding.EncodeToString(b), CreatedAt: now}, nil
	default:
		return NodeKeyPair{}, fmt.Errorf("kind 只能是 wireguard / uuid / password")
	}
}

// Probe performs a REAL TCP dial to address:port and returns the elapsed
// latency in milliseconds. A failure is returned as an error (never as a
// synthetic success).
func Probe(address string, port int, timeout time.Duration) (float64, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	target := net.JoinHostPort(strings.TrimSpace(address), fmt.Sprintf("%d", port))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", target)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return float64(time.Since(start).Microseconds()) / 1000.0, nil
}

// SafeNodeID scrubs a node id so it can never escape the node-configs
// directory. filepath.Base strips any directory component, then the
// whitelist removes path separators and leading dots.
func SafeNodeID(id string) string {
	base := filepath.Base(strings.TrimSpace(id))
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, base)
	clean = strings.Trim(clean, ".-")
	if clean == "" {
		clean = "node"
	}
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return clean
}

// ConfigFileExt maps an optional format hint to a safe file extension.
func ConfigFileExt(format string) string {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), ".")) {
	case "yaml", "yml":
		return "yaml"
	case "json":
		return "json"
	case "conf", "config", "toml", "ini":
		return "conf"
	default:
		return "txt"
	}
}

// ValidateConfigContent performs a REAL, format-aware syntax check on the
// content. Unknown formats are accepted (validated=true) because there is no
// parser to honestly claim a failure for them.
func ValidateConfigContent(content, format string) bool {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), ".")) {
	case "json":
		return json.Valid([]byte(content))
	case "yaml", "yml":
		var v any
		return yaml.Unmarshal([]byte(content), &v) == nil
	default:
		return true
	}
}

// WriteConfigFile writes <dataDir>/node-configs/<id>.<ext> atomically with
// mode 0600 and returns the path, byte count and whether the content passed a
// format-aware syntax check.
func WriteConfigFile(dataDir, nodeID, format, content string) (string, int, bool, error) {
	path := filepath.Join(dataDir, "node-configs", SafeNodeID(nodeID)+"."+ConfigFileExt(format))
	validated := ValidateConfigContent(content, format)
	if err := atomicfile.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", 0, false, err
	}
	return path, len(content), validated, nil
}

// Result structures returned by the node runtime endpoints.
type NodeMetrics struct {
	OK             bool    `json:"ok"`
	NodeID         string  `json:"node_id"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryRSS      uint64  `json:"memory_rss"`
	BytesIn        uint64  `json:"bytes_in"`
	BytesOut       uint64  `json:"bytes_out"`
	ActiveSessions int     `json:"active_sessions"`
	SampledAt      string  `json:"sampled_at"`
	Source         string  `json:"source"` // local | unavailable
	Note           string  `json:"note"`
}

type NodeProbeResult struct {
	OK        bool    `json:"ok"`
	NodeID    string  `json:"node_id"`
	Address   string  `json:"address"`
	Port      int     `json:"port"`
	Reachable bool    `json:"reachable"`
	LatencyMs float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
	CheckedAt string  `json:"checked_at"`
}

type NodeConfigFileResult struct {
	OK        bool   `json:"ok"`
	NodeID    string `json:"node_id"`
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	Validated bool   `json:"validated"`
}

type NodePairResult struct {
	OK        bool           `json:"ok"`
	Kind      string         `json:"kind"`
	Paired    bool           `json:"paired"`
	Persisted bool           `json:"persisted"`
	Inbound   map[string]any `json:"inbound"`
	Outbound  map[string]any `json:"outbound"`
}

// NewPairResult builds a NodePairResult from a freshly generated key pair and
// the two node ids. The caller fills persisted after the registry write.
func NewPairResult(kind string, kp NodeKeyPair, inboundID, outboundID string) NodePairResult {
	inbound := map[string]any{"id": inboundID, "private_key": kp.PrivateKey}
	outbound := map[string]any{"id": outboundID, "peer_public_key": kp.PublicKey}
	if kp.UUID != "" {
		inbound["uuid"] = kp.UUID
		outbound["uuid"] = kp.UUID
	}
	if kp.Password != "" {
		inbound["password"] = kp.Password
		outbound["password"] = kp.Password
	}
	return NodePairResult{
		OK: true, Kind: kind, Paired: true,
		Inbound: inbound, Outbound: outbound,
	}
}

// RandomHex is a small crypto/rand helper for generating ids.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(b)
}
