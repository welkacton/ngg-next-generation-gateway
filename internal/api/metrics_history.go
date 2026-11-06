package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/netengine"
)

// Real traffic history for the Network Overview console.
//
// The sampler only reads metrics already produced by the Unified Network
// Engine (see netengine.Metrics.Snapshot) or by the virtual adapter itself
// (netengine.Engine.TunTraffic). It never invents a datapoint: when the engine
// is stopped there is no traffic, so rates are zero and the sample is flagged
// running=false. Rates are derived from consecutive REAL counter deltas divided
// by the REAL elapsed time; a counter reset (engine restart, adapter recreated)
// yields zero instead of a huge fake spike.

// MetricsSample is one genuine observation of the network counters.
//
// BytesIn/BytesOut and RateIn/RateOut carry the EFFECTIVE traffic: the virtual
// adapter counters while the TUN is up (all machine traffic), otherwise the
// session counters of the unified engine. Source says which one it is, and the
// raw numbers of both sides are exposed so the UI never has to guess.
type MetricsSample struct {
	TS          int64   `json:"ts"` // Unix milliseconds
	Running     bool    `json:"running"`
	Source      string  `json:"source"` // "tun" | "engine"
	BytesIn     uint64  `json:"bytes_in"`
	BytesOut    uint64  `json:"bytes_out"`
	RateIn      float64 `json:"rate_in"`  // bytes / second, derived from real deltas
	RateOut     float64 `json:"rate_out"` // bytes / second
	Active      int64   `json:"active"`
	Connections int64   `json:"connections"`
	Direct      int64   `json:"direct"`
	Proxied     int64   `json:"proxied"`
	Dropped     int64   `json:"dropped"`
	DNSQueries  int64   `json:"dns_queries"`
	RuleMatches int64   `json:"rule_matches"`
	Failovers   int64   `json:"failovers"`

	TunOpen        bool    `json:"tun_open"`
	TunBytesIn     uint64  `json:"tun_bytes_in"`
	TunBytesOut    uint64  `json:"tun_bytes_out"`
	TunRateIn      float64 `json:"tun_rate_in"`
	TunRateOut     float64 `json:"tun_rate_out"`
	EngineBytesIn  uint64  `json:"engine_bytes_in"`
	EngineBytesOut uint64  `json:"engine_bytes_out"`
}

type metricsSampler struct {
	mu       sync.RWMutex
	samples  []MetricsSample
	last     *MetricsSample
	limit    int
	interval time.Duration
	status   func() map[string]any
	tun      func() (netengine.TunStats, bool)
	now      func() time.Time
	stop     chan struct{}
	stopOnce sync.Once
}

func newMetricsSampler(status func() map[string]any, tun ...func() (netengine.TunStats, bool)) *metricsSampler {
	m := &metricsSampler{
		samples:  []MetricsSample{},
		limit:    900, // 30 minutes at 2s resolution
		interval: 2 * time.Second,
		status:   status,
		now:      time.Now,
		stop:     make(chan struct{}),
	}
	if len(tun) > 0 {
		m.tun = tun[0]
	}
	return m
}

func (m *metricsSampler) Start() {
	m.takeSample()
	go func() {
		t := time.NewTicker(m.interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				m.takeSample()
			case <-m.stop:
				return
			}
		}
	}()
}

func (m *metricsSampler) Stop() {
	m.stopOnce.Do(func() { close(m.stop) })
}

func asUint(v any) uint64 {
	switch x := v.(type) {
	case uint64:
		return x
	case uint32:
		return uint64(x)
	case int64:
		if x > 0 {
			return uint64(x)
		}
	case int:
		if x > 0 {
			return uint64(x)
		}
	case float64:
		if x > 0 {
			return uint64(x)
		}
	}
	return 0
}

func asInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func (m *metricsSampler) takeSample() {
	if m.status == nil {
		return
	}
	st := m.status()
	metrics, _ := st["metrics"].(map[string]any)
	running, _ := st["running"].(bool)

	var (
		tunStats netengine.TunStats
		tunOpen  bool
	)
	if m.tun != nil {
		tunStats, tunOpen = m.tun()
	}
	// A closed adapter reports zeroes; never attribute them to live traffic.
	if !tunOpen {
		tunStats = netengine.TunStats{}
	}

	s := MetricsSample{
		TS:             m.now().UnixMilli(),
		Running:        running,
		EngineBytesIn:  asUint(metrics["bytes_in"]),
		EngineBytesOut: asUint(metrics["bytes_out"]),
		TunOpen:        tunOpen,
		TunBytesIn:     tunStats.BytesIn,
		TunBytesOut:    tunStats.BytesOut,
		Active:         asInt(metrics["active"]),
		Connections:    asInt(metrics["connections"]),
		Direct:         asInt(metrics["direct"]),
		Proxied:        asInt(metrics["proxied"]),
		Dropped:        asInt(metrics["dropped"]),
		DNSQueries:     asInt(metrics["dns_queries"]),
		RuleMatches:    asInt(metrics["rule_matches"]),
		Failovers:      asInt(metrics["failovers"]),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Only a live engine produces meaningful byte deltas. If the engine was
	// restarted (or the adapter recreated) the counters reset; a negative delta
	// must not become a spike.
	if prev := m.last; prev != nil && running && prev.Running {
		if dt := float64(s.TS-prev.TS) / 1000; dt > 0 {
			if s.EngineBytesIn >= prev.EngineBytesIn {
				s.RateIn = float64(s.EngineBytesIn-prev.EngineBytesIn) / dt
			}
			if s.EngineBytesOut >= prev.EngineBytesOut {
				s.RateOut = float64(s.EngineBytesOut-prev.EngineBytesOut) / dt
			}
		}
	}
	// Adapter counters are independent: they may advance while the dispatcher
	// sees nothing at all (DNS, dropped UDP, half-open TCP), which is the whole
	// point of showing them.
	if prev := m.last; prev != nil && tunOpen && prev.TunOpen {
		if dt := float64(s.TS-prev.TS) / 1000; dt > 0 {
			if s.TunBytesIn >= prev.TunBytesIn {
				s.TunRateIn = float64(s.TunBytesIn-prev.TunBytesIn) / dt
			}
			if s.TunBytesOut >= prev.TunBytesOut {
				s.TunRateOut = float64(s.TunBytesOut-prev.TunBytesOut) / dt
			}
		}
	}

	// The effective series is the virtual adapter while it is up: that IS the
	// real machine traffic the operator asked to see.
	if tunOpen {
		s.Source = "tun"
		s.BytesIn, s.BytesOut = s.TunBytesIn, s.TunBytesOut
		s.RateIn, s.RateOut = s.TunRateIn, s.TunRateOut
	} else {
		s.Source = "engine"
		s.BytesIn, s.BytesOut = s.EngineBytesIn, s.EngineBytesOut
	}

	cp := s
	m.last = &cp
	m.samples = append(m.samples, s)
	if len(m.samples) > m.limit {
		m.samples = append([]MetricsSample(nil), m.samples[len(m.samples)-m.limit:]...)
	}
}

// List returns a copy of the samples inside the requested time window.
func (m *metricsSampler) List(minutes int) []MetricsSample {
	if minutes < 1 {
		minutes = 1
	}
	if minutes > 30 {
		minutes = 30
	}
	cutoff := m.now().Add(-time.Duration(minutes) * time.Minute).UnixMilli()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]MetricsSample, 0, len(m.samples))
	for _, s := range m.samples {
		if s.TS >= cutoff {
			out = append(out, s)
		}
	}
	return out
}

func (s *Server) handleNetworkMetricsHistory(w http.ResponseWriter, r *http.Request) {
	if s.sampler == nil {
		writeErr(w, http.StatusServiceUnavailable, "metrics sampler not initialized")
		return
	}
	minutes := 5
	if raw := r.URL.Query().Get("minutes"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			minutes = n
		}
	}
	if minutes < 1 {
		minutes = 1
	}
	if minutes > 30 {
		minutes = 30
	}
	list := s.sampler.List(minutes)
	running := false
	source := "engine"
	tunOpen := false
	if len(list) > 0 {
		last := list[len(list)-1]
		running = last.Running
		source = last.Source
		tunOpen = last.TunOpen
	}
	note := ""
	if !running {
		note = "网络引擎未运行：没有真实流量采样，界面显示空状态而不绘制模拟曲线"
	} else if len(list) < 2 {
		note = "采样中：至少需要两个采样点才能计算真实速率"
	} else if source == "tun" {
		note = "数据源：NGG 虚拟网卡（网卡级真实收发字节）"
	} else {
		note = "数据源：统一网络引擎会话计数（虚拟网卡未创建）"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running":        running,
		"source":         source,
		"tun_open":       tunOpen,
		"interval_sec":   s.sampler.interval.Seconds(),
		"window_minutes": minutes,
		"count":          len(list),
		"samples":        list,
		"note":           note,
	})
}
