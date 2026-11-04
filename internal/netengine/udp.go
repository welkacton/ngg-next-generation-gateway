package netengine

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// handleUDPAssociate implements a REAL SOCKS5 UDP ASSOCIATE relay (RFC 1928 §7).
// The TCP control connection stays open for the lifetime of the association,
// as required by the spec. Datagrams are dispatched through the same NGG
// Rule Engine / Protocol Dispatcher as TCP sessions.
func (s *IngressServer) handleUDPAssociate(control net.Conn, tenant string) {
	pc, err := net.ListenPacket("udp4", net.JoinHostPort(s.cfg.Listen, "0"))
	if err != nil {
		_, _ = control.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer pc.Close()
	local := pc.LocalAddr().(*net.UDPAddr)
	reply := []byte{0x05, 0x00, 0x00, 0x01}
	reply = append(reply, local.IP.To4()...)
	reply = binary.BigEndian.AppendUint16(reply, uint16(local.Port))
	if _, err := control.Write(reply); err != nil {
		return
	}

	relay := &udpRelay{ingress: s, pc: pc, tenant: tenant, assocs: map[contracts.CoreID]*udpAssoc{}}
	defer relay.close()

	// The control connection's lifetime defines the UDP association lifetime.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		for {
			_ = control.SetReadDeadline(time.Now().Add(5 * time.Minute))
			if _, err := control.Read(buf); err != nil {
				return
			}
		}
	}()

	buf := make([]byte, 65535)
	for {
		select {
		case <-done:
			return
		default:
		}
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, clientAddr, err := pc.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if ok := asNetError(err, &ne); ok && ne.Timeout() {
				continue
			}
			return
		}
		pkt := append([]byte(nil), buf[:n]...)
		go relay.forward(pkt, clientAddr)
	}
}

func asNetError(err error, target *net.Error) bool {
	if ne, ok := err.(net.Error); ok {
		*target = ne
		return true
	}
	return false
}

type udpAssoc struct {
	mu      sync.Mutex
	control net.Conn
	relay   *net.UDPAddr
}

type udpRelay struct {
	ingress *IngressServer
	pc      net.PacketConn
	tenant  string
	mu      sync.Mutex
	assocs  map[contracts.CoreID]*udpAssoc
}

func (r *udpRelay) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.assocs {
		if a.control != nil {
			_ = a.control.Close()
		}
	}
	r.assocs = map[contracts.CoreID]*udpAssoc{}
}

func (r *udpRelay) forward(raw []byte, clientAddr net.Addr) {
	if len(raw) < 4 || raw[0] != 0 || raw[1] != 0 || raw[2] != 0 {
		return // fragmented UDP is not supported; drop honestly
	}
	atyp := raw[3]
	idx := 4
	var host string
	switch atyp {
	case 0x01:
		if len(raw) < idx+6 {
			return
		}
		host = net.IP(raw[idx : idx+4]).String()
		idx += 4
	case 0x04:
		if len(raw) < idx+18 {
			return
		}
		host = net.IP(raw[idx : idx+16]).String()
		idx += 16
	case 0x03:
		if len(raw) < idx+1 {
			return
		}
		l := int(raw[idx])
		idx++
		if len(raw) < idx+l+2 {
			return
		}
		host = string(raw[idx : idx+l])
		idx += l
	default:
		return
	}
	if len(raw) < idx+2 {
		return
	}
	port := int(binary.BigEndian.Uint16(raw[idx : idx+2]))
	idx += 2
	data := raw[idx:]
	dst := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	sess := Session{
		Network: "udp", Src: clientAddr,
		Dst:    &net.UDPAddr{IP: net.ParseIP(host), Port: port},
		Domain: host, TenantID: r.tenant, FailClosed: true, CreatedAt: time.Now().UTC(),
	}
	decision, err := r.ingress.engine.dispatcher.Decide(context.Background(), sess)
	if err != nil || decision.Block {
		if r.ingress.engine.metrics != nil {
			r.ingress.engine.metrics.Dropped.Add(1)
		}
		return
	}
	var replyData []byte
	if decision.Direct {
		replyData = r.directUDP(decision, dst, data)
	} else {
		replyData = r.kernelUDP(decision, dst, host, port, data)
	}
	if len(replyData) == 0 {
		return
	}
	enc := append([]byte{0, 0, 0, 0x01}, net.IPv4zero.To4()...)
	enc = binary.BigEndian.AppendUint16(enc, uint16(port))
	enc = append(enc, replyData...)
	_, _ = r.pc.WriteTo(enc, clientAddr)
	if r.ingress.engine.metrics != nil {
		r.ingress.engine.metrics.BytesOut.Add(uint64(len(data)))
		r.ingress.engine.metrics.BytesIn.Add(uint64(len(replyData)))
	}
}

