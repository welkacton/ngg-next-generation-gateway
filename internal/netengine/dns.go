package netengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// DNSEngine is owned by NGG. Kernel-level DNS hijacking stays disabled in
// Worker Mode; all system DNS policy is applied here.
type dnsEngine struct {
	cfg       DNSConfig
	metrics   *Metrics
	rules     *RuleSet
	mu        sync.RWMutex
	hosts     map[string]string
	block     map[string]bool
	cache     map[string]cacheEntry
	servers   []*dns.Server
	listeners []net.PacketConn
	running   bool
	client    *dns.Client
	http      *http.Client
}

type cacheEntry struct {
	Answers []dns.RR
	Expires time.Time
}

var _ DNSEngine = (*dnsEngine)(nil)

func newDNSEngine(cfg DNSConfig, metrics *Metrics, rules *RuleSet) *dnsEngine {
	hosts := map[string]string{}
	for k, v := range cfg.Hosts {
		hosts[strings.ToLower(dns.Fqdn(k))] = v
	}
	block := map[string]bool{}
	for _, d := range cfg.Block {
		block[strings.ToLower(dns.Fqdn(d))] = true
	}
	return &dnsEngine{
		cfg: cfg, metrics: metrics, rules: rules,
		hosts: hosts, block: block, cache: map[string]cacheEntry{},
		client: &dns.Client{Timeout: 4 * time.Second},
		http:   &http.Client{Timeout: 6 * time.Second},
	}
}

func (e *dnsEngine) Start() error {
	if !e.cfg.Enabled {
		return nil
	}
	addr := net.JoinHostPort(e.cfg.Listen, fmt.Sprintf("%d", e.cfg.Port))
	udp, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("NGG DNS UDP listen %s: %w", addr, err)
	}
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		_ = udp.Close()
		return fmt.Errorf("NGG DNS TCP listen %s: %w", addr, err)
	}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", e.handle)
	us := &dns.Server{PacketConn: udp, Handler: mux}
	ts := &dns.Server{Listener: tcp, Handler: mux}
	e.mu.Lock()
	e.servers = []*dns.Server{us, ts}
	e.listeners = []net.PacketConn{udp}
	e.running = true
	e.mu.Unlock()
	go func() { _ = us.ActivateAndServe() }()
	go func() { _ = ts.ActivateAndServe() }()
	return nil
}

func (e *dnsEngine) Stop() {
	e.mu.Lock()
	servers := e.servers
	listeners := e.listeners
	e.servers, e.listeners, e.running = nil, nil, false
	e.mu.Unlock()
	for _, s := range servers {
		_ = s.Shutdown()
	}
	for _, l := range listeners {
		_ = l.Close()
	}
}

func (e *dnsEngine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

func (e *dnsEngine) handle(w dns.ResponseWriter, req *dns.Msg) {
	if e.metrics != nil {
		e.metrics.DNSQueries.Add(1)
	}
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true
	resp.RecursionAvailable = true
	if len(req.Question) == 0 {
		_ = w.WriteMsg(resp)
		return
	}
	q := req.Question[0]
	name := strings.ToLower(q.Name)
	e.mu.RLock()
	blocked := e.block[name]
	hostIP, hasHost := e.hosts[name]
	e.mu.RUnlock()

	if blocked {
		resp.Rcode = dns.RcodeNameError
		_ = w.WriteMsg(resp)
		return
	}
	if hasHost && (q.Qtype == dns.TypeA || q.Qtype == dns.TypeAAAA) {
		ip := net.ParseIP(hostIP)
		if q.Qtype == dns.TypeA && ip != nil && ip.To4() != nil {
			resp.Answer = append(resp.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: ip.To4()})
		} else if q.Qtype == dns.TypeAAAA && ip != nil && ip.To16() != nil {
			resp.Answer = append(resp.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}, AAAA: ip.To16()})
		}
		_ = w.WriteMsg(resp)
		return
	}

	key := fmt.Sprintf("%s/%d", name, q.Qtype)
	if cached, ok := e.cacheLookup(key); ok {
		if e.metrics != nil {
			e.metrics.DNSCacheHits.Add(1)
		}
		resp.Answer = append(resp.Answer, cached...)
		_ = w.WriteMsg(resp)
		return
	}
	upstreamResp, err := e.exchange(req)
	if err != nil {
		resp.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(resp)
		return
	}
	resp.Rcode = upstreamResp.Rcode
	resp.Answer = append(resp.Answer, upstreamResp.Answer...)
	resp.Ns = append(resp.Ns, upstreamResp.Ns...)
	resp.Extra = append(resp.Extra, upstreamResp.Extra...)
	e.cacheStore(key, upstreamResp.Answer)
	_ = w.WriteMsg(resp)
}

