package netengine

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// CoreBridge is the Stage 2 Core Adapter Bridge. It translates an NGG session
// into a REAL connection to a kernel's isolated local SOCKS5 / HTTP CONNECT
// inbound. Nothing here pretends a kernel is reachable: every endpoint is
// dialed for real before traffic is handed over.
type CoreBridge struct {
	registry  *core.Registry
	ports     core.PortAllocator
	resources *ResourceManager
	metrics   *Metrics
	mu        sync.RWMutex
	closed    bool
	conns     map[net.Conn]struct{}
}

var _ CoreAdapterBridge = (*CoreBridge)(nil)

func NewCoreBridge(reg *core.Registry, ports core.PortAllocator, resources *ResourceManager, metrics *Metrics) *CoreBridge {
	return &CoreBridge{registry: reg, ports: ports, resources: resources, metrics: metrics, conns: map[net.Conn]struct{}{}}
}

func (b *CoreBridge) endpoint(coreID contracts.CoreID) (core.ProxyEndpoint, error) {
	a, err := b.registry.Get(coreID)
	if err != nil {
		return core.ProxyEndpoint{}, err
	}
	p, ok := b.ports.Get(coreID)
	if !ok {
		return core.ProxyEndpoint{}, fmt.Errorf("kernel %q has no isolated resource block", coreID)
	}
	p.Dir = ""
	return core.ResolveLocalProxy(a, p)
}

// Endpoints reports the resolved local endpoint AND its real reachability for
// every registered kernel. Used by the dispatcher and the /network API.
func (b *CoreBridge) Endpoints() []map[string]any {
	out := []map[string]any{}
	for _, d := range b.registry.List() {
		ep, err := b.endpoint(d.ID)
		entry := map[string]any{"core_id": d.ID, "kind": "", "address": "", "healthy": false}
		if err == nil {
			entry["kind"] = string(ep.Kind)
			entry["address"] = ep.Address
			entry["healthy"] = core.IsHealthyEndpoint(ep)
		} else {
			entry["error"] = err.Error()
		}
		out = append(out, entry)
	}
	return out
}

// Connect establishes the tunnel to dst THROUGH the selected kernel.
func (b *CoreBridge) Connect(ctx context.Context, coreID contracts.CoreID, network, dst string) (net.Conn, error) {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("core bridge is closed")
	}
	if network == "udp" {
		return nil, fmt.Errorf("UDP is handled by the SOCKS5 UDP ASSOCIATE relay, not Connect(tcp)")
	}
	ep, err := b.endpoint(coreID)
	if err != nil {
		return nil, err
	}
	if ep.Kind != core.ProxySOCKS5 && ep.Kind != core.ProxyMixed && ep.Kind != core.ProxyHTTPConnect {
		return nil, fmt.Errorf("kernel %q endpoint kind %q is not a supported local proxy", coreID, ep.Kind)
	}
	if b.resources != nil {
		if tcpAddr, err := net.ResolveTCPAddr("tcp", dst); err == nil {
			if err := b.resources.AssertNoLoop(coreID, tcpAddr); err != nil {
				return nil, err
			}
		}
	}
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", ep.Address)
	if err != nil {
		if b.metrics != nil {
			b.metrics.KernelErrors.Add(1)
		}
		return nil, fmt.Errorf("kernel %q local endpoint %s unreachable: %w", coreID, ep.Address, err)
	}
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	if ep.Kind == core.ProxyHTTPConnect {
		err = handshakeHTTPConnect(conn, dst, "")
	} else {
		err = handshakeSOCKS5(conn, dst, "", "")
	}
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		if b.metrics != nil {
			b.metrics.KernelErrors.Add(1)
		}
		return nil, fmt.Errorf("kernel %q proxy handshake to %s failed: %w", coreID, dst, err)
	}
	b.mu.Lock()
	b.conns[conn] = struct{}{}
	b.mu.Unlock()
	return &trackedConn{Conn: conn, bridge: b}, nil
}

func (b *CoreBridge) untrack(c net.Conn) {
	b.mu.Lock()
	delete(b.conns, c)
	b.mu.Unlock()
}

func (b *CoreBridge) Close() error {
	b.mu.Lock()
	b.closed = true
	conns := make([]net.Conn, 0, len(b.conns))
	for c := range b.conns {
		conns = append(conns, c)
	}
	b.conns = map[net.Conn]struct{}{}
	b.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return nil
}

type trackedConn struct {
	net.Conn
	bridge *CoreBridge
	once   sync.Once
}

func (t *trackedConn) Close() error {
	t.once.Do(func() { t.bridge.untrack(t.Conn) })
	return t.Conn.Close()
}

func (t *trackedConn) CloseWrite() error {
	if c, ok := t.Conn.(interface{ CloseWrite() error }); ok {
		return c.CloseWrite()
	}
	return nil
}

func (t *trackedConn) CloseRead() error {
	if c, ok := t.Conn.(interface{ CloseRead() error }); ok {
		return c.CloseRead()
	}
	return nil
}

// handshakeSOCKS5 performs RFC 1928 no-auth / user-pass negotiation followed
// by a CONNECT request. Hostnames are passed through so the KERNEL resolves
// them remotely (NGG DNS engine is used only for the DIRECT path / policy).
func handshakeSOCKS5(conn net.Conn, dst, user, pass string) error {
	method := byte(0x00)
	methods := []byte{0x05, 0x01, 0x00}
	if user != "" {
		method = 0x02
		methods = []byte{0x05, 0x01, 0x02}
	}
	if _, err := conn.Write(methods); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 0x05 {
		return fmt.Errorf("bad SOCKS5 version %d", reply[0])
	}
	if reply[1] != method {
		return fmt.Errorf("SOCKS5 method rejected (got %d)", reply[1])
	}
	if method == 0x02 {
		auth := []byte{0x01, byte(len(user))}
		auth = append(auth, []byte(user)...)
		auth = append(auth, byte(len(pass)))
		auth = append(auth, []byte(pass)...)
		if _, err := conn.Write(auth); err != nil {
			return err
		}
		ar := make([]byte, 2)
		if _, err := io.ReadFull(conn, ar); err != nil {
			return err
		}
		if ar[1] != 0 {
			return errors.New("SOCKS5 authentication failed")
		}
	}
	host, portStr, err := net.SplitHostPort(dst)
	if err != nil {
		return fmt.Errorf("invalid destination %q: %w", dst, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid destination port %q", portStr)
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("hostname too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[1] != 0 {
		return fmt.Errorf("SOCKS5 CONNECT rejected with REP=%d", head[1])
	}
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(conn, lb); err != nil {
			return err
		}
		skip = int(lb[0])
	default:
		return fmt.Errorf("unknown SOCKS5 ATYP %d", head[3])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip+2)); err != nil {
		return err
	}
	return nil
}

func handshakeHTTPConnect(conn net.Conn, dst, auth string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", dst, dst)
	if auth != "" {
		fmt.Fprintf(&b, "Proxy-Authorization: Basic %s\r\n", auth)
	}
	b.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return err
	}
	resp, err := httpReadResponse(conn)
	if err != nil {
		return err
	}
	if resp != 200 {
		return fmt.Errorf("HTTP CONNECT rejected with status %d", resp)
	}
	return nil
}

func httpReadResponse(r io.Reader) (int, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0, fmt.Errorf("malformed HTTP response %q", line)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return 0, err
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}
	return code, nil
}
