package main

import (
	"fmt"
	stdnet "net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/ngg-project/ngg/internal/agent"
)

// cpuWarmup is the sampling window used for the instantaneous CPU percentage.
// gopsutil's Percent needs a non-zero interval to produce a real number; the
// first call after start-up would otherwise always report 0.
const cpuWarmup = 200 * time.Millisecond

// sampler keeps the previous cumulative counters so byte rates are real
// differences since the last sample, not made-up numbers. The first sample of
// a process therefore reports rate 0.
type sampler struct {
	mu        sync.Mutex
	prevAt    time.Time
	prevIface map[string]ifaceCounters
}

type ifaceCounters struct {
	rx uint64
	tx uint64
}

func newSampler() *sampler {
	return &sampler{prevIface: map[string]ifaceCounters{}}
}

// collect gathers one snapshot. Individual collector failures are reported in
// metrics.note instead of being replaced by fake numbers.
func (s *sampler) collect() (agent.AgentMetrics, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	problems := []string{}
	m := agent.AgentMetrics{Source: "agent"}

	if pct, err := cpu.Percent(cpuWarmup, false); err == nil && len(pct) > 0 {
		m.CPUPct = round2(pct[0])
	} else if err != nil {
		problems = append(problems, "cpu: "+err.Error())
	}
	if n, err := cpu.Counts(true); err == nil {
		m.CPUCores = n
	}
	if avg, err := load.Avg(); err == nil {
		m.Load1 = round2(avg.Load1)
	} else {
		// Windows has no load average; report 0 and say so rather than inventing.
		problems = append(problems, "load1 不可用（该平台无 load average）")
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemTotal, m.MemUsed = vm.Total, vm.Used
	} else {
		problems = append(problems, "mem: "+err.Error())
	}
	if sw, err := mem.SwapMemory(); err == nil {
		m.SwapTotal, m.SwapUsed = sw.Total, sw.Used
	}

	m.Disks = collectDisks(&m, &problems)

	interfaces, rx, tx, rates := s.collectInterfaces(&problems)
	m.Interfaces = interfaces
	m.RxBytes, m.TxBytes = rx, tx
	m.RxRate, m.TxRate = rates.rxRate, rates.txRate

	if up, err := host.Uptime(); err == nil {
		m.UptimeSec = up
	}
	hostname := ""
	if info, err := host.Info(); err == nil {
		hostname = info.Hostname
	} else {
		problems = append(problems, "host: "+err.Error())
	}
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	m.SampledAt = time.Now().UTC().Format(time.RFC3339)
	if len(problems) > 0 {
		m.Note = strings.Join(problems, "; ")
	}
	return m, hostname
}

// collectDisks lists every real mount point once, skipping pseudo filesystems
// that report 0 total bytes.
func collectDisks(m *agent.AgentMetrics, problems *[]string) []agent.AgentDisk {
	parts, err := disk.Partitions(false)
	if err != nil {
		*problems = append(*problems, "disk: "+err.Error())
		return []agent.AgentDisk{}
	}
	out := []agent.AgentDisk{}
	seen := map[string]bool{}
	var total, used uint64
	for _, p := range parts {
		mount := strings.TrimSpace(p.Mountpoint)
		if mount == "" || seen[mount] {
			continue
		}
		seen[mount] = true
		u, err := disk.Usage(mount)
		if err != nil || u == nil || u.Total == 0 {
			continue
		}
		out = append(out, agent.AgentDisk{Mount: mount, FSType: p.Fstype, Total: u.Total, Used: u.Used})
		total += u.Total
		used += u.Used
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	m.DiskTotal, m.DiskUsed = total, used
	return out
}

type ratePair struct{ rxRate, txRate float64 }

// collectInterfaces merges gopsutil interface metadata with real IO counters
// and computes rates from the previous sample. Addrs come from the standard
// library because gopsutil does not fill them on every platform.
func (s *sampler) collectInterfaces(problems *[]string) ([]agent.AgentInterface, uint64, uint64, ratePair) {
	ifaces, err := gnet.Interfaces()
	if err != nil {
		*problems = append(*problems, "net.Interfaces: "+err.Error())
		return []agent.AgentInterface{}, 0, 0, ratePair{}
	}
	counters, err := gnet.IOCounters(true)
	if err != nil {
		*problems = append(*problems, "net.IOCounters: "+err.Error())
	}
	byName := make(map[string]gnet.IOCountersStat, len(counters))
	for _, c := range counters {
		byName[c.Name] = c
	}

	now := time.Now()
	elapsed := 0.0
	if !s.prevAt.IsZero() {
		elapsed = now.Sub(s.prevAt).Seconds()
	}

	out := make([]agent.AgentInterface, 0, len(ifaces))
	var totalRx, totalTx uint64
	var aggRxRate, aggTxRate float64
	next := map[string]ifaceCounters{}
	for _, ifc := range ifaces {
		entry := agent.AgentInterface{
			Name:      ifc.Name,
			MAC:       ifc.HardwareAddr,
			MTU:       ifc.MTU,
			Up:        hasFlag(ifc.Flags, "up"),
			Addresses: ifaceAddresses(ifc.Name),
		}
		if c, ok := byName[ifc.Name]; ok {
			entry.RxBytes, entry.TxBytes = c.BytesRecv, c.BytesSent
		}
		if prev, ok := s.prevIface[ifc.Name]; ok && elapsed > 0 {
			if entry.RxBytes >= prev.rx {
				entry.RxRate = round2(float64(entry.RxBytes-prev.rx) / elapsed)
			}
			if entry.TxBytes >= prev.tx {
				entry.TxRate = round2(float64(entry.TxBytes-prev.tx) / elapsed)
			}
		}
		next[ifc.Name] = ifaceCounters{rx: entry.RxBytes, tx: entry.TxBytes}
		if !hasFlag(ifc.Flags, "loopback") {
			totalRx += entry.RxBytes
			totalTx += entry.TxBytes
			aggRxRate += entry.RxRate
			aggTxRate += entry.TxRate
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	s.prevIface = next
	s.prevAt = now
	return out, totalRx, totalTx, ratePair{rxRate: round2(aggRxRate), txRate: round2(aggTxRate)}
}

func ifaceAddresses(name string) []string {
	out := []string{}
	ifi, err := stdnet.InterfaceByName(name)
	if err != nil {
		return out
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*stdnet.IPNet); ok && ipnet.IP != nil {
			out = append(out, fmt.Sprintf("%s/%d", ipnet.IP.String(), prefixLen(ipnet)))
			continue
		}
		out = append(out, a.String())
	}
	return out
}

func prefixLen(n *stdnet.IPNet) int {
	ones, _ := n.Mask.Size()
	return ones
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func round2(v float64) float64 {
	if v < 0 {
		return 0
	}
	return float64(int64(v*100+0.5)) / 100
}
