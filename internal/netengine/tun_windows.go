//go:build windows

package netengine

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// TunDevice is the REAL Windows implementation: a wintun adapter plus a
// userspace TCP/IP stack (gVisor). Packets read from the adapter are injected
// into the stack; every accepted TCP flow is handed to the SAME Engine
// dispatcher the SOCKS5 ingress uses, so rules / DNS / outbound selection
// behave identically no matter how the traffic arrived.
//
// It is honest about its prerequisites: creating the adapter needs an elevated
// token (the NGG service runs as LocalSystem) and wintun.dll next to nggd.exe.
type TunDevice struct {
	mu     sync.Mutex
	dev    tun.Device
	name   string
	cfg    TunConfig
	engine *Engine

	ep     *channel.Endpoint
	stack  *stack.Stack
	cancel context.CancelFunc
	wg     sync.WaitGroup

	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64
	pktIn    atomic.Uint64
	pktOut   atomic.Uint64
	active   atomic.Int64
	routeAll atomic.Bool

	openedAt time.Time
}

var _ TunManager = (*TunDevice)(nil)

func NewTunDevice() *TunDevice { return &TunDevice{} }

// AttachEngine wires the packet path to the connection dispatcher.
func (t *TunDevice) AttachEngine(e *Engine) { t.engine = e }

func (t *TunDevice) Open(ctx context.Context, cfg TunConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dev != nil {
		return fmt.Errorf("TUN 已经创建")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("%w：创建 wintun 虚拟网卡需要管理员权限；当前进程不是提升令牌（NGG 服务以 LocalSystem 运行即可）", ErrTUNUnsupported)
	}
	if wintunDLLPath() == "" {
		return fmt.Errorf("%w：未找到 wintun.dll。请先在「接管全局」点击「下载并安装驱动」，或把 wintun.dll 放到 nggd.exe 同目录", ErrTUNUnsupported)
	}

	name := cfg.Name
	if name == "" {
		name = "NGG"
	}
	mtu := cfg.MTU
	if mtu <= 0 {
		mtu = 1500
	}

	// Resolve the physical interface BEFORE the adapter exists: every dial the
	// engine makes while the TUN is up must leave through it, otherwise the
	// traffic would be routed back into the tunnel and loop.
	if idx, err := primaryInterfaceIndex(); err == nil {
		setEgressInterface(idx)
	} else {
		return fmt.Errorf("%w：无法确定物理网卡（%v），为避免流量环路已取消创建", ErrTUNUnsupported, err)
	}

	dev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return fmt.Errorf("%w：创建 wintun 网卡失败：%v", ErrTUNUnsupported, err)
	}
	realName, err := dev.Name()
	if err != nil || realName == "" {
		realName = name
	}

	ip, ipnet, err := parseIPv4CIDR(cfg.IPv4)
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("%w：%v", ErrTUNUnsupported, err)
	}
	if err := configureInterface(realName, ip, ipnet, mtu); err != nil {
		_ = dev.Close()
		return err
	}

	ep := channel.New(1024, uint32(mtu), "")
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(1, ep); err != nil {
		_ = dev.Close()
		return fmt.Errorf("%w：创建协议栈失败：%v", ErrTUNUnsupported, err)
	}
	_ = s.SetSpoofing(1, true)
	_ = s.SetPromiscuousMode(1, true)
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFromSlice(ip.To4()).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		_ = dev.Close()
		return fmt.Errorf("%w：为 %s 分配地址失败：%v", ErrTUNUnsupported, realName, err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})

	t.dev, t.name, t.cfg, t.ep, t.stack = dev, realName, cfg, ep, s
	t.openedAt = time.Now().UTC()
	t.bytesIn.Store(0)
	t.bytesOut.Store(0)
	t.pktIn.Store(0)
	t.pktOut.Store(0)

	runCtx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.installHandlers(runCtx)
	t.pump(runCtx, dev, ep, mtu)
	return nil
}

func configureInterface(name string, ip net.IP, ipnet *net.IPNet, mtu int) error {
	mask := net.IP(ipnet.Mask).String()
	if _, err := runNetsh("interface", "ipv4", "set", "address", "name="+name, "static", ip.String(), mask); err != nil {
		return fmt.Errorf("%w：为 %s 分配 IP 失败：%v", ErrTUNUnsupported, name, err)
	}
	_, _ = runNetsh("interface", "ipv4", "set", "subinterface", name, fmt.Sprintf("mtu=%d", mtu), "store=persistent")
	if _, err := runNetsh("interface", "set", "interface", "name="+name, "admin=enable"); err != nil {
		return fmt.Errorf("%w：启用网卡 %s 失败：%v", ErrTUNUnsupported, name, err)
	}
	return nil
}

