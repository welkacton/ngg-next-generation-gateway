package hosts

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func sample(id string, role Role) Host {
	return Host{
		ID: id, Name: "US " + id, Internal: id, Country: "us",
		Address: "127.0.0.1", NodePort: 2222, SvcPort: 443,
		Role: role, Enabled: true, Protocol: "vless-reality",
	}
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry(filepath.Join(t.TempDir(), "hosts-config.json"))
	if err := r.Load(); err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	return r
}

func TestValidationRejectsBadHosts(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Host)
	}{
		{"empty id", func(h *Host) { h.ID = "" }},
		{"bad id", func(h *Host) { h.ID = "bad id!" }},
		{"empty name", func(h *Host) { h.Name = "" }},
		{"bad role", func(h *Host) { h.Role = "sideways" }},
		{"empty address", func(h *Host) { h.Address = "" }},
		{"address with scheme", func(h *Host) { h.Address = "https://1.2.3.4" }},
		{"bad node port", func(h *Host) { h.NodePort = 0 }},
		{"port too high", func(h *Host) { h.NodePort = 70000 }},
		{"bad ratio", func(h *Host) { h.UserRatio = 0.01 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := sample("n1", RoleOutbound)
			tc.mut(&h)
			if _, err := (&Registry{hosts: map[string]Host{}, probes: map[string]ProbeResult{}}).Add(context.Background(), h); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestRegistryCRUDAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts-config.json")
	r := NewRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	h := sample("us01", RoleInbound)
	if _, err := r.Add(context.Background(), h); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := r.Add(context.Background(), h); err == nil {
		t.Fatal("duplicate id must be rejected")
	}

	got, ok := r.Get("us01")
	if !ok {
		t.Fatal("host missing")
	}
	if got.Country != "US" {
		t.Fatalf("country not normalised: %q", got.Country)
	}
	if got.UserRatio != 1 || got.NodeRatio != 1 {
		t.Fatalf("ratios not defaulted: %+v", got)
	}

	updated, err := r.SetRole("us01", RoleOutbound)
	if err != nil || updated.Role != RoleOutbound {
		t.Fatalf("SetRole: %v %+v", err, updated)
	}
	if updated.CreatedAt.IsZero() {
		t.Fatal("CreatedAt lost on update")
	}

	// Reload from disk: the registry must survive a restart.
	r2 := NewRegistry(path)
	if err := r2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got2, ok := r2.Get("us01")
	if !ok || got2.Role != RoleOutbound {
		t.Fatalf("persisted state wrong: %+v ok=%v", got2, ok)
	}
	// Probe cache is intentionally NOT persisted.
	if cached := r2.Views("")[0].Probe; cached != nil {
		t.Fatal("probe result must not be persisted as online state")
	}

	if err := r2.Remove("us01"); err != nil {
		t.Fatal(err)
	}
	if err := r2.Remove("us01"); err == nil {
		t.Fatal("removing missing host must fail")
	}
}

func TestListFilterIncludesBothRole(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	for _, x := range []struct {
		id   string
		role Role
	}{
		{"in", RoleInbound}, {"out", RoleOutbound}, {"both", RoleBoth},
	} {
		if _, err := r.Add(ctx, sample(x.id, x.role)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.List("inbound")); n != 2 {
		t.Fatalf("inbound filter should include both-role host, got %d", n)
	}
	if n := len(r.List("outbound")); n != 2 {
		t.Fatalf("outbound filter should include both-role host, got %d", n)
	}
	if n := len(r.List("")); n != 3 {
		t.Fatalf("all hosts = %d", n)
	}
	s := r.Stats()
	if s.Inbound != 1 || s.Outbound != 1 || s.Both != 1 || s.Total != 3 {
		t.Fatalf("stats wrong: %+v", s)
	}
}

func TestProbeUsesRealTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	h := sample("live", RoleOutbound)
	h.NodePort = port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := ProbeHost(ctx, h)
	if !res.Online {
		t.Fatalf("real listener should be reachable: %+v", res)
	}
	if res.Target != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		t.Fatalf("unexpected target %q", res.Target)
	}

	// A closed port must be reported offline, never faked as online.
	closed := sample("dead", RoleOutbound)
	closed.NodePort = 1
	bad := ProbeHost(ctx, closed)
	if bad.Online || bad.Error == "" {
		t.Fatalf("closed port must be offline with error: %+v", bad)
	}
}

func TestProbeAllAndImport(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r := newTestRegistry(t)
	live := sample("live", RoleInbound)
	live.NodePort = port
	if _, err := r.Add(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	results := r.ProbeAll(context.Background())
	if !results["live"].Online {
		t.Fatalf("ProbeAll missed live host: %+v", results)
	}
	if s := r.Stats(); s.Online != 1 || s.Checked != 1 {
		t.Fatalf("stats after probe: %+v", s)
	}

	// All-or-nothing import: one invalid record must leave the registry intact.
	good := sample("fresh", RoleOutbound)
	good.NodePort = port
	bad := good
	bad.ID = "bad id"
	if _, err := r.Replace([]Host{good, bad}); err == nil {
		t.Fatal("invalid import must fail")
	}
	if _, ok := r.Get("live"); !ok {
		t.Fatal("failed import must not destroy existing hosts")
	}
	n, err := r.Replace([]Host{good})
	if err != nil || n != 1 {
		t.Fatalf("Replace: n=%d err=%v", n, err)
	}
	if _, ok := r.Get("live"); ok {
		t.Fatal("Replace must remove hosts not in the import")
	}
}
