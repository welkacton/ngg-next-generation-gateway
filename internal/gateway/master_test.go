package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// 网关总控（v2.7）契约：默认值、旧文件迁移、校验、命令构造。
// 这里只断言可以从纯函数验证的东西，涉网操作一律用「不存在的适配器」验证
// 保守路径（永远不该真的执行 netsh / ip）。

func TestDefaultMasterMatchesContract(t *testing.T) {
	m := Default().Master
	if m.Enabled {
		t.Error("master.enabled must default to false")
	}
	if m.AdapterName != "NGG" {
		t.Errorf("adapter_name = %q, want NGG", m.AdapterName)
	}
	if m.AdapterMetric != 0 {
		t.Errorf("adapter_metric = %d, want 0", m.AdapterMetric)
	}
	if !m.CaptureAll {
		t.Error("capture_all must default to true")
	}
	if m.AutoStart {
		t.Error("auto_start must default to false")
	}
	if !m.BypassPrivate {
		t.Error("bypass_private must default to true")
	}
	if m.Split.Mode != "smart" || m.Split.Default != "local" {
		t.Errorf("split = %+v, want smart/local", m.Split)
	}
	if m.Uplink.Mode != "gateway-wan" || m.Uplink.WANID != "wan1" || m.Uplink.PipelineID != "" {
		t.Errorf("uplink = %+v, want gateway-wan/wan1/empty", m.Uplink)
	}
	if m.BypassCIDRs == nil || len(m.BypassCIDRs) != 0 {
		t.Errorf("bypass_cidrs = %v, want empty slice", m.BypassCIDRs)
	}
	for name, list := range map[string][]string{
		"proxy_domains": m.Split.ProxyDomains,
		"local_domains": m.Split.LocalDomains,
		"proxy_cidrs":   m.Split.ProxyCIDRs,
		"local_cidrs":   m.Split.LocalCIDRs,
	} {
		if list == nil {
			t.Errorf("%s must be an empty slice, not nil (JSON null)", name)
		}
	}
}

func TestMasterJSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(DefaultMaster())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{
		"adapter_metric", "adapter_name", "auto_start", "bypass_cidrs", "bypass_private",
		"capture_all", "enabled", "split", "uplink",
	}
	got := make([]string, 0, len(doc))
	for k := range doc {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("master keys = %v, want %v", got, want)
	}
	split, _ := doc["split"].(map[string]any)
	for _, k := range []string{"mode", "default", "proxy_domains", "local_domains", "proxy_cidrs", "local_cidrs"} {
		if _, ok := split[k]; !ok {
			t.Errorf("split is missing %q: %v", k, split)
		}
	}
	uplink, _ := doc["uplink"].(map[string]any)
	for _, k := range []string{"mode", "wan_id", "pipeline_id"} {
		if _, ok := uplink[k]; !ok {
			t.Errorf("uplink is missing %q: %v", k, uplink)
		}
	}

	// The whole document must carry the sub-object under "master".
	full, err := json.Marshal(Default())
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if !strings.Contains(string(full), `"master":{"enabled":false`) {
		t.Fatalf("config JSON must carry the master object: %s", full)
	}
}

// A v2.6 file has no "master" key at all. It must migrate to the documented
// defaults instead of to a zero value (which would mean "capture nothing").
func TestLegacyDocumentMigratesMasterDefaults(t *testing.T) {
	legacy := `{"wan":{"id":"wan1","name":"WAN 1","enabled":true,"mode":"dhcp","mtu":1500,
		"dns":["223.5.5.5"],"weight":1,"priority":1},
		"lan":{"address":"192.168.10.1","netmask":"255.255.255.0"},
		"dns":{"mode":"forward","servers":["223.5.5.5"]},
		"firewall":{"default_inbound":"drop"}}`
	var cfg Config
	if err := json.Unmarshal([]byte(legacy), &cfg); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if cfg.masterSet {
		t.Fatal("a document without \"master\" must not report masterSet")
	}
	got := normalize(cfg)
	want := DefaultMaster()
	gotRaw, _ := json.Marshal(got.Master)
	wantRaw, _ := json.Marshal(want)
	if string(gotRaw) != string(wantRaw) {
		t.Fatalf("migrated master = %s, want %s", gotRaw, wantRaw)
	}
	if len(got.WANs) != WANCount {
		t.Errorf("wans = %d, want %d", len(got.WANs), WANCount)
	}
	if got.WAN.ID != got.WANs[0].ID {
		t.Errorf("wan must mirror wans[0]: %q vs %q", got.WAN.ID, got.WANs[0].ID)
	}
	if err := validate(got); err != nil {
		t.Fatalf("migrated legacy document must validate: %v", err)
	}
}

