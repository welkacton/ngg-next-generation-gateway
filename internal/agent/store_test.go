package agent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	return s
}

func TestCreateBindsTokenAndNeverPersistsPlaintext(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	view, token, err := s.Create("HK hk03", "203.0.113.7", 2222, "home box", "outbound")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if token == "" || len(token) != 64 {
		t.Fatalf("token must be 32 random bytes hex, got %q", token)
	}
	if view.ID == "" || view.Name != "HK hk03" || view.Port != 2222 {
		t.Fatalf("unexpected view: %+v", view)
	}
	if view.Bound {
		t.Fatal("a fresh agent must not be bound")
	}
	if view.Status != StatusNever || view.Online {
		t.Fatalf("fresh agent must be never/offline, got %s/%v", view.Status, view.Online)
	}
	if view.Metrics != nil {
		t.Fatalf("metrics must be null before the first report, got %+v", view.Metrics)
	}
	if view.LastSeen != "" {
		t.Fatalf("last_seen must be empty before the first report, got %q", view.LastSeen)
	}

	b, err := os.ReadFile(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), token) {
		t.Fatal("plaintext token must never be persisted")
	}
	if !strings.Contains(string(b), hashToken(token)) {
		t.Fatal("token hash missing from the persisted file")
	}
}

func TestReportUpdatesMetricsAndLastSeen(t *testing.T) {
	s := newTestStore(t)
	view, token, err := s.Create("US us09", "198.51.100.9", 2222, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Role != RoleOutbound {
		t.Fatalf("role must default to outbound, got %q", view.Role)
	}

	m := AgentMetrics{CPUPct: 12.5, CPUCores: 4, MemTotal: 100, MemUsed: 40, RxBytes: 1000, TxBytes: 2000, RxRate: 10, TxRate: 20}
	if err := s.Report(view.ID, token, m); err != nil {
		t.Fatalf("Report: %v", err)
	}
	got, ok := s.Get(view.ID)
	if !ok {
		t.Fatal("agent vanished")
	}
	if !got.Bound || !got.Online || got.Status != StatusOnline {
		t.Fatalf("agent should be bound+online after report: %+v", got)
	}
	if got.LastSeen == "" {
		t.Fatal("last_seen not set")
	}
	if got.Metrics == nil || got.Metrics.CPUPct != 12.5 || got.Metrics.MemUsed != 40 {
		t.Fatalf("metrics not stored: %+v", got.Metrics)
	}
	if got.Metrics.SampledAt == "" || got.Metrics.Source != "agent" {
		t.Fatalf("sampled_at/source must be filled: %+v", got.Metrics)
	}

	samples := s.Samples(view.ID, 10)
	if len(samples) != 1 || samples[0].CPUPct != 12.5 || samples[0].RxBytes != 1000 {
		t.Fatalf("sample history not appended: %+v", samples)
	}

	if err := s.Report(view.ID, "deadbeef", m); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token must be unauthorized, got %v", err)
	}
	if err := s.Report("ag-unknown", token, m); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown agent must be unauthorized, got %v", err)
	}
	if err := s.Report(view.ID, "", m); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty token must be unauthorized, got %v", err)
	}
}

func TestReportPayloadStoresHostIdentity(t *testing.T) {
	s := newTestStore(t)
	view, token, err := s.Create("win", "198.51.100.10", 2222, "", "both")
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReportFull(ReportPayload{
		AgentID: view.ID, Hostname: "srv-1", OS: "windows", Arch: "amd64",
		AgentVersion: "2.7.0", IntervalSec: 15, Metrics: AgentMetrics{CPUPct: 1},
	}, token)
	if err != nil {
		t.Fatalf("ReportFull: %v", err)
	}
	got, _ := s.Get(view.ID)
	if got.Hostname != "srv-1" || got.OS != "windows" || got.Arch != "amd64" || got.AgentVersion != "2.7.0" {
		t.Fatalf("host identity not stored: %+v", got)
	}
	// 3x the reported interval (15s) = 45s window.
	s.mu.Lock()
	a := s.agents[view.ID]
	s.mu.Unlock()
	if status, _ := a.statusAt(a.LastSeen.Add(40 * time.Second)); status != StatusOnline {
		t.Fatalf("40s after report (interval 15s) must still be online, got %s", status)
	}
	if status, _ := a.statusAt(a.LastSeen.Add(46 * time.Second)); status != StatusOffline {
		t.Fatalf("46s after report (interval 15s) must be offline, got %s", status)
	}
}

func TestRotateTokenInvalidatesOldToken(t *testing.T) {
	s := newTestStore(t)
	view, oldToken, err := s.Create("hk", "198.51.100.11", 2222, "", "outbound")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Report(view.ID, oldToken, AgentMetrics{}); err != nil {
		t.Fatal(err)
	}
	newToken, err := s.RotateToken(view.ID)
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}
	if newToken == oldToken {
		t.Fatal("rotated token must differ")
	}
	if err := s.Report(view.ID, oldToken, AgentMetrics{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token must stop working, got %v", err)
	}
	if err := s.Report(view.ID, newToken, AgentMetrics{}); err != nil {
		t.Fatalf("new token must work: %v", err)
	}
	if _, err := s.RotateToken("ag-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id must be not-found, got %v", err)
	}
}

