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
	CheckedAt        time.Time       `json:"checked_at"`
}

// probeTarget is a routable address that is never actually contacted: a UDP
// "dial" only fills in the local socket address chosen by the kernel's own
// routing table, which is exactly the egress we want to report.
const probeTarget = "8.8.8.8:80"

const probeTimeout = 2 * time.Second

// Status reads the host state now. It never blocks longer than probeTimeout
// plus the cost of enumerating interfaces.
func (s *Store) Status() Status {
	st := Status{
		Interfaces: []InterfaceInfo{},
		DNS:        []string{},
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
	type liveIface struct {
		info InterfaceInfo
		ips  []net.IP
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
	return st
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
