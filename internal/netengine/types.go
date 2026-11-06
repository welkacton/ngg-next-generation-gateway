package netengine

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

var (
	ErrNotRunning      = errors.New("NGG network engine is not running")
	ErrAlreadyRunning  = errors.New("NGG network engine is already running")
	ErrFailClosed      = errors.New("fail-closed: no valid execution path, connection dropped")
	ErrLoopDetected    = errors.New("traffic loop detected: destination is an NGG/kernel listener")
	ErrKernelDown      = errors.New("selected kernel has no healthy local endpoint")
	ErrTUNUnsupported  = errors.New("当前环境无法创建统一 TUN")
	ErrIngressInsecure = errors.New("non-loopback ingress requires authentication")
)

// Config is the Stage 2-5 network engine configuration. It is owned by NGG,
// never by a kernel.
type Config struct {
	Enabled         bool          `json:"enabled" yaml:"enabled"`
	FailClosed      bool          `json:"fail_closed" yaml:"fail_closed"`
	Failover        bool          `json:"failover" yaml:"failover"`
	Ingress         IngressConfig `json:"ingress" yaml:"ingress"`
	TUN             TunConfig     `json:"tun" yaml:"tun"`
	DNS             DNSConfig     `json:"dns" yaml:"dns"`
	Egress          EgressConfig  `json:"egress" yaml:"egress"`
	DefaultCore     string        `json:"default_core" yaml:"default_core"`
	DirectOnFailure bool          `json:"direct_on_failure" yaml:"direct_on_failure"`
}

type IngressConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Listen   string `json:"listen" yaml:"listen"`
	Port     int    `json:"port" yaml:"port"`
	AuthUser string `json:"auth_user" yaml:"auth_user"`
	AuthPass string `json:"auth_pass" yaml:"auth_pass"`
}

type DNSConfig struct {
	Enabled   bool              `json:"enabled" yaml:"enabled"`
	Listen    string            `json:"listen" yaml:"listen"`
	Port      int               `json:"port" yaml:"port"`
	Upstreams []string          `json:"upstreams" yaml:"upstreams"`
	CacheTTL  int               `json:"cache_ttl" yaml:"cache_ttl"`
	Hosts     map[string]string `json:"hosts" yaml:"hosts"`
	Block     []string          `json:"block" yaml:"block"`
}

type EgressConfig struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	Interface string `json:"interface" yaml:"interface"`
	LocalAddr string `json:"local_addr" yaml:"local_addr"`
	Mark      int    `json:"mark" yaml:"mark"`
	BypassTUN bool   `json:"bypass_tun" yaml:"bypass_tun"`
}

func DefaultConfig() Config {
	return Config{
		Enabled: true, FailClosed: true, Failover: true,
		Ingress: IngressConfig{Enabled: true, Listen: "127.0.0.1", Port: 18080},
		TUN:     TunConfig{Name: "NGG", MTU: 1500, IPv4: "10.89.0.1/24", IPv6: "fd89::1/64", AutoRoute: runtime.GOOS == "linux", StrictRoute: true},
		DNS:     DNSConfig{Enabled: true, Listen: "127.0.0.1", Port: 18053, Upstreams: []string{"223.5.5.5:53", "119.29.29.29:53"}, CacheTTL: 60},
		Egress:  EgressConfig{Enabled: true, BypassTUN: true, Mark: 0x4E47},
	}
}

// Metrics are real counters incremented on the data path.
type Metrics struct {
	Connections   atomic.Int64
	Active        atomic.Int64
	BytesIn       atomic.Uint64
	BytesOut      atomic.Uint64
	Direct        atomic.Int64
	Proxied       atomic.Int64
	Dropped       atomic.Int64
	DNSQueries    atomic.Int64
	DNSCacheHits  atomic.Int64
	RuleMatches   atomic.Int64
	KernelErrors  atomic.Int64
	Failovers     atomic.Int64
	TUNPacketsIn  atomic.Uint64
	TUNPacketsOut atomic.Uint64
	UDPFlows      atomic.Int64
	// Why connections were dropped. Dropped stays the total, these break it
	// down so "丢包 506" can be explained instead of guessed at.
	DropBlocked  atomic.Int64
	DropLoop     atomic.Int64
	DropNoKernel atomic.Int64
	DropDial     atomic.Int64
	DropSession  atomic.Int64
	DropRule     atomic.Int64
	DropUDP      atomic.Int64
	StartedAt    time.Time
}

