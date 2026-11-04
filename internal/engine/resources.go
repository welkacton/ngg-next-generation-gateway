package engine

import (
	"fmt"
	"net"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// portAllocator is the Stage-1 Network Resource Manager. Each kernel receives
// a disjoint 5-port block (mixed / socks / http / api / dns) so no two kernels
// can ever fight for the same listener. Availability is checked against the
// REAL OS socket table, not a bookkeeping flag.
type portAllocator struct {
	mu     sync.Mutex
	base   int
	stride int
	secret string
	alloc  map[contracts.CoreID]core.RuntimeParams
	used   map[int]contracts.CoreID
}

var _ core.PortAllocator = (*portAllocator)(nil)

func newPortAllocator(base, stride int, secret string) *portAllocator {
	if stride < 5 {
		stride = 10
	}
	return &portAllocator{base: base, stride: stride, secret: secret,
		alloc: map[contracts.CoreID]core.RuntimeParams{}, used: map[int]contracts.CoreID{}}
}

func (p *portAllocator) Allocate(id contracts.CoreID) (core.RuntimeParams, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.alloc[id]; ok {
		return v, nil
	}
	for slot := 0; slot < 200; slot++ {
		b := p.base + slot*p.stride
		candidate := core.RuntimeParams{
			MixedPort: b, SocksPort: b + 1, HTTPPort: b + 2,
			APIPort: b + 3, DNSPort: b + 4, Secret: p.secret,
		}
		if err := candidate.Validate(); err != nil {
			return core.RuntimeParams{}, err
		}
		ports := candidate.Ports()
		blocked := false
		for _, port := range ports {
			if _, taken := p.used[port]; taken {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		if len(portIsOccupied(ports)) > 0 {
			continue
		}
		for _, port := range ports {
			p.used[port] = id
		}
		p.alloc[id] = candidate
		return candidate, nil
	}
	return core.RuntimeParams{}, fmt.Errorf("no free isolated port block for kernel %q (base=%d stride=%d)", id, p.base, p.stride)
}

func (p *portAllocator) Get(id contracts.CoreID) (core.RuntimeParams, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.alloc[id]
	return v, ok
}

func (p *portAllocator) Release(id contracts.CoreID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.alloc[id]; ok {
		for _, port := range v.Ports() {
			delete(p.used, port)
		}
	}
	delete(p.alloc, id)
}

// Check returns the ports that are actually occupied by some OS listener.
func (p *portAllocator) Check(ports []int) []int { return portIsOccupied(ports) }

func (p *portAllocator) Allocations() []contracts.PortAllocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.alloc))
	for id := range p.alloc {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]contracts.PortAllocation, 0, len(ids))
	for _, sid := range ids {
		id := contracts.CoreID(sid)
		v := p.alloc[id]
		out = append(out, contracts.PortAllocation{CoreID: id, Ports: v.Ports(), Names: v.PortNames()})
	}
	return out
}

// portIsOccupied probes both TCP and UDP. A failure to bind means occupied.
func portIsOccupied(ports []int) []int {
	var out []int
	for _, port := range ports {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		tcp, errTCP := net.Listen("tcp", addr)
		if errTCP == nil {
			_ = tcp.Close()
		}
		udp, errUDP := net.ListenPacket("udp", addr)
		if errUDP == nil {
			_ = udp.Close()
		}
		if errTCP != nil || errUDP != nil {
			out = append(out, port)
		}
	}
	return out
}

// archDetector reports the REAL host platform via runtime.GOOS/GOARCH.
type archDetector struct{}

var _ core.ArchDetector = (*archDetector)(nil)

func (archDetector) Detect() contracts.Platform {
	return contracts.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func (d archDetector) SupportedBy(a core.CoreAdapter) bool {
	if a == nil {
		return false
	}
	p := d.Detect()
	for _, sp := range a.SupportedPlatforms() {
		if sp.OS == p.OS && sp.Arch == p.Arch {
			return true
		}
	}
	return false
}

// healthMonitor reads real OS process metrics via gopsutil.
type healthMonitor struct {
	started map[contracts.CoreID]time.Time
	mu      sync.Mutex
}

var _ core.HealthMonitor = (*healthMonitor)(nil)

func newHealthMonitor() *healthMonitor {
	return &healthMonitor{started: map[contracts.CoreID]time.Time{}}
}

func (h *healthMonitor) MarkStarted(id contracts.CoreID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.started[id] = time.Now()
}

func (h *healthMonitor) Sample(id contracts.CoreID, pid int) (contracts.ProcessStats, error) {
	if pid <= 0 {
		return contracts.ProcessStats{}, fmt.Errorf("kernel %q is not running", id)
	}
	stats, err := sampleProcess(pid)
	if err != nil {
		return contracts.ProcessStats{}, err
	}
	h.mu.Lock()
	start := h.started[id]
	h.mu.Unlock()
	if !start.IsZero() {
		stats.UptimeSec = int64(time.Since(start).Seconds())
	}
	stats.SampledAt = time.Now().UTC()
	return stats, nil
}
