package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	d := Default()

	if len(d.WANs) != WANCount {
		t.Fatalf("len(wans) = %d, want %d", len(d.WANs), WANCount)
	}
	for i, w := range d.WANs {
		wantID := fmt.Sprintf("wan%d", i+1)
		wantName := fmt.Sprintf("WAN %d", i+1)
		if w.ID != wantID || w.Name != wantName {
			t.Errorf("wans[%d] id/name = %q/%q, want %q/%q", i, w.ID, w.Name, wantID, wantName)
		}
		if w.Mode != "dhcp" {
			t.Errorf("wans[%d].mode = %q, want dhcp", i, w.Mode)
		}
		if w.Route != "local" {
			t.Errorf("wans[%d].route = %q, want local", i, w.Route)
		}
		if w.MTU != 1500 || !w.IPv6 || w.Weight != 1 {
			t.Errorf("wans[%d] mtu/ipv6/weight = %d/%v/%d", i, w.MTU, w.IPv6, w.Weight)
		}
		if w.Priority != i+1 {
			t.Errorf("wans[%d].priority = %d, want %d", i, w.Priority, i+1)
		}
		if len(w.DNS) != 2 || w.DNS[0] != "223.5.5.5" || w.DNS[1] != "119.29.29.29" {
			t.Errorf("wans[%d].dns = %v", i, w.DNS)
		}
		if w.Password != "" || w.PasswordSet {
			t.Errorf("wans[%d] must not claim a stored password", i)
		}
		wantEnabled := i == 0
		if w.Enabled != wantEnabled {
			t.Errorf("wans[%d].enabled = %v, want %v", i, w.Enabled, wantEnabled)
		}
	}

	// wan is the compatibility mirror of wans[0].
	if !reflect.DeepEqual(d.WAN, d.WANs[0]) {
		t.Fatalf("wan must mirror wans[0]: %+v vs %+v", d.WAN, d.WANs[0])
	}

	if d.DNS.Mode != "forward" {
		t.Errorf("dns.mode = %q, want forward", d.DNS.Mode)
	}
	if len(d.DNS.Servers) != 2 || d.DNS.Servers[0] != "223.5.5.5" || d.DNS.Servers[1] != "119.29.29.29" {
		t.Errorf("dns.servers = %v", d.DNS.Servers)
	}
	if len(d.DNS.Fallback) != 1 || d.DNS.Fallback[0] != "8.8.8.8" {
		t.Errorf("dns.fallback = %v", d.DNS.Fallback)
	}
	if d.DNS.DoH != "" || !d.DNS.CacheEnabled || d.DNS.CacheSize != 10000 || d.DNS.CacheTTL != 3600 {
		t.Errorf("dns cache defaults = %+v", d.DNS)
	}
	if d.DNS.LocalDomain != "lan" || d.DNS.DNSSEC || d.DNS.LogQueries {
		t.Errorf("dns misc defaults = %+v", d.DNS)
	}
	if d.DNS.Hosts == nil || d.DNS.Blocklist == nil {
		t.Error("hosts / blocklist must be non-nil so JSON is {} / [] not null")
	}

	if d.LAN.Address != "192.168.10.1" || d.LAN.Netmask != "255.255.255.0" {
		t.Errorf("lan.address/netmask = %s/%s", d.LAN.Address, d.LAN.Netmask)
	}
	if !d.LAN.DHCPEnabled || d.LAN.DHCPStart != "192.168.10.100" || d.LAN.DHCPEnd != "192.168.10.200" {
		t.Errorf("lan dhcp pool = %v %s-%s", d.LAN.DHCPEnabled, d.LAN.DHCPStart, d.LAN.DHCPEnd)
	}
	if d.LAN.LeaseMinutes != 1440 || d.LAN.Domain != "lan" {
		t.Errorf("lan lease/domain = %d/%s", d.LAN.LeaseMinutes, d.LAN.Domain)
	}
	if d.Firewall.DefaultInbound != "drop" || d.Firewall.UPnP || !d.Firewall.Conntrack {
		t.Errorf("firewall defaults = %+v", d.Firewall)
	}
	if d.Firewall.Rules == nil || d.Firewall.PortForwards == nil {
		t.Error("rules / port_forwards must be empty slices so JSON is [] not null")
	}
	// The default document must survive its own validator.
	if err := validate(normalize(d)); err != nil {
		t.Fatalf("default config does not validate: %v", err)
	}
}

func TestNewStoreStartsFromDefaults(t *testing.T) {
	st := NewStore(t.TempDir())
	if st.Loaded() {
		t.Fatal("a store with no file on disk must not report Loaded")
	}
	if !st.UpdatedAt().IsZero() {
		t.Fatal("a store with no file must not report an update time")
	}
	if got := st.Get(); !reflect.DeepEqual(got, Default()) {
		t.Fatalf("Get() = %+v, want defaults", got)
	}
}

