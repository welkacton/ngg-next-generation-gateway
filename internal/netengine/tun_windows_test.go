//go:build windows

package netengine

import (
	"context"
	"net"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

// newTestStack builds exactly the same gVisor stack TunDevice builds, so these
// tests cover the real Windows packet path without needing an elevated wintun
// adapter. The full three-way handshake + relay is additionally covered by
// scripts/verify-tun.ps1, which runs against a real adapter.
func newTestStack(t *testing.T) (*stack.Stack, *channel.Endpoint) {
	t.Helper()
	ep := channel.New(64, 1500, "")
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(1, ep); err != nil {
		t.Fatalf("CreateNIC: %v", err)
	}
	_ = s.SetSpoofing(1, true)
	_ = s.SetPromiscuousMode(1, true)
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFromSlice(net.ParseIP("10.99.0.1").To4()).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		t.Fatalf("AddProtocolAddress: %v", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	return s, ep
}

// A real UDP datagram (DNS query) must reach NGG's UDP handler with the
// original destination — this is how TUN DNS keeps working inside the tunnel.
func TestTunUDPForwarderDeliversDNS(t *testing.T) {
	s, ep := newTestStack(t)
	got := make(chan stack.TransportEndpointID, 1)
	installForwarders(s,
		func(_ *gonet.TCPConn, _ stack.TransportEndpointID) {},
		func(conn *gonet.UDPConn, id stack.TransportEndpointID) {
			got <- id
			_ = conn.Close()
		})

	// Minimal DNS query header (id, flags, qdcount) - enough payload to route.
	dns := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	injectUDP(t, ep, net.ParseIP("10.99.0.1").To4(), net.ParseIP("223.5.5.5").To4(), 53123, 53, dns)

	select {
	case id := <-got:
		if id.LocalPort != 53 {
			t.Fatalf("UDP destination port = %d, want 53", id.LocalPort)
		}
		if got := id.LocalAddress.String(); got != "223.5.5.5" {
			t.Fatalf("UDP destination = %s, want 223.5.5.5", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the UDP forwarder never handed the DNS datagram to NGG")
	}
}

// A TCP SYN must be accepted: the forwarder performs the handshake, so a
// SYN-ACK for the original tuple comes back out of the stack.
func TestTunTCPForwarderAnswersSYN(t *testing.T) {
	s, ep := newTestStack(t)
	installForwarders(s,
		func(conn *gonet.TCPConn, _ stack.TransportEndpointID) {
			_ = conn.Close()
		},
		func(_ *gonet.UDPConn, _ stack.TransportEndpointID) {})

	injectTCP(t, ep, net.ParseIP("10.99.0.1").To4(), net.ParseIP("192.0.2.10").To4(), 41234, 80, 1000, header.TCPFlagSyn, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reply := ep.ReadContext(ctx)
	if reply == nil {
		t.Fatal("no SYN-ACK was emitted: the TCP path from the adapter is not wired to the stack")
	}
	raw := reply.ToBuffer()
	payload := raw.Flatten()
	reply.DecRef()
	if len(payload) < header.IPv4MinimumSize+header.TCPMinimumSize {
		t.Fatalf("short reply packet: %d bytes", len(payload))
	}
	tcpHdr := header.TCP(payload[header.IPv4MinimumSize:])
	if tcpHdr.Flags()&header.TCPFlagSyn == 0 || tcpHdr.Flags()&header.TCPFlagAck == 0 {
		t.Fatalf("reply is not a SYN-ACK: flags=%b", tcpHdr.Flags())
	}
	if tcpHdr.DestinationPort() != 41234 || tcpHdr.SourcePort() != 80 {
		t.Fatalf("SYN-ACK ports = %d->%d, want 80->41234", tcpHdr.SourcePort(), tcpHdr.DestinationPort())
	}
}

// ---------------------------------------------------------------- helpers ---

func injectUDP(t *testing.T, ep *channel.Endpoint, src, dst net.IP, srcPort, dstPort uint16, payload []byte) {
	t.Helper()
	const ipLen = header.IPv4MinimumSize
	const udpLen = header.UDPMinimumSize
	buf := make([]byte, ipLen+udpLen+len(payload))

	ip := header.IPv4(buf)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(buf)),
		TTL:         64,
		Protocol:    uint8(header.UDPProtocolNumber),
		SrcAddr:     tcpip.AddrFromSlice(src),
		DstAddr:     tcpip.AddrFromSlice(dst),
		Flags:       header.IPv4FlagDontFragment,
	})
	ip.SetChecksum(0)
	ip.SetChecksum(^ip.CalculateChecksum())

	u := header.UDP(ip.Payload())
	u.Encode(&header.UDPFields{
		SrcPort: srcPort,
		DstPort: dstPort,
		Length:  uint16(udpLen + len(payload)),
	})
	copy(u[udpLen:], payload)
	u.SetChecksum(0)
	xsum := header.PseudoHeaderChecksum(header.UDPProtocolNumber, ip.SourceAddress(), ip.DestinationAddress(), uint16(udpLen+len(payload)))
	u.SetChecksum(^u.CalculateChecksum(xsum))

	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(buf)})
	pkt.RXChecksumValidated = true
	ep.InjectInbound(ipv4.ProtocolNumber, pkt)
	pkt.DecRef()
}

func injectTCP(t *testing.T, ep *channel.Endpoint, src, dst net.IP, srcPort, dstPort uint16, seq uint32, flags header.TCPFlags, ack uint32) {
	t.Helper()
	const ipLen = header.IPv4MinimumSize
	const tcpLen = header.TCPMinimumSize
	buf := make([]byte, ipLen+tcpLen)

	ip := header.IPv4(buf)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(buf)),
		TTL:         64,
		Protocol:    uint8(header.TCPProtocolNumber),
		SrcAddr:     tcpip.AddrFromSlice(src),
		DstAddr:     tcpip.AddrFromSlice(dst),
		Flags:       header.IPv4FlagDontFragment,
	})
	ip.SetChecksum(0)
	ip.SetChecksum(^ip.CalculateChecksum())

	tcpHdr := header.TCP(ip.Payload())
	tcpHdr.Encode(&header.TCPFields{
		SrcPort:    srcPort,
		DstPort:    dstPort,
		SeqNum:     seq,
		AckNum:     ack,
		DataOffset: tcpLen,
		Flags:      flags,
		WindowSize: 65535,
	})
	tcpHdr.SetChecksum(0)
	xsum := header.PseudoHeaderChecksum(header.TCPProtocolNumber, ip.SourceAddress(), ip.DestinationAddress(), uint16(tcpLen))
	tcpHdr.SetChecksum(^tcpHdr.CalculateChecksum(xsum))

	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(buf)})
	pkt.RXChecksumValidated = true
	ep.InjectInbound(ipv4.ProtocolNumber, pkt)
	pkt.DecRef()
}
