package pipeline

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ngg-project/ngg/internal/hosts"
)

func chainInputs() Inputs {
	in := testInputs()
	for i := range in.Hosts {
		if in.Hosts[i].ID != "us01" {
			continue
		}
		in.Hosts[i].Chain = []hosts.ChainHop{
			{Kind: hosts.ChainKindSOCKS5, Address: "198.51.100.20", Port: 1080, Username: "u1", Password: "p1", Enabled: true},
			{Kind: hosts.ChainKindHTTP, Address: "198.51.100.21", Port: 3128, Enabled: true},
			{Kind: hosts.ChainKindSOCKS5, Address: "198.51.100.22", Port: 1080, Enabled: false},
		}
	}
	return in
}

func proxyByName(t *testing.T, doc map[string]any) map[string]map[string]any {
	t.Helper()
	raw, _ := doc["proxies"].([]any)
	out := map[string]map[string]any{}
	for _, item := range raw {
		p, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("proxy entry is not a map: %v", item)
		}
		name, _ := p["name"].(string)
		out[name] = p
	}
	return out
}

func TestRenderMihomoChainsHopsWithDialerProxy(t *testing.T) {
	in := chainInputs()
	cfg := Default()
	cfg.Outbound.HostIDs = []string{"us01"}
	cfg.Inbound.HostID = "hk01"

	out, warnings, err := Render(cfg, in)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered config is not valid YAML: %v\n%s", err, out)
	}
	proxies := proxyByName(t, doc)

	hop1, ok := proxies["chain-us01-1"]
	if !ok {
		t.Fatalf("first chain hop missing:\n%s", out)
	}
	if hop1["type"] != "socks5" || hop1["server"] != "198.51.100.20" || hop1["port"] != 1080 {
		t.Fatalf("unexpected first hop: %v", hop1)
	}
	if hop1["username"] != "u1" || hop1["password"] != "p1" {
		t.Fatalf("hop credentials not rendered: %v", hop1)
	}
	if hop1["dialer-proxy"] != "entry-hk01" {
		t.Fatalf("first hop must dial through the entry node: %v", hop1)
	}

	hop2, ok := proxies["chain-us01-2"]
	if !ok {
		t.Fatalf("second chain hop missing:\n%s", out)
	}
	if hop2["type"] != "http" || hop2["dialer-proxy"] != "chain-us01-1" {
		t.Fatalf("second hop must dial hop 1: %v", hop2)
	}
	if _, disabled := proxies["chain-us01-3"]; disabled {
		t.Fatal("disabled hops must not be rendered")
	}

	outbound := proxies["out-us01"]
	if outbound["dialer-proxy"] != "chain-us01-2" {
		t.Fatalf("outbound must dial the last hop: %v", outbound)
	}
	if len(proxies) != 4 {
		t.Fatalf("expected entry + 2 hops + outbound, got %d:\n%s", len(proxies), out)
	}

	groups, _ := doc["proxy-groups"].([]any)
	members, _ := groups[0].(map[string]any)["proxies"].([]any)
	for _, m := range members {
		if strings.HasPrefix(m.(string), "chain-") {
			t.Fatalf("chain hops are dialers only and must not be group members: %v", members)
		}
	}

	joined := strings.Join(warnings, " | ")
	if !strings.Contains(joined, "链式代理") || !strings.Contains(joined, "sing-box") {
		t.Fatalf("warnings must state the chain is mihomo-only: %v", warnings)
	}
}

func TestRenderMihomoChainWithoutEntryDialsDirectly(t *testing.T) {
	in := chainInputs()
	cfg := Default()
	cfg.Outbound.HostIDs = []string{"us01"} // no inbound host

	out, _, err := Render(cfg, in)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	hop1 := proxyByName(t, doc)["chain-us01-1"]
	if _, chained := hop1["dialer-proxy"]; chained {
		t.Fatalf("first hop must dial directly when no entry exists: %v", hop1)
	}
}

func TestSkeletonWarnsAboutChain(t *testing.T) {
	cfg := Default()
	cfg.Kernel = "sing-box"
	cfg.Outbound.HostIDs = []string{"us01"}
	_, warnings, err := Render(cfg, chainInputs())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "链式代理") {
			found = true
		}
	}
	if !found {
		t.Fatalf("sing-box skeleton must warn that dialer-proxy chains are not rendered: %v", warnings)
	}
}