func (e *dnsEngine) exchange(req *dns.Msg) (*dns.Msg, error) {
	var lastErr error
	for _, up := range e.cfg.Upstreams {
		var resp *dns.Msg
		var err error
		if strings.HasPrefix(up, "https://") {
			resp, err = e.exchangeDoH(req, up)
		} else {
			addr := up
			if !strings.Contains(addr, ":") {
				addr += ":53"
			}
			resp, _, err = e.client.Exchange(req, addr)
		}
		if err == nil && resp != nil {
			return resp, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no DNS upstream configured")
	}
	return nil, lastErr
}

func (e *dnsEngine) exchangeDoH(req *dns.Msg, endpoint string) (*dns.Msg, error) {
	packed, err := req.Pack()
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/dns-message")
	httpReq.Header.Set("Accept", "application/dns-message")
	resp, err := e.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	out := new(dns.Msg)
	if err := out.Unpack(body); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *dnsEngine) cacheLookup(key string) ([]dns.RR, bool) {
	e.mu.RLock()
	entry, ok := e.cache[key]
	e.mu.RUnlock()
	if !ok || time.Now().After(entry.Expires) {
		return nil, false
	}
	return append([]dns.RR(nil), entry.Answers...), true
}

func (e *dnsEngine) cacheStore(key string, answers []dns.RR) {
	if len(answers) == 0 {
		return
	}
	ttl := uint32(e.cfg.CacheTTL)
	if ttl == 0 {
		ttl = 30
	}
	for _, a := range answers {
		if a.Header().Ttl > 0 && a.Header().Ttl < ttl {
			ttl = a.Header().Ttl
		}
	}
	e.mu.Lock()
	e.cache[key] = cacheEntry{Answers: append([]dns.RR(nil), answers...), Expires: time.Now().Add(time.Duration(ttl) * time.Second)}
	e.mu.Unlock()
}

// Resolve implements the reserved DNSEngine contract. It applies hosts/block
// policy first, then really queries the configured upstreams.
func (e *dnsEngine) Resolve(ctx context.Context, name, qtype string) ([]net.IP, error) {
	fqdn := strings.ToLower(dns.Fqdn(name))
	e.mu.RLock()
	blocked := e.block[fqdn]
	hostIP, hasHost := e.hosts[fqdn]
	e.mu.RUnlock()
	if blocked {
		return nil, fmt.Errorf("domain %s blocked by NGG DNS policy", name)
	}
	if hasHost {
		return []net.IP{net.ParseIP(hostIP)}, nil
	}
	t := dns.TypeA
	if strings.EqualFold(qtype, "AAAA") {
		t = dns.TypeAAAA
	}
	m := new(dns.Msg)
	m.SetQuestion(fqdn, t)
	m.RecursionDesired = true
	resp, err := e.exchangeContext(ctx, m)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, a := range resp.Answer {
		switch rr := a.(type) {
		case *dns.A:
			ips = append(ips, rr.A)
		case *dns.AAAA:
			ips = append(ips, rr.AAAA)
		}
	}
	return ips, nil
}

func (e *dnsEngine) exchangeContext(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	// dns.Client does not take a context directly; enforce it with a channel.
	type result struct {
		msg *dns.Msg
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := e.exchange(m)
		ch <- result{msg, err}
	}()
	select {
	case r := <-ch:
		return r.msg, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *dnsEngine) Reload(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, v := range e.cfg.Hosts {
		e.hosts[strings.ToLower(dns.Fqdn(k))] = v
	}
	for _, d := range e.cfg.Block {
		e.block[strings.ToLower(dns.Fqdn(d))] = true
	}
	e.cache = map[string]cacheEntry{}
	return nil
}

func (e *dnsEngine) SetHosts(hosts map[string]string, block []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, v := range hosts {
		e.hosts[strings.ToLower(dns.Fqdn(k))] = v
	}
	for _, d := range block {
		e.block[strings.ToLower(dns.Fqdn(d))] = true
	}
	e.cache = map[string]cacheEntry{}
}

func (e *dnsEngine) Status() map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return map[string]any{
		"enabled": e.cfg.Enabled, "running": e.running,
		"listen": e.cfg.Listen, "port": e.cfg.Port,
		"upstreams": e.cfg.Upstreams, "hosts": len(e.hosts),
		"blocked": len(e.block), "cache_entries": len(e.cache),
	}
}

// HostsJSON is used by the API to export the current DNS policy.
func (e *dnsEngine) HostsJSON() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	b, _ := json.Marshal(map[string]any{"hosts": e.hosts, "block": e.block})
	return b
}
