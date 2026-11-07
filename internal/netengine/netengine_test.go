package netengine

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngg-project/ngg/internal/adapters/mihomo"
	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// ---------------------------------------------------------------------------
// Test helpers. The SOCKS5 "kernel" below is a REAL TCP proxy server (a test
// fixture), and the target is a REAL HTTP/TCP server. Every byte travels over
// real sockets through NGG's ingress -> session -> rule -> dispatcher ->
// bridge -> kernel endpoint -> target path.
// ---------------------------------------------------------------------------

type fixedPorts struct{ p core.RuntimeParams }

func (f *fixedPorts) Allocate(contracts.CoreID) (core.RuntimeParams, error) { return f.p, nil }
func (f *fixedPorts) Get(contracts.CoreID) (core.RuntimeParams, bool)       { return f.p, true }
func (f *fixedPorts) Release(contracts.CoreID)                              {}
func (f *fixedPorts) Check([]int) []int                                     { return nil }
func (f *fixedPorts) Allocations() []contracts.PortAllocation               { return nil }

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startTestKernel runs a genuine SOCKS5 proxy that dials real destinations.
func startTestKernel(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				greet := make([]byte, 3)
				if _, err := io.ReadFull(c, greet); err != nil || greet[0] != 5 {
					return
				}
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
					return
				}
				head := make([]byte, 4)
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				host, err := readSOCKSAddress(c, head[3])
				if err != nil {
					return
				}
				pb := make([]byte, 2)
				if _, err := io.ReadFull(c, pb); err != nil {
					return
				}
				dst := net.JoinHostPort(host, fmt.Sprintf("%d", binary.BigEndian.Uint16(pb)))
				up, err := net.DialTimeout("tcp", dst, 5*time.Second)
				if err != nil {
					_, _ = c.Write([]byte{0x05, 0x05, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				if _, err := c.Write([]byte{0x05, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
					return
				}
				_ = c.SetDeadline(time.Time{})
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
				<-done
				_ = c.Close()
				_ = up.Close()
				<-done
			}()
		}
	}()
	return func() { _ = ln.Close(); wg.Wait() }
}

func newTestEngine(t *testing.T, kernelPort, ingressPort int) *Engine {
	t.Helper()
	reg := core.NewRegistry()
	if err := reg.Register(mihomo.New()); err != nil {
		t.Fatal(err)
	}
	ports := &fixedPorts{p: core.RuntimeParams{
		MixedPort: kernelPort, SocksPort: kernelPort + 1, HTTPPort: kernelPort + 2,
		APIPort: kernelPort + 3, DNSPort: kernelPort + 4, Secret: "test",
	}}
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.FailClosed = true
	cfg.Failover = false
	cfg.DefaultCore = "mihomo"
	cfg.Ingress = IngressConfig{Enabled: true, Listen: "127.0.0.1", Port: ingressPort}
	cfg.DNS = DNSConfig{Enabled: false, Listen: "127.0.0.1", Port: freePort(t)}
	cfg.TUN = TunConfig{Name: "ngg-test", AutoRoute: false}
	cfg.Egress = EgressConfig{BypassTUN: false}
	e := NewEngine(&cfg, reg, ports, nil, func(contracts.CoreID) (contracts.CoreStatus, error) {
		return contracts.CoreStatus{State: contracts.StateRunning}, nil
	})
	return e
}

// -------------------------------------------------------------- unit tests ---

func TestPacketProcessorIPv4DNS(t *testing.T) {
	r := NewResourceManager(EgressConfig{})
	pp := NewPacketProcessor(r)
	raw := syntheticIPv4UDPDNS()
	pkt, err := pp.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !pkt.IsDNS || pkt.Protocol != "udp" || pkt.DstPort != 53 || pkt.Version != 4 {
		t.Fatalf("bad parse: %+v", pkt)
	}
	if pkt.Src.String() != "10.89.0.2" || pkt.Dst.String() != "10.89.0.1" {
		t.Fatalf("bad addresses: %s -> %s", pkt.Src, pkt.Dst)
	}
	action, err := pp.Process(context.Background(), raw)
	if err != nil || action != ActionDNS {
		t.Fatalf("action=%s err=%v", action, err)
	}
	r.ProtectPort(53, "ngg-dns")
	if _, err := pp.Process(context.Background(), raw); err == nil {
		t.Fatal("protected destination must be dropped as a loop")
	}
}

