//go:build !windows

package main

// runAsWindowsService is a no-op outside Windows: systemd/supervisor/launchd
// handle the lifecycle there, and main() owns signal handling.
func runAsWindowsService(_ string, _ bool) bool { return false }
