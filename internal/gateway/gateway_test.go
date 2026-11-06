package gateway

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	d := Default()
	if d.WAN.Mode != "dhcp" {
		t.Errorf("wan.mode = %q, want dhcp", d.WAN.Mode)
	}
	if d.WAN.MTU != 1500 {
		t.Errorf("wan.mtu = %d, want 1500", d.WAN.MTU)
	}
	if !d.WAN.IPv6 {
		t.Error("wan.ipv6 should default to true")
	}
	if len(d.WAN.DNS) != 2 || d.WAN.DNS[0] != "223.5.5.5" || d.WAN.DNS[1] != "119.29.29.29" {
		t.Errorf("wan.dns = %v", d.WAN.DNS)
	}
	if d.WAN.Password != "" || d.WAN.PasswordSet {
		t.Error("a fresh config must not claim a stored password")
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

func TestSaveRejectsInvalidConfigsWithoutWriting(t *testing.T) {
	base := func() Config {
		c := Default()
		c.WAN.Mode = "static"
		c.WAN.Address = "203.0.113.10"
		c.WAN.Gateway = "203.0.113.1"
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
		{"unknown wan mode", "wan.mode", func(c *Config) { c.WAN.Mode = "pptp" }},
		{"mtu too small", "wan.mtu", func(c *Config) { c.WAN.MTU = 575 }},
		{"mtu too large", "wan.mtu", func(c *Config) { c.WAN.MTU = 9001 }},
		{"static without address", "wan.address", func(c *Config) { c.WAN.Address = "" }},
		{"static without gateway", "wan.gateway", func(c *Config) { c.WAN.Gateway = "" }},
		{"static with bogus address", "wan.address", func(c *Config) { c.WAN.Address = "192.168.1.999" }},
		{"pppoe without credentials", "wan.username", func(c *Config) {
			c.WAN.Mode = "pppoe"
			c.WAN.Username = ""
			c.WAN.Password = ""
		}},
		{"pppoe without password", "wan.password", func(c *Config) {
			c.WAN.Mode = "pppoe"
			c.WAN.Username = "user@isp"
			c.WAN.Password = ""
		}},
		{"bad dns entry", "wan.dns", func(c *Config) { c.WAN.DNS = []string{"223.5.5.5", "not-an-ip"} }},
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

func TestPasswordIsMaskedAndPreserved(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)

	c := Default()
	c.WAN.Mode = "pppoe"
	c.WAN.Username = "user@isp"
	c.WAN.Password = "s3cret-pppoe"
	saved, changed, warnings, err := st.Save(c)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.WAN.Password != "s3cret-pppoe" || !saved.WAN.PasswordSet {
		t.Fatalf("store must keep the secret internally, got %+v", saved.WAN)
	}
	if len(changed) == 0 {
		t.Fatal("changing the WAN mode must be reported in changed")
	}
	if len(warnings) == 0 {
		t.Fatal("a declarative save must carry its honest warnings")
	}
	pub := saved.Public()
	if pub.WAN.Password != "" {
		t.Fatalf("Public() leaked the password: %q", pub.WAN.Password)
	}
	if !pub.WAN.PasswordSet {
		t.Fatal("Public() must report password_set=true")
	}

	// A second save with an empty password must keep the stored one.
	next := st.Get()
	next.WAN.Password = ""
	next.LAN.Domain = "home"
	saved2, changed2, _, err := st.Save(next)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if saved2.WAN.Password != "s3cret-pppoe" {
		t.Fatalf("empty password must keep the stored secret, got %q", saved2.WAN.Password)
	}
	for _, p := range changed2 {
		if p == "wan.password" {
			t.Fatal("an unchanged password must not appear in changed")
		}
	}
	if !contains(changed2, "lan.domain") {
		t.Fatalf("changed = %v, want lan.domain", changed2)
	}

	// The secret is on disk (0600 file) and only there.
	raw, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if !strings.Contains(string(raw), "s3cret-pppoe") {
		t.Fatal("persisted file should contain the secret so it survives a restart")
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
	// The API-facing document must never serialise the secret.
	blob, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "s3cret-pppoe") {
		t.Fatalf("public JSON leaked the password: %s", blob)
	}
}

func TestSavePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	c := Default()
	c.WAN.Mode = "static"
	c.WAN.Address = "198.51.100.20"
	c.WAN.Gateway = "198.51.100.1"
	c.WAN.Interface = "eth0"
	c.WAN.MTU = 1400
	c.WAN.DNS = []string{"1.1.1.1"}
	c.LAN.LeaseMinutes = 720
	c.Firewall.Rules = []FirewallRule{{ID: "r1", Name: "ssh", Action: "accept", Protocol: "tcp", Src: "any", Dst: "any", Port: "22", Enabled: true}}
	c.Firewall.PortForwards = []PortForward{{ID: "f1", Name: "nas", Protocol: "both", ExtPort: 8443, IntAddr: "192.168.10.20", IntPort: 443, Enabled: true}}

	saved, changed, _, err := st.Save(c)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !contains(changed, "wan.address") || !contains(changed, "firewall.rules[0].id") {
		t.Fatalf("changed = %v, want field level paths", changed)
	}
	if st.UpdatedAt().IsZero() {
		t.Fatal("save must record updated_at")
	}
	if !st.Loaded() {
		t.Fatal("save must mark the store loaded")
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reloaded.Loaded() {
		t.Fatal("Load must mark the store loaded")
	}
	got := reloaded.Get()
	if got.WAN.Mode != "static" || got.WAN.Address != "198.51.100.20" || got.WAN.MTU != 1400 {
		t.Fatalf("reloaded WAN = %+v", got.WAN)
	}
	if len(got.WAN.DNS) != 1 || got.WAN.DNS[0] != "1.1.1.1" {
		t.Fatalf("reloaded DNS = %v", got.WAN.DNS)
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
	c.WAN.DNS[0] = "9.9.9.9"
	c.Firewall.Rules = append(c.Firewall.Rules, FirewallRule{ID: "x"})
	if got := st.Get(); got.WAN.DNS[0] != "223.5.5.5" || len(got.Firewall.Rules) != 0 {
		t.Fatal("Get must not expose the store's internal slices")
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
	if stt.Interfaces == nil || stt.DNS == nil {
		t.Fatal("interfaces / dns must serialise as [] not null")
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

	c := Default()
	c.WAN.DNS = []string{"9.9.9.9"}
	if _, _, _, err := st.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved := st.Status()
	if !saved.FirewallActive {
		t.Fatal("a saved drop-by-default policy must report firewall_active")
	}
	if len(saved.DNS) != 1 || saved.DNS[0] != "9.9.9.9" {
		t.Fatalf("status dns after save = %v", saved.DNS)
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
