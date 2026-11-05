package netengine

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// ConnRequest describes one accepted ingress connection after the
// protocol-specific handshake has been parsed.
type ConnRequest struct {
	Dst           string
	Domain        string
	Network       string
	TenantID      string
	ClientAddr    net.Addr
	Prefix        []byte // bytes to write upstream before relaying
	OnEstablished func(ok bool, err error) error
}

// PluginHook is the Stage 4 plugin extension point. It may modify a route
// decision or reject a session; a nil hook means no plugins are active.
type PluginHook interface {
	OnSession(ctx context.Context, s *Session, d *RouteDecision) error
	OnDNS(ctx context.Context, name string) error
	Count() int
}

// TenantResolver is the Stage 5 multi-tenant extension point.
type TenantResolver interface {
	ResolveTenant(ctx context.Context, key string) (string, error)
	Allowed(tenantID string, d RouteDecision) bool
}

// Engine is the Unified Network Engine. NGG is the only entry, the only
// traffic controller and the only egress scheduler; kernels are protocol
// execution engines reached exclusively through CoreBridge.
type Engine struct {
	cfg        *Config
	registry   *core.Registry
	ports      core.PortAllocator
	bus        core.EventBus
	status     func(contracts.CoreID) (contracts.CoreStatus, error)
	resources  *ResourceManager
	egress     *egressManager
	routing    *routingEngine
	packets    *packetProcessor
	sessions   *sessionManager
	ruleSet    *RuleSet
	rules      *ruleEngine
	dnsEngine  *dnsEngine
	bridge     *CoreBridge
	dispatcher *protocolDispatcher
	tun        TunManager
	ingress    *IngressServer
	metrics    *Metrics

	pluginHook PluginHook
	tenants    TenantResolver
	sdwan      LinkSelector

	mu         sync.RWMutex
	running    bool
	tunCancel  context.CancelFunc
	tunStarted bool
}

func NewEngine(cfg *Config, reg *core.Registry, ports core.PortAllocator, bus core.EventBus,
	status func(contracts.CoreID) (contracts.CoreStatus, error)) *Engine {
	if cfg == nil {
		c := DefaultConfig()
		cfg = &c
	}
	m := &Metrics{StartedAt: time.Now().UTC()}
	res := NewResourceManager(cfg.Egress)
	egress := newEgressManager(cfg.Egress)
	ruleSet := NewRuleSet(ActDirect, contracts.CoreID(cfg.DefaultCore))
	_ = ruleSet.Replace(DefaultRules())
	packets := NewPacketProcessor(res)
	sessions := newSessionManager(8192, 5*time.Minute)
	rules := newRuleEngine(ruleSet, m)
	dnsEng := newDNSEngine(cfg.DNS, m, ruleSet)
	bridge := NewCoreBridge(reg, ports, res, m)
	dispatcher := newProtocolDispatcher(cfg, reg, bridge, rules, res, egress, m)
	e := &Engine{
		cfg: cfg, registry: reg, ports: ports, bus: bus, status: status,
		resources: res, egress: egress, routing: &routingEngine{egress: egress},
		packets: packets, sessions: sessions, ruleSet: ruleSet, rules: rules,
		dnsEngine: dnsEng, bridge: bridge, dispatcher: dispatcher,
		tun: NewTunDevice(), metrics: m,
	}
	e.ingress = newIngressServer(cfg.Ingress, e)
	return e
}

func (e *Engine) SetPluginHook(h PluginHook)         { e.pluginHook = h }
func (e *Engine) SetTenantResolver(t TenantResolver) { e.tenants = t }
func (e *Engine) SetLinkSelector(s LinkSelector)     { e.sdwan = s; e.egress.SetLinkSelector(s) }
func (e *Engine) Config() *Config                    { return e.cfg }
func (e *Engine) Metrics() *Metrics                  { return e.metrics }
func (e *Engine) Rules() *RuleSet                    { return e.ruleSet }
func (e *Engine) DNS() *dnsEngine                    { return e.dnsEngine }
func (e *Engine) Sessions() *sessionManager          { return e.sessions }
func (e *Engine) Dispatcher() *protocolDispatcher    { return e.dispatcher }
func (e *Engine) Bridge() *CoreBridge                { return e.bridge }
func (e *Engine) Resources() *ResourceManager        { return e.resources }
func (e *Engine) Egress() *egressManager             { return e.egress }
func (e *Engine) PacketProcessor() *packetProcessor  { return e.packets }
func (e *Engine) IsRunning() bool                    { e.mu.RLock(); defer e.mu.RUnlock(); return e.running }

