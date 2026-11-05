//go:build windows

package netengine

import (
	"encoding/binary"
	"sync/atomic"

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
