package substore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNoServer is returned by Render when the node has no address to publish.
var ErrNoServer = errors.New("节点没有可供客户端连接的地址")

// Node is one upstream NGG wants Sub-Store to consume. It mirrors what the
// subscription renderer already resolved: address, port, protocol (NGG
// spelling), credential, and the transport/TLS shape of the inbound.
type Node struct {
	Name       string
	Server     string
	Port       int
	Protocol   string // vless / trojan / shadowsocks (or ss) / socks5 / http
	Credential string
	TLS        bool
	SNI        string
	Transport  string // "" | ws | grpc | h2
	Path       string
	Host       string
	// Method is the Shadowsocks cipher. When empty and the credential is a bare
	// password, DefaultCipher is used and the caller is expected to say so.
	Method string
}

// DefaultCipher is the Shadowsocks cipher used when neither the credential nor
// the provision names one. It matches hosts.SSMethod; the import path and the
// subscription renderer both surface a warning instead of hiding the assumption.
const DefaultCipher = "aes-256-gcm"

// Render produces the plain-JSON document Sub-Store ingests: a JSON array of
// proxy objects using Sub-Store's own normalized key names (the shape its
// `target=json` output has). Sub-Store accepts it as a "Clash Proxy JSON"
// source, so an NGG link can be added to Sub-Store with no conversion.
func Render(nodes ...Node) ([]byte, error) {
	list := make([]object, 0, len(nodes))
	for _, n := range nodes {
		o, err := n.object()
		if err != nil {
			return nil, err
		}
		list = append(list, o)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("生成 Sub-Store 配置失败: %w", err)
	}
	return append(b, '\n'), nil
}

func (n Node) object() (object, error) {
	server := strings.TrimSpace(n.Server)
	if server == "" {
		return nil, ErrNoServer
	}
	if n.Port < 1 || n.Port > 65535 {
		return nil, fmt.Errorf("端口 %d 不在 1..65535", n.Port)
	}
	name := strings.TrimSpace(n.Name)
	if name == "" {
		name = fmt.Sprintf("%s:%d", server, n.Port)
	}

	protocol := strings.ToLower(strings.TrimSpace(n.Protocol))
	canonical, known := typeAliases[protocol]
	if !known {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProtocol, n.Protocol)
	}

	o := object{
		{"type", canonical},
		{"name", name},
		{"server", server},
		{"port", n.Port},
	}

	switch canonical {
	case TypeShadowsocks:
		method, password := ssCipher(n)
		o = append(o, field{"cipher", method}, field{"password", password}, field{"udp", true})

	case TypeVLESS:
		if strings.TrimSpace(n.Credential) == "" {
			return nil, fmt.Errorf("%w：vless 缺少 uuid", ErrUnsupportedProtocol)
		}
		o = append(o, field{"uuid", strings.TrimSpace(n.Credential)}, field{"udp", true})
		o = appendTLS(o, n)
		o = appendTransport(o, n)

	case TypeTrojan:
		if strings.TrimSpace(n.Credential) == "" {
			return nil, fmt.Errorf("%w：trojan 缺少密码", ErrUnsupportedProtocol)
		}
		// Trojan is TLS-only: Sub-Store and every client assume it.
		o = append(o, field{"password", strings.TrimSpace(n.Credential)}, field{"udp", true})
		o = append(o, field{"sni", firstNonEmpty(n.SNI, server)})
		o = appendTransport(o, n)

	case TypeSOCKS5:
		user, pass := splitUserPassword(n.Credential)
		if user != "" {
			o = append(o, field{"username", user})
		}
		if pass != "" {
			o = append(o, field{"password", pass})
		}
		o = append(o, field{"udp", true})

	case TypeHTTP:
		user, pass := splitUserPassword(n.Credential)
		if user != "" {
			o = append(o, field{"username", user})
		}
		if pass != "" {
			o = append(o, field{"password", pass})
		}
		if n.TLS {
			o = append(o, field{"tls", true}, field{"servername", firstNonEmpty(n.SNI, server)})
		}

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProtocol, n.Protocol)
	}
	return o, nil
}

// appendTLS writes the two keys Sub-Store needs for a TLS outbound. A node with
// an SNI is treated as TLS even when the provision did not say so, because an
// SNI without TLS is not a thing any client can use.
func appendTLS(o object, n Node) object {
	if !n.TLS && strings.TrimSpace(n.SNI) == "" {
		return o
	}
	return append(o, field{"tls", true}, field{"servername", firstNonEmpty(n.SNI, n.Server)})
}

// appendTransport writes the transport sub-object in the same spelling Clash
// Meta and Sub-Store share.
func appendTransport(o object, n Node) object {
	switch strings.ToLower(strings.TrimSpace(n.Transport)) {
	case "", "tcp", "raw", "none":
		return o
	case "ws", "websocket":
		opts := object{{"path", firstNonEmpty(n.Path, "/")}}
		if host := strings.TrimSpace(n.Host); host != "" {
			opts = append(opts, field{"headers", object{{"Host", host}}})
		}
		return append(o, field{"network", "ws"}, field{"ws-opts", opts})
	case "grpc":
		opts := object{{"grpc-service-name", strings.TrimSpace(n.Path)}}
		return append(o, field{"network", "grpc"}, field{"grpc-opts", opts})
	case "h2", "http":
		opts := object{{"path", firstNonEmpty(n.Path, "/")}}
		if host := strings.TrimSpace(n.Host); host != "" {
			opts = append(opts, field{"host", object{{"Host", host}}})
		}
		return append(o, field{"network", "h2"}, field{"h2-opts", opts})
	default:
		// An unknown transport is passed through as its own name: dropping it
		// would silently produce a TCP node that cannot connect.
		return append(o, field{"network", strings.ToLower(strings.TrimSpace(n.Transport))})
	}
}

// ssCipher resolves the Shadowsocks cipher/password pair from the node, in the
// same precedence the subscription renderer uses.
func ssCipher(n Node) (method, password string) {
	cred := strings.TrimSpace(n.Credential)
	if m := strings.TrimSpace(n.Method); m != "" {
		if i := strings.Index(cred, ":"); i > 0 && strings.EqualFold(cred[:i], m) {
			return m, cred[i+1:]
		}
		return m, cred
	}
	if i := strings.Index(cred, ":"); i > 0 && i < len(cred)-1 {
		return strings.TrimSpace(cred[:i]), cred[i+1:]
	}
	return DefaultCipher, cred
}

// splitUserPassword accepts "user:password"; a credential without a colon is
// treated as the username, exactly like the rest of the console.
func splitUserPassword(cred string) (user, pass string) {
	cred = strings.TrimSpace(cred)
	if cred == "" {
		return "", ""
	}
	if i := strings.Index(cred, ":"); i >= 0 {
		return cred[:i], cred[i+1:]
	}
	return cred, ""
}

// object is a JSON object that keeps insertion order. A map[string]any would be
// alphabetically sorted by encoding/json, which makes the generated document
// hard to read next to Sub-Store's own output.
type object []field

type field struct {
	key   string
	value any
}

func (o object) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		val, err := json.Marshal(f.value)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(val)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}
