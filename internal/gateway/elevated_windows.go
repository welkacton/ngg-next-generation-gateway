//go:build windows

package gateway

import "golang.org/x/sys/windows"

// isElevated reports whether the current process holds an elevated token.
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
