//go:build !windows

package gateway

import "os"

// isElevated reports whether the process runs as root. That is the only
// privilege that can rewrite routes on Unix-like hosts.
func isElevated() bool {
	return os.Geteuid() == 0
}