// An explicit master object must survive normalisation unchanged where the
// operator set it, including the booleans that a zero value would flip.
func TestExplicitMasterIsNotOverwritten(t *testing.T) {
	body := `{"master":{"enabled":true,"adapter_name":"  NGG-Test  ","adapter_metric":120,
		"capture_all":false,"auto_start":true,"bypass_private":false,
		"bypass_cidrs":["10.0.0.0/8"],
		"split":{"mode":"GLOBAL-PROXY","default":"PROXY",
			"proxy_domains":["*.Example.COM."],"local_domains":[],
			"proxy_cidrs":["1.1.1.0/24"],"local_cidrs":[]},
		"uplink":{"mode":"PIPELINE","wan_id":"","pipeline_id":"PL-abc123"}}}`
	var cfg Config
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := normalize(cfg)
	m := got.Master
	if !m.Enabled || m.CaptureAll || !m.AutoStart || m.BypassPrivate {
		t.Errorf("booleans changed by normalisation: %+v", m)
	}
	if m.AdapterName != "NGG-Test" {
		t.Errorf("adapter_name = %q, want the trimmed value", m.AdapterName)
	}
	if m.AdapterMetric != 120 {
		t.Errorf("adapter_metric = %d, want 120", m.AdapterMetric)
	}
	if m.Split.Mode != "global-proxy" || m.Split.Default != "proxy" {
		t.Errorf("split enums = %q/%q, want lower-cased", m.Split.Mode, m.Split.Default)
	}
	if len(m.Split.ProxyDomains) != 1 || m.Split.ProxyDomains[0] != "*.example.com" {
		t.Errorf("proxy_domains = %v, want the lower-cased wildcard", m.Split.ProxyDomains)
	}
	if m.Uplink.Mode != "pipeline" || m.Uplink.PipelineID != "pl-abc123" {
		t.Errorf("uplink = %+v, want pipeline/pl-abc123", m.Uplink)
	}
	if err := validate(got); err != nil {
		t.Fatalf("explicit master must validate: %v", err)
	}
}