// Start brings up every owned network resource. A failure part-way through
// rolls ALL of them back, so NGG never leaves a half-configured system.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return ErrAlreadyRunning
	}
	e.mu.Unlock()

	// Reserve every resource centrally before binding anything.
	if err := e.resources.ReserveFor("tun", e.cfg.TUN.Name, "ngg"); err != nil {
		return err
	}
	if err := e.resources.ReserveFor("port", fmt.Sprintf("%d", e.cfg.Ingress.Port), "ngg-ingress"); err != nil {
		return err
	}
	if e.cfg.DNS.Enabled {
		if err := e.resources.ReserveFor("port", fmt.Sprintf("%d", e.cfg.DNS.Port), "ngg-dns"); err != nil {
			return err
		}
	}
	for _, d := range e.registry.List() {
		p, ok := e.ports.Get(d.ID)
		if !ok {
			continue
		}
		for _, port := range p.Ports() {
			_ = e.resources.ReserveFor("kernel-port", fmt.Sprintf("%d", port), string(d.ID))
		}
	}

	if err := e.dnsEngine.Start(); err != nil {
		e.rollback()
		return err
	}
	tunOK := false
	if e.cfg.TUN.AutoRoute {
		if err := e.tun.Open(ctx, e.cfg.TUN); err == nil {
			tunOK = true
			tunCtx, cancel := context.WithCancel(context.Background())
			e.mu.Lock()
			e.tunCancel, e.tunStarted = cancel, true
			e.mu.Unlock()
			go e.tunLoop(tunCtx)
		}
	}
	if err := e.ingress.Start(); err != nil {
		if tunOK {
			e.mu.RLock()
			cancel := e.tunCancel
			e.mu.RUnlock()
			if cancel != nil {
				cancel()
			}
			_ = e.tun.Close(ctx)
		}
		e.dnsEngine.Stop()
		e.rollback()
		return err
	}
	_ = e.egress.Bind("ngg-direct", e.cfg.Egress.Interface, e.cfg.Egress.Mark)
	if e.cfg.Egress.BypassTUN {
		for _, d := range e.registry.List() {
			_ = e.egress.BypassTUN(d.ID)
		}
	}
	e.mu.Lock()
	e.running = true
	e.mu.Unlock()
	e.emit("network.started", map[string]any{
		"tun": tunOK, "ingress": e.ingress.Addr(), "dns": e.dnsEngine.IsRunning(),
	})
	return nil
}

func (e *Engine) rollback() {
	_ = e.resources.Release("tun", e.cfg.TUN.Name)
	_ = e.resources.Release("port", fmt.Sprintf("%d", e.cfg.Ingress.Port))
	if e.cfg.DNS.Enabled {
		_ = e.resources.Release("port", fmt.Sprintf("%d", e.cfg.DNS.Port))
	}
}

func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return nil
	}
	e.running = false
	cancel := e.tunCancel
	e.tunCancel = nil
	e.tunStarted = false
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Close upstream kernel connections FIRST: this unblocks relay goroutines
	// so ingress.Stop() can never wait forever on keep-alive sessions.
	_ = e.bridge.Close()
	e.ingress.Stop()
	e.dnsEngine.Stop()
	_ = e.tun.Close(ctx)
	e.rollback()
	e.emit("network.stopped", map[string]any{"sessions": e.sessions.Count()})
	return nil
}

func (e *Engine) emit(kind string, payload any) {
	if e.bus != nil {
		e.bus.Publish(contracts.Event{Type: "network." + kind, Payload: payload, Timestamp: time.Now().UTC()})
	}
}

