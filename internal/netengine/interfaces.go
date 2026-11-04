// Package netengine contains ONLY interface reservations for Stage 2 of the
// NGG architecture ("Single Unified TUN + Unified Network Engine + Multi-Core
// Protocol Runtime").
//
// HARD RULE (architecture revision §10): interfaces are reserved up front, but
// no unverified implementation may be piled in early. This package therefore
// contains zero executable logic in Phase 1. Nothing here is started, bound,
// or registered at runtime. Adding a real implementation belongs to Stage 2
// and must be validated in a real network environment.
package netengine

import (
	"context"
	"net"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// Stage2NotImplemented is returned by any future stub so callers can never
// mistake a placeholder for a working feature.
const Stage2NotImplemented = "NGG Unified Network Engine is reserved for Stage 2 and is not implemented in Phase 1"

// TunManager: NGG is the ONLY owner of the system TUN. Kernels run in Worker
// Mode and must never create a system-level default TUN or rewrite global
// routes themselves.
type TunManager interface {
	Open(ctx context.Context, cfg TunConfig) error
	Close(ctx context.Context) error
	Read(ctx context.Context, buf []byte) (int, error)
	Write(ctx context.Context, p []byte) (int, error)
	Stats() TunStats
}

type TunConfig struct {
	Name        string
	MTU         int
	IPv4        string
	IPv6        string
	AutoRoute   bool // owned by NGG, never by a kernel
	StrictRoute bool
}

type TunStats struct {
	BytesIn    uint64    `json:"bytes_in"`
	BytesOut   uint64    `json:"bytes_out"`
	PacketsIn  uint64    `json:"packets_in"`
	PacketsOut uint64    `json:"packets_out"`
	Sessions   int       `json:"sessions"`
	Since      time.Time `json:"since"`
}

// PacketProcessor: NGG TUN -> Packet Processor -> Session Manager.
type PacketProcessor interface {
	Process(ctx context.Context, pkt []byte) (Action, error)
}

type Action string

const (
	ActionForward Action = "forward"
	ActionDirect  Action = "direct"
	ActionDrop    Action = "drop"
	ActionDNS     Action = "dns"
)

// SessionManager tracks every connection independently, so each connection's
// execution path is decided by NGG (never by a kernel).
type SessionManager interface {
	Create(ctx context.Context, s Session) (SessionID, error)
	Lookup(id SessionID) (Session, bool)
	Close(id SessionID) error
	Count() int
}

type SessionID uint64

type Session struct {
	ID         SessionID
	Network    string // tcp | udp
	Src        net.Addr
	Dst        net.Addr
	Domain     string
	Rule       string
	CoreID     contracts.CoreID
	CreatedAt  time.Time
	FailClosed bool
	TenantID   string
	Tags       []string
}

// RuleEngine: Stage 3. Reserved here because Protocol Dispatcher needs the
// decision type in its signature. Phase 1 has NO NGG rule engine.
type RuleEngine interface {
	Match(ctx context.Context, s Session) (RouteDecision, error)
}

type RouteDecision struct {
	CoreID   contracts.CoreID
	Outbound string
	Direct   bool
	Block    bool
	Reason   string
	// Additive fields for Stage 2/3/5: multi-kernel chains, tenant isolation
	// and SD-WAN link selection. Keeping them here preserves the contract.
	Chain      []contracts.CoreID
	TenantID   string
	EgressLink string
	DNSPolicy  string
}

// DNSEngine: Stage 3. NGG owns DNS; kernel-level DNS hijacking stays disabled.
type DNSEngine interface {
	Resolve(ctx context.Context, name string, qtype string) ([]net.IP, error)
	Reload(ctx context.Context) error
}

// RoutingEngine / EgressManager: Stage 3. Egress Manager also owns the
// "kernel remote transport must bypass the NGG TUN" rule (§8).
type RoutingEngine interface {
	Select(ctx context.Context, d RouteDecision) (Egress, error)
}

type EgressManager interface {
	Bind(coreID contracts.CoreID, iface string, mark int) error
	Release(coreID contracts.CoreID) error
	BypassTUN(coreID contracts.CoreID) error
}

type Egress struct {
	Interface string `json:"interface"`
	Mark      int    `json:"mark"`
	Gateway   net.IP `json:"gateway"`
}

// ProtocolDispatcher: Stage 2. Picks the kernel for each connection. All
// registered kernels may run at once; the dispatcher is per-connection.
type ProtocolDispatcher interface {
	Dispatch(ctx context.Context, s Session) (StreamAdapter, error)
}

// CoreAdapterBridge: Stage 2. Bridges NGG sessions to a kernel's local
// SOCKS5 / HTTP CONNECT / transparent proxy endpoint.
type CoreAdapterBridge interface {
	Connect(ctx context.Context, coreID contracts.CoreID, network, addr string) (net.Conn, error)
	Close() error
}

// StreamAdapter / DatagramAdapter: the Stage-2 extension of core.CoreAdapter
// (architecture revision §5). They are deliberately NOT part of the Phase-1
// CoreAdapter interface so no kernel is forced to fake unsupported operations.
type StreamAdapter interface {
	Capabilities() contracts.Capability
	RegisterProtocols() []string
	OpenTCP(ctx context.Context, dst string) (net.Conn, error)
	CloseConn(id string) error
	Stats() ConnectionStats
	State() contracts.CoreState
}

type DatagramAdapter interface {
	OpenUDP(ctx context.Context, dst string) (net.PacketConn, error)
	SendTo(connID string, b []byte, addr net.Addr) error
	RecvFrom(connID string, b []byte) (int, net.Addr, error)
}

type ConnectionStats struct {
	Active    int64
	Total     int64
	Failed    int64
	BytesIn   uint64
	BytesOut  uint64
	LastError string
}

// NetworkResourceManager: Stage 2. Central ownership of TUN, TCP/UDP ports,
// DNS listeners, routing tables, firewall rules, interfaces, PID ownership
// and outbound marks — preventing loops and conflicts (§8).
type NetworkResourceManager interface {
	Reserve(kind string, key string) error
	Release(kind string, key string) error
	Conflicts() []string
	AssertNoLoop(coreID contracts.CoreID, dst net.Addr) error
}