func TestPacketProcessorIPv6TCP(t *testing.T) {
	pp := NewPacketProcessor(NewResourceManager(EgressConfig{}))
	raw := make([]byte, 60)
	raw[0] = 0x60
	raw[6] = 6 // TCP
	copy(raw[8:24], net.ParseIP("2001:db8::1").To16())
	copy(raw[24:40], net.ParseIP("2001:db8::2").To16())
	binary.BigEndian.PutUint16(raw[40:42], 12345)
	binary.BigEndian.PutUint16(raw[42:44], 443)
	raw[52] = 0x50 // data offset 5
	pkt, err := pp.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Version != 6 || pkt.Protocol != "tcp" || pkt.DstPort != 443 || pkt.SrcPort != 12345 {
		t.Fatalf("bad parse: %+v", pkt)
	}
	action, err := pp.Process(context.Background(), raw)
	if err != nil || action != ActionForward {
		t.Fatalf("action=%s err=%v", action, err)
	}
}

func syntheticIPv4UDPDNS() []byte {
	b := make([]byte, 20+8+12)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	b[8], b[9] = 64, 17
	copy(b[12:16], []byte{10, 89, 0, 2})
	copy(b[16:20], []byte{10, 89, 0, 1})
	binary.BigEndian.PutUint16(b[20:22], 12345)
	binary.BigEndian.PutUint16(b[22:24], 53)
	binary.BigEndian.PutUint16(b[24:26], 20)
	return b
}