func TestDeleteClearsHistoryAndPersists(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	view, token, err := s.Create("del", "198.51.100.12", 2222, "", "inbound")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Report(view.ID, token, AgentMetrics{CPUPct: 3}); err != nil {
		t.Fatal(err)
	}
	if !s.HasHistory(view.ID) {
		t.Fatal("expected history before delete")
	}
	if err := s.Delete(view.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get(view.ID); ok {
		t.Fatal("agent still present after delete")
	}
	if s.HasHistory(view.ID) || len(s.Samples(view.ID, 10)) != 0 {
		t.Fatal("history must be cleared with the agent")
	}
	if err := s.Report(view.ID, token, AgentMetrics{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted agent must reject reports, got %v", err)
	}
	if err := s.Delete(view.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete must be not-found, got %v", err)
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(view.ID); ok {
		t.Fatal("deleted agent came back after reload")
	}
}

func TestPersistenceReloadKeepsState(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	view, token, err := s.Create("persist", "198.51.100.13", 2222, "note", "both")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Report(view.ID, token, AgentMetrics{CPUPct: 7.5, MemUsed: 2048}); err != nil {
		t.Fatal(err)
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reloaded.Get(view.ID)
	if !ok {
		t.Fatal("agent lost on reload")
	}
	if got.Metrics == nil || got.Metrics.CPUPct != 7.5 || got.Metrics.MemUsed != 2048 {
		t.Fatalf("metrics lost on reload: %+v", got.Metrics)
	}
	if got.LastSeen == "" || !got.Bound {
		t.Fatal("last_seen lost on reload")
	}
	if got.Note != "note" || got.Role != RoleBoth {
		t.Fatalf("fields lost on reload: %+v", got)
	}
	// History is memory-only by design: a restart must not show stale samples.
	if len(reloaded.Samples(view.ID, 10)) != 0 {
		t.Fatal("sample history must not survive a restart")
	}
	// The reloaded token must still authenticate.
	if err := reloaded.Report(view.ID, token, AgentMetrics{}); err != nil {
		t.Fatalf("token must still work after reload: %v", err)
	}
}

func TestHistoryRingIsCapped(t *testing.T) {
	s := newTestStore(t)
	view, token, err := s.Create("ring", "198.51.100.14", 2222, "", "outbound")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxHistory+10; i++ {
		if err := s.Report(view.ID, token, AgentMetrics{CPUPct: float64(i)}); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	all := s.Samples(view.ID, 1000)
	if len(all) != maxHistory {
		t.Fatalf("history ring must cap at %d, got %d", maxHistory, len(all))
	}
	if all[len(all)-1].CPUPct != float64(maxHistory+9) {
		t.Fatalf("newest sample must be last, got %+v", all[len(all)-1])
	}
}

func TestUpdateAndValidation(t *testing.T) {
	s := newTestStore(t)
	view, _, err := s.Create("edit", "198.51.100.15", 2222, "", "outbound")
	if err != nil {
		t.Fatal(err)
	}
	name, role := "edited", "both"
	updated, err := s.Update(view.ID, Patch{Name: &name, Role: &role})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "edited" || updated.Role != RoleBoth {
		t.Fatalf("patch not applied: %+v", updated)
	}
	badPort := 0
	if _, err := s.Update(view.ID, Patch{Port: &badPort}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid port must be rejected, got %v", err)
	}
	badRole := "sideways"
	if _, err := s.Update(view.ID, Patch{Role: &badRole}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid role must be rejected, got %v", err)
	}
	after, _ := s.Get(view.ID)
	if after.Port != 2222 {
		t.Fatalf("failed update must not change the record: %+v", after)
	}
	if _, err := s.Update("ag-missing", Patch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id must be not-found, got %v", err)
	}
	if _, _, err := s.Create("", "1.2.3.4", 1, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name must be rejected, got %v", err)
	}
	if _, _, err := s.Create("x", "http://1.2.3.4", 1, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("address with scheme must be rejected, got %v", err)
	}
}

func TestPersistedFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not enforced on Windows")
	}
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("perm", "198.51.100.16", 2222, "", "outbound"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("agents.json must be 0600, got %o", perm)
	}
}

func TestServerConfigDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	cfg := NewServerStore(dir)
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	got := cfg.Get()
	if !got.Enabled || got.Listen != "0.0.0.0" || got.Port != 9191 || got.PublicURL != "" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	if cfg.PublicBase() != "" {
		t.Fatalf("0.0.0.0 must not become a public base, got %q", cfg.PublicBase())
	}

	for _, bad := range []ServerConfig{
		{Enabled: true, Listen: "not-an-ip", Port: 9191},
		{Enabled: true, Listen: "0.0.0.0", Port: 0},
		{Enabled: true, Listen: "0.0.0.0", Port: 9191, PublicURL: "ftp://x"},
		{Enabled: true, Listen: "0.0.0.0", Port: 9191, PublicURL: "203.0.113.1"},
	} {
		if _, _, err := cfg.Save(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected validation error for %+v, got %v", bad, err)
		}
	}

	saved, warnings, err := cfg.Save(ServerConfig{Enabled: true, Listen: "127.0.0.1", Port: 9200, PublicURL: "https://ngg.example.com/"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.PublicURL != "https://ngg.example.com" {
		t.Fatalf("public_url must be normalised without a trailing slash: %q", saved.PublicURL)
	}
	if cfg.PublicBase() != "https://ngg.example.com" {
		t.Fatalf("PublicBase: %q", cfg.PublicBase())
	}
	if len(warnings) != 0 {
		t.Fatalf("loopback + https should not warn, got %v", warnings)
	}

	reloaded := NewServerStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Get() != saved {
		t.Fatalf("config lost on reload: %+v", reloaded.Get())
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "agent-server.json"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("agent-server.json must be 0600, got %o", perm)
		}
	}
}