// A request that only carries the compatibility "wan" field (the pre-multi-WAN
// API shape) must land on wans[0] and leave the other five ports at defaults.
func TestNormalizePadsToSixPortsAndMirrorsWan(t *testing.T) {
	c := Config{
		WAN: WANConfig{
			Mode:      "static",
			Address:   "203.0.113.10",
			Gateway:   "203.0.113.1",
			MTU:       1400,
			DNS:       []string{"1.1.1.1", "1.1.1.1", " "},
			Username:  "  user@isp  ",
			Password:  "legacy",
			Interface: " eth0 ",
		},
	}
	got := normalize(c)
	if len(got.WANs) != WANCount {
		t.Fatalf("len(wans) = %d, want %d", len(got.WANs), WANCount)
	}
	w0 := got.WANs[0]
	if w0.ID != "wan1" || w0.Name != "WAN 1" || !w0.Enabled {
		t.Fatalf("migrated wans[0] = %+v", w0)
	}
	if w0.Mode != "static" || w0.Address != "203.0.113.10" || w0.MTU != 1400 {
		t.Fatalf("migrated wans[0] lost the uplink settings: %+v", w0)
	}
	if w0.Interface != "eth0" || w0.Username != "user@isp" {
		t.Fatalf("migrated wans[0] not trimmed: %+v", w0)
	}
	if len(w0.DNS) != 1 || w0.DNS[0] != "1.1.1.1" {
		t.Fatalf("wan dns not deduped: %v", w0.DNS)
	}
	if w0.Route != "local" || w0.Weight != 1 || w0.Priority != 1 {
		t.Fatalf("migrated wans[0] routing defaults = %+v", w0)
	}
	if !w0.PasswordSet || w0.Password != "legacy" {
		t.Fatalf("migrated wans[0] password = %+v", w0)
	}
	for i := 1; i < WANCount; i++ {
		w := got.WANs[i]
		if w.ID != fmt.Sprintf("wan%d", i+1) {
			t.Errorf("wans[%d].id = %q", i, w.ID)
		}
		if w.Enabled {
			t.Errorf("wans[%d] must stay disabled after migration", i)
		}
		if w.MTU != 1500 || w.Weight != 1 || w.Priority != i+1 || w.Route != "local" {
			t.Errorf("wans[%d] defaults = %+v", i, w)
		}
	}
	if !reflect.DeepEqual(got.WAN, got.WANs[0]) {
		t.Fatal("normalize must keep wan == wans[0]")
	}

	// wans wins over the compat field once both are present.
	both := got
	both.WANs[0].Address = "198.51.100.9"
	both.WAN.Address = "192.0.2.1"
	if mirrored := normalize(both); mirrored.WAN.Address != "198.51.100.9" {
		t.Fatalf("wan = %q, want wans[0].address to win", mirrored.WAN.Address)
	}

	// More than six ports are truncated, never persisted.
	extra := Default()
	extra.WANs = append(extra.WANs, DefaultWAN(0))
	if got := normalize(extra); len(got.WANs) != WANCount {
		t.Fatalf("len(wans) = %d, want %d after truncation", len(got.WANs), WANCount)
	}
}

// A real v2.6 file on disk only has "wan" and no "dns" section.
func TestLoadMigratesLegacySingleWANDocument(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	legacy := `{
  "config": {
    "wan": {
      "mode": "static",
      "interface": "eth0",
      "address": "203.0.113.10",
      "gateway": "203.0.113.1",
      "mtu": 1400,
      "dns": ["1.1.1.1"],
      "ipv6": false,
      "username": "",
      "password": "legacy-secret",
      "password_set": true
    },
    "lan": {
      "interface": "br0",
      "address": "192.168.10.1",
      "netmask": "255.255.255.0",
      "dhcp_enabled": true,
      "dhcp_start": "192.168.10.100",
      "dhcp_end": "192.168.10.200",
      "lease_minutes": 1440,
      "domain": "lan"
    },
    "firewall": {
      "default_inbound": "drop",
      "rules": [],
      "port_forwards": [],
      "upnp": false,
      "conntrack": true
    }
  },
  "updated_at": "2026-01-02T03:04:05Z"
}`
	path := filepath.Join(dir, "gateway-config.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !st.Loaded() {
		t.Fatal("Load must mark the store loaded")
	}
	got := st.Get()
	if len(got.WANs) != WANCount {
		t.Fatalf("len(wans) = %d, want %d", len(got.WANs), WANCount)
	}
	w0 := got.WANs[0]
	if w0.ID != "wan1" || !w0.Enabled || w0.Mode != "static" || w0.Address != "203.0.113.10" {
		t.Fatalf("migrated wans[0] = %+v", w0)
	}
	if w0.MTU != 1400 || len(w0.DNS) != 1 || w0.DNS[0] != "1.1.1.1" {
		t.Fatalf("migrated wans[0] mtu/dns = %d/%v", w0.MTU, w0.DNS)
	}
	if w0.Password != "legacy-secret" || !w0.PasswordSet {
		t.Fatal("migration must not lose the stored password")
	}
	if !reflect.DeepEqual(got.WAN, got.WANs[0]) {
		t.Fatal("wan must equal wans[0] after migration")
	}
	if got.DNS.Mode != "forward" || len(got.DNS.Servers) != 2 || got.DNS.CacheSize != 10000 || got.DNS.LocalDomain != "lan" {
		t.Fatalf("dns section not defaulted: %+v", got.DNS)
	}
	if got.DNS.Hosts == nil {
		t.Fatal("dns.hosts must be an empty map, not nil")
	}
	// The migrated document must be savable as-is.
	if _, _, _, err := st.Save(got); err != nil {
		t.Fatalf("a migrated legacy config must validate: %v", err)
	}
}

