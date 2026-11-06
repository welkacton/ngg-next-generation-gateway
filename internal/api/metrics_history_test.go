package api

import (
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ngg-project/ngg/internal/netengine"
)

type fakeClock struct{ ms int64 }

func (c *fakeClock) now() time.Time      { return time.UnixMilli(atomic.LoadInt64(&c.ms)) }
func (c *fakeClock) add(d time.Duration) { atomic.AddInt64(&c.ms, d.Milliseconds()) }

func testSampler(clock *fakeClock, running bool, min, mout uint64) *metricsSampler {
	metrics := map[string]any{
		"bytes_in": min, "bytes_out": mout, "active": int64(3),
		"connections": int64(7), "direct": int64(4), "proxied": int64(2),
		"dropped": int64(1), "dns_queries": int64(9),
		"rule_matches": int64(11), "failovers": int64(0),
	}
	status := func() map[string]any {
		return map[string]any{"running": running, "metrics": metrics}
	}
	s := newMetricsSampler(status)
	s.now = clock.now
	return s
}

func TestSamplerDerivesRatesFromRealDeltas(t *testing.T) {
	clock := &fakeClock{ms: 1_700_000_000_000}
	s := testSampler(clock, true, 0, 0)
	s.takeSample()
	clock.add(2 * time.Second)
	// 1000 bytes in 2s = 500 B/s, 2000 bytes in 2s = 1000 B/s.
	s.status = func() map[string]any {
		return map[string]any{"running": true, "metrics": map[string]any{
			"bytes_in": uint64(1000), "bytes_out": uint64(2000),
		}}
	}
	s.takeSample()

	got := s.List(5)
	if len(got) != 2 {
		t.Fatalf("want 2 samples, got %d", len(got))
	}
	last := got[1]
	if last.RateIn != 500 || last.RateOut != 1000 {
		t.Fatalf("rates wrong: in=%v out=%v", last.RateIn, last.RateOut)
	}
	if last.BytesIn != 1000 || last.BytesOut != 2000 {
		t.Fatalf("counters wrong: %+v", last)
	}
	if got[0].RateIn != 0 || got[0].RateOut != 0 {
		t.Fatal("first sample cannot have a rate without a predecessor")
	}
}

func TestSamplerNeverFakesTrafficWhenEngineStopped(t *testing.T) {
	clock := &fakeClock{ms: 1_700_000_000_000}
	s := testSampler(clock, false, 0, 0)
	s.takeSample()
	clock.add(2 * time.Second)
	s.status = func() map[string]any {
		return map[string]any{"running": false, "metrics": map[string]any{
			"bytes_in": uint64(9999), "bytes_out": uint64(9999),
		}}
	}
	s.takeSample()
	last := s.List(5)[1]
	if last.Running {
		t.Fatal("sample must be flagged as not running")
	}
	if last.RateIn != 0 || last.RateOut != 0 {
		t.Fatalf("stopped engine must not produce a rate: %+v", last)
	}
}

func TestSamplerHandlesCounterReset(t *testing.T) {
	clock := &fakeClock{ms: 1_700_000_000_000}
	s := testSampler(clock, true, 5000, 5000)
	s.takeSample()
	clock.add(2 * time.Second)
	s.status = func() map[string]any {
		return map[string]any{"running": true, "metrics": map[string]any{
			"bytes_in": uint64(10), "bytes_out": uint64(10),
		}}
	}
	s.takeSample()
	last := s.List(5)[1]
	if last.RateIn != 0 || last.RateOut != 0 {
		t.Fatalf("counter reset must yield zero, not a negative/huge spike: %+v", last)
	}
}

func TestSamplerWindowAndLimit(t *testing.T) {
	clock := &fakeClock{ms: 1_700_000_000_000}
	s := testSampler(clock, true, 0, 0)
	s.limit = 3
	for i := 0; i < 5; i++ {
		s.takeSample()
		clock.add(2 * time.Second)
	}
	if n := len(s.List(30)); n != 3 {
		t.Fatalf("limit should keep 3 samples, got %d", n)
	}
	// Move the clock past the window: nothing stale may be returned.
	clock.add(31 * time.Minute)
	if n := len(s.List(30)); n != 0 {
		t.Fatalf("window filter returned %d stale samples", n)
	}
}