func runNetsh(args ...string) (string, error) {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = syscallHideWindow()
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("netsh %s: %v (%s)", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func (t *TunDevice) installHandlers(ctx context.Context) {
	installForwarders(t.stack, func(conn *gonet.TCPConn, id stack.TransportEndpointID) {
		dst := net.JoinHostPort(id.LocalAddress.String(), strconv.Itoa(int(id.LocalPort)))
		client := &net.TCPAddr{IP: net.ParseIP(id.RemoteAddress.String()), Port: int(id.RemotePort)}
		t.active.Add(1)
		go func() {
			defer t.active.Add(-1)
			defer conn.Close()
			if t.engine == nil {
				return
			}
			_ = t.engine.HandleTCP(ctx, conn, ConnRequest{Dst: dst, ClientAddr: client, Network: "tcp"})
		}()
	}, func(conn *gonet.UDPConn, id stack.TransportEndpointID) {
		go t.handleUDP(conn, id)
	})
}

// installForwarders wires a gVisor stack to NGG's protocol handlers. It is
// separated from TunDevice so the packet path can be unit-tested without an
// elevated wintun adapter (see tun_windows_test.go).
func installForwarders(
	s *stack.Stack,
	onTCP func(*gonet.TCPConn, stack.TransportEndpointID),
	onUDP func(*gonet.UDPConn, stack.TransportEndpointID),
) {
	fwd := tcp.NewForwarder(s, 0, 65535, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		var wq waiter.Queue
		// CreateEndpoint performs the 3-way handshake and only returns once
		// the client has ACKed, so it must happen before Complete().
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			r.Complete(true)
			return
		}
		r.Complete(false)
		onTCP(gonet.NewTCPConn(&wq, ep), id)
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, fwd.HandlePacket)

	ufwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		id := r.ID()
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			return
		}
		onUDP(gonet.NewUDPConn(&wq, ep), id)
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, ufwd.HandlePacket)
}

// handleUDP relays DNS to the configured upstream and drops other UDP so a
// tunnelled machine never silently leaks QUIC traffic around the proxy.
// udpIdleTimeout closes a UDP flow that has been silent for this long. The old
// code held every non-DNS flow for 30s doing nothing, which leaked one gVisor
// endpoint per QUIC attempt until the stack ran out.
const udpIdleTimeout = 90 * time.Second

func (t *TunDevice) handleUDP(conn *gonet.UDPConn, id stack.TransportEndpointID) {
	if id.LocalPort == 53 {
		t.handleDNSUDP(conn, id)
		return
	}
	t.relayUDP(conn, id)
}

// handleDNSUDP forwards a DNS query to the configured upstream. The upstream
// dial goes through the engine's pinned egress dialer (see dialer()), otherwise
// the query would be routed back into this very tunnel.
func (t *TunDevice) handleDNSUDP(conn *gonet.UDPConn, id stack.TransportEndpointID) {
	defer conn.Close()
	buf := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			return
		}
		upstream := "223.5.5.5:53"
		if t.engine != nil {
			if cfg := t.engine.Config(); len(cfg.DNS.Upstreams) > 0 {
				upstream = cfg.DNS.Upstreams[0]
			}
		}
		upstream = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(upstream), "udp://"), "tcp://")
		if !strings.Contains(upstream, ":") {
			upstream += ":53"
		}
		if resp, err := dnsExchange(t.dialer(), upstream, buf[:n]); err == nil {
			_, _ = conn.Write(resp)
		}
	}
}

// relayUDP carries a real UDP flow (QUIC, HTTP/3, game traffic, …) instead of
// silently swallowing it.
//
// Silently dropping was expensive: browsers open QUIC first, wait for their
// handshake timeout, then fall back to TCP — seconds of perceived latency and
// retransmissions that look exactly like packet loss. A single userspace UDP
// relay costs one socket and keeps the flow working.
//
// The decision still comes from the dispatcher: only DIRECT flows are relayed.
// Proxied UDP needs SOCKS5 UDP ASSOCIATE, which this build does not implement,
// so those flows are closed immediately (a fast, explicit refusal makes the
// client fall back to TCP at once rather than time out).
func (t *TunDevice) relayUDP(conn *gonet.UDPConn, id stack.TransportEndpointID) {
	defer conn.Close()
	host := id.LocalAddress.String()
	port := int(id.LocalPort)
	dst := net.JoinHostPort(host, strconv.Itoa(port))

	if t.engine != nil {
		decision, err := t.engine.dispatcher.Decide(context.Background(), Session{
			Network: "udp", Dst: &net.UDPAddr{IP: net.ParseIP(host), Port: port},
			CreatedAt: time.Now().UTC(),
		})
		switch {
		case err != nil:
			t.engine.metrics.Drop("rule")
			return
		case decision.Block:
			t.engine.metrics.Drop("block")
			return
		case !decision.Direct:
			t.engine.metrics.Drop("udp")
			return
		}
	}

	upstream, err := t.dialer().Dial("udp", dst)
	if err != nil {
		if t.engine != nil {
			t.engine.metrics.Drop("dial")
		}
		return
	}
	defer upstream.Close()
	if t.engine != nil {
		t.engine.metrics.UDPFlows.Add(1)
	}

	var once sync.Once
	shut := func() {
		once.Do(func() {
			_ = conn.Close()
			_ = upstream.Close()
		})
	}

	go func() { // client -> upstream (upload)
		buf := make([]byte, 65535)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			n, err := conn.Read(buf)
			if err != nil {
				shut()
				return
			}
			if _, err := upstream.Write(buf[:n]); err != nil {
				shut()
				return
			}
			if t.engine != nil {
				t.engine.metrics.BytesOut.Add(uint64(n))
			}
		}
	}()

	buf := make([]byte, 65535)
	for { // upstream -> client (download)
		_ = upstream.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		n, err := upstream.Read(buf)
		if err != nil {
			shut()
			return
		}
		if _, err := conn.Write(buf[:n]); err != nil {
			shut()
			return
		}
		if t.engine != nil {
			t.engine.metrics.BytesIn.Add(uint64(n))
		}
	}
}