func TestSaveRejectsInvalidConfigsWithoutWriting(t *testing.T) {
	base := func() Config {
		c := Default()
		c.WANs[0].Mode = "static"
		c.WANs[0].Address = "203.0.113.10"
		c.WANs[0].Gateway = "203.0.113.1"
		c.WAN = c.WANs[0]
		return c
	}
	withRule := func(c *Config, rule FirewallRule) {
		c.Firewall.Rules = append(c.Firewall.Rules, rule)
	}
	longName := strings.Repeat("名", 65)

	cases := []struct {
		name  string
		want  string
		mutat func(c *Config)
	}{
		{"unknown wan mode", "wans[0].mode", func(c *Config) { c.WANs[0].Mode = "pptp" }},
		{"mtu too small", "wans[0].mtu", func(c *Config) { c.WANs[0].MTU = 575 }},
		{"mtu too large", "wans[0].mtu", func(c *Config) { c.WANs[0].MTU = 9001 }},
		{"static without address", "wans[0].address", func(c *Config) { c.WANs[0].Address = "" }},
		{"static without gateway", "wans[0].gateway", func(c *Config) { c.WANs[0].Gateway = "" }},
		{"static with bogus address", "wans[0].address", func(c *Config) { c.WANs[0].Address = "192.168.1.999" }},
		{"pppoe without username", "wans[0].username", func(c *Config) {
			c.WANs[0].Mode = "pppoe"
			c.WANs[0].Username = ""
			c.WANs[0].Password = ""
		}},
		{"pppoe without password", "wans[0].password", func(c *Config) {
			c.WANs[0].Mode = "pppoe"
			c.WANs[0].Username = "user@isp"
			c.WANs[0].Password = ""
		}},
		{"bad wan dns entry", "wans[0].dns", func(c *Config) {
			c.WANs[0].DNS = []string{"223.5.5.5", "not-an-ip"}
		}},
		{"wan id outside wan1..wan6", "wans[0].id", func(c *Config) { c.WANs[0].ID = "eth7" }},
		{"duplicate wan id", "wans[1].id", func(c *Config) { c.WANs[1].ID = "wan1" }},
		{"no wan enabled", "wans", func(c *Config) {
			for i := range c.WANs {
				c.WANs[i].Enabled = false
			}
		}},
		{"bad route", "wans[0].route", func(c *Config) { c.WANs[0].Route = "vpn" }},
		{"weight above range", "wans[0].weight", func(c *Config) { c.WANs[0].Weight = 101 }},
		{"weight below range", "wans[2].weight", func(c *Config) { c.WANs[2].Enabled = true; c.WANs[2].Weight = -1 }},
		{"priority above range", "wans[1].priority", func(c *Config) { c.WANs[1].Enabled = true; c.WANs[1].Priority = 11 }},
		{"priority below range", "wans[1].priority", func(c *Config) { c.WANs[1].Enabled = true; c.WANs[1].Priority = -1 }},
		{"wan name too long", "wans[0].name", func(c *Config) { c.WANs[0].Name = longName }},
		{"non-contiguous netmask", "lan.netmask", func(c *Config) { c.LAN.Netmask = "255.0.255.0" }},
		{"dhcp pool outside the lan subnet", "lan.dhcp_start", func(c *Config) {
			c.LAN.DHCPStart = "10.0.0.10"
		}},
		{"dhcp start after end", "lan.dhcp_start", func(c *Config) {
			c.LAN.DHCPStart = "192.168.10.200"
			c.LAN.DHCPEnd = "192.168.10.100"
		}},
		{"dhcp pool equals network address", "lan.dhcp_start", func(c *Config) {
			c.LAN.DHCPStart = "192.168.10.0"
		}},
		{"lease out of range", "lan.lease_minutes", func(c *Config) { c.LAN.LeaseMinutes = 20000 }},
		{"bad default inbound", "firewall.default_inbound", func(c *Config) {
			c.Firewall.DefaultInbound = "allow"
		}},
		{"duplicate rule id", "firewall.rules[1].id", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Name: "ssh", Action: "accept", Protocol: "tcp", Port: "22"})
			withRule(c, FirewallRule{ID: "r1", Name: "web", Action: "accept", Protocol: "tcp", Port: "80"})
		}},
		{"rule without name", "firewall.rules[0].name", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Action: "accept", Protocol: "tcp"})
		}},
		{"rule name too long", "firewall.rules[0].name", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Name: longName, Action: "accept", Protocol: "tcp"})
		}},
		{"bad rule action", "firewall.rules[0].action", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Name: "ssh", Action: "deny", Protocol: "tcp"})
		}},
		{"bad rule protocol", "firewall.rules[0].protocol", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Name: "ssh", Action: "accept", Protocol: "icmp"})
		}},
		{"rule port out of range", "firewall.rules[0].port", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Name: "ssh", Action: "accept", Protocol: "tcp", Port: "70000"})
		}},
		{"rule port range inverted", "firewall.rules[0].port", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Name: "web", Action: "accept", Protocol: "tcp", Port: "8080-80"})
		}},
		{"rule src is not an ip", "firewall.rules[0].src", func(c *Config) {
			withRule(c, FirewallRule{ID: "r1", Name: "web", Action: "accept", Protocol: "tcp", Src: "office"})
		}},
		{"forward port zero", "firewall.port_forwards[0].ext_port", func(c *Config) {
			c.Firewall.PortForwards = []PortForward{{ID: "f1", Name: "nas", Protocol: "tcp", ExtPort: 0, IntAddr: "192.168.10.20", IntPort: 5000}}
		}},
		{"forward internal port too large", "firewall.port_forwards[0].int_port", func(c *Config) {
			c.Firewall.PortForwards = []PortForward{{ID: "f1", Name: "nas", Protocol: "tcp", ExtPort: 8000, IntAddr: "192.168.10.20", IntPort: 65536}}
		}},
		{"forward bad protocol", "firewall.port_forwards[0].protocol", func(c *Config) {
			c.Firewall.PortForwards = []PortForward{{ID: "f1", Name: "nas", Protocol: "sctp", ExtPort: 8000, IntAddr: "192.168.10.20", IntPort: 5000}}
		}},
		{"forward bad internal address", "firewall.port_forwards[0].int_addr", func(c *Config) {
			c.Firewall.PortForwards = []PortForward{{ID: "f1", Name: "nas", Protocol: "tcp", ExtPort: 8000, IntAddr: "nas.local", IntPort: 5000}}
		}},
		{"duplicate forward id", "firewall.port_forwards[1].id", func(c *Config) {
			c.Firewall.PortForwards = []PortForward{
				{ID: "f1", Name: "nas", Protocol: "tcp", ExtPort: 8000, IntAddr: "192.168.10.20", IntPort: 5000},
				{ID: "f1", Name: "cam", Protocol: "tcp", ExtPort: 8001, IntAddr: "192.168.10.21", IntPort: 5000},
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			st := NewStore(dir)
			c := base()
			tc.mutat(&c)
			if _, _, _, err := st.Save(c); err == nil {
				t.Fatalf("expected rejection, config was accepted: %+v", c)
			} else {
				var verr *ValidationError
				if !errors.As(err, &verr) {
					t.Fatalf("error %v is not a *ValidationError", err)
				}
				if verr.Field != tc.want {
					t.Fatalf("rejected field = %q, want %q (%v)", verr.Field, tc.want, err)
				}
			}
			if _, err := os.Stat(st.Path()); !os.IsNotExist(err) {
				t.Fatalf("a rejected config must not be persisted (stat err = %v)", err)
			}
			if st.Loaded() {
				t.Fatal("a rejected config must not mark the store loaded")
			}
			if !reflect.DeepEqual(st.Get(), Default()) {
				t.Fatal("a rejected config must not change the in-memory config")
			}
		})
	}
}

