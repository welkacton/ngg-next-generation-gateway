package gateway

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// 数据面（虚拟网卡 + 默认路由）状态与接管。
//
// HONESTY RULES for this file:
//   - every field of DatapathStatus is read from the host, never invented;
//     a value that cannot be read is -1 (metric) or false, with a reason;
//   - the plan (netsh / ip commands) is always returned so an operator can run
//     it by hand;
//   - only Windows + elevated really executes, and "applied" is only true
//     when the post-condition was verified afterwards. A command that exited 0
//     but did not change the default route is NOT a success.

// DatapathStatus is the live data-path capability report (contract B2).
type DatapathStatus struct {
	Platform               string `json:"platform"`
	Elevated               bool   `json:"elevated"`
	AdapterPresent         bool   `json:"adapter_present"`
	AdapterName            string `json:"adapter_name"`
	AdapterMetric          int    `json:"adapter_metric"`
	DefaultRouteViaAdapter bool   `json:"default_route_via_adapter"`
	TunSupported           bool   `json:"tun_supported"`
	WintunPresent          bool   `json:"wintun_present"`
	CaptureActive          bool   `json:"capture_active"`
	Reason                 string `json:"reason"`
	CheckedAt              string `json:"checked_at"`
}

// DatapathResult is the outcome of one apply / revert attempt.
//
// Executed says "commands were actually run", Applied says "the intended
// state is verified on the host". They are separate because a plan that was
// only printed is executed=false, applied=false.
type DatapathResult struct {
	Applied  bool           `json:"applied"`
	Executed bool           `json:"executed"`
	Commands []string       `json:"commands"`
	Reason   string         `json:"reason"`
	Status   DatapathStatus `json:"status"`
}

// command is one platform command in argv form. The display string is derived
// from the same argv, so what the UI shows and what gets executed can never
// drift apart.
type command struct {
	bin  string
	args []string
}

func (c command) String() string {
	parts := make([]string, 0, len(c.args)+1)
	parts = append(parts, c.bin)
	for _, a := range c.args {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

// quoteArg quotes an argument only when it needs it, so the displayed command
// stays copy-pasteable on Windows.
func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"&|<>^") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// planCapture returns the commands that take the data path over, for the
// current platform. An unsupported platform yields an empty plan.
func planCapture(cfg Config) []string {
	return displayCommands(planCaptureFor(runtime.GOOS, cfg.Master, adapterIPv4(cfg.Master.AdapterName)))
}

// planRevert returns the commands that undo planCapture.
func planRevert(cfg Config) []string {
	return displayCommands(planRevertFor(runtime.GOOS, cfg.Master, adapterIPv4(cfg.Master.AdapterName)))
}

func displayCommands(cmds []command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.String())
	}
	return out
}

// planCaptureFor builds the takeover commands for one platform. nexthop is the
// TUN peer address; a Wintun adapter is point-to-point, so the default route
// needs it and the route step is omitted while it is unknown (a guessed
// nexthop would create a black hole).
//
//   - windows: interface metric + default route via the adapter
//   - linux:   link up + default route replace
//   - others:  empty (no backend implemented in this build)
func planCaptureFor(goos string, m Master, nexthop string) []command {
	name := strings.TrimSpace(m.AdapterName)
	if name == "" {
		return nil
	}
	metric := m.AdapterMetric
	switch goos {
	case "windows":
		cmds := []command{{
			bin:  "netsh",
			args: []string{"interface", "ipv4", "set", "interface", name, fmt.Sprintf("metric=%d", metric)},
		}}
		if m.CaptureAll && nexthop != "" {
			cmds = append(cmds, command{
				bin: "netsh",
				args: []string{
					"interface", "ipv4", "add", "route", "0.0.0.0/0",
					"interface=" + name,
					"nexthop=" + nexthop,
					fmt.Sprintf("metric=%d", metric),
					"store=active",
				},
			})
		}
		return cmds
	case "linux":
		cmds := []command{{
			bin:  "ip",
			args: []string{"link", "set", "dev", name, "up"},
		}}
		if m.CaptureAll {
			cmds = append(cmds, command{
				bin:  "ip",
				args: []string{"route", "replace", "default", "dev", name, "metric", strconv.Itoa(metric)},
			})
		}
		return cmds
	}
	return nil
}

