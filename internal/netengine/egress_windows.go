//go:build windows

package netengine

import (
	"encoding/binary"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ipUnicastIf pins a socket to one interface. Without it, NGG's own outbound
// dial would be routed back into the NGG TUN and loop forever once the default
// route points at the adapter. The value must be network byte order.
const ipUnicastIf = 31

var egressIfIndex atomic.Uint32

func setSockMark(fd uintptr, _ int) error {
	idx := egressIfIndex.Load()
	if idx == 0 {
		return nil
	}
	// IP_UNICAST_IF expects the index in network byte order.
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], idx)
	return windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIf, int(binary.LittleEndian.Uint32(b[:])))
}

func setEgressInterface(index uint32) { egressIfIndex.Store(index) }

// interfaceIndexByName maps a Windows adapter friendly name ("WLAN", "以太网")
// to its interface index, which is what IP_UNICAST_IF needs.
func interfaceIndexByName(name string) (uint32, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false
	}
	var buf [32 * 1024]byte
	size := uint32(len(buf))
	addr := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0, addr, &size); err != nil {
		return 0, false
	}
	for a := addr; a != nil; a = a.Next {
		friendly := windows.UTF16PtrToString(a.FriendlyName)
		if strings.EqualFold(friendly, name) {
			return a.IfIndex, true
		}
	}
	return 0, false
}

// bindEgressInterface pins NGG's own dials to the interface the operator chose
// as the WAN uplink. Without it, a captured default route would send the
// engine's own traffic back into its own TUN.
func bindEgressInterface(name string) {
	if strings.TrimSpace(name) == "" {
		// Keep the current pin: clearing it while a TUN is attached would send
		// the engine's own dials straight back into the tunnel.
		return
	}
	if idx, ok := interfaceIndexByName(name); ok {
		setEgressInterface(idx)
		return
	}
	setEgressInterface(0)
}

// adapterDescriptions maps friendly name -> Windows adapter description
// ("Wintun Userspace Tunnel", "TAP-Windows Adapter V9", ...). The description
// is what makes virtual-adapter detection reliable; the interface name alone
// can be a generic "本地连接".
func adapterDescriptions() map[string]string {
	out := map[string]string{}
	var buf [64 * 1024]byte
	size := uint32(len(buf))
	addr := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0, addr, &size); err != nil {
		return out
	}
	for a := addr; a != nil; a = a.Next {
		name := windows.UTF16PtrToString(a.FriendlyName)
		if name == "" {
			continue
		}
		out[name] = windows.UTF16PtrToString(a.Description)
	}
	return out
}