// The resolver policy is validated field by field; every case must be refused
// with the exact field name so the UI can point at the right input.
func TestDNSValidation(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		mutat func(d *DNSConfig)
	}{
		{"unknown mode", "dns.mode", func(d *DNSConfig) { d.Mode = "dot" }},
		{"no servers", "dns.servers", func(d *DNSConfig) { d.Servers = []string{} }},
		{"bogus server", "dns.servers", func(d *DNSConfig) { d.Servers = []string{"223.5.5.5", "dns.example.com"} }},
		{"bogus fallback", "dns.fallback", func(d *DNSConfig) { d.Fallback = []string{"8.8.8.8", "1.1.1"} }},
		{"doh without https", "dns.doh", func(d *DNSConfig) { d.DoH = "dns.alidns.com/dns-query" }},
		{"cache size too small", "dns.cache_size", func(d *DNSConfig) { d.CacheSize = 1000001 }},
		{"cache ttl too small", "dns.cache_ttl", func(d *DNSConfig) { d.CacheTTL = 9; d.CacheSize = 500 }},
		{"cache ttl too large", "dns.cache_ttl", func(d *DNSConfig) { d.CacheTTL = 86401 }},
		{"bad local domain", "dns.local_domain", func(d *DNSConfig) { d.LocalDomain = "not a domain" }},
		{"hosts key is not a domain", "dns.hosts", func(d *DNSConfig) {
			d.Hosts = map[string]string{"nas .lan": "192.168.10.20"}
		}},
		{"hosts value is not an ip", "dns.hosts", func(d *DNSConfig) {
			d.Hosts = map[string]string{"nas.lan": "192.168.10.300"}
		}},
		{"blocklist entry is not a domain", "dns.blocklist", func(d *DNSConfig) {
			d.Blocklist = []string{"ads.example.com", "http://tracker.example.com"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.WANs[0].Mode = "static"
			c.WANs[0].Address = "203.0.113.10"
			c.WANs[0].Gateway = "203.0.113.1"
			tc.mutat(&c.DNS)
			err := validate(normalize(c))
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error %v is not a *ValidationError", err)
			}
			if verr.Field != tc.want {
				t.Fatalf("rejected field = %q, want %q (%v)", verr.Field, tc.want, err)
			}
		})
	}

	// The documented default and a fully populated policy must both pass.
	ok := Default()
	ok.DNS = DNSConfig{
		Mode:         "recursive",
		Servers:      []string{"223.5.5.5", "2400:3200::1"},
		Fallback:     []string{},
		DoH:          "https://dns.alidns.com/dns-query",
		CacheEnabled: false,
		CacheSize:    100,
		CacheTTL:     10,
		LocalDomain:  "home.lan",
		Hosts:        map[string]string{"nas.home.lan": "192.168.10.20"},
		Blocklist:    []string{"ads.example.com", "*.tracker.example.com"},
		DNSSEC:       true,
		LogQueries:   true,
	}
	ok.WANs[0].Mode = "static"
	ok.WANs[0].Address = "203.0.113.10"
	ok.WANs[0].Gateway = "203.0.113.1"
	if err := validate(normalize(ok)); err != nil {
		t.Fatalf("a valid DNS policy was rejected: %v", err)
	}
}