func (r *udpRelay) directUDP(decision RouteDecision, dst string, data []byte) []byte {
	conn, err := net.DialTimeout("udp", dst, 5*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	if _, err := conn.Write(data); err != nil {
		return nil
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil {
		return nil
	}
	if r.ingress.engine.metrics != nil {
		r.ingress.engine.metrics.Direct.Add(1)
	}
	return buf[:n]
}

func (r *udpRelay) kernelUDP(decision RouteDecision, dst, host string, port int, data []byte) []byte {
	id := decision.CoreID
	if id == "" && len(decision.Chain) > 0 {
		id = decision.Chain[0]
	}
	if id == "" {
		return nil
	}
	assoc := r.association(id)
	if assoc == nil {
		return nil
	}
	enc := []byte{0, 0, 0}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		enc = append(enc, 0x01)
		enc = append(enc, ip.To4()...)
	} else {
		if len(host) > 255 {
			return nil
		}
		enc = append(enc, 0x03, byte(len(host)))
		enc = append(enc, []byte(host)...)
	}
	enc = binary.BigEndian.AppendUint16(enc, uint16(port))
	enc = append(enc, data...)

	assoc.mu.Lock()
	defer assoc.mu.Unlock()
	conn, err := net.DialTimeout("udp", assoc.relay.String(), 5*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	if _, err := conn.Write(enc); err != nil {
		return nil
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil || n < 4 {
		return nil
	}
	resp := buf[:n]
	idx := 4
	switch resp[3] {
	case 0x01:
		idx += 4
	case 0x04:
		idx += 16
	case 0x03:
		if n < idx+1 {
			return nil
		}
		idx += 1 + int(resp[idx])
	default:
		return nil
	}
	if n < idx+2 {
		return nil
	}
	if r.ingress.engine.metrics != nil {
		r.ingress.engine.metrics.Proxied.Add(1)
	}
	return resp[idx+2:]
}

func (r *udpRelay) association(id contracts.CoreID) *udpAssoc {
	r.mu.Lock()
	if a, ok := r.assocs[id]; ok {
		r.mu.Unlock()
		return a
	}
	a := &udpAssoc{}
	r.assocs[id] = a
	r.mu.Unlock()

	ep, err := r.ingress.engine.bridge.endpoint(id)
	if err != nil {
		return nil
	}
	conn, err := net.DialTimeout("tcp", ep.Address, 5*time.Second)
	if err != nil {
		return nil
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = conn.Close()
		return nil
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(conn, greet); err != nil || greet[1] != 0 {
		_ = conn.Close()
		return nil
	}
	// CMD=3 UDP ASSOCIATE with wildcard client address.
	req := []byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(req); err != nil {
		_ = conn.Close()
		return nil
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[1] != 0 {
		_ = conn.Close()
		return nil
	}
	relayHost, err := readSOCKSAddress(conn, head[3])
	if err != nil {
		_ = conn.Close()
		return nil
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(conn, pb); err != nil {
		_ = conn.Close()
		return nil
	}
	relayPort := int(binary.BigEndian.Uint16(pb))
	if relayHost == "0.0.0.0" || relayHost == "::" {
		host, _, _ := net.SplitHostPort(ep.Address)
		relayHost = host
	}
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(relayHost, fmt.Sprintf("%d", relayPort)))
	if err != nil {
		_ = conn.Close()
		return nil
	}
	_ = conn.SetDeadline(time.Time{})
	a.control, a.relay = conn, addr
	return a
}
