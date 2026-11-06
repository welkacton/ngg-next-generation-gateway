package netengine

import (
	"net"
	"sort"
	"strings"
)

// AdapterInfo describes one interface of the host. It is deliberately a thin,
// honest view of what the OS reports: nothing is inferred beyond the virtual
// adapter heuristic, and the "default" flag comes from a real route probe.
type AdapterInfo struct {
	Name     string   `json:"name"`
	Index    int      `json:"index"`
	MTU      int      `json:"mtu"`
	Up       bool     `json:"up"`
	Loopback bool     `json:"loopback"`
	IPv4     []string `json:"ipv4"`
	IPv6     []string `json:"ipv6"`
	// Virtual marks adapters that are almost always created by a tunnel
	// client (wintun / tap / tailscale / vmware ...). It is a hint for the
	// console, never a decision: the operator can still pick any interface.
	Virtual bool `json:"virtual"`
	// IsNGG marks the adapter NGG itself creates/owns (config tun.name).
	IsNGG bool `json:"is_ngg"`
	// Default marks the interface that currently carries the default route.
	Default bool   `json:"default"`
	Detail  string `json:"detail"`
}

var virtualHints = []string{
	"wintun", "tap", "tun", "tailscale", "wireguard", "vpn",
	"vmware", "virtualbox", "vbox", "hyper-v", "vethernet", "docker",
	"clash", "mihomo", "sing", "xray", "proxy", "loopback",
}

// ListAdapters enumerates the host's interfaces, flagging the one that
// currently carries the default route and the one NGG owns.
func ListAdapters(tunName string) []AdapterInfo {
	tunName = strings.TrimSpace(tunName)
	defIP := defaultSourceIPv4()

	descriptions := adapterDescriptions()
	ifaces, err := net.Interfaces()
	if err != nil {
		return []AdapterInfo{}
	}
	out := make([]AdapterInfo, 0, len(ifaces))
	for _, ifc := range ifaces {
		info := AdapterInfo{
			Name:     ifc.Name,
			Index:    ifc.Index,
			MTU:      ifc.MTU,
			Up:       ifc.Flags&net.FlagUp != 0,
			Loopback: ifc.Flags&net.FlagLoopback != 0,
			Detail:   ifc.HardwareAddr.String(),
			IPv4:     []string{},
			IPv6:     []string{},
		}
		if desc := descriptions[ifc.Name]; desc != "" {
			info.Detail = desc
		} else if info.Detail == "" {
			info.Detail = "无 MAC（虚拟接口）"
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				info.IPv4 = append(info.IPv4, v4.String())
				if defIP != "" && v4.String() == defIP {
					info.Default = true
				}
				continue
			}
			info.IPv6 = append(info.IPv6, ipnet.IP.String())
		}
		if tunName != "" && strings.EqualFold(info.Name, tunName) {
			info.IsNGG = true
			info.Virtual = true
		}
		if !info.IsNGG {
			info.Virtual = info.Virtual || looksVirtual(info.Name, info.Detail)
		}
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		if out[i].IsNGG != out[j].IsNGG {
			return out[i].IsNGG
		}
		if len(out[i].IPv4) != len(out[j].IPv4) {
			return len(out[i].IPv4) > 0
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func looksVirtual(name, detail string) bool {
	haystack := strings.ToLower(name + " " + detail)
	for _, hint := range virtualHints {
		if strings.Contains(haystack, hint) {
			return true
		}
	}
	return false
}

// defaultSourceIPv4 returns the local address the OS would use to reach the
// internet. A UDP "connect" sends no packet but resolves the route, so this is
// a real probe rather than a guess.
func defaultSourceIPv4() string {
	c, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if addr, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}
