//go:build linux

package netengine

import "golang.org/x/sys/unix"

func setSockMark(fd uintptr, mark int) error {
	return unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
}

// bindEgressInterface is a no-op on Linux: the physical uplink is isolated with
// SO_MARK plus the dedicated NGG routing table, not by pinning an interface.
func bindEgressInterface(_ string) {}

// adapterDescriptions has no equivalent in the standard library on Linux; the
// console falls back to the interface name for virtual-adapter hints.
func adapterDescriptions() map[string]string { return nil }
