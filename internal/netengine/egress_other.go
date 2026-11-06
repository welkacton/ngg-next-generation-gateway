//go:build !linux && !windows

package netengine

// On non-Linux platforms NGG uses a bound local address for egress isolation.
// SO_MARK is Linux-specific; the absence is reported honestly via Status().
func setSockMark(fd uintptr, mark int) error { return nil }

// setEgressInterface is Windows-only (IP_UNICAST_IF); other platforms use
// SO_MARK or a dedicated routing table.
func setEgressInterface(_ uint32) {}

// bindEgressInterface is Windows-only; Linux isolates egress by SO_MARK and a
// dedicated routing table, other platforms fall back to the bound source IP.
func bindEgressInterface(_ string) {}

// adapterDescriptions is Windows-only; other platforms have no equivalent
// description string in the standard library.
func adapterDescriptions() map[string]string { return nil }