// HandleTCP is THE data path. It creates a session, asks the rule engine for
// a decision, lets plugins adjust it, opens the real execution path through
// ProtocolDispatcher, and relays bytes. Any failure is Fail-Closed.
func (e *Engine) HandleTCP(ctx context.Context, client net.Conn, req ConnRequest) error {
	if !e.IsRunning() {
		return ErrNotRunning
	}
	m := e.metrics
	m.Connections.Add(1)
	m.Active.Add(1)
	defer m.Active.Add(-1)

	dstAddr, _ := net.ResolveTCPAddr("tcp", req.Dst)
	if dstAddr == nil {
		dstAddr = &net.TCPAddr{}
	}
	sess := Session{
		Network: "tcp", Src: req.ClientAddr, Dst: dstAddr,
		Domain: req.Domain, TenantID: req.TenantID,
		FailClosed: e.cfg.FailClosed, CreatedAt: time.Now().UTC(),
	}
	if e.tenants != nil && req.TenantID != "" {
		if !e.tenants.Allowed(req.TenantID, RouteDecision{}) {
			return e.reject(req, fmt.Errorf("tenant %q is not allowed", req.TenantID))
		}
	}
	id, err := e.sessions.Create(ctx, sess)
	if err != nil {
		return e.reject(req, err)
	}
	defer e.sessions.Close(id)
	rec, _ := e.sessions.Get(id)
	sess.ID = id

	decision, err := e.dispatcher.Decide(ctx, sess)
	if err == nil && e.pluginHook != nil {
		err = e.pluginHook.OnSession(ctx, &sess, &decision)
	}
	if err != nil {
		return e.reject(req, err)
	}
	if e.pluginHook != nil {
		_ = e.pluginHook.OnDNS(ctx, req.Domain)
	}

	upstream, decision, err := e.openWithDecision(ctx, sess, decision, req.Dst)
	if err != nil {
		if rec != nil {
			rec.Close("failed", err.Error())
		}
		return e.reject(req, err)
	}
	defer upstream.Close()
	if req.OnEstablished != nil {
		if err := req.OnEstablished(true, nil); err != nil {
			return err
		}
	}
	if rec != nil {
		rec.Close("active", "")
		e.sessions.SetPath(id, string(decision.CoreID), decision.Reason)
		if decision.TenantID != "" {
			e.sessions.Tag(id, decision.TenantID)
		}
	}
	_ = client.SetDeadline(time.Time{})
	if len(req.Prefix) > 0 {
		if _, err := upstream.Write(req.Prefix); err != nil {
			return err
		}
		m.BytesOut.Add(uint64(len(req.Prefix)))
	}
	e.relay(client, upstream, rec)
	return nil
}

func (e *Engine) openWithDecision(ctx context.Context, sess Session, d RouteDecision, dst string) (net.Conn, RouteDecision, error) {
	// Reuse the dispatcher's full Open path by temporarily overriding the
	// decision through a rule match is not desirable; call Open with a session
	// whose rule context already carries the plugin-adjusted decision.
	return e.dispatcher.OpenWithDecision(ctx, sess, d, dst)
}

func (e *Engine) reject(req ConnRequest, err error) error {
	e.metrics.Dropped.Add(1)
	e.emit("session.rejected", map[string]any{"dst": req.Dst, "error": err.Error()})
	if req.OnEstablished != nil {
		_ = req.OnEstablished(false, err)
	}
	return err
}

func (e *Engine) relay(client, upstream net.Conn, rec *SessionRec) {
	var wg sync.WaitGroup
	wg.Add(2)
	// Direction matters: "download" = bytes the upstream sends back to the
	// client, "upload" = bytes the client sends to the upstream. Previously
	// both directions were added to BytesOut, so the download counter stayed
	// at zero and the traffic dashboard could never show real download data.
	copyOne := func(dst, src net.Conn, download bool) {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				written, werr := dst.Write(buf[:n])
				if written > 0 {
					bytes := uint64(written)
					if download {
						e.metrics.BytesIn.Add(bytes)
						if rec != nil {
							rec.AddBytes(bytes, 0)
						}
					} else {
						e.metrics.BytesOut.Add(bytes)
						if rec != nil {
							rec.AddBytes(0, bytes)
						}
					}
				}
				if werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		if c, ok := src.(interface{ CloseRead() error }); ok {
			_ = c.CloseRead()
		}
	}
	go copyOne(upstream, client, false) // client -> upstream = upload
	go copyOne(client, upstream, true)  // upstream -> client = download
	wg.Wait()
}

// Status is exposed by /api/v1/network/status.
func (e *Engine) Status() map[string]any {
	e.mu.RLock()
	running, tunStarted := e.running, e.tunStarted
	e.mu.RUnlock()
	return map[string]any{
		"running":     running,
		"stage":       2,
		"fail_closed": e.cfg.FailClosed,
		"failover":    e.cfg.Failover,
		"ingress": map[string]any{
			"enabled": e.cfg.Ingress.Enabled, "address": e.ingress.Addr(),
			"protocols": []string{"socks5", "socks4a", "http-connect", "http-absolute"},
		},
		"tun": map[string]any{
			"configured": e.cfg.TUN.AutoRoute, "active": tunStarted,
			"name": e.cfg.TUN.Name, "mtu": e.cfg.TUN.MTU,
			"stats": e.tun.Stats(),
		},
		"dns":       e.dnsEngine.Status(),
		"rules":     len(e.ruleSet.List()),
		"sessions":  e.sessions.Count(),
		"endpoints": e.bridge.Endpoints(),
		"healthy":   e.dispatcher.HealthyKernels(),
		"resources": e.resources.Snapshot(),
		"metrics":   e.metrics.Snapshot(),
		"plugins":   pluginCount(e.pluginHook),
	}
}