func TestMetricsHistoryHandlerHonestNote(t *testing.T) {
	clock := &fakeClock{ms: 1_700_000_000_000}
	s := testSampler(clock, false, 0, 0)
	s.takeSample()
	srv := &Server{sampler: s}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/network/metrics/history?minutes=5", nil)
	srv.handleNetworkMetricsHistory(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Running       bool            `json:"running"`
		Count         int             `json:"count"`
		WindowMinutes int             `json:"window_minutes"`
		Note          string          `json:"note"`
		Samples       []MetricsSample `json:"samples"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Running || body.Note == "" || body.Count != 1 || body.WindowMinutes != 5 {
		t.Fatalf("unexpected response: %+v", body)
	}
	if len(body.Samples) != 1 || body.Samples[0].RateIn != 0 {
		t.Fatalf("samples must be real, got %+v", body.Samples)
	}
}

// While the virtual adapter is up it is the source of truth: the chart must show
// what actually crossed the NIC, not just the sessions the dispatcher managed to
// proxy. A tunnel that receives but cannot send (the field bug this release
// fixes) is visible precisely because these counters are independent.
func TestSamplerPrefersVirtualAdapterCounters(t *testing.T) {
	clock := &fakeClock{ms: 1_700_000_000_000}
	var tunIn, tunOut uint64
	tunOpen := true

	metrics := map[string]any{"bytes_in": uint64(10), "bytes_out": uint64(5)}
	s := newMetricsSampler(
		func() map[string]any { return map[string]any{"running": true, "metrics": metrics} },
		func() (netengine.TunStats, bool) {
			return netengine.TunStats{BytesIn: tunIn, BytesOut: tunOut}, tunOpen
		},
	)
	s.now = clock.now

	s.takeSample()
	clock.add(2 * time.Second)
	tunIn, tunOut = 2_000_000, 1_000_000
	s.takeSample()

	last := s.List(5)[1]
	if last.Source != "tun" {
		t.Fatalf("source = %q, want tun", last.Source)
	}
	if last.BytesIn != 2_000_000 || last.BytesOut != 1_000_000 {
		t.Fatalf("effective counters must come from the adapter: %+v", last)
	}
	if last.RateIn != 1_000_000 || last.RateOut != 500_000 {
		t.Fatalf("adapter rates wrong: in=%v out=%v", last.RateIn, last.RateOut)
	}
	if last.EngineBytesIn != 10 || last.EngineBytesOut != 5 {
		t.Fatalf("engine counters must stay visible: %+v", last)
	}

	// Closing the adapter falls back to the engine counters without inventing a
	// spike from the reset.
	clock.add(2 * time.Second)
	tunOpen = false
	s.takeSample()
	last = s.List(5)[2]
	if last.Source != "engine" {
		t.Fatalf("source after detach = %q, want engine", last.Source)
	}
	if last.RateIn != 0 || last.RateOut != 0 {
		t.Fatalf("detaching must not produce a rate spike: in=%v out=%v", last.RateIn, last.RateOut)
	}
	if last.TunBytesIn != 0 || last.TunBytesOut != 0 {
		t.Fatalf("a closed adapter must report zero, got %+v", last)
	}
}

// Recreating the adapter restarts its counters; the sampler must not turn the
// negative delta into a huge fake peak.
func TestSamplerHandlesAdapterCounterReset(t *testing.T) {
	clock := &fakeClock{ms: 1_700_000_000_000}
	var tunIn uint64
	s := newMetricsSampler(
		func() map[string]any { return map[string]any{"running": true} },
		func() (netengine.TunStats, bool) { return netengine.TunStats{BytesIn: tunIn}, true },
	)
	s.now = clock.now

	tunIn = 5_000_000
	s.takeSample()
	clock.add(2 * time.Second)
	tunIn = 0 // adapter recreated
	s.takeSample()

	if got := s.List(5)[1]; got.RateIn != 0 {
		t.Fatalf("counter reset produced a fake rate: %v", got.RateIn)
	}
}
