package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/nodeapi"
	"github.com/ngg-project/ngg/internal/nodedeploy"
)

// Node metrics: the panel PULLS them from every deployed node over mutual TLS.
//
// This replaces the SSH collector that used to read /proc on the machine. The
// shape of the data is the same kind of thing (cpu / mem / disk / traffic), but
// the source is the node's own API — the same numbers it reports to Remnawave's
// panel — and a node that is down simply produces an error instead of a
// fabricated zero.
//
// Polling is centralised so the console never waits on a dead node: the timer
// fills the cache, HTTP handlers only read it.

const (
	// nodePollInterval is how often every deployed node is polled.
	nodePollInterval = 30 * time.Second
	// nodePollTimeout bounds one poll. Health + stats, no config push.
	nodePollTimeout = 8 * time.Second
	// nodePollWorkers caps concurrent dials so a fleet of dead nodes cannot
	// spawn an unbounded number of goroutines or sockets.
	nodePollWorkers = 6
)

// nodeMetric is one node's last known state, flattened for the card grid.
type nodeMetric struct {
	HostID      string `json:"host_id"`
	Online      bool   `json:"online"`
	Error       string `json:"error,omitempty"`
	CollectedAt string `json:"collected_at"`
	LatencyMS   int64  `json:"latency_ms"`

	Hostname  string  `json:"hostname"`
	OS        string  `json:"os"`
	Kernel    string  `json:"kernel"`
	Arch      string  `json:"arch"`
	CPUCores  int     `json:"cpu_cores"`
	CPUModel  string  `json:"cpu_model"`
	CPUPct    float64 `json:"cpu_usage_pct"`
	MemTotal  uint64  `json:"mem_total"`
	MemUsed   uint64  `json:"mem_used"`
	DiskTotal uint64  `json:"disk_total"`
	DiskUsed  uint64  `json:"disk_used"`
	Load1     float64 `json:"load1"`
	UptimeSec uint64  `json:"uptime_sec"`

	NetRxBytes uint64  `json:"net_rx_bytes"`
	NetTxBytes uint64  `json:"net_tx_bytes"`
	NetRxRate  float64 `json:"net_rx_rate"`
	NetTxRate  float64 `json:"net_tx_rate"`

	NodeVersion string `json:"node_version"`
	XrayVersion string `json:"xray_version"`
	CoreActive  bool   `json:"core_active"`

	// Health is the node's own answer, kept verbatim.
	Health *nodeapi.Health `json:"health,omitempty"`
}

// nodeMetricsCache holds the last result per host.
type nodeMetricsCache struct {
	mu   sync.RWMutex
	m    map[string]nodeMetric
	stop chan struct{}
	once sync.Once
}

func newNodeMetricsCache() *nodeMetricsCache {
	return &nodeMetricsCache{m: map[string]nodeMetric{}, stop: make(chan struct{})}
}

func (c *nodeMetricsCache) set(m nodeMetric) {
	c.mu.Lock()
	c.m[m.HostID] = m
	c.mu.Unlock()
}

func (c *nodeMetricsCache) get(hostID string) (nodeMetric, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.m[hostID]
	return m, ok
}

func (c *nodeMetricsCache) drop(hostID string) {
	c.mu.Lock()
	delete(c.m, hostID)
	c.mu.Unlock()
}

func (c *nodeMetricsCache) all() []nodeMetric {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]nodeMetric, 0, len(c.m))
	for _, m := range c.m {
		out = append(out, m)
	}
	return out
}

// record stores the result of an explicit connect so the card updates without
// waiting for the next poll tick.
func (c *nodeMetricsCache) record(hostID string, res nodedeploy.ConnectResult) {
	c.set(metricFromConnect(hostID, res))
}

// start runs the poller until stopPolling.
func (c *nodeMetricsCache) start(interval time.Duration, poll func(context.Context) []nodeMetric) {
	if interval <= 0 {
		interval = nodePollInterval
	}
	go func() {
		// One immediate round so a restart of the panel shows data at once.
		c.collect(interval, poll)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-t.C:
				c.collect(interval, poll)
			}
		}
	}()
}

// collect runs one polling round under a deadline derived from the interval, so
// a stuck node cannot pile rounds on top of each other.
func (c *nodeMetricsCache) collect(interval time.Duration, poll func(context.Context) []nodeMetric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*interval)
	defer cancel()
	for _, m := range poll(ctx) {
		c.set(m)
	}
}

func (c *nodeMetricsCache) stopPolling() { c.once.Do(func() { close(c.stop) }) }