func TestMasterValidation(t *testing.T) {
	base := func() Config {
		c := Default()
		c.Master.Enabled = true
		return c
	}
	cases := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{"metric negative", func(c *Config) { c.Master.AdapterMetric = -1 }, "master.adapter_metric"},
		{"metric too large", func(c *Config) { c.Master.AdapterMetric = 10000 }, "master.adapter_metric"},
		{"adapter with quote", func(c *Config) { c.Master.AdapterName = `NG"G` }, "master.adapter_name"},
		{"adapter with slash", func(c *Config) { c.Master.AdapterName = "NGG/0" }, "master.adapter_name"},
		{"split mode", func(c *Config) { c.Master.Split.Mode = "auto" }, "master.split.mode"},
		{"split default", func(c *Config) { c.Master.Split.Default = "global" }, "master.split.default"},
		{"bad domain", func(c *Config) { c.Master.Split.ProxyDomains = []string{"bad domain"} }, "master.split.proxy_domains"},
		{"wildcard in middle", func(c *Config) { c.Master.Split.LocalDomains = []string{"a.*.com"} }, "master.split.local_domains"},
		{"bad proxy cidr", func(c *Config) { c.Master.Split.ProxyCIDRs = []string{"10.0.0.0/33"} }, "master.split.proxy_cidrs"},
		{"bad local cidr", func(c *Config) { c.Master.Split.LocalCIDRs = []string{"nope"} }, "master.split.local_cidrs"},
		{"bad bypass cidr", func(c *Config) { c.Master.BypassCIDRs = []string{"10.0.0.1"} }, "master.bypass_cidrs"},
		{"uplink mode", func(c *Config) { c.Master.Uplink.Mode = "wan" }, "master.uplink.mode"},
		{"uplink wan id", func(c *Config) { c.Master.Uplink.WANID = "wan9" }, "master.uplink.wan_id"},
		{"pipeline id with space", func(c *Config) { c.Master.Uplink.PipelineID = "pl a" }, "master.uplink.pipeline_id"},
		{"pipeline id leading dash", func(c *Config) { c.Master.Uplink.PipelineID = "-pl-1" }, "master.uplink.pipeline_id"},
		{"wan pipeline id invalid", func(c *Config) {
			c.WANs[0].PipelineID = "bad id!"
			c.WAN = c.WANs[0]
		}, "wans[0].pipeline_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(&c)
			got := normalize(c)
			err := validate(got)
			if err == nil {
				t.Fatalf("config was accepted, want a 422 on %s", tc.field)
			}
			verr, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("error is not a ValidationError: %v", err)
			}
			if key := verr.Field; key != tc.field {
				t.Fatalf("field = %q, want %q (%v)", key, tc.field, err)
			}
		})
	}

	// An empty adapter name is not an error: normalisation fills the documented
	// default, which is what makes a partial PUT work.
	blank := base()
	blank.Master.AdapterName = "   "
	if got := normalize(blank).Master.AdapterName; got != "NGG" {
		t.Fatalf("blank adapter name = %q, want the default", got)
	}

	// A pipeline id in the documented shape must be accepted after lowering.
	pipe := base()
	pipe.Master.Uplink.Mode = "pipeline"
	pipe.Master.Uplink.PipelineID = "PL-Default"
	gotPipe := normalize(pipe)
	if err := validate(gotPipe); err != nil {
		t.Fatalf("normalised pipeline id must validate: %v", err)
	}
	if gotPipe.Master.Uplink.PipelineID != "pl-default" {
		t.Fatalf("pipeline_id = %q, want lower-cased", gotPipe.Master.Uplink.PipelineID)
	}

	// The untouched base must be accepted, otherwise the table above proves
	// nothing about which field was wrong.
	if err := validate(normalize(base())); err != nil {
		t.Fatalf("baseline master must validate: %v", err)
	}
}

func TestMasterPipelineIDShapes(t *testing.T) {
	ok := []string{"", "pl-abc", "pl-abc123", "default", "pl-x.y_z"}
	for _, id := range ok {
		if err := checkPipelineID("f", id); err != nil {
			t.Errorf("checkPipelineID(%q) = %v, want nil", id, err)
		}
	}
	bad := []string{"-pl", "_x", ".x", "pl abc", "pl/abc", "PL-ABC!"}
	for _, id := range bad {
		if err := checkPipelineID("f", id); err == nil {
			t.Errorf("checkPipelineID(%q) = nil, want an error", id)
		}
	}
}

