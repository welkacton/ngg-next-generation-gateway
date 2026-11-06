package gateway

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// InterfaceInfo is one real NIC as reported by the host.
type InterfaceInfo struct {
	Name         string   `json:"name"`
	HardwareAddr string   `json:"hardware_addr"`
	MTU          int      `json:"mtu"`
	Addresses    []string `json:"addresses"`
	Up           bool     `json:"up"`
}

// WANLink is the live view of one enabled WAN port. Everything in it is read
// from the host: the interface name and route come from the saved policy, the
// address and online flag from the real NIC.
//
// A port whose interface is not configured, or is not present on this host,
// reports online=false with an empty address — it is never guessed.
type WANLink struct {
	ID        string `json:"id"`
	Interface string `json:"interface"`
	Route     string `json:"route"`
	Address   string `json:"address"`
	Online    bool   `json:"online"`
}

// Status is the live host view plus the parts of the saved policy that can be
// answered honestly.
//
// DefaultGateway is only filled on Linux (see defaultGateway). default_interface
// / default_address come from a real UDP route lookup, so behind a full-tunnel
// VPN they describe the tunnel NIC that actually carries the traffic.
type Status struct {
	Interfaces       []InterfaceInfo `json:"interfaces"`
	DefaultInterface string          `json:"default_interface"`
	DefaultAddress   string          `json:"default_address"`
	DefaultGateway   string          `json:"default_gateway"`
	DNS              []string        `json:"dns"`
	FirewallActive   bool            `json:"firewall_active"`
	WANLinks         []WANLink       `json:"wan_links"`
	CheckedAt        time.Time       `json:"checked_at"`
}

// probeTarget is a routable address that is never actually contacted: a UDP
// "dial" only fills in the local socket address chosen by the kernel's own
// routing table, which is exactly the egress we want to report.
const probeTarget = "8.8.8.8:80"

const probeTimeout = 2 * time.Second

// liveIface pairs what the UI sees with the parsed addresses, so an IP from
// the kernel's routing table can be matched back to its interface.
type liveIface struct {
	info InterfaceInfo
	ips  []net.IP
}

// Status reads the host state now. It never blocks longer than probeTimeout
// plus the cost of enumerating interfaces.
func (s *Store) Status() Status {
	st := Status{
		Interfaces: []InterfaceInfo{},
		DNS:        []string{},
		WANLinks:   []WANLink{},
		CheckedAt:  time.Now().UTC(),
	}
	cfg := s.Get()
	st.DNS = append(st.DNS, cfg.WAN.DNS...)
	// FirewallActive means "a policy was actually saved and it drops inbound
	// by default". A never-saved default does not count: nothing was applied.
	st.FirewallActive = s.Loaded() && cfg.Firewall.DefaultInbound == "drop"

	ifaces, err := net.Interfaces()
	if err != nil {
		return st
	}
	live := make([]liveIface, 0, len(ifaces))
	for _, ifc := range ifaces {
		info := InterfaceInfo{
			Name:         ifc.Name,
			HardwareAddr: ifc.HardwareAddr.String(),
			MTU:          ifc.MTU,
			Addresses:    []string{},
			Up:           ifc.Flags&net.FlagUp != 0,
		}
		li := liveIface{info: info}
		if addrs, err := ifc.Addrs(); err == nil {
			for _, a := range addrs {
				li.info.Addresses = append(li.info.Addresses, a.String())
				ip := addrIP(a)
				if ip != nil {
					li.ips = append(li.ips, ip)
				}
			}
		}
		live = append(live, li)
	}

	if ip := outboundIP(); ip != nil {
		st.DefaultAddress = ip.String()
		for _, li := range live {
			if containsIP(li.ips, ip) {
				st.DefaultInterface = li.info.Name
				break
			}
		}
	}
	for _, li := range live {
		st.Interfaces = append(st.Interfaces, li.info)
	}
	st.DefaultGateway = defaultGateway()
	st.WANLinks = wanLinks(cfg, live)
	return st
}

// wanLinks maps the enabled ports of the saved policy onto the NICs that
// really exist. A port is online only when its interface is present, up, and
// holds at least one address; anything else is reported as offline rather than
// assumed reachable.
func wanLinks(cfg Config, live []liveIface) []WANLink {
	links := make([]WANLink, 0, len(cfg.WANs))
	byName := make(map[string]liveIface, len(live))
	for _, li := range live {
		byName[li.info.Name] = li
	}
	for _, w := range cfg.WANs {
		if !w.Enabled {
			continue
		}
		link := WANLink{ID: w.ID, Interface: w.Interface, Route: w.Route}
		if w.Interface != "" {
			if li, ok := byName[w.Interface]; ok {
				link.Address = firstIPv4(li.info.Addresses)
				link.Online = li.info.Up && len(li.info.Addresses) > 0 && link.Address != ""
			}
		}
		links = append(links, link)
	}
	return links
}

// firstIPv4 picks the address that would actually be used, skipping loopback
// and link-local entries. It returns "" when the interface only carries IPv6.
func firstIPv4(addresses []string) string {
	for _, raw := range addresses {
		host := raw
		if i := strings.Index(host, "/"); i > 0 {
			host = host[:i]
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return ip.To4().String()
	}
	return ""
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	}
	// Fallback for exotic net.Addr implementations: parse "ip/prefix".
	raw := a.String()
	if i := strings.Index(raw, "/"); i > 0 {
		raw = raw[:i]
	}
	if i := strings.Index(raw, "%"); i > 0 {
		raw = raw[:i]
	}
	return net.ParseIP(raw)
}

func containsIP(list []net.IP, ip net.IP) bool {
	for _, candidate := range list {
		if candidate.Equal(ip) {
			return true
		}
	}
	return false
}

// outboundIP returns the local address the kernel would use to reach the
// internet, or nil when there is no route (offline host, blocked by policy).
func outboundIP() net.IP {
	conn, err := net.DialTimeout("udp", probeTarget, probeTimeout)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ua, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || ua == nil || ua.IP == nil || ua.IP.IsUnspecified() {
		return nil
	}
	return ua.IP
}

// defaultGateway reads the kernel IPv4 routing table.
//
// HONEST LIMIT: /proc/net/route only exists on Linux. Windows, macOS and the
// BSDs need a platform routing API that the standard library does not expose,
// and NGG will not shell out just to fill one field — so those platforms get
// an empty string instead of a guessed address.
func defaultGateway() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	return parseProcNetRoute(f)
}

// parseProcNetRoute parses /proc/net/route. Addresses there are 32-bit
// little-endian hex, so "0102A8C0" is 192.168.2.1.
func parseProcNetRoute(r io.Reader) string {
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // header row
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		// fields: Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		if fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&0x2 == 0 { // RTF_GATEWAY
			continue
		}
		gw, err := hexToIPv4(fields[2])
		if err != nil {
			continue
		}
		return gw
	}
	if err := sc.Err(); err != nil {
		return ""
	}
	return ""
}

func hexToIPv4(hex string) (string, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 32)
	if err != nil {
		return "", fmt.Errorf("%q 不是 32 位十六进制地址", hex)
	}
	ip := net.IPv4(byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
	if ip.IsUnspecified() {
		return "", fmt.Errorf("网关地址为 0.0.0.0")
	}
	return ip.String(), nil
}

// interfaceExists reports whether name is a real NIC on this host.
func interfaceExists(name string) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifc := range ifaces {
		if ifc.Name == name {
			return true
		}
	}
	return false
}
