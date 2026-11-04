// Package sdwan implements Stage 5 link health probing, policy-based WAN
// selection and automatic failover. All measurements are REAL TCP probes,
// never cached booleans set by a caller.
package sdwan

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/netengine"
)

type Policy string

const (
	PolicyLowestLatency Policy = "lowest-latency"
	PolicyWeighted      Policy = "weighted"
	PolicyPriority      Policy = "priority"
	PolicyFailover      Policy = "failover"
)

type Link struct {
	ID          string  `json:"id" yaml:"id"`
	Name        string  `json:"name" yaml:"name"`
	Interface   string  `json:"interface" yaml:"interface"`
	Gateway     string  `json:"gateway" yaml:"gateway"`
	ProbeTarget string  `json:"probe_target" yaml:"probe_target"`
	Weight      int     `json:"weight" yaml:"weight"`
	Cost        int     `json:"cost" yaml:"cost"`
	Priority    int     `json:"priority" yaml:"priority"`
	Enabled     bool    `json:"enabled" yaml:"enabled"`
	Healthy     bool    `json:"healthy" yaml:"healthy"`
	RTTMs       float64 `json:"rtt_ms" yaml:"rtt_ms"`
	LossPct     float64 `json:"loss_pct" yaml:"loss_pct"`
	Probes      int64   `json:"probes" yaml:"probes"`
	Failures    int64   `json:"failures" yaml:"failures"`
}

type Manager struct {
	mu       sync.RWMutex
	links    map[string]*Link
	policy   Policy
	active   string
	interval time.Duration
	timeout  time.Duration
	cancel   context.CancelFunc
	running  bool
	switches int64
}

var _ netengine.LinkSelector = (*Manager)(nil)

func NewManager(policy Policy, interval time.Duration) *Manager {
	if policy == "" {
		policy = PolicyLowestLatency
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &Manager{links: map[string]*Link{}, policy: policy, interval: interval, timeout: 1500 * time.Millisecond}
}

func (m *Manager) AddLink(l Link) error {
	if l.ID == "" || l.ProbeTarget == "" {
		return fmt.Errorf("link needs id and probe_target")
	}
	if l.Weight <= 0 {
		l.Weight = 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[l.ID] = &l
	return nil
}

func (m *Manager) RemoveLink(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[id]; !ok {
		return false
	}
	delete(m.links, id)
	if m.active == id {
		m.active = ""
	}
	return true
}

func (m *Manager) SetPolicy(p Policy) {
	m.mu.Lock()
	m.policy = p
	m.mu.Unlock()
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	c, cancel := context.WithCancel(context.Background())
	m.cancel, m.running = cancel, true
	interval, timeout := m.interval, m.timeout
	links := make([]*Link, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, l)
	}
	m.mu.Unlock()
	for _, l := range links {
		m.probe(l, timeout)
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-c.Done():
				return
			case <-ctx.Done():
				return
			case <-t.C:
				for _, l := range m.snapshot() {
					m.probe(l, timeout)
				}
				_, _ = m.SelectLink(c, "healthcheck")
			}
		}
	}()
}

func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, running := m.cancel, m.running
	m.running = false
	m.cancel = nil
	m.mu.Unlock()
	if running && cancel != nil {
		cancel()
	}
}

func (m *Manager) snapshot() []*Link {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Link, 0, len(m.links))
	for _, l := range m.links {
		out = append(out, l)
	}
	return out
}

// probe performs a REAL TCP connect to the link's probe target and measures
// elapsed time. Loss is an exponentially weighted moving average.
func (m *Manager) probe(l *Link, timeout time.Duration) {
	if !l.Enabled || l.ProbeTarget == "" {
		return
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", l.ProbeTarget, timeout)
	rtt := float64(time.Since(start).Microseconds()) / 1000.0
	m.mu.Lock()
	defer m.mu.Unlock()
	l.Probes++
	if err != nil {
		l.Failures++
		l.LossPct = l.LossPct*0.7 + 30
		l.Healthy = false
	} else {
		_ = conn.Close()
		l.RTTMs = l.RTTMs*0.7 + rtt*0.3
		l.LossPct *= 0.7
		l.Healthy = l.LossPct < 50
	}
}

// ReportResult lets the data path feed real per-connection outcomes back.
func (m *Manager) ReportResult(linkID string, rttMs float64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, found := m.links[linkID]; found {
		l.Probes++
		if ok {
			l.RTTMs = l.RTTMs*0.8 + rttMs*0.2
			l.LossPct *= 0.8
		} else {
			l.Failures++
			l.LossPct = l.LossPct*0.8 + 20
		}
		l.Healthy = l.LossPct < 50
	}
}

// SelectLink implements netengine.LinkSelector using the active policy.
func (m *Manager) SelectLink(_ context.Context, _ string) (netengine.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var healthy []*Link
	for _, l := range m.links {
		if l.Enabled && l.Healthy {
			healthy = append(healthy, l)
		}
	}
	if len(healthy) == 0 {
		return netengine.Link{}, fmt.Errorf("no healthy WAN link available (fail-closed)")
	}
	switch m.policy {
	case PolicyWeighted:
		sort.SliceStable(healthy, func(i, j int) bool { return healthy[i].Weight > healthy[j].Weight })
	case PolicyPriority:
		sort.SliceStable(healthy, func(i, j int) bool { return healthy[i].Priority > healthy[j].Priority })
	case PolicyFailover:
		if m.active != "" {
			for _, l := range healthy {
				if l.ID == m.active {
					return toNetLink(l), nil
				}
			}
		}
		sort.SliceStable(healthy, func(i, j int) bool { return healthy[i].Cost < healthy[j].Cost })
	default:
		sort.SliceStable(healthy, func(i, j int) bool { return healthy[i].RTTMs < healthy[j].RTTMs })
	}
	chosen := healthy[0]
	if m.active != chosen.ID {
		m.active = chosen.ID
		m.switches++
	}
	return toNetLink(chosen), nil
}

func toNetLink(l *Link) netengine.Link {
	return netengine.Link{
		ID: l.ID, Name: l.Name, Interface: l.Interface, Gateway: l.Gateway,
		Weight: l.Weight, Cost: l.Cost, Healthy: l.Healthy,
		RTTMs: l.RTTMs, LossPct: l.LossPct,
	}
}

// ForceFailover marks the active link down and immediately selects another.
func (m *Manager) ForceFailover(ctx context.Context) (netengine.Link, error) {
	m.mu.Lock()
	if id := m.active; id != "" {
		if l, ok := m.links[id]; ok {
			l.Healthy = false
			l.LossPct = 100
		}
	}
	m.active = ""
	m.mu.Unlock()
	return m.SelectLink(ctx, "manual-failover")
}

func (m *Manager) Status() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	links := make([]Link, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, *l)
	}
	sort.Slice(links, func(i, j int) bool { return links[i].ID < links[j].ID })
	return map[string]any{
		"policy": m.policy, "active": m.active, "running": m.running,
		"switches": m.switches, "interval_ms": m.interval.Milliseconds(), "links": links,
	}
}

func (m *Manager) Links() []Link {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Link, 0, len(m.links))
	for _, l := range m.links {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
