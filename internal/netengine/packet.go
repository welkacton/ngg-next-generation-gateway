package netengine

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
)

// PacketProcessor parses real IP packets coming from the NGG-owned TUN and
// classifies them. No gopacket dependency: a compact, auditable parser is
// enough for the Stage 2 forwarding decisions.
type packetProcessor struct {
	resources *ResourceManager
}

var _ PacketProcessor = (*packetProcessor)(nil)
var _ PacketProcessorInterface = (*packetProcessor)(nil)

// PacketProcessorInterface is an alias kept so the reserved interface in
// interfaces.go remains the authoritative contract.
type PacketProcessorInterface interface {
	Process(ctx context.Context, pkt []byte) (Action, error)
	Parse(pkt []byte) (Packet, error)
}

func NewPacketProcessor(r *ResourceManager) *packetProcessor {
	return &packetProcessor{resources: r}
}

func (p *packetProcessor) Parse(raw []byte) (Packet, error) {
	if len(raw) < 20 {
		return Packet{}, fmt.Errorf("IP packet too short: %d bytes", len(raw))
	}
	version := int(raw[0] >> 4)
	pkt := Packet{Raw: raw, Version: version}
	var headerLen int
	var proto uint8
	switch version {
	case 4:
		headerLen = int(raw[0]&0x0f) * 4
		if headerLen < 20 || len(raw) < headerLen {
			return Packet{}, fmt.Errorf("invalid IPv4 IHL")
		}
		proto = raw[9]
		pkt.Src = net.IPv4(raw[12], raw[13], raw[14], raw[15])
		pkt.Dst = net.IPv4(raw[16], raw[17], raw[18], raw[19])
	case 6:
		if len(raw) < 40 {
			return Packet{}, fmt.Errorf("IPv6 packet too short")
		}
		headerLen = 40
		proto = raw[6]
		pkt.Src = make(net.IP, 16)
		pkt.Dst = make(net.IP, 16)
		copy(pkt.Src, raw[8:24])
		copy(pkt.Dst, raw[24:40])
		// Skip the common extension headers so TCP/UDP ports are found.
		for proto == 0 || proto == 43 || proto == 44 || proto == 60 {
			if len(raw) < headerLen+2 {
				return Packet{}, fmt.Errorf("truncated IPv6 extension header")
			}
			next := raw[headerLen]
			l := int(raw[headerLen+1])*8 + 8
			proto = next
			headerLen += l
			if len(raw) < headerLen {
				return Packet{}, fmt.Errorf("truncated IPv6 extension header")
			}
		}
	default:
		return Packet{}, fmt.Errorf("unsupported IP version %d", version)
	}

	switch proto {
	case 6:
		pkt.Protocol = "tcp"
	case 17:
		pkt.Protocol = "udp"
	case 1, 58:
		pkt.Protocol = "icmp"
	default:
		pkt.Protocol = "other"
	}
	if len(raw) >= headerLen+4 && (pkt.Protocol == "tcp" || pkt.Protocol == "udp") {
		pkt.SrcPort = binary.BigEndian.Uint16(raw[headerLen : headerLen+2])
		pkt.DstPort = binary.BigEndian.Uint16(raw[headerLen+2 : headerLen+4])
	}
	if pkt.Protocol == "tcp" && len(raw) >= headerLen+20 {
		off := (raw[headerLen+12] >> 4) * 4
		if int(off) < 20 {
			off = 20
		}
		if len(raw) > headerLen+int(off) {
			pkt.Payload = raw[headerLen+int(off):]
		}
	} else if pkt.Protocol == "udp" && len(raw) >= headerLen+8 {
		if len(raw) > headerLen+8 {
			pkt.Payload = raw[headerLen+8:]
		}
	}
	pkt.IsDNS = pkt.Protocol == "udp" && pkt.DstPort == 53
	if p.resources != nil {
		pkt.IsLoop = p.resources.IsProtectedDestination(pkt.Dst, pkt.DstPort)
	}
	return pkt, nil
}

// Process maps a parsed packet onto the NGG action set. The actual per-session
// routing decision is made by RuleEngine + ProtocolDispatcher; this stage only
// handles packet-level classification and hard safety rules.
func (p *packetProcessor) Process(_ context.Context, raw []byte) (Action, error) {
	pkt, err := p.Parse(raw)
	if err != nil {
		return ActionDrop, err
	}
	if pkt.IsLoop {
		return ActionDrop, ErrLoopDetected
	}
	if pkt.IsDNS {
		return ActionDNS, nil
	}
	if pkt.Protocol == "other" || pkt.Protocol == "icmp" {
		return ActionForward, nil
	}
	return ActionForward, nil
}
