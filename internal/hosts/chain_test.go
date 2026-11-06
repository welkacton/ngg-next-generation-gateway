package hosts

import (
	"context"
	"strings"
	"testing"
)

func TestChainValidation(t *testing.T) {
	cases := []struct {
		name string
		hops []ChainHop
	}{
		{"bad kind", []ChainHop{{Kind: "wireguard", Address: "1.2.3.4", Port: 1080, Enabled: true}}},
		{"missing kind", []ChainHop{{Address: "1.2.3.4", Port: 1080, Enabled: true}}},
		{"port zero when enabled", []ChainHop{{Kind: ChainKindSOCKS5, Address: "1.2.3.4", Port: 0, Enabled: true}}},
		{"port too high", []ChainHop{{Kind: ChainKindHTTP, Address: "1.2.3.4", Port: 70000, Enabled: true}}},
		{"empty address when enabled", []ChainHop{{Kind: ChainKindSOCKS5, Port: 1080, Enabled: true}}},
		{"address with scheme", []ChainHop{{Kind: ChainKindSOCKS5, Address: "socks5://1.2.3.4", Port: 1080, Enabled: true}}},
		{"too many hops", func() []ChainHop {
			hops := make([]ChainHop, 0, MaxChainHops+1)
			for i := 0; i < MaxChainHops+1; i++ {
				hops = append(hops, ChainHop{Kind: ChainKindSOCKS5, Address: "1.2.3.4", Port: 1080 + i, Enabled: true})
			}
			return hops
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := sample("n1", RoleOutbound)
			h.Chain = tc.hops
			if _, err := newTestRegistry(t).Add(context.Background(), h); err == nil {
				t.Fatalf("expected chain validation error for %s", tc.name)
			}
		})
	}
}

func TestChainAcceptedAndKeptOnReload(t *testing.T) {
	r := newTestRegistry(t)
	h := sample("n2", RoleOutbound)
	h.Chain = []ChainHop{
		{Kind: "SOCKS5", Address: " 10.0.0.9 ", Port: 1080, Username: " u ", Password: "p@ss", Enabled: true},
		{Kind: "http", Address: "10.0.0.10", Port: 3128, Enabled: false}, // draft hop, incomplete is fine
	}
	saved, err := r.Add(context.Background(), h)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(saved.Chain) != 2 {
		t.Fatalf("chain not stored: %+v", saved.Chain)
	}
	if saved.Chain[0].Kind != ChainKindSOCKS5 || saved.Chain[0].Address != "10.0.0.9" || saved.Chain[0].Username != "u" {
		t.Fatalf("chain hop not normalized: %+v", saved.Chain[0])
	}

	again := NewRegistry(r.Path())
	if err := again.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := again.Get("n2")
	if !ok || len(got.Chain) != 2 || got.Chain[0].Password != "p@ss" {
		t.Fatalf("chain lost on reload: %+v", got.Chain)
	}
}

func TestChainEmptyStaysNull(t *testing.T) {
	h := sample("n3", RoleOutbound)
	normalize(&h)
	if h.Chain != nil {
		t.Fatalf("host without a chain must keep chain=null, got %#v", h.Chain)
	}
	// Disabled hops alone must not break validate.
	h.Chain = []ChainHop{{Kind: ChainKindHTTP, Enabled: false}}
	if err := validate(h); err != nil {
		t.Fatalf("disabled draft hop should validate: %v", err)
	}
}

func TestChainMaxHopsAccepted(t *testing.T) {
	h := sample("n4", RoleOutbound)
	for i := 0; i < MaxChainHops; i++ {
		h.Chain = append(h.Chain, ChainHop{Kind: ChainKindSOCKS5, Address: "10.0.0.1", Port: 1000 + i, Enabled: true})
	}
	normalize(&h)
	if err := validate(h); err != nil {
		t.Fatalf("%d hops must be accepted: %v", MaxChainHops, err)
	}
}

func TestChainErrorsMentionHopIndex(t *testing.T) {
	h := sample("n5", RoleOutbound)
	h.Chain = []ChainHop{
		{Kind: ChainKindSOCKS5, Address: "10.0.0.1", Port: 1080, Enabled: true},
		{Kind: ChainKindSOCKS5, Address: "10.0.0.2", Port: 0, Enabled: true},
	}
	_, err := newTestRegistry(t).Add(context.Background(), h)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "第 2 跳") {
		t.Fatalf("error should point at the failing hop: %v", err)
	}
}