func TestSplitEntriesByMode(t *testing.T) {
	m := DefaultMaster()
	m.Split.ProxyDomains = []string{"*.google.com", "openai.com"}
	m.Split.ProxyCIDRs = []string{"1.1.1.0/24"}
	m.Split.LocalDomains = []string{"intranet.lan"}
	m.Split.LocalCIDRs = []string{"10.0.0.0/8"}

	smart := m.SplitEntries()
	if len(smart) != 5 {
		t.Fatalf("smart entries = %d, want 5 (%+v)", len(smart), smart)
	}
	if smart[0].Kind != SplitDomain || smart[0].Value != "google.com" || smart[0].Action != SplitProxy {
		t.Errorf("first entry = %+v, want bare proxy domain", smart[0])
	}

	m.Split.Mode = "global-proxy"
	proxyOnly := m.SplitEntries()
	if len(proxyOnly) != 3 {
		t.Fatalf("global-proxy entries = %d, want 3 (%+v)", len(proxyOnly), proxyOnly)
	}
	for _, e := range proxyOnly {
		if e.Action != SplitProxy {
			t.Errorf("global-proxy emitted a non-proxy entry: %+v", e)
		}
	}

	m.Split.Mode = "direct"
	localOnly := m.SplitEntries()
	if len(localOnly) != 2 {
		t.Fatalf("direct entries = %d, want 2 (%+v)", len(localOnly), localOnly)
	}
	for _, e := range localOnly {
		if e.Action != SplitDirect {
			t.Errorf("direct emitted a non-local entry: %+v", e)
		}
	}

	if got := m.DefaultSplitAction(); got != "local" {
		t.Errorf("direct mode default action = %q, want local", got)
	}
	m.Split.Mode = "global-proxy"
	if got := m.DefaultSplitAction(); got != "proxy" {
		t.Errorf("global-proxy default action = %q, want proxy", got)
	}
	m.Split.Mode = "smart"
	m.Split.Default = "proxy"
	if got := m.DefaultSplitAction(); got != "proxy" {
		t.Errorf("smart default action = %q, want the policy default", got)
	}
}

func TestDiffCoversMasterFields(t *testing.T) {
	old := Default()
	now := Default()
	now.Master.AdapterMetric = 42
	now.Master.Split.Mode = "direct"
	now.Master.Uplink.PipelineID = "pl-1"

	changed := diffPaths(old, now)
	for _, want := range []string{"master.adapter_metric", "master.split.mode", "master.uplink.pipeline_id"} {
		found := false
		for _, c := range changed {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Errorf("diff %v is missing %q", changed, want)
		}
	}
	if len(diffPaths(now, now)) != 0 {
		t.Errorf("identical configs must produce no diff: %v", diffPaths(now, now))
	}
}

func TestPlanCaptureWindows(t *testing.T) {
	m := DefaultMaster()
	m.AdapterMetric = 5
	cmds := displayCommands(planCaptureFor("windows", m, "10.89.0.1"))
	if len(cmds) != 2 {
		t.Fatalf("windows plan = %v, want 2 commands", cmds)
	}
	if !strings.Contains(cmds[0], `netsh interface ipv4 set interface NGG metric=5`) {
		t.Errorf("metric command = %q", cmds[0])
	}
	// A name with a space must be quoted so the printed command is still
	// copy-pasteable.
	spaced := DefaultMaster()
	spaced.AdapterName = "NGG Tunnel"
	if got := displayCommands(planCaptureFor("windows", spaced, "")); len(got) != 1 ||
		!strings.Contains(got[0], `"NGG Tunnel"`) {
		t.Errorf("spaced adapter plan = %v, want a quoted name", got)
	}
	for _, want := range []string{"add route 0.0.0.0/0", "interface=NGG", "nexthop=10.89.0.1", "metric=5", "store=active"} {
		if !strings.Contains(cmds[1], want) {
			t.Errorf("route command %q is missing %q", cmds[1], want)
		}
	}
	for _, c := range cmds {
		if strings.Contains(c, `""`) {
			t.Errorf("command has an empty argument: %q", c)
		}
	}

	// Without a known nexthop the route step must be omitted, never guessed.
	if got := planCaptureFor("windows", m, ""); len(got) != 1 {
		t.Fatalf("plan without nexthop = %v, want only the metric command", got)
	}

	// capture_all=false must not install a default route.
	m.CaptureAll = false
	if got := planCaptureFor("windows", m, "10.89.0.1"); len(got) != 1 {
		t.Fatalf("capture_all=false plan = %v, want only the metric command", got)
	}

	revert := displayCommands(planRevertFor("windows", DefaultMaster(), "10.89.0.1"))
	if len(revert) != 2 {
		t.Fatalf("windows revert = %v, want 2 commands", revert)
	}
	if !strings.Contains(revert[0], "delete route 0.0.0.0/0") || !strings.Contains(revert[0], "nexthop=10.89.0.1") {
		t.Errorf("revert route command = %q", revert[0])
	}
	if !strings.Contains(revert[1], "metric=automatic") {
		t.Errorf("revert metric command = %q", revert[1])
	}
}

