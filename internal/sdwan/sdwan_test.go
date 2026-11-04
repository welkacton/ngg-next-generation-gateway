package sdwan

import (
	"context"
	"net"
	"testing"
	"time"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln
}

func TestRealLinkProbingAndAutomaticFailover(t *testing.T) {
	ln1, ln2 := listen(t), listen(t)
	defer ln2.Close()

	m := NewManager(PolicyLowestLatency, 150*time.Millisecond)
	if err := m.AddLink(Link{ID: "wan1", Name: "Primary", ProbeTarget: ln1.Addr().String(), Enabled: true, Cost: 1}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddLink(Link{ID: "wan2", Name: "Backup", ProbeTarget: ln2.Addr().String(), Enabled: true, Cost: 2}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	defer func() { cancel(); m.Stop() }()
	time.Sleep(250 * time.Millisecond)

	first, err := m.SelectLink(ctx, "test")
	if err != nil {
		t.Fatalf("no healthy link: %v", err)
	}
	active := m.Status()["active"].(string)
	if active == "" {
		t.Fatal("no active link recorded")
	}
	// Kill the ACTIVE link's probe target. Real failed TCP dials must degrade
	// its health and the next selection must switch to the surviving link.
	if active == "wan1" {
		_ = ln1.Close()
	} else {
		_ = ln2.Close()
	}
	time.Sleep(500 * time.Millisecond)
	next, err := m.SelectLink(ctx, "test")
	if err != nil {
		t.Fatalf("automatic failover failed: %v", err)
	}
	if next.ID == active {
		t.Fatalf("selection did not fail over from %s: %+v", active, next)
	}
	if !next.Healthy {
		t.Fatalf("failover selected an unhealthy link: %+v", next)
	}
	if m.Status()["switches"].(int64) == 0 {
		t.Fatal("no failover switch was recorded")
	}
	for _, l := range m.Links() {
		if l.Probes == 0 {
			t.Fatalf("link %s was never really probed", l.ID)
		}
	}
	_ = first
}

func TestForceFailoverWithTwoHealthyLinks(t *testing.T) {
	ln1, ln2 := listen(t), listen(t)
	defer ln1.Close()
	defer ln2.Close()
	m := NewManager(PolicyFailover, time.Hour) // no background probes needed
	if err := m.AddLink(Link{ID: "a", ProbeTarget: ln1.Addr().String(), Enabled: true, Cost: 1, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddLink(Link{ID: "b", ProbeTarget: ln2.Addr().String(), Enabled: true, Cost: 2, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := m.SelectLink(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.ForceFailover(ctx)
	if err != nil {
		t.Fatalf("manual failover: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("manual failover kept the same link: %s", first.ID)
	}
}
