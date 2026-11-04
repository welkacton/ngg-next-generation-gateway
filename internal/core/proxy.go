package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// ProxyKind is the local access dialect a kernel exposes to the NGG bridge.
type ProxyKind string

const (
	ProxySOCKS5      ProxyKind = "socks5"
	ProxyHTTPConnect ProxyKind = "http-connect"
	ProxyMixed       ProxyKind = "mixed" // SOCKS5 + HTTP CONNECT on one port
	ProxyTransparent ProxyKind = "transparent"
)

// ProxyEndpoint is where Core Adapter Bridge dials a kernel's Worker Mode
// inbound. It is registered per kernel through the standard Core Adapter
// contract, never hardcoded for the three builtin kernels.
type ProxyEndpoint struct {
	Kind     ProxyKind `json:"kind" yaml:"kind"`
	Address  string    `json:"address" yaml:"address"`
	Networks []string  `json:"networks" yaml:"networks"` // tcp | udp
}

// LocalProxyProvider is an OPTIONAL CoreAdapter extension. Stage 2's Protocol
// Dispatcher uses it to find the local TCP/UDP access point of any kernel.
// Kernels that do not implement it fall back to the capability descriptor.
type LocalProxyProvider interface {
	LocalProxy(p RuntimeParams) ProxyEndpoint
}

// ResolveLocalProxy is the single place that maps a registered kernel to a
// dialable local endpoint. New kernels work automatically as long as they
// either implement LocalProxyProvider or declare capability.local_tcp_endpoint.
func ResolveLocalProxy(a CoreAdapter, p RuntimeParams) (ProxyEndpoint, error) {
	if a == nil {
		return ProxyEndpoint{}, fmt.Errorf("nil adapter")
	}
	if provider, ok := a.(LocalProxyProvider); ok {
		ep := provider.LocalProxy(p)
		if ep.Address != "" {
			if ep.Kind == "" {
				ep.Kind = ProxySOCKS5
			}
			if len(ep.Networks) == 0 {
				ep.Networks = []string{"tcp"}
			}
			return ep, nil
		}
	}
	cap := a.Descriptor().Capability
	raw := cap.LocalTCPEndpoint
	if raw == "" {
		return ProxyEndpoint{}, fmt.Errorf("kernel %q registered no local proxy endpoint", a.Descriptor().ID)
	}
	addr := strings.NewReplacer(
		"{{.MixedPort}}", strconv.Itoa(p.MixedPort),
		"{{.SocksPort}}", strconv.Itoa(p.SocksPort),
		"{{.HTTPPort}}", strconv.Itoa(p.HTTPPort),
		"{{.APIPort}}", strconv.Itoa(p.APIPort),
		"{{.DNSPort}}", strconv.Itoa(p.DNSPort),
		"{mixed_port}", strconv.Itoa(p.MixedPort),
		"{socks_port}", strconv.Itoa(p.SocksPort),
	).Replace(raw)
	if !strings.Contains(addr, ":") {
		return ProxyEndpoint{}, fmt.Errorf("invalid local endpoint %q for kernel %q", addr, a.Descriptor().ID)
	}
	kind := ProxyKind(cap.LocalProxyKind)
	if kind == "" {
		kind = ProxySOCKS5
	}
	return ProxyEndpoint{Kind: kind, Address: addr, Networks: []string{"tcp", "udp"}}, nil
}

// IsHealthyEndpoint performs a REAL TCP dial, so the dispatcher never routes
// a connection to a kernel listener that is not actually alive.
func IsHealthyEndpoint(ep ProxyEndpoint) bool {
	if ep.Address == "" {
		return false
	}
	return TCPReachable(ep.Address, 800*time.Millisecond)
}

// TCPReachable is a real dial, used both for endpoint health and for SD-WAN
// probes. It is deliberately not a cached boolean.
func TCPReachable(address string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