func TestPlanCaptureLinux(t *testing.T) {
	m := DefaultMaster()
	m.AdapterName = "ngg0"
	m.AdapterMetric = 10
	cmds := displayCommands(planCaptureFor("linux", m, ""))
	if len(cmds) != 2 {
		t.Fatalf("linux plan = %v, want 2 commands", cmds)
	}
	if cmds[0] != "ip link set dev ngg0 up" {
		t.Errorf("link command = %q", cmds[0])
	}
	if cmds[1] != "ip route replace default dev ngg0 metric 10" {
		t.Errorf("route command = %q", cmds[1])
	}
	revert := displayCommands(planRevertFor("linux", m, ""))
	if len(revert) != 1 || revert[0] != "ip route del default dev ngg0" {
		t.Fatalf("linux revert = %v", revert)
	}
}

func TestPlanCaptureUnsupportedPlatform(t *testing.T) {
	for _, goos := range []string{"darwin", "freebsd", "plan9"} {
		if got := planCaptureFor(goos, DefaultMaster(), "10.89.0.1"); len(got) != 0 {
			t.Errorf("%s plan = %v, want empty", goos, got)
		}
		if got := planRevertFor(goos, DefaultMaster(), "10.89.0.1"); len(got) != 0 {
			t.Errorf("%s revert = %v, want empty", goos, got)
		}
	}
	// An empty adapter name can never make a valid command.
	if got := planCaptureFor("windows", Master{}, "10.89.0.1"); len(got) != 0 {
		t.Errorf("empty adapter plan = %v, want empty", got)
	}
}

func TestParseNetshMetric(t *testing.T) {
	// Real shape of `netsh interface ipv4 show interfaces`.
	out := "Idx     Met         MTU          State                Name\r\n" +
		"---  ----------  ----------  ------------  ---------------------------\r\n" +
		"  1          75  4294967295  connected     Loopback Pseudo-Interface 1\r\n" +
		" 12           0        1500  connected     NGG\r\n" +
		"  7          25        1500  disconnected  Ethernet 2\r\n"
	if got := parseNetshMetric(out, "NGG"); got != 0 {
		t.Errorf("metric(NGG) = %d, want 0", got)
	}
	if got := parseNetshMetric(out, "ngg"); got != 0 {
		t.Errorf("metric must be case-insensitive, got %d", got)
	}
	if got := parseNetshMetric(out, "Ethernet 2"); got != 25 {
		t.Errorf("metric(Ethernet 2) = %d, want 25", got)
	}
	if got := parseNetshMetric(out, "Loopback Pseudo-Interface 1"); got != 75 {
		t.Errorf("metric(loopback) = %d, want 75", got)
	}
	if got := parseNetshMetric(out, "missing"); got != -1 {
		t.Errorf("metric(missing) = %d, want -1", got)
	}
	if got := parseNetshMetric("", "NGG"); got != -1 {
		t.Errorf("metric of empty output = %d, want -1", got)
	}
}

