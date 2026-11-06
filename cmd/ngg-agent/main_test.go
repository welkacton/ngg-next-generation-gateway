package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/agent"
)

func TestPostReportSendsTokenInHeaderAndRealPayload(t *testing.T) {
	var (
		gotPath  string
		gotToken string
		gotBody  agent.ReportPayload
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-NGG-Agent-Token")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"ok":true,"interval_sec":10,"config_version":1}`))
	}))
	defer srv.Close()

	cfg := fileConfig{Server: srv.URL, AgentID: "ag-1", Token: "secret-token", IntervalSec: 10}
	err := postReport(context.Background(), srv.Client(), cfg, agent.AgentMetrics{CPUPct: 3.5, MemUsed: 7}, "host-1")
	if err != nil {
		t.Fatalf("postReport: %v", err)
	}
	if gotPath != "/v1/agent/report" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if gotToken != "secret-token" {
		t.Fatalf("token must travel in the header, got %q", gotToken)
	}
	if gotBody.AgentID != "ag-1" || gotBody.Hostname != "host-1" || gotBody.IntervalSec != 10 {
		t.Fatalf("unexpected payload: %+v", gotBody)
	}
	if gotBody.Metrics.CPUPct != 3.5 || gotBody.Metrics.MemUsed != 7 {
		t.Fatalf("metrics not forwarded: %+v", gotBody.Metrics)
	}
	if gotBody.OS != "" && gotBody.OS != "windows" && gotBody.OS != "linux" && gotBody.OS != "darwin" {
		t.Fatalf("unexpected os %q", gotBody.OS)
	}
}

func TestPostReportRejects401WithoutLeakingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := fileConfig{Server: srv.URL, AgentID: "ag-1", Token: "secret-token"}
	err := postReport(context.Background(), srv.Client(), cfg, agent.AgentMetrics{}, "h")
	if err == nil {
		t.Fatal("401 must surface as an error")
	}
	if strings.Contains(err.Error(), cfg.Token) {
		t.Fatal("error text must never contain the token")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should name the status: %v", err)
	}
}

func TestRedactRemovesToken(t *testing.T) {
	out := redact("POST http://x?token=abc123 failed", "abc123")
	if strings.Contains(out, "abc123") {
		t.Fatalf("token leaked: %s", out)
	}
	if redact("nothing", "") != "nothing" {
		t.Fatal("empty token must be a no-op")
	}
}

func TestConfigValidateRejectsIncompleteSettings(t *testing.T) {
	base := fileConfig{Server: "http://1.2.3.4:9191", AgentID: "ag-1", Token: "t", IntervalSec: 10}
	if err := base.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []fileConfig{
		{AgentID: "ag-1", Token: "t"},
		{Server: "1.2.3.4:9191", AgentID: "ag-1", Token: "t"},
		{Server: "http://1.2.3.4:9191", Token: "t"},
		{Server: "http://1.2.3.4:9191", AgentID: "ag-1"},
		{Server: "http://1.2.3.4:9191", AgentID: "ag-1", Token: "t", IntervalSec: 0},
	}
	for i, c := range cases {
		if err := c.validate(); err == nil {
			t.Fatalf("case %d must be rejected", i)
		}
	}
}

func TestSamplerCollectsRealHostMetrics(t *testing.T) {
	smp := newSampler()
	m, hostname := smp.collect()
	if m.CPUCores <= 0 {
		t.Fatalf("cpu cores not collected: %+v", m)
	}
	if m.MemTotal == 0 || m.MemUsed > m.MemTotal {
		t.Fatalf("memory not collected: %+v", m)
	}
	if len(m.Disks) == 0 {
		t.Fatalf("no real disks collected (total must be > 0): %+v", m.Disks)
	}
	for _, d := range m.Disks {
		if d.Total == 0 || d.Mount == "" {
			t.Fatalf("disk entry is not real: %+v", d)
		}
	}
	if len(m.Interfaces) == 0 {
		t.Fatal("no network interfaces collected")
	}
	if m.SampledAt == "" || m.Source != "agent" {
		t.Fatalf("sampled_at/source missing: %+v", m)
	}
	if m.UptimeSec == 0 {
		t.Fatal("uptime not collected")
	}
	if hostname == "" {
		t.Fatal("hostname not collected")
	}
	// The first sample of a process has nothing to diff against, so rates must
	// be zero rather than invented.
	if m.RxRate != 0 || m.TxRate != 0 {
		t.Fatalf("first sample rates must be 0, got rx=%v tx=%v", m.RxRate, m.TxRate)
	}

	second, _ := smp.collect()
	if second.MemTotal == 0 || second.CPUCores != m.CPUCores {
		t.Fatalf("second sample inconsistent: %+v", second)
	}
}
