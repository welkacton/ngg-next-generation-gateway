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
		name = "ngg0"
	}
	mtu := cfg.MTU
	if mtu <= 0 {
		mtu = 1500
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
	t.pump(runCtx, mtu)
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
	fwd := tcp.NewForwarder(t.stack, 0, 65535, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			r.Complete(true)
			return
		}
		r.Complete(false)
		conn := gonet.NewTCPConn(&wq, ep)
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
	})
	t.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, fwd.HandlePacket)

	ufwd := udp.NewForwarder(t.stack, func(r *udp.ForwarderRequest) {
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			return
		}
		conn := gonet.NewUDPConn(&wq, ep)
		go t.handleUDP(conn, r.ID())
	})
	t.stack.SetTransportProtocolHandler(udp.ProtocolNumber, ufwd.HandlePacket)
}

// handleUDP relays DNS to the configured upstream and drops other UDP so a
// tunnelled machine never silently leaks QUIC traffic around the proxy.
func (t *TunDevice) handleUDP(conn *gonet.UDPConn, id stack.TransportEndpointID) {
	defer conn.Close()
	buf := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			return
		}
		if id.LocalPort != 53 {
			continue
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
		if resp, err := dnsExchange(upstream, buf[:n]); err == nil {
			_, _ = conn.Write(resp)
		}
	}
}

func dnsExchange(upstream string, query []byte) ([]byte, error) {
	c, err := net.DialTimeout("udp", upstream, 4*time.Second)
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

func (t *TunDevice) pump(ctx context.Context, mtu int) {
	t.wg.Add(2)
	go func() {
		defer t.wg.Done()
		buf := make([]byte, mtu+128)
		sizes := make([]int, 1)
		for {
			if ctx.Err() != nil {
				return
			}
			n, err := t.dev.Read([][]byte{buf}, sizes, 0)
			if err != nil {
				return
			}
			if n == 0 || sizes[0] <= 0 {
				continue
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithData(append([]byte(nil), buf[:sizes[0]]...)),
			})
			t.ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
			pkt.DecRef()
			t.pktIn.Add(1)
			t.bytesIn.Add(uint64(sizes[0]))
		}
	}()
	go func() {
		defer t.wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			pkt := t.ep.Read()
			if pkt == nil {
				return
			}
			buf := pkt.ToBuffer()
			payload := buf.Flatten()
			pkt.DecRef()
			if len(payload) == 0 {
				continue
			}
			if _, err := t.dev.Write([][]byte{payload}, 0); err != nil {
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
		return nil
	}
	ip, _, err := parseIPv4CIDR(cfg.IPv4)
	if err != nil {
		return err
	}
	if _, err := runNetsh("interface", "ipv4", "add", "route", "0.0.0.0/0", "interface="+name,
		"nexthop="+ip.String(), "metric=1", "store=active"); err != nil {
		return err
	}
	t.routeAll.Store(true)
	return nil
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