// planRevertFor undoes planCaptureFor: the default route is removed and the
// interface metric goes back to automatic.
func planRevertFor(goos string, m Master, nexthop string) []command {
	name := strings.TrimSpace(m.AdapterName)
	if name == "" {
		return nil
	}
	switch goos {
	case "windows":
		cmds := []command{}
		if nexthop != "" {
			cmds = append(cmds, command{
				bin: "netsh",
				args: []string{
					"interface", "ipv4", "delete", "route", "0.0.0.0/0",
					"interface=" + name,
					"nexthop=" + nexthop,
				},
			})
		}
		cmds = append(cmds, command{
			bin:  "netsh",
			args: []string{"interface", "ipv4", "set", "interface", name, "metric=automatic"},
		})
		return cmds
	case "linux":
		return []command{{
			bin:  "ip",
			args: []string{"route", "del", "default", "dev", name},
		}}
	}
	return nil
}

// Datapath reads the live data-path state. It never blocks longer than the
// existing probe timeout and never shells out except for the documented
// Windows metric read.
func (s *Store) Datapath() DatapathStatus {
	cfg := s.Get()
	m := cfg.Master
	st := DatapathStatus{
		Platform:      runtime.GOOS,
		Elevated:      isElevated(),
		AdapterName:   m.AdapterName,
		AdapterMetric: -1,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	st.AdapterPresent = interfaceExists(m.AdapterName)
	if st.AdapterPresent {
		st.AdapterMetric = adapterMetric(m.AdapterName)
	}
	st.WintunPresent = wintunPresent()
	st.TunSupported = tunSupported()
	if ip := outboundIP(); ip != nil {
		if name := interfaceForIP(ip); name != "" && strings.EqualFold(name, m.AdapterName) {
			st.DefaultRouteViaAdapter = true
		}
	}
	st.CaptureActive = st.AdapterPresent && st.DefaultRouteViaAdapter
	st.Reason = datapathReason(st, m)
	return st
}

// datapathReason explains, in plain language, why the data path is or is not
// taken over. Every clause is a fact about this host.
func datapathReason(st DatapathStatus, m Master) string {
	reasons := []string{}
	if st.Platform != "windows" {
		reasons = append(reasons, fmt.Sprintf("当前平台（%s）只生成命令，不会自动执行；请在管理员 / root 下手工运行", st.Platform))
	} else {
		if !st.Elevated {
			reasons = append(reasons, "Windows 需要管理员权限才能改写接口 metric 与默认路由，当前进程未提权，只会生成命令")
		}
		if !st.WintunPresent {
			reasons = append(reasons, "未找到 wintun.dll（exe 同目录、PATH 或 System32），需要先安装 Wintun 才能创建虚拟网卡")
		}
	}
	if !st.AdapterPresent {
		reasons = append(reasons, fmt.Sprintf("适配器 %q 不在本机网卡列表中", m.AdapterName))
	}
	if st.AdapterPresent && !st.CaptureActive {
		reasons = append(reasons, fmt.Sprintf("默认路由当前不经过 %q，数据面尚未接管", m.AdapterName))
	}
	if st.CaptureActive {
		reasons = append(reasons, fmt.Sprintf("默认路由已经过 %q，数据面接管生效中", m.AdapterName))
	}
	if !m.CaptureAll {
		reasons = append(reasons, "master.capture_all=false：只调整接口优先级，不下发默认路由（按域分流依赖内核侧支持）")
	}
	return strings.Join(reasons, "；")
}

// ApplyDatapath executes the capture plan when, and only when, this host can
// really do it (Windows + elevated + adapter present).
func (s *Store) ApplyDatapath() DatapathResult {
	return s.runDatapath(true)
}

// RevertDatapath undoes a previously applied capture plan under the same
// conditions as ApplyDatapath.
func (s *Store) RevertDatapath() DatapathResult {
	return s.runDatapath(false)
}

func (s *Store) runDatapath(apply bool) DatapathResult {
	cfg := s.Get()
	m := cfg.Master
	res := DatapathResult{Commands: []string{}, Status: s.Datapath()}
	action := "接管"
	if !apply {
		action = "撤销"
	}

	// The command line is built from configuration, so configuration is what
	// has to be trustworthy: reject a bad adapter name / metric before any
	// command exists at all.
	if err := validateMaster(normalizeMaster(m, DefaultMaster())); err != nil {
		res.Reason = "配置不合法，未生成任何命令：" + err.Error()
		return res
	}

	nexthop := adapterIPv4(m.AdapterName)
	cmds := planCaptureFor(runtime.GOOS, m, nexthop)
	if !apply {
		cmds = planRevertFor(runtime.GOOS, m, nexthop)
	}
	res.Commands = displayCommands(cmds)
	if len(cmds) == 0 {
		res.Reason = fmt.Sprintf("当前平台（%s）不支持自动%s，未生成任何命令", runtime.GOOS, action)
		return res
	}
	if !m.Enabled {
		res.Reason = "网关总控未启用（master.enabled=false），命令未执行；commands 是启用后需要执行的计划"
		return res
	}
	if runtime.GOOS != "windows" {
		res.Reason = fmt.Sprintf("本版只在 Windows 上自动执行数据面命令；当前平台（%s）的 %d 条命令请以 root 手工执行", runtime.GOOS, len(cmds))
		return res
	}
	if !res.Status.Elevated {
		res.Reason = "需要管理员权限：当前进程未提权，命令未执行，可复制 commands 以管理员身份运行"
		return res
	}
	if !res.Status.AdapterPresent {
		res.Reason = fmt.Sprintf("适配器 %q 不在本机网卡列表中，命令未执行（需先创建 Wintun 虚拟网卡）", m.AdapterName)
		return res
	}

	for i, c := range cmds {
		out, err := exec.Command(c.bin, c.args...).CombinedOutput()
		if err != nil {
			res.Executed = true
			res.Reason = fmt.Sprintf("第 %d 条命令执行失败：%v；输出：%s", i+1, err, summarizeOutput(string(out)))
			res.Status = s.Datapath()
			return res
		}
	}
	res.Executed = true

	// Re-read the host instead of trusting the exit codes: netsh returns 0 for
	// a route it accepted but that is not the one the kernel selected.
	res.Status = s.Datapath()
	switch {
	case !m.CaptureAll:
		res.Applied = true
		res.Reason = fmt.Sprintf("已执行 %d 条命令（capture_all=false，未下发默认路由）；默认路由是否经过 %q 请以 status 为准", len(cmds), m.AdapterName)
	case apply && res.Status.CaptureActive:
		res.Applied = true
		res.Reason = fmt.Sprintf("已执行 %d 条命令，且默认路由已经过 %q，接管生效", len(cmds), m.AdapterName)
	case !apply && !res.Status.CaptureActive:
		res.Applied = true
		res.Reason = fmt.Sprintf("已执行 %d 条命令，默认路由已不经过 %q，接管已撤销", len(cmds), m.AdapterName)
	case apply:
		res.Reason = fmt.Sprintf("命令已全部执行，但默认路由仍不经过 %q，可能适配器尚未就绪；applied 保持 false", m.AdapterName)
	default:
		res.Reason = fmt.Sprintf("命令已全部执行，但默认路由仍经过 %q，撤销未完全生效；applied 保持 false", m.AdapterName)
	}
	return res
}

func summarizeOutput(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "（无输出）"
	}
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

// isElevated lives in elevated_windows.go / elevated_other.go: only Windows
// has a token to inspect.

// tunSupported reports whether this build can create a TUN adapter here.
func tunSupported() bool {
	switch runtime.GOOS {
	case "windows":
		// Wintun is the only TUN backend in the Windows build.
		return wintunPresent()
	case "linux":
		_, err := os.Stat("/dev/net/tun")
		return err == nil
	}
	return false
}

// wintunPresent looks for wintun.dll where Windows would find it. Only the
// documented locations are probed; a missing DLL is reported as missing.
func wintunPresent() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if exe, err := os.Executable(); err == nil {
		if fileExists(filepath.Join(filepath.Dir(exe), "wintun.dll")) {
			return true
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if fileExists(filepath.Join(dir, "wintun.dll")) {
			return true
		}
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	return fileExists(filepath.Join(systemRoot, "System32", "wintun.dll"))
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// adapterMetric reads the interface metric. Only Windows exposes it through a
// stable text output (netsh); elsewhere it is reported as -1 rather than
// guessed.
func adapterMetric(name string) int {
	if runtime.GOOS != "windows" || name == "" {
		return -1
	}
	out, err := exec.Command("netsh", "interface", "ipv4", "show", "interfaces").Output()
	if err != nil {
		return -1
	}
	return parseNetshMetric(string(out), name)
}

// parseNetshMetric parses the "Met" column of
// `netsh interface ipv4 show interfaces` for one interface name. Names may
// contain spaces, which is why the name is the rest of the line.
func parseNetshMetric(out, name string) int {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if !strings.EqualFold(strings.Join(fields[4:], " "), name) {
			continue
		}
		metric, err := strconv.Atoi(fields[1])
		if err != nil || metric < 0 {
			return -1
		}
		return metric
	}
	return -1
}

// adapterIPv4 returns the IPv4 address of the named adapter, used as the
// point-to-point nexthop for the default route.
func adapterIPv4(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if !strings.EqualFold(ifc.Name, name) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			return ""
		}
		list := make([]string, 0, len(addrs))
		for _, a := range addrs {
			list = append(list, a.String())
		}
		return firstIPv4(list)
	}
	return ""
}

// interfaceForIP maps a local address back to its interface name.
func interfaceForIP(ip net.IP) string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if got := addrIP(a); got != nil && got.Equal(ip) {
				return ifc.Name
			}
		}
	}
	return ""
}