// dialer returns the engine's pinned egress dialer. Using a plain net.Dial
// here would be a trap: once the default route points at the TUN, an unpinned
// socket sends the packet straight back into the tunnel, whose only handler
// dials again — an endless loop and no name resolution at all.
func (t *TunDevice) dialer() *net.Dialer {
	const fallback = 4 * time.Second
	if t.engine == nil {
		return &net.Dialer{Timeout: fallback}
	}
	d := t.engine.egress.Dialer("ngg-direct")
	if d.LocalAddr == nil {
		return d
	}
	// The bound source is stored as a TCP address; it cannot be reused for a
	// UDP socket, but the Control hook (IP_UNICAST_IF) must be kept.
	return &net.Dialer{Timeout: fallback, Control: d.Control}
}

func dnsExchange(d *net.Dialer, upstream string, query []byte) ([]byte, error) {
	if d == nil {
		d = &net.Dialer{Timeout: 4 * time.Second}
	}
	c, err := d.Dial("udp", upstream)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// pump moves packets in both directions between the adapter and the userspace
// stack: Read() takes what the OS sent into the tunnel, Write() returns the
// stack's replies.
//
// The outbound direction must use ReadContext. channel.Endpoint.Read() is
// NON-BLOCKING: it returns nil the moment the queue is empty, so the writer
// goroutine used to exit on its very first iteration (taken before the stack
// had queued anything). The adapter then received traffic and could never send
// a single packet back — the machine showed "已发送 16 MB / 已接收 0" and every
// browser request died with ERR_CONNECTION_TIMED_OUT.
func (t *TunDevice) pump(ctx context.Context, dev tun.Device, ep *channel.Endpoint, mtu int) {
	t.wg.Add(2)
	go func() {
		defer t.wg.Done()
		buf := make([]byte, mtu+128)
		sizes := make([]int, 1)
		for {
			if ctx.Err() != nil {
				return
			}
			n, err := dev.Read([][]byte{buf}, sizes, 0)
			if err != nil {
				return
			}
			if n == 0 || sizes[0] <= 0 {
				continue
			}
			// The wintun ring buffer is reused by the next Read, so the bytes
			// must be copied before they are handed to the stack.
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithData(append([]byte(nil), buf[:sizes[0]]...)),
			})
			ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
			pkt.DecRef()
			t.pktIn.Add(1)
			t.bytesIn.Add(uint64(sizes[0]))
		}
	}()
	go func() {
		defer t.wg.Done()
		for {
			pkt := ep.ReadContext(ctx) // blocks until the stack emits a packet
			if pkt == nil {
				return // context cancelled or endpoint closed
			}
			// Flatten() allocates and copies, so the bytes stay valid after the
			// packet buffer is released.
			buf := pkt.ToBuffer()
			payload := buf.Flatten()
			buf.Release()
			pkt.DecRef()
			if len(payload) == 0 {
				continue
			}
			if _, err := dev.Write([][]byte{payload}, 0); err != nil {
				return
			}
			t.pktOut.Add(1)
			t.bytesOut.Add(uint64(len(payload)))
		}
	}()
}

func (t *TunDevice) Close(_ context.Context) error {
	t.mu.Lock()
	dev, name, cancel := t.dev, t.name, t.cancel
	t.dev, t.stack, t.ep, t.cancel = nil, nil, nil, nil
	t.mu.Unlock()
	if dev == nil {
		return nil
	}
	_ = t.SetDefaultRoute(false)
	if cancel != nil {
		cancel()
	}
	_ = dev.Close()
	t.wg.Wait()
	if name != "" {
		_, _ = runNetsh("interface", "set", "interface", "name="+name, "admin=disable")
	}
	setEgressInterface(0)
	return nil
}