// Each WAN keeps its own PPPoE secret: rotating one must not wipe another, and
// no surface ever echoes a secret back.
func TestPasswordPerWANIsIndependentAndNeverEchoed(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)

	c := Default()
	c.WANs[0].Mode = "pppoe"
	c.WANs[0].Username = "user1@isp"
	c.WANs[0].Password = "secret-wan1"
	c.WANs[1].Enabled = true
	c.WANs[1].Mode = "pppoe"
	c.WANs[1].Interface = "eth1"
	c.WANs[1].Username = "user2@isp"
	c.WANs[1].Password = "secret-wan2"
	saved, changed, warnings, err := st.Save(c)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.WANs[0].Password != "secret-wan1" || saved.WANs[1].Password != "secret-wan2" {
		t.Fatalf("per-port secrets not stored: %+v", saved.WANs)
	}
	if len(changed) == 0 {
		t.Fatal("changing the WAN mode must be reported in changed")
	}
	if len(warnings) == 0 {
		t.Fatal("a declarative save must carry its honest warnings")
	}
	pub := saved.Public()
	for i, w := range pub.WANs {
		if w.Password != "" {
			t.Fatalf("Public() leaked the password of wans[%d]: %q", i, w.Password)
		}
	}
	if !pub.WANs[0].PasswordSet || !pub.WANs[1].PasswordSet || pub.WANs[2].PasswordSet {
		t.Fatalf("password_set flags = %+v", pub.WANs)
	}
	if pub.WAN.Password != "" || !pub.WAN.PasswordSet {
		t.Fatal("the compat wan field must also be masked")
	}

	// Save wan2 with an empty password and a new username: wan1 must keep its
	// secret, wan2 must keep its own.
	next := st.Get()
	next.WANs[1].Password = ""
	next.WANs[1].Username = "user2b@isp"
	saved2, changed2, _, err := st.Save(next)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if saved2.WANs[0].Password != "secret-wan1" {
		t.Fatalf("wan1 secret lost: %q", saved2.WANs[0].Password)
	}
	if saved2.WANs[1].Password != "secret-wan2" {
		t.Fatalf("wan2 secret not preserved on empty password: %q", saved2.WANs[1].Password)
	}
	for _, p := range changed2 {
		if strings.HasSuffix(p, "password") {
			t.Fatalf("an unchanged password must not appear in changed: %v", changed2)
		}
	}
	if !contains(changed2, "wans[1].username") {
		t.Fatalf("changed = %v, want wans[1].username", changed2)
	}

	// A fresh store that loads the file still has both secrets.
	reload := NewStore(dir)
	if err := reload.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reload.Get(); got.WANs[0].Password != "secret-wan1" || got.WANs[1].Password != "secret-wan2" {
		t.Fatal("secrets must survive a restart")
	}

	raw, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	for _, secret := range []string{"secret-wan1", "secret-wan2"} {
		if !strings.Contains(string(raw), secret) {
			t.Fatalf("persisted file should contain %s so it survives a restart", secret)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(st.Path())
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("persisted file mode = %o, want 600", perm)
		}
	}
	blob, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "secret-wan1") || strings.Contains(string(blob), "secret-wan2") {
		t.Fatalf("public JSON leaked a password: %s", blob)
	}
}

func TestSavePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	c := Default()
	c.WANs[0].Mode = "static"
	c.WANs[0].Address = "198.51.100.20"
	c.WANs[0].Gateway = "198.51.100.1"
	c.WANs[0].Interface = "eth0"
	c.WANs[0].MTU = 1400
	c.WANs[0].DNS = []string{"1.1.1.1"}
	c.WANs[0].Route = "proxy"
	c.WANs[0].Weight = 7
	c.WANs[1].Enabled = true
	c.WANs[1].Interface = "eth1"
	c.WANs[1].Priority = 2
	c.WANs[1].Route = "proxy"
	c.DNS.Servers = []string{"9.9.9.9"}
	c.DNS.DoH = "https://dns.quad9.net/dns-query"
	c.LAN.LeaseMinutes = 720
	c.Firewall.Rules = []FirewallRule{{ID: "r1", Name: "ssh", Action: "accept", Protocol: "tcp", Src: "any", Dst: "any", Port: "22", Enabled: true}}
	c.Firewall.PortForwards = []PortForward{{ID: "f1", Name: "nas", Protocol: "both", ExtPort: 8443, IntAddr: "192.168.10.20", IntPort: 443, Enabled: true}}

	saved, changed, warnings, err := st.Save(c)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !contains(changed, "wan.address") || !contains(changed, "firewall.rules[0].id") {
		t.Fatalf("changed = %v, want field level paths", changed)
	}
	proxyWarned := false
	for _, w := range warnings {
		if strings.Contains(w, "route=proxy") {
			proxyWarned = true
		}
	}
	if !proxyWarned {
		t.Fatalf("a proxy-routed WAN must warn that no policy routing is applied: %v", warnings)
	}
	if st.UpdatedAt().IsZero() {
		t.Fatal("save must record updated_at")
	}
	if !st.Loaded() {
		t.Fatal("save must mark the store loaded")
	}
	if !reflect.DeepEqual(saved.WAN, saved.WANs[0]) {
		t.Fatal("wan must mirror wans[0] after a save")
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reloaded.Loaded() {
		t.Fatal("Load must mark the store loaded")
	}
	got := reloaded.Get()
	if len(got.WANs) != WANCount {
		t.Fatalf("reloaded wans = %d, want %d", len(got.WANs), WANCount)
	}
	w0, w1 := got.WANs[0], got.WANs[1]
	if w0.Mode != "static" || w0.Address != "198.51.100.20" || w0.MTU != 1400 {
		t.Fatalf("reloaded WAN = %+v", w0)
	}
	if w0.Route != "proxy" || w0.Weight != 7 || w0.Priority != 1 {
		t.Fatalf("reloaded routing = %+v", w0)
	}
	if len(w0.DNS) != 1 || w0.DNS[0] != "1.1.1.1" {
		t.Fatalf("reloaded DNS = %v", w0.DNS)
	}
	if !w1.Enabled || w1.Interface != "eth1" || w1.Route != "proxy" || w1.Priority != 2 {
		t.Fatalf("reloaded wans[1] = %+v", w1)
	}
	if !reflect.DeepEqual(got.WAN, got.WANs[0]) {
		t.Fatal("reloaded wan must equal wans[0]")
	}
	if got.DNS.Servers[0] != "9.9.9.9" || got.DNS.DoH != "https://dns.quad9.net/dns-query" {
		t.Fatalf("reloaded dns = %+v", got.DNS)
	}
	if got.LAN.LeaseMinutes != 720 {
		t.Fatalf("reloaded lease = %d", got.LAN.LeaseMinutes)
	}
	if len(got.Firewall.Rules) != 1 || got.Firewall.Rules[0].Port != "22" {
		t.Fatalf("reloaded rules = %+v", got.Firewall.Rules)
	}
	if len(got.Firewall.PortForwards) != 1 || got.Firewall.PortForwards[0].ExtPort != 8443 {
		t.Fatalf("reloaded forwards = %+v", got.Firewall.PortForwards)
	}
	if !reflect.DeepEqual(saved.Public(), got.Public()) {
		t.Fatalf("reloaded config drifted:\nsaved   = %+v\nreload  = %+v", saved.Public(), got.Public())
	}
	if reloaded.UpdatedAt().IsZero() {
		t.Fatal("updated_at must survive a reload")
	}
}