func TestRuleEngineMatchTypes(t *testing.T) {
	rs := NewRuleSet(ActDirect, "")
	err := rs.Replace([]Rule{
		{ID: "a", Type: RuleDomainSuffix, Value: ".example.com", Action: ActProxy, CoreID: "k1", Priority: 50, Enabled: true},
		{ID: "b", Type: RuleDomainKeyword, Value: "blocked", Action: ActBlock, Priority: 40, Enabled: true},
		{ID: "c", Type: RuleIPCIDR, Value: "10.0.0.0/8", Action: ActDirect, Priority: 30, Enabled: true},
		{ID: "d", Type: RulePort, Value: "53", Action: ActDNS, Priority: 20, Enabled: true},
		{ID: "e", Type: RuleTenant, Value: "acme", Action: ActProxy, CoreID: "k2", Priority: 60, Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   MatchInput
		want string
	}{
		{MatchInput{Domain: "www.example.com"}, "a"},
		{MatchInput{Domain: "this-is-blocked.net"}, "b"},
		{MatchInput{Domain: "x.net", IP: net.ParseIP("10.1.2.3")}, "c"},
		{MatchInput{Domain: "x.net", Port: 53}, "d"},
		{MatchInput{Domain: "x.net", Tenant: "acme"}, "e"},
		{MatchInput{Domain: "nothing.net"}, "DEFAULT"},
	}
	for _, c := range cases {
		r, _ := rs.Match(c.in)
		if r.ID != c.want {
			t.Errorf("input %+v matched %s, want %s", c.in, r.ID, c.want)
		}
	}
	if err := rs.Add(Rule{ID: "bad", Type: RuleDomainRegex, Value: "[", Action: ActDirect}); err == nil {
		t.Fatal("invalid regex must be rejected")
	}
	if err := rs.Add(Rule{ID: "bad2", Type: RuleIPCIDR, Value: "nope", Action: ActDirect}); err == nil {
		t.Fatal("invalid CIDR must be rejected")
	}
}

func TestSessionManagerAndResources(t *testing.T) {
	sm := newSessionManager(2, time.Minute)
	id, err := sm.Create(context.Background(), Session{Network: "tcp", Domain: "a.com"})
	if err != nil || sm.Count() != 1 {
		t.Fatalf("create: %v count=%d", err, sm.Count())
	}
	sm.SetPath(id, "mihomo", "rule-a")
	s, ok := sm.Lookup(id)
	if !ok || string(s.CoreID) != "mihomo" || s.Rule != "rule-a" {
		t.Fatalf("path not recorded: %+v", s)
	}
	_, _ = sm.Create(context.Background(), Session{})
	_, _ = sm.Create(context.Background(), Session{})
	if err := sm.Close(id); err != nil {
		t.Fatal(err)
	}
	if sm.Count() != 1 {
		t.Fatalf("close did not remove session: %d", sm.Count())
	}

	r := NewResourceManager(EgressConfig{})
	if err := r.ReserveFor("port", "18080", "ngg-ingress"); err != nil {
		t.Fatal(err)
	}
	if err := r.ReserveFor("port", "18080", "other"); err == nil {
		t.Fatal("double reservation must conflict")
	}
	if err := r.AssertNoLoop("mihomo", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18080}); err == nil {
		t.Fatal("loop to own ingress must be rejected")
	}
	if err := r.AssertNoLoop("mihomo", &net.TCPAddr{IP: net.ParseIP("10.89.0.9"), Port: 443}); err == nil {
		t.Fatal("loop into TUN subnet must be rejected")
	}
	if err := r.AssertNoLoop("mihomo", &net.TCPAddr{IP: net.ParseIP("93.184.216.34"), Port: 443}); err != nil {
		t.Fatalf("normal destination must be allowed: %v", err)
	}
}

// ------------------------------------------------------- real network tests ---

// TestEndToEndThroughRealSockets pushes real bytes through the entire NGG
// pipeline using an actual SOCKS5 server as the kernel endpoint.
func TestEndToEndThroughRealSockets(t *testing.T) {
	kernelPort, ingressPort := freePort(t), freePort(t)
	stopKernel := startTestKernel(t, kernelPort)
	defer stopKernel()

	e := newTestEngine(t, kernelPort, ingressPort)
	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer e.Stop(ctx)
	e.Rules().SetDefaults(ActProxy, "mihomo")

	if !core.IsHealthyEndpoint(core.ProxyEndpoint{Address: fmt.Sprintf("127.0.0.1:%d", kernelPort)}) {
		t.Fatal("kernel endpoint health check failed")
	}
	res, err := e.SelfTest(ctx, "mihomo")
	if err != nil {
		t.Fatalf("self test: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("self test did not pass: %+v", res)
	}
	// Counters are incremented by the relay goroutines, which can outlive
	// SelfTest on a fast runner. relay_test.go already polls for this reason;
	// do the same here instead of racing the assertion against the copy loop.
	// The deadline is a hard failure: zero real bytes is still a broken path.
	deadline := time.Now().Add(3 * time.Second)
	for e.Metrics().Proxied.Load() == 0 || e.Metrics().BytesOut.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no bytes were relayed: proxied=%d bytes_out=%d",
				e.Metrics().Proxied.Load(), e.Metrics().BytesOut.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("path=%s latency=%vms bytes_out=%d", res["path"], res["latency_ms"], e.Metrics().BytesOut.Load())
}

// TestHTTPProxyThroughNGG uses net/http with NGG as an absolute-form HTTP
// proxy against a real httptest server.
func TestHTTPProxyThroughNGG(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("NGG-HTTP-OK:" + r.URL.Path))
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	targetPort := targetURL.Port()

	kernelPort, ingressPort := freePort(t), freePort(t)
	stopKernel := startTestKernel(t, kernelPort)
	defer stopKernel()
	e := newTestEngine(t, kernelPort, ingressPort)
	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer e.Stop(ctx)
	e.Rules().SetDefaults(ActProxy, "mihomo")

	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", ingressPort))
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	resp, err := client.Get(target.URL + "/hello")
	if err != nil {
		t.Fatalf("HTTP GET through NGG: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "NGG-HTTP-OK:/hello" {
		t.Fatalf("unexpected response: %d %q (target port %s)", resp.StatusCode, body, targetPort)
	}
}

// TestFailClosedWhenKernelDown verifies NGG never leaks to DIRECT when the
// selected kernel is unreachable.
func TestFailClosedWhenKernelDown(t *testing.T) {
	deadPort := freePort(t)
	ingressPort := freePort(t)
	e := newTestEngine(t, deadPort, ingressPort)
	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(ctx)
	e.Rules().SetDefaults(ActProxy, "mihomo")
	res, err := e.SelfTest(ctx, "mihomo")
	if err != nil {
		t.Fatal(err)
	}
	if res["ok"] != false {
		t.Fatalf("expected Fail-Closed failure, got %+v", res)
	}
	if e.Metrics().Direct.Load() != 0 {
		t.Fatal("DIRECT leak detected")
	}
}

// TestSOCKS5DirectDial exercises the exported SOCKS5 client against a real
// proxy server and a real TCP echo target.
func TestSOCKS5DirectDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('\n')
		_, _ = c.Write([]byte(strings.ToUpper(line)))
	}()
	kernelPort := freePort(t)
	stop := startTestKernel(t, kernelPort)
	defer stop()
	conn, err := DialSOCKS5(context.Background(), fmt.Sprintf("127.0.0.1:%d", kernelPort), ln.Addr().String(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "abc\n")
	got, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || got != "ABC\n" {
		t.Fatalf("echo = %q err=%v", got, err)
	}
}
