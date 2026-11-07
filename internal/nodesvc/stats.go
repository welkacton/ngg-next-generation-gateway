package nodesvc

// Real host metrics for the node API.
//
// These are the same sources Remnawave's node reads (systeminformation in
// JS, /proc and uname under the hood), read here through gopsutil so the
// numbers are measured rather than remembered: memory from /proc/meminfo,
// load from /proc/loadavg, counters from /proc/net/dev, disk via statfs.
//
// Interface rates need two samples, so a 1 s sampler runs in the background
// and the request handler reads the latest snapshot. Without it the first
// request after a restart would report 0 B/s, which looks like a broken node.

import (
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"

	"github.com/ngg-project/ngg/internal/nodeapi"
)

// ifaceSample is one interface's counters at a point in time.
type ifaceSample struct {
	rx, tx uint64
	at     time.Time
}

// Collector produces System snapshots. It is safe for concurrent use.
type Collector struct {
	mu   sync.Mutex
	last map[string]ifaceSample
	// rates holds the per-interface byte/s computed by the last sampler tick.
	rates map[string][2]float64
	stop  chan struct{}
	once  sync.Once
}

// NewCollector builds a collector and primes the counter baseline so the first
// request already has rates instead of a divide-by-zero.
func NewCollector() *Collector {
	c := &Collector{last: map[string]ifaceSample{}, rates: map[string][2]float64{}, stop: make(chan struct{})}
	c.sample(time.Now())
	return c
}

// Start runs the rate sampler until Stop.
func (c *Collector) Start(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-c.stop:
				return
			case now := <-t.C:
				c.sample(now)
			}
		}
	}()
}

// Stop ends the sampler. Safe to call more than once.
func (c *Collector) Stop() {
	c.once.Do(func() { close(c.stop) })
}

// sample reads the counters and derives rates against the previous tick.
func (c *Collector) sample(now time.Time) {
	counters, err := psnet.IOCounters(true)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ct := range counters {
		if isLoopback(ct.Name) {
			continue
		}
		cur := ifaceSample{rx: ct.BytesRecv, tx: ct.BytesSent, at: now}
		if prev, ok := c.last[ct.Name]; ok {
			dt := now.Sub(prev.at).Seconds()
			if dt > 0 {
				rx := float64(cur.rx-prev.rx) / dt
				tx := float64(cur.tx-prev.tx) / dt
				if rx < 0 {
					rx = 0 // counter reset (reboot / interface recreated)
				}
				if tx < 0 {
					tx = 0
				}
				c.rates[ct.Name] = [2]float64{rx, tx}
			}
		}
		c.last[ct.Name] = cur
	}
}

// System assembles the payload the panel reads.
func (c *Collector) System() nodeapi.System {
	info := c.info()
	stats := nodeapi.SystemStats{LoadAvg: []float64{0, 0, 0}}

	if vm, err := mem.VirtualMemory(); err == nil {
		stats.MemoryTotal = vm.Total
		stats.MemoryUsed = vm.Used
		stats.MemoryFree = vm.Free
	}
	// 300 ms is enough for a real percentage on an idle box without making the
	// console wait; gopsutil computes the delta itself.
	if pct, err := cpu.Percent(300*time.Millisecond, false); err == nil && len(pct) > 0 {
		stats.CPUPercent = round1(pct[0])
	}
	if avg, err := load.Avg(); err == nil {
		stats.LoadAvg = []float64{round2(avg.Load1), round2(avg.Load5), round2(avg.Load15)}
	}
	if u, err := host.Uptime(); err == nil {
		stats.Uptime = u
	}
	if du, err := disk.Usage("/"); err == nil {
		stats.DiskTotal = du.Total
		stats.DiskUsed = du.Used
		stats.DiskMount = du.Path
	}

	stats.Interfaces = c.interfaces()
	if len(stats.Interfaces) > 0 {
		// The headline interface is the busiest one: that is the uplink the
		// node's traffic actually crosses, not whatever the OS lists first.
		best := stats.Interfaces[0]
		for _, it := range stats.Interfaces[1:] {
			if it.RxBytesPerSec+it.TxBytesPerSec > best.RxBytesPerSec+best.TxBytesPerSec {
				best = it
			}
		}
		stats.Interface = &best
	}
	stats.SampledAt = time.Now().UTC().Format(time.RFC3339)
	return nodeapi.System{Info: info, Stats: stats}
}

func (c *Collector) info() nodeapi.SystemInfo {
	out := nodeapi.SystemInfo{
		Arch:    runtime.GOARCH,
		CPUs:    runtime.NumCPU(),
		Type:    "Linux",
		Release: "",
		Version: "",
	}
	if hi, err := host.Info(); err == nil {
		out.Hostname = hi.Hostname
		out.Platform = hi.Platform
		out.Release = hi.KernelVersion
		out.Version = hi.PlatformVersion
		if hi.OS != "" {
			out.Type = capitalize(hi.OS) // "linux" → "Linux", matching upstream
		}
		if hi.KernelArch != "" {
			out.Arch = hi.KernelArch
		}
	}
	if cs, err := cpu.Info(); err == nil && len(cs) > 0 {
		out.CPUModel = strings.TrimSpace(cs[0].ModelName)
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		out.MemoryTotal = vm.Total
	}
	if counters, err := psnet.IOCounters(true); err == nil {
		names := make([]string, 0, len(counters))
		for _, ct := range counters {
			if isLoopback(ct.Name) {
				continue
			}
			names = append(names, ct.Name)
		}
		sort.Strings(names)
		out.NetworkInterfaces = names
	}
	return out
}

func (c *Collector) interfaces() []nodeapi.NetworkInterface {
	counters, err := psnet.IOCounters(true)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]nodeapi.NetworkInterface, 0, len(counters))
	for _, ct := range counters {
		if isLoopback(ct.Name) {
			continue
		}
		r := c.rates[ct.Name]
		out = append(out, nodeapi.NetworkInterface{
			Interface:     ct.Name,
			RxBytesPerSec: round2(r[0]),
			TxBytesPerSec: round2(r[1]),
			RxTotal:       ct.BytesRecv,
			TxTotal:       ct.BytesSent,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Interface < out[j].Interface })
	return out
}

func isLoopback(name string) bool {
	return name == "lo" || name == "lo0" || strings.HasPrefix(name, "Loopback")
}

// capitalize upper-cases the first letter (host.Info reports "linux").
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }
