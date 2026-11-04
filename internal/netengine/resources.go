package netengine

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// ResourceManager is NGG's single owner of TUN, TCP/UDP ports, DNS listeners,
// routing tables, firewall marks, interfaces and process identity. Kernels are
// never allowed to grab these resources themselves.
type ResourceManager struct {
	mu         sync.Mutex
	reserved   map[string]map[string]string // kind -> key -> owner
	protected  map[int]string               // port -> owner label
	protectNet []*net.IPNet
	egress     EgressConfig
}

var _ NetworkResourceManager = (*ResourceManager)(nil)

func NewResourceManager(cfg EgressConfig) *ResourceManager {
	r := &ResourceManager{
		reserved:  map[string]map[string]string{},
		protected: map[int]string{},
		egress:    cfg,
	}
	for _, cidr := range []string{"169.254.0.0/16", "10.89.0.0/24", "fd89::/64"} {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			r.protectNet = append(r.protectNet, n)
		}
	}
	return r
}

func (r *ResourceManager) Reserve(kind, key string) error { return r.ReserveFor(kind, key, "ngg") }

func (r *ResourceManager) ReserveFor(kind, key, owner string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reserved[kind] == nil {
		r.reserved[kind] = map[string]string{}
	}
	if existing, ok := r.reserved[kind][key]; ok && existing != owner {
		return fmt.Errorf("resource conflict: %s/%s already owned by %s", kind, key, existing)
	}
	r.reserved[kind][key] = owner
	if kind == "port" {
		var port int
		if _, err := fmt.Sscanf(key, "%d", &port); err == nil {
			r.protected[port] = owner
		}
	}
	return nil
}

func (r *ResourceManager) Release(kind, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.reserved[kind]; m != nil {
		delete(m, key)
	}
	if kind == "port" {
		var port int
		if _, err := fmt.Sscanf(key, "%d", &port); err == nil {
			delete(r.protected, port)
		}
	}
	return nil
}

func (r *ResourceManager) Conflicts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for kind, m := range r.reserved {
		for key, owner := range m {
			out = append(out, fmt.Sprintf("%s/%s=%s", kind, key, owner))
		}
	}
	sort.Strings(out)
	return out
}

func (r *ResourceManager) ProtectPort(port int, owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.protected[port] = owner
}

func (r *ResourceManager) IsProtectedDestination(ip net.IP, port uint16) bool {
	r.mu.Lock()
	_, hit := r.protected[int(port)]
	r.mu.Unlock()
	return hit
}

// AssertNoLoop refuses connections whose destination is NGG's own ingress,
// DNS listener, a kernel's local proxy, or the TUN subnet. This is the
// concrete implementation of architecture revision §8 "防止流量回环".
func (r *ResourceManager) AssertNoLoop(coreID contracts.CoreID, dst net.Addr) error {
	var host string
	var port int
	switch a := dst.(type) {
	case *net.TCPAddr:
		host, port = a.IP.String(), a.Port
	case *net.UDPAddr:
		host, port = a.IP.String(), a.Port
	default:
		return nil
	}
	r.mu.Lock()
	owner, protected := r.protected[port]
	r.mu.Unlock()
	if protected {
		return fmt.Errorf("%w: %s:%d is %s", ErrLoopDetected, host, port, owner)
	}
	ip := net.ParseIP(host)
	if ip != nil {
		for _, n := range r.protectNet {
			if n.Contains(ip) && port != 0 {
				return fmt.Errorf("%w: %s is in NGG reserved subnet", ErrLoopDetected, host)
			}
		}
	}
	return nil
}

func (r *ResourceManager) Snapshot() map[string][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string][]string{}
	for kind, m := range r.reserved {
		for key, owner := range m {
			out[kind] = append(out[kind], key+"="+owner)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// EgressManager owns outbound interface binding and the "kernel remote
// transport must bypass the NGG TUN" rule (§8).
type egressManager struct {
	mu    sync.Mutex
	cfg   EgressConfig
	binds map[contracts.CoreID]Egress
	sel   LinkSelector
}

var _ EgressManager = (*egressManager)(nil)

func newEgressManager(cfg EgressConfig) *egressManager {
	return &egressManager{cfg: cfg, binds: map[contracts.CoreID]Egress{}}
}

func (e *egressManager) SetLinkSelector(s LinkSelector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sel = s
}

func (e *egressManager) Bind(coreID contracts.CoreID, iface string, mark int) error {
	if mark == 0 {
		mark = e.cfg.Mark
	}
	addr := ""
	if iface != "" && net.ParseIP(iface) == nil {
		ip, err := interfaceIPv4(iface)
		if err != nil {
			return err
		}
		addr = ip
	} else if iface != "" {
		addr = iface
	}
	e.mu.Lock()
	e.binds[coreID] = Egress{Interface: iface, Mark: mark, Gateway: net.ParseIP(addr)}
	e.mu.Unlock()
	return nil
}

func (e *egressManager) Release(coreID contracts.CoreID) error {
	e.mu.Lock()
	delete(e.binds, coreID)
	e.mu.Unlock()
	return nil
}

func (e *egressManager) BypassTUN(coreID contracts.CoreID) error {
	e.mu.Lock()
	b := e.binds[coreID]
	if b.Mark == 0 {
		b.Mark = e.cfg.Mark
	}
	e.binds[coreID] = b
	e.mu.Unlock()
	return nil
}

func (e *egressManager) Binding(coreID contracts.CoreID) Egress {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.binds[coreID]
}

// Dialer builds a REAL net.Dialer bound to the selected physical egress. On
// Linux the Control hook sets SO_MARK so policy routing can bypass NGG TUN.
func (e *egressManager) Dialer(coreID contracts.CoreID) *net.Dialer {
	e.mu.Lock()
	b := e.binds[coreID]
	cfg := e.cfg
	sel := e.sel
	e.mu.Unlock()
	if b.Mark == 0 {
		b.Mark = cfg.Mark
	}
	local := ""
	if b.Gateway != nil {
		local = b.Gateway.String()
	} else if cfg.LocalAddr != "" {
		local = cfg.LocalAddr
	}
	var laddr net.Addr
	if local != "" {
		laddr = &net.TCPAddr{IP: net.ParseIP(local)}
	}
	mark := b.Mark
	d := &net.Dialer{LocalAddr: laddr, Timeout: 10 * time.Second}
	if cfg.BypassTUN && mark != 0 {
		d.Control = func(_, _ string, c syscall.RawConn) error {
			var ctrlErr error
			if err := c.Control(func(fd uintptr) { ctrlErr = setSockMark(fd, mark) }); err != nil {
				return err
			}
			return ctrlErr
		}
	}
	if sel != nil {
		_ = sel // Stage 5 link selection is consulted by RoutingEngine.
	}
	return d
}

func (e *egressManager) Config() EgressConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

func interfaceIPv4(name string) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range ifaces {
		if i.Name != name && !strings.EqualFold(i.Name, name) {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.To4().String(), nil
			}
		}
	}
	return "", fmt.Errorf("no IPv4 address on interface %q", name)
}

// RoutingEngine selects the physical egress for a route decision.
type routingEngine struct{ egress *egressManager }

var _ RoutingEngine = (*routingEngine)(nil)

func (r *routingEngine) Select(_ context.Context, d RouteDecision) (Egress, error) {
	if r.egress == nil {
		return Egress{}, fmt.Errorf("egress manager not configured")
	}
	return r.egress.Binding(contracts.CoreID(d.CoreID)), nil
}