func TestGetReturnsACopy(t *testing.T) {
	st := NewStore(t.TempDir())
	c := st.Get()
	c.WANs[0].DNS[0] = "9.9.9.9"
	c.WANs[1].Interface = "mutated"
	c.DNS.Servers[0] = "6.6.6.6"
	c.DNS.Hosts["nas.lan"] = "192.168.10.5"
	c.Firewall.Rules = append(c.Firewall.Rules, FirewallRule{ID: "x"})
	got := st.Get()
	if got.WANs[0].DNS[0] != "223.5.5.5" || got.WANs[1].Interface != "" {
		t.Fatal("Get must not expose the store's WAN slices")
	}
	if got.DNS.Servers[0] != "223.5.5.5" || len(got.DNS.Hosts) != 0 {
		t.Fatal("Get must not expose the store's DNS slices / map")
	}
	if len(got.Firewall.Rules) != 0 {
		t.Fatal("Get must not expose the store's rule slice")
	}
}

// wanLinks is the pure mapping from saved policy + real NICs to the status
// view, so it can be tested without depending on the host's adapters.
func TestWANLinksMapping(t *testing.T) {
	live := []liveIface{
		{info: InterfaceInfo{Name: "eth0", Up: true, Addresses: []string{"fe80::1/64", "192.0.2.10/24"}}},
		{info: InterfaceInfo{Name: "eth1", Up: false, Addresses: []string{"198.51.100.7/24"}}},
		{info: InterfaceInfo{Name: "eth2", Up: true, Addresses: []string{}}},
		{info: InterfaceInfo{Name: "lo", Up: true, Addresses: []string{"127.0.0.1/8"}}},
	}
	cfg := Default()
	cfg.WANs[0].Interface = "eth0"
	cfg.WANs[0].Route = "proxy"
	cfg.WANs[1].Enabled = true
	cfg.WANs[1].Interface = "eth1"
	cfg.WANs[2].Enabled = true
	cfg.WANs[2].Interface = "eth2"
	cfg.WANs[3].Enabled = true
	cfg.WANs[3].Interface = "eth9" // not present on this host
	cfg.WANs[4].Enabled = true     // no interface configured
	cfg.WANs[5].Interface = "eth0" // disabled: must not show up
	cfg.WANs[5].Enabled = false

	links := wanLinks(cfg, live)
	if len(links) != 5 {
		t.Fatalf("len(wan_links) = %d, want 5 enabled ports (%+v)", len(links), links)
	}
	if links[0].ID != "wan1" || links[0].Interface != "eth0" || links[0].Route != "proxy" {
		t.Fatalf("links[0] = %+v", links[0])
	}
	if links[0].Address != "192.0.2.10" || !links[0].Online {
		t.Fatalf("links[0] must report the real IPv4 and online=true: %+v", links[0])
	}
	if links[1].Online || links[1].Address != "198.51.100.7" {
		t.Fatalf("a down interface must be offline but keep its address: %+v", links[1])
	}
	if links[2].Online || links[2].Address != "" {
		t.Fatalf("an interface without addresses must be offline: %+v", links[2])
	}
	if links[3].Online || links[3].Address != "" {
		t.Fatalf("an unknown interface must be offline with no address: %+v", links[3])
	}
	if links[4].Online || links[4].Interface != "" {
		t.Fatalf("a port without an interface must be offline: %+v", links[4])
	}
	for _, l := range links {
		if l.ID == "wan6" {
			t.Fatal("disabled ports must not appear in wan_links")
		}
	}

	// Loopback-only interfaces are not a usable uplink.
	lo := []liveIface{{info: InterfaceInfo{Name: "lo", Up: true, Addresses: []string{"127.0.0.1/8"}}}}
	one := Default()
	one.WANs[0].Interface = "lo"
	if got := wanLinks(one, lo); got[0].Online || got[0].Address != "" {
		t.Fatalf("loopback must not count as an online WAN: %+v", got[0])
	}
}