// metricFromConnect flattens a connect result into the card's shape.
func metricFromConnect(hostID string, res nodedeploy.ConnectResult) nodeMetric {
	m := nodeMetric{
		HostID:      hostID,
		Online:      res.OK,
		CollectedAt: res.At.UTC().Format(time.RFC3339),
		LatencyMS:   res.DurationMS,
		Health:      res.Health,
	}
	if !res.OK {
		m.Error = res.Error
	}
	if res.Health != nil {
		m.NodeVersion = res.Health.NodeVersion
		m.XrayVersion = res.Health.XrayVersion
		m.CoreActive = res.Health.CoreActive
	}
	if sys := res.System; sys != nil {
		m.Hostname = sys.Info.Hostname
		m.OS = sys.Info.Platform
		m.Kernel = sys.Info.Release
		m.Arch = sys.Info.Arch
		m.CPUCores = sys.Info.CPUs
		m.CPUModel = sys.Info.CPUModel
		m.MemTotal = sys.Info.MemoryTotal
		if m.MemTotal == 0 {
			m.MemTotal = sys.Stats.MemoryTotal
		}
		m.MemUsed = sys.Stats.MemoryUsed
		m.DiskTotal = sys.Stats.DiskTotal
		m.DiskUsed = sys.Stats.DiskUsed
		m.CPUPct = sys.Stats.CPUPercent
		m.UptimeSec = sys.Stats.Uptime
		if len(sys.Stats.LoadAvg) > 0 {
			m.Load1 = sys.Stats.LoadAvg[0]
		}
		if it := sys.Stats.Interface; it != nil {
			m.NetRxBytes = it.RxTotal
			m.NetTxBytes = it.TxTotal
			m.NetRxRate = it.RxBytesPerSec
			m.NetTxRate = it.TxBytesPerSec
		}
	}
	return m
}

// pollNode reads one node's state. It never pushes config: a 30 s timer must
// not bounce the kernel (and drop every live connection) on a healthy node.
func (s *Server) pollNode(ctx context.Context, rec nodedeploy.Record) nodeMetric {
	started := time.Now()
	client, err := s.nodeClient(rec.Spec)
	if err != nil {
		return nodeMetric{
			HostID:      rec.HostID,
			Online:      false,
			Error:       err.Error(),
			CollectedAt: started.UTC().Format(time.RFC3339),
		}
	}
	ctx, cancel := context.WithTimeout(ctx, nodePollTimeout)
	defer cancel()

	health, _, err := client.Health(ctx)
	if err != nil {
		return nodeMetric{
			HostID:      rec.HostID,
			Online:      false,
			Error:       friendlyNodeError(err),
			CollectedAt: started.UTC().Format(time.RFC3339),
			LatencyMS:   time.Since(started).Milliseconds(),
		}
	}
	sys, _, serr := client.SystemStats(ctx)
	res := nodedeploy.ConnectResult{At: started.UTC(), Health: &health, DurationMS: time.Since(started).Milliseconds()}
	if serr != nil {
		res.Error = friendlyNodeError(serr)
	} else {
		res.OK = true
		res.System = &sys
	}
	return metricFromConnect(rec.HostID, res)
}

// pollAllNodes dials every deployed node, bounded by nodePollWorkers.
func (s *Server) pollAllNodes(ctx context.Context) []nodeMetric {
	if s.deploy == nil {
		return nil
	}
	// Only dial plans that describe a live, mTLS-capable node. A v2.12
	// probe/SSH record has no NODE_PORT, and a plan left behind by a deleted
	// host has nobody to report to; polling either would just fill the console
	// with misleading "offline" rows every 30 seconds.
	var reg = (*hosts.Registry)(nil)
	if s.engine != nil {
		reg = s.engine.Hosts()
	}
	recs := make([]nodedeploy.Record, 0, len(s.deploy.List()))
	for _, rec := range s.deploy.List() {
		if rec.Spec.NodePort < 1 || strings.TrimSpace(rec.Spec.Address) == "" {
			continue
		}
		if reg != nil {
			if _, ok := reg.Get(rec.HostID); !ok {
				continue
			}
		}
		recs = append(recs, rec)
	}
	if len(recs) == 0 {
		return nil
	}
	out := make([]nodeMetric, len(recs))
	sem := make(chan struct{}, nodePollWorkers)
	var wg sync.WaitGroup
	for i, rec := range recs {
		wg.Add(1)
		go func(i int, rec nodedeploy.Record) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = s.pollNode(ctx, rec)
		}(i, rec)
	}
	wg.Wait()
	return out
}

// handleHostNodeMetrics serves GET /api/v1/hosts/node-metrics: the whole cache in
// one call, so the card grid refreshes with a single request.
func (s *Server) handleHostNodeMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	nodes := []nodeMetric{}
	if s.nodeMetrics != nil {
		if cached := s.nodeMetrics.all(); len(cached) > 0 {
			nodes = cached
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":        nodes,
		"interval_sec": int(nodePollInterval.Seconds()),
	})
}
