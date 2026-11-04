//go:build linux

package netengine

import "golang.org/x/sys/unix"

func setSockMark(fd uintptr, mark int) error {
	return unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
}