func (t *TunDevice) Read(_ context.Context, buf []byte) (int, error) {
	t.mu.Lock()
	dev := t.dev
	t.mu.Unlock()
	if dev == nil {
		return 0, ErrTUNUnsupported
	}
	sizes := make([]int, 1)
	n, err := dev.Read([][]byte{buf}, sizes, 0)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return sizes[0], nil
}

func (t *TunDevice) Write(_ context.Context, p []byte) (int, error) {
	t.mu.Lock()
	dev := t.dev
	t.mu.Unlock()
	if dev == nil {
		return 0, ErrTUNUnsupported
	}
	n, err := dev.Write([][]byte{p}, 0)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return len(p), nil
}

func (t *TunDevice) Stats() TunStats {
	s := TunStats{Since: t.openedAt}
	s.BytesIn = t.bytesIn.Load()
	s.BytesOut = t.bytesOut.Load()
	s.PacketsIn = t.pktIn.Load()
	s.PacketsOut = t.pktOut.Load()
	s.Sessions = int(t.active.Load())
	return s
}

// Status is the raw material for the console card.
func (t *TunDevice) Status() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	open := t.dev != nil
	out := map[string]any{
		"open":       open,
		"adapter":    t.name,
		"mtu":        t.cfg.MTU,
		"ipv4":       t.cfg.IPv4,
		"route_all":  t.routeAll.Load(),
		"elevated":   windows.GetCurrentProcessToken().IsElevated(),
		"wintun_dll": wintunDLLPath(),
		"os":         runtime.GOOS,
		"stack":      "gVisor netstack (userspace)",
		"stats":      t.Stats(),
	}
	if open {
		out["since"] = t.openedAt
	}
	return out
}

// setDefaultRoute adds/removes 0.0.0.0/0 pointing at the adapter. It is the
// LAST step: if the proxy chain is broken the machine would lose connectivity,
// so the caller must have verified the chain first.
func (t *TunDevice) SetDefaultRoute(enable bool) error {
	t.mu.Lock()
	name := t.name
	cfg := t.cfg
	t.mu.Unlock()
	if name == "" {
		return fmt.Errorf("TUN 未就绪")
	}
	if !enable {
		if t.routeAll.Load() {
			_, _ = runNetsh("interface", "ipv4", "delete", "route", "0.0.0.0/0", "interface="+name)
			t.routeAll.Store(false)
		}
		setEgressInterface(0)
		return nil
	}
	ip, _, err := parseIPv4CIDR(cfg.IPv4)
	if err != nil {
		return err
	}
	if _, err := runNetsh("interface", "ipv4", "add", "route", "0.0.0.0/0", "interface="+name,
		"nexthop="+ip.String(), "metric=1", "store=active"); err != nil && !netshAlreadyExists(err.Error()) {
		return err
	}
	t.routeAll.Store(true)
	return nil
}

// netshAlreadyExists reports whether netsh refused an operation because the
// object is already in the requested state. Windows returns exit code 1 with
// "对象已存在。" (or "The object already exists.") for a route that is present,
// which is exactly what a repeated takeover wants — not a failure.
func netshAlreadyExists(text string) bool {
	low := strings.ToLower(text)
	for _, marker := range []string{
		"对象已存在", "已存在", "已经存在",
		"already exists", "already present", "object already exists",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

var (
	modIphlpapi          = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetBestInterface = modIphlpapi.NewProc("GetBestInterface")
)

// primaryInterfaceIndex asks Windows which interface it would use right now.
// It must be resolved BEFORE the default route moves onto the TUN, otherwise
// the answer is the TUN itself.
func primaryInterfaceIndex() (uint32, error) {
	ip := net.ParseIP("8.8.8.8").To4()
	if ip == nil {
		return 0, fmt.Errorf("no probe address")
	}
	dest := *(*uint32)(unsafe.Pointer(&ip[0]))
	var idx uint32
	r, _, _ := procGetBestInterface.Call(uintptr(dest), uintptr(unsafe.Pointer(&idx)))
	if r != 0 {
		return 0, fmt.Errorf("GetBestInterface 返回 %d", r)
	}
	return idx, nil
}

func parseIPv4CIDR(s string) (net.IP, *net.IPNet, error) {
	if strings.TrimSpace(s) == "" {
		s = "10.89.0.1/24"
	}
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, nil, fmt.Errorf("TUN IPv4 地址 %q 不合法: %w", s, err)
	}
	if ip.To4() == nil {
		return nil, nil, fmt.Errorf("TUN IPv4 地址 %q 不是 IPv4", s)
	}
	return ip, ipnet, nil
}