// SaveMaster must go through the one write path: the rest of the document is
// preserved, and a later full-document save without "master" must not wipe it.
func TestStoreMasterPersistence(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := DefaultMaster()
	m.Enabled = true
	m.AdapterMetric = 33
	m.Split.ProxyDomains = []string{"example.com"}
	if _, _, _, err := st.SaveMaster(m); err != nil {
		t.Fatalf("SaveMaster: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gateway-config.json")); err != nil {
		t.Fatalf("gateway-config.json not written: %v", err)
	}

	// A legacy-shaped full save (no master key) keeps the stored master.
	cfg := st.Get()
	cfg.Master = Master{}
	cfg.masterSet = false
	if _, _, _, err := st.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after := st.Get().Master
	if !after.Enabled || after.AdapterMetric != 33 || len(after.Split.ProxyDomains) != 1 {
		t.Fatalf("a save without master wiped it: %+v", after)
	}

	// Reload from disk: the master survives a restart.
	st2 := NewStore(dir)
	if err := st2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := st2.Get().Master
	if got.AdapterMetric != 33 || !got.Enabled {
		t.Fatalf("persisted master = %+v", got)
	}
}

func TestSaveMasterRejectsInvalid(t *testing.T) {
	st := NewStore(t.TempDir())
	m := DefaultMaster()
	m.AdapterMetric = 10000
	if _, _, _, err := st.SaveMaster(m); err == nil {
		t.Fatal("an out-of-range metric must be rejected")
	} else if _, ok := err.(*ValidationError); !ok {
		t.Fatalf("error = %v, want a ValidationError", err)
	}
	if st.Loaded() {
		t.Fatal("a rejected save must not mark the store as saved")
	}
}

// ApplyDatapath must never claim success on a host that cannot execute: the
// adapter used here does not exist anywhere, so the plan is returned and
// executed/applied stay false on every platform.
func TestApplyDatapathIsHonest(t *testing.T) {
	st := NewStore(t.TempDir())
	m := DefaultMaster()
	m.Enabled = true
	m.AdapterName = "zzz-ngg-missing-adapter"
	m.AdapterMetric = 7
	if _, _, _, err := st.SaveMaster(m); err != nil {
		t.Fatalf("SaveMaster: %v", err)
	}

	res := st.ApplyDatapath()
	if res.Applied {
		t.Fatalf("applied must be false when nothing ran: %+v", res)
	}
	if res.Executed && runtime.GOOS != "windows" {
		t.Errorf("executed must be false on %s", runtime.GOOS)
	}
	if len(res.Commands) == 0 {
		if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
			t.Fatalf("the plan must be returned for manual execution: %+v", res)
		}
	}
	if res.Reason == "" {
		t.Error("reason must explain why nothing was applied")
	}
	if res.Status.Platform != runtime.GOOS {
		t.Errorf("status.platform = %q, want %q", res.Status.Platform, runtime.GOOS)
	}
	if res.Status.AdapterPresent {
		t.Errorf("a fake adapter cannot be present: %+v", res.Status)
	}

	rev := st.RevertDatapath()
	if rev.Applied {
		t.Fatalf("revert applied must be false when nothing ran: %+v", rev)
	}
	if rev.Reason == "" {
		t.Error("revert must explain itself")
	}
}

func TestDatapathStatusShape(t *testing.T) {
	st := NewStore(t.TempDir())
	status := st.Datapath()
	if status.Platform != runtime.GOOS {
		t.Errorf("platform = %q, want %q", status.Platform, runtime.GOOS)
	}
	if status.AdapterName != "NGG" {
		t.Errorf("adapter_name = %q, want the configured name", status.AdapterName)
	}
	if status.AdapterMetric < -1 {
		t.Errorf("adapter_metric = %d, want -1 or a real metric", status.AdapterMetric)
	}
	if status.CaptureActive && !status.DefaultRouteViaAdapter {
		t.Error("capture_active requires default_route_via_adapter")
	}
	if status.CheckedAt == "" {
		t.Error("checked_at must be filled")
	}
	if status.Reason == "" {
		t.Error("reason must always explain the state")
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"platform"`, `"elevated"`, `"adapter_present"`, `"adapter_name"`, `"adapter_metric"`,
		`"default_route_via_adapter"`, `"tun_supported"`, `"wintun_present"`,
		`"capture_active"`, `"reason"`, `"checked_at"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("status JSON is missing %s: %s", key, raw)
		}
	}
}