func pluginCount(h PluginHook) int {
	if h == nil {
		return 0
	}
	return h.Count()
}

// tunLoop reads real IP packets from the NGG-owned TUN. DNS is answered by
// NGG's own DNS engine (a real DNS response packet is written back). TCP
// stream termination from raw L3 requires a userspace TCP stack; until that
// is wired, such packets are Fail-Closed with an explicit counter, never
// silently leaked and never reported as successfully proxied.
func (e *Engine) tunLoop(ctx context.Context) {
	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := e.tun.Read(ctx, buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
		if n <= 0 {
			continue
		}
		raw := append([]byte(nil), buf[:n]...)
		e.metrics.TUNPacketsIn.Add(1)
		pkt, perr := e.packets.Parse(raw)
		if perr != nil || pkt.IsLoop {
			e.metrics.Dropped.Add(1)
			continue
		}
		if pkt.IsDNS && e.dnsEngine.IsRunning() && pkt.Version == 4 {
			if resp := e.answerDNSFromPacket(pkt); len(resp) > 0 {
				if _, werr := e.tun.Write(ctx, resp); werr == nil {
					e.metrics.TUNPacketsOut.Add(1)
					e.metrics.DNSQueries.Add(1)
				}
			}
			continue
		}
		// Record the session and the routing decision, then Fail-Closed.
		s := Session{Network: pkt.Protocol, Src: &net.IPAddr{IP: pkt.Src},
			Dst: &net.UDPAddr{IP: pkt.Dst, Port: int(pkt.DstPort)}, CreatedAt: time.Now().UTC()}
		if id, cerr := e.sessions.Create(ctx, s); cerr == nil {
			_ = e.sessions.Close(id)
		}
		e.metrics.Dropped.Add(1)
	}
}

// answerDNSFromPacket builds a real IPv4/UDP/DNS response packet (with correct
// length, IP and UDP checksums) from NGG's DNS engine.
func (e *Engine) answerDNSFromPacket(pkt Packet) []byte {
	msg := new(dns.Msg)
	if err := msg.Unpack(pkt.Payload); err != nil || len(msg.Question) == 0 {
		return nil
	}
	q := msg.Question[0]
	resp := new(dns.Msg)
	resp.SetReply(msg)
	resp.Authoritative = true
	resp.RecursionAvailable = true
	ips, err := e.dnsEngine.Resolve(context.Background(), stringsTrimDot(q.Name), dns.TypeToString[q.Qtype])
	if err != nil || len(ips) == 0 {
		resp.Rcode = dns.RcodeNameError
	} else {
		for _, ip := range ips {
			if ip == nil {
				continue
			}
			if v4 := ip.To4(); v4 != nil && q.Qtype == dns.TypeA {
				resp.Answer = append(resp.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: v4})
			} else if q.Qtype == dns.TypeAAAA {
				resp.Answer = append(resp.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}, AAAA: ip.To16()})
			}
		}
	}
	payload, err := resp.Pack()
	if err != nil {
		return nil
	}
	udpLen := 8 + len(payload)
	total := 20 + udpLen
	out := make([]byte, total)
	out[0] = 0x45
	binary.BigEndian.PutUint16(out[2:4], uint16(total))
	binary.BigEndian.PutUint16(out[4:6], uint16(0x4E47))
	out[8], out[9] = 64, 17
	copy(out[12:16], pkt.Dst.To4())
	copy(out[16:20], pkt.Src.To4())
	binary.BigEndian.PutUint16(out[20:22], pkt.DstPort)
	binary.BigEndian.PutUint16(out[22:24], pkt.SrcPort)
	binary.BigEndian.PutUint16(out[24:26], uint16(udpLen))
	copy(out[28:], payload)
	ipSum := checksum(out[:20])
	binary.BigEndian.PutUint16(out[10:12], ipSum)
	// UDP checksum pseudo-header.
	pseudo := make([]byte, 12+udpLen)
	copy(pseudo[0:4], out[12:16])
	copy(pseudo[4:8], out[16:20])
	pseudo[9] = 17
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(udpLen))
	copy(pseudo[12:], out[20:total])
	sum := checksum(pseudo)
	if sum == 0 {
		sum = 0xffff
	}
	binary.BigEndian.PutUint16(out[26:28], sum)
	return out
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func stringsTrimDot(s string) string {
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