// Drop records exactly one dropped connection, with its reason. Every reject
// path goes through here so the total is never double counted.
func (m *Metrics) Drop(reason string) {
	m.Dropped.Add(1)
	switch reason {
	case "block":
		m.DropBlocked.Add(1)
	case "loop":
		m.DropLoop.Add(1)
	case "no_kernel":
		m.DropNoKernel.Add(1)
	case "session":
		m.DropSession.Add(1)
	case "rule":
		m.DropRule.Add(1)
	case "udp":
		m.DropUDP.Add(1)
	default: // "dial" and anything unexpected
		m.DropDial.Add(1)
	}
}

func (m *Metrics) Snapshot() map[string]any {
	return map[string]any{
		"connections": m.Connections.Load(), "active": m.Active.Load(),
		"bytes_in": m.BytesIn.Load(), "bytes_out": m.BytesOut.Load(),
		"direct": m.Direct.Load(), "proxied": m.Proxied.Load(),
		"dropped": m.Dropped.Load(), "dns_queries": m.DNSQueries.Load(),
		"dns_cache_hits": m.DNSCacheHits.Load(), "rule_matches": m.RuleMatches.Load(),
		"kernel_errors": m.KernelErrors.Load(), "failovers": m.Failovers.Load(),
		"tun_packets_in": m.TUNPacketsIn.Load(), "tun_packets_out": m.TUNPacketsOut.Load(),
		"udp_flows": m.UDPFlows.Load(),
		"drop_reasons": map[string]int64{
			"blocked": m.DropBlocked.Load(), "loop": m.DropLoop.Load(),
			"no_kernel": m.DropNoKernel.Load(), "dial": m.DropDial.Load(),
			"session": m.DropSession.Load(), "rule": m.DropRule.Load(),
			"udp": m.DropUDP.Load(),
		},
		"uptime_sec": int64(time.Since(m.StartedAt).Seconds()),
	}
}

// Packet is the normalized result of parsing a raw IP packet from NGG TUN.
type Packet struct {
	Version  int
	Protocol string // tcp | udp | icmp | other
	Src      net.IP
	Dst      net.IP
	SrcPort  uint16
	DstPort  uint16
	Payload  []byte
	Raw      []byte
	IsDNS    bool
	IsLoop   bool
}

// SessionRec is the runtime record kept by SessionManager.
type SessionRec struct {
	Session   Session
	TenantID  string
	Tags      []string
	BytesIn   uint64
	BytesOut  uint64
	State     string // establishing | active | closed | failed
	LastError string
	mu        sync.Mutex
}

func (r *SessionRec) AddBytes(in, out uint64) {
	r.mu.Lock()
	r.BytesIn += in
	r.BytesOut += out
	r.mu.Unlock()
}

func (r *SessionRec) Close(state, errMsg string) {
	r.mu.Lock()
	r.State = state
	r.LastError = errMsg
	r.mu.Unlock()
}

// StreamAdapterImpl is the Stage-2 implementation of the reserved
// StreamAdapter contract. It reports real connection statistics.
type StreamAdapterImpl struct {
	coreID contracts.CoreID
	bridge *CoreBridge
	stats  ConnectionStats
	mu     sync.Mutex
}

var _ StreamAdapter = (*StreamAdapterImpl)(nil)

func (s *StreamAdapterImpl) Capabilities() contracts.Capability {
	return contracts.Capability{Protocols: []string{"tcp"}, NativeStream: true}
}
func (s *StreamAdapterImpl) RegisterProtocols() []string { return []string{"tcp"} }
func (s *StreamAdapterImpl) State() contracts.CoreState  { return contracts.StateRunning }
func (s *StreamAdapterImpl) OpenTCP(ctx context.Context, dst string) (net.Conn, error) {
	conn, err := s.bridge.Connect(ctx, s.coreID, "tcp", dst)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Total++
	if err != nil {
		s.stats.Failed++
		s.stats.LastError = err.Error()
	} else {
		s.stats.Active++
	}
	return conn, err
}
func (s *StreamAdapterImpl) CloseConn(string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stats.Active > 0 {
		s.stats.Active--
	}
	return nil
}
func (s *StreamAdapterImpl) Stats() ConnectionStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// DropError carries the reason a connection was refused, so the caller can
// count it once under the right bucket instead of guessing.
type DropError struct {
	Reason string
	Err    error
}

func (e *DropError) Error() string { return e.Err.Error() }
func (e *DropError) Unwrap() error { return e.Err }

// DropReasonOf maps an error to a drop bucket. Anything unclassified is a dial
// failure, which is what the majority of real-world rejects are.
func DropReasonOf(err error) string {
	var de *DropError
	if errors.As(err, &de) {
		return de.Reason
	}
	return "dial"
}
