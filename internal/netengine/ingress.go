package netengine

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IngressServer is NGG's unified userspace ingress: one listener speaking
// SOCKS5 (RFC 1928), SOCKS4a and HTTP CONNECT / absolute-form HTTP. On Linux
// the same Engine also owns the system TUN; this ingress is the portable
// ingress adapter used by LAN clients and by the integration tests.
type IngressServer struct {
	cfg      IngressConfig
	engine   *Engine
	listener net.Listener
	mu       sync.Mutex
	running  bool
	conns    map[net.Conn]struct{}
	wg       sync.WaitGroup
}

func newIngressServer(cfg IngressConfig, e *Engine) *IngressServer {
	return &IngressServer{cfg: cfg, engine: e, conns: map[net.Conn]struct{}{}}
}

func (s *IngressServer) Start() error {
	if !s.cfg.Enabled {
		return nil
	}
	if s.cfg.Port <= 0 {
		return fmt.Errorf("ingress port is not configured")
	}
	if !isLoopbackHost(s.cfg.Listen) && (s.cfg.AuthUser == "" || s.cfg.AuthPass == "") {
		return ErrIngressInsecure
	}
	addr := net.JoinHostPort(s.cfg.Listen, fmt.Sprintf("%d", s.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("NGG ingress listen %s: %w", addr, err)
	}
	s.mu.Lock()
	s.listener, s.running = ln, true
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				s.mu.Lock()
				running := s.running
				s.mu.Unlock()
				if running {
					continue
				}
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.serveConn(conn)
			}()
		}
	}()
	return nil
}

func (s *IngressServer) Stop() {
	s.mu.Lock()
	ln := s.listener
	s.running = false
	s.listener = nil
	active := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		active = append(active, c)
	}
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	// Actively close live sessions so Stop() can never hang on keep-alive.
	for _, c := range active {
		_ = c.Close()
	}
	s.wg.Wait()
}

func (s *IngressServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func isLoopbackHost(h string) bool {
	if h == "" || h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// bufferedConn preserves bytes already peeked by bufio.Reader.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *bufferedConn) CloseWrite() error {
	if c, ok := b.Conn.(interface{ CloseWrite() error }); ok {
		return c.CloseWrite()
	}
	return nil
}

func (b *bufferedConn) CloseRead() error {
	if c, ok := b.Conn.(interface{ CloseRead() error }); ok {
		return c.CloseRead()
	}
	return nil
}

func (s *IngressServer) serveConn(raw net.Conn) {
	s.mu.Lock()
	s.conns[raw] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, raw)
		s.mu.Unlock()
		_ = raw.Close()
	}()
	_ = raw.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(raw)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	bc := &bufferedConn{Conn: raw, r: br}
	switch {
	case first[0] == 0x05:
		s.handleSOCKS5(bc)
	case first[0] == 0x04:
		s.handleSOCKS4a(bc)
	default:
		s.handleHTTP(bc)
	}
}

func (s *IngressServer) authorized(user, pass string) bool {
	if s.cfg.AuthUser == "" {
		return true
	}
	return user == s.cfg.AuthUser && pass == s.cfg.AuthPass
}

// ---------------------------------------------------------------- SOCKS5 ---