func TestStatusReportsRealInterfaces(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	stt := st.Status()
	if len(stt.Interfaces) == 0 {
		t.Fatal("Status must enumerate the host interfaces")
	}
	if stt.CheckedAt.IsZero() {
		t.Fatal("Status must stamp checked_at")
	}
	if stt.Interfaces == nil || stt.DNS == nil || stt.WANLinks == nil {
		t.Fatal("interfaces / dns / wan_links must serialise as [] not null")
	}
	for _, ifc := range stt.Interfaces {
		if ifc.Name == "" {
			t.Fatal("interface without a name")
		}
		if ifc.Addresses == nil {
			t.Fatalf("interface %s must report an address list", ifc.Name)
		}
	}
	if stt.FirewallActive {
		t.Fatal("a never-saved default config must not report the firewall as active")
	}
	if len(stt.DNS) != 2 {
		t.Fatalf("status dns = %v, want the WAN defaults", stt.DNS)
	}
	// The default policy has exactly one enabled port and no interface bound,
	// so it must appear as offline rather than be invented.
	if len(stt.WANLinks) != 1 || stt.WANLinks[0].ID != "wan1" {
		t.Fatalf("wan_links = %+v, want only the enabled wan1", stt.WANLinks)
	}
	if stt.WANLinks[0].Online || stt.WANLinks[0].Interface != "" {
		t.Fatalf("unbound wan1 must be offline: %+v", stt.WANLinks[0])
	}

	// Bind wan1 to a real NIC: the reported link must mirror that NIC's state.
	// Pick a NIC the status view itself would consider online (up, and holding
	// a routable IPv4); loopback-only or address-less NICs are skipped.
	var liveIface *InterfaceInfo
	for i, ifc := range stt.Interfaces {
		if ifc.Up && firstIPv4(ifc.Addresses) != "" {
			liveIface = &stt.Interfaces[i]
			break
		}
	}
	c := Default()
	c.WANs[0].DNS = []string{"9.9.9.9"}
	if liveIface != nil {
		c.WANs[0].Interface = liveIface.Name
		c.WANs[1].Enabled = true
		c.WANs[1].Interface = "ngg-no-such-nic"
	}
	if _, _, _, err := st.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved := st.Status()
	if len(saved.DNS) != 1 || saved.DNS[0] != "9.9.9.9" {
		t.Fatalf("status dns after save = %v", saved.DNS)
	}
	if !saved.FirewallActive {
		t.Fatal("a saved drop-by-default policy must report firewall_active")
	}
	if liveIface != nil {
		if len(saved.WANLinks) != 2 {
			t.Fatalf("wan_links = %+v, want both enabled ports", saved.WANLinks)
		}
		if saved.WANLinks[0].Interface != liveIface.Name {
			t.Fatalf("wan1 link = %+v, want interface %q", saved.WANLinks[0], liveIface.Name)
		}
		if !saved.WANLinks[0].Online {
			t.Fatalf("wan1 bound to an up NIC with addresses must be online: %+v", saved.WANLinks[0])
		}
		if saved.WANLinks[1].Online {
			t.Fatalf("an unknown interface must be offline: %+v", saved.WANLinks[1])
		}
	}

	open := Default()
	open.Firewall.DefaultInbound = "accept"
	if _, _, _, err := st.Save(open); err != nil {
		t.Fatalf("Save accept: %v", err)
	}
	if st.Status().FirewallActive {
		t.Fatal("default_inbound=accept must not report firewall_active")
	}
}

func TestParseProcNetRoute(t *testing.T) {
	sample := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t0002A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	if got := parseProcNetRoute(strings.NewReader(sample)); got != "192.168.2.1" {
		t.Fatalf("gateway = %q, want 192.168.2.1", got)
	}

	noDefault := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\n" +
		"eth0\t0002A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\n"
	if got := parseProcNetRoute(strings.NewReader(noDefault)); got != "" {
		t.Fatalf("gateway = %q, want empty when no default route exists", got)
	}

	// A default route without RTF_GATEWAY (on-link) is not a gateway.
	onLink := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\n" +
		"eth0\t00000000\t00000000\t0001\t0\t0\t100\t00000000\t0\n"
	if got := parseProcNetRoute(strings.NewReader(onLink)); got != "" {
		t.Fatalf("gateway = %q, want empty for an on-link default route", got)
	}
}

func TestDefaultGatewayHonesty(t *testing.T) {
	got := defaultGateway()
	if runtime.GOOS != "linux" && got != "" {
		t.Fatalf("defaultGateway() = %q on %s; unsupported platforms must stay empty", got, runtime.GOOS)
	}
	if got != "" && !isIPv4(got) {
		t.Fatalf("defaultGateway() = %q is not an IPv4 address", got)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