func (s *IngressServer) handleSOCKS5(c net.Conn) {
	ver := make([]byte, 1)
	if _, err := io.ReadFull(c, ver); err != nil || ver[0] != 5 {
		return
	}
	n := make([]byte, 1)
	if _, err := io.ReadFull(c, n); err != nil {
		return
	}
	methods := make([]byte, int(n[0]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	wantUser := s.cfg.AuthUser != ""
	chosen := byte(0x00)
	if wantUser {
		chosen = 0x02
	}
	found := false
	for _, m := range methods {
		if m == chosen {
			found = true
			break
		}
	}
	if !found {
		_, _ = c.Write([]byte{0x05, 0xFF})
		return
	}
	if _, err := c.Write([]byte{0x05, chosen}); err != nil {
		return
	}
	tenant := ""
	if chosen == 0x02 {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 1 {
			return
		}
		ub := make([]byte, int(hdr[1]))
		if _, err := io.ReadFull(c, ub); err != nil {
			return
		}
		lb := make([]byte, 1)
		if _, err := io.ReadFull(c, lb); err != nil {
			return
		}
		pb := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(c, pb); err != nil {
			return
		}
		user, pass := string(ub), string(pb)
		if !s.authorized(user, pass) {
			_, _ = c.Write([]byte{0x01, 0x01})
			return
		}
		tenant = tenantFromUser(user)
		if _, err := c.Write([]byte{0x01, 0x00}); err != nil {
			return
		}
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil || head[0] != 5 {
		return
	}
	cmd := head[1]
	host, err := readSOCKSAddress(c, head[3])
	if err != nil {
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return
	}
	port := int(pb[0])<<8 | int(pb[1])
	dst := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	if cmd == 0x03 {
		s.handleUDPAssociate(c, tenant)
		return
	}
	if cmd != 0x01 {
		_, _ = c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	reply := func(ok bool, _ error) error {
		if ok {
			_, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return err
		}
		_, err := c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return err
	}
	_ = s.engine.HandleTCP(context.Background(), c, ConnRequest{
		Dst: dst, Domain: host, Network: "tcp", TenantID: tenant,
		ClientAddr: c.RemoteAddr(), OnEstablished: reply,
	})
}

func readSOCKSAddress(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x03:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(r, lb); err != nil {
			return "", err
		}
		b := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", fmt.Errorf("unknown SOCKS ATYP %d", atyp)
}

func tenantFromUser(user string) string {
	if i := strings.IndexByte(user, ':'); i > 0 {
		return user[:i]
	}
	return ""
}

// -------------------------------------------------------------- SOCKS4a ---

func (s *IngressServer) handleSOCKS4a(c net.Conn) {
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[1] != 1 {
		return
	}
	readCString := func() (string, error) {
		var b []byte
		one := make([]byte, 1)
		for len(b) < 1024 {
			if _, err := io.ReadFull(c, one); err != nil {
				return "", err
			}
			if one[0] == 0 {
				return string(b), nil
			}
			b = append(b, one[0])
		}
		return "", errors.New("SOCKS4 string too long")
	}
	if _, err := readCString(); err != nil { // USERID
		return
	}
	ip := net.IPv4(hdr[4], hdr[5], hdr[6], hdr[7]).String()
	if ip == "0.0.0.1" {
		domain, err := readCString()
		if err != nil {
			return
		}
		ip = domain
	}
	port := int(hdr[2])<<8 | int(hdr[3])
	dst := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
	reply := func(ok bool, _ error) error {
		if ok {
			_, err := c.Write([]byte{0, 0x5A, 0, 0, 0, 0, 0, 0})
			return err
		}
		_, err := c.Write([]byte{0, 0x5B, 0, 0, 0, 0, 0, 0})
		return err
	}
	_ = s.engine.HandleTCP(context.Background(), c, ConnRequest{
		Dst: dst, Domain: ip, Network: "tcp", ClientAddr: c.RemoteAddr(),
		OnEstablished: reply,
	})
}

// ------------------------------------------------------------------ HTTP ---

func (s *IngressServer) handleHTTP(c net.Conn) {
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if s.cfg.AuthUser != "" {
		ok := false
		if h := req.Header.Get("Proxy-Authorization"); strings.HasPrefix(h, "Basic ") {
			decoded, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "Basic "))
			if e == nil {
				parts := strings.SplitN(string(decoded), ":", 2)
				if len(parts) == 2 && s.authorized(parts[0], parts[1]) {
					ok = true
				}
			}
		}
		if !ok {
			_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"NGG\"\r\nContent-Length: 0\r\n\r\n")
			return
		}
	}
	if req.Method == http.MethodConnect {
		host := req.Host
		if !strings.Contains(host, ":") {
			host += ":443"
		}
		tenant := req.Header.Get("X-NGG-Tenant")
		reply := func(ok bool, _ error) error {
			if ok {
				_, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
				return err
			}
			_, err := io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return err
		}
		_ = s.engine.HandleTCP(context.Background(), &bufferedConn{Conn: c, r: br}, ConnRequest{
			Dst: host, Domain: hostOnly(host), Network: "tcp", TenantID: tenant,
			ClientAddr: c.RemoteAddr(), OnEstablished: reply,
		})
		return
	}
	// Absolute-form HTTP proxy request.
	if !req.URL.IsAbs() {
		_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		return
	}
	host := req.URL.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	req.RequestURI = ""
	req.URL.Scheme, req.URL.Host = "", ""
	req.Host = hostOnly(host)
	var buf strings.Builder
	_ = req.Write(&buf)
	reply := func(ok bool, _ error) error {
		if !ok {
			_, err := io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return err
		}
		return nil
	}
	_ = s.engine.HandleTCP(context.Background(), &bufferedConn{Conn: c, r: br}, ConnRequest{
		Dst: host, Domain: hostOnly(host), Network: "tcp",
		TenantID: req.Header.Get("X-NGG-Tenant"), ClientAddr: c.RemoteAddr(),
		OnEstablished: reply, Prefix: []byte(buf.String()),
	})
}

func hostOnly(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return h
}
