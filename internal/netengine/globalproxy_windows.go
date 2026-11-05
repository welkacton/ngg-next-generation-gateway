//go:build windows

package netengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v4/net"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows "接管全局" has two honest levels:
//
//  1. System proxy (implemented here): writes HKCU Internet Settings and
//     broadcasts WinINET change notifications. Works as the current user,
//     immediately, no driver and no elevation. This is what "attach" does.
//  2. TUN virtual adapter: requires a signed WinTun/TAP driver and
//     Administrator privileges. NGG does NOT fake it — TunCapabilities()
//     reports whether the machine can actually create one, and the UI disables
//     the button with the real reason when it cannot.

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type proxyBackup struct {
	Saved  bool   `json:"saved"`
	Enable uint32 `json:"enable"`
	Server string `json:"server"`
	Bypass string `json:"bypass"`
}

var (
	wininetDLL            = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOption = wininetDLL.NewProc("InternetSetOptionW")
)

func notifyProxyChanged() {
	const (
		internetOptionRefresh         = 37
		internetOptionSettingsChanged = 39
	)
	_, _, _ = procInternetSetOption.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = procInternetSetOption.Call(0, internetOptionRefresh, 0, 0)
}

func (e *Engine) backupPath() string {
	// netengine must not depend on the control plane's storage layout; keep the
	// operator's original proxy in the per-user config directory.
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "NGG", "global-proxy-backup.json")
}

func (e *Engine) AttachGlobalProxy(_ context.Context) error {
	if !e.IsRunning() {
		return fmt.Errorf("统一网络引擎未运行：请先启动网络引擎，再接管系统代理")
	}
	addr := e.ingress.Addr()
	if addr == "" {
		return fmt.Errorf("入口地址为空：请先在配置中启用 ingress")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开 Internet Settings 失败: %w", err)
	}
	defer k.Close()

	// Back up the operator's original proxy once, so detach can restore it.
	if _, statErr := os.Stat(e.backupPath()); os.IsNotExist(statErr) {
		enable, _, _ := k.GetIntegerValue("ProxyEnable")
		server, _, _ := k.GetStringValue("ProxyServer")
		bypass, _, _ := k.GetStringValue("ProxyOverride")
		b, _ := json.Marshal(proxyBackup{
			Saved: true, Enable: uint32(enable), Server: server, Bypass: bypass,
		})
		_ = os.MkdirAll(filepath.Dir(e.backupPath()), 0o755)
		_ = os.WriteFile(e.backupPath(), b, 0o600)
	}

	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return fmt.Errorf("写入 ProxyEnable 失败: %w", err)
	}
	if err := k.SetStringValue("ProxyServer", addr); err != nil {
		return fmt.Errorf("写入 ProxyServer 失败: %w", err)
	}
	if err := k.SetStringValue("ProxyOverride", "<local>;127.*;localhost"); err != nil {
		return fmt.Errorf("写入 ProxyOverride 失败: %w", err)
	}
	notifyProxyChanged()
	e.globalProxy.Store(true)
	return nil
}

func (e *Engine) DetachGlobalProxy(_ context.Context) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开 Internet Settings 失败: %w", err)
	}
	defer k.Close()

	var bkp proxyBackup
	if raw, err := os.ReadFile(e.backupPath()); err == nil {
		_ = json.Unmarshal(raw, &bkp)
	}
	if bkp.Saved {
		_ = k.SetDWordValue("ProxyEnable", bkp.Enable)
		_ = k.SetStringValue("ProxyServer", bkp.Server)
		_ = k.SetStringValue("ProxyOverride", bkp.Bypass)
	} else {
		_ = k.SetDWordValue("ProxyEnable", 0)
	}
	notifyProxyChanged()
	_ = os.Remove(e.backupPath())
	e.globalProxy.Store(false)
	return nil
}

func (e *Engine) GlobalProxyStatus() map[string]any {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return map[string]any{"supported": true, "attached": false, "error": err.Error()}
	}
	defer k.Close()
	enable, _, _ := k.GetIntegerValue("ProxyEnable")
	server, _, _ := k.GetStringValue("ProxyServer")
	bypass, _, _ := k.GetStringValue("ProxyOverride")
	ingress := e.ingress.Addr()
	managed := e.globalProxy.Load()
	// "attached" must mean NGG owns the setting, not merely "the user already
	// had a proxy". A pre-existing Clash/V2Ray proxy is reported separately.
	attached := enable == 1 && managed && ingress != "" && strings.TrimSpace(server) == ingress
	return map[string]any{
		"supported":       true,
		"attached":        attached,
		"server":          server,
		"bypass":          bypass,
		"managed":         managed,
		"system_proxy_on": enable == 1 && strings.TrimSpace(server) != "",
		"ingress":         ingress,
		"mode":            "system-proxy",
	}
}

// TunCapabilities reports the REAL ability to create a virtual adapter.
func (e *Engine) TunCapabilities() map[string]any {
	admin := windows.GetCurrentProcessToken().IsElevated()
	dll := wintunDLLPath()
	adapters := virtualAdapters()
	signed := false
	if dll != "" {
		signed = verifyAuthenticode(dll) == nil
	}
	supported := admin && dll != ""
	reason := ""
	switch {
	case dll == "" && !admin:
		reason = "缺少 wintun.dll，且当前进程不是管理员；点「下载并安装驱动」后由服务（LocalSystem）自动创建网卡"
	case dll == "":
		reason = "缺少 wintun.dll；点「下载并安装驱动」自动下载官方签名版本"
	case !admin:
		reason = "已找到 wintun.dll，但当前进程不是管理员；NGG 服务以 LocalSystem 运行即可创建网卡"
	}
	return map[string]any{
		"os":               runtime.GOOS,
		"tun_supported":    supported,
		"admin":            admin,
		"wintun_dll":       dll,
		"reason":           reason,
		"virtual_adapters": adapters,
		"adapter":          e.tun.Status(),
		"driver": map[string]any{
			"present": dll != "",
			"path":    dll,
			"signed":  signed,
			"version": wintunVersionLabel(),
			"url":     wintunURL,
			"sha256":  wintunZipSHA256,
		},
		"note": "系统代理接管在用户态立即生效；TUN 虚拟网卡由 NGG 独占地创建并驱动，kernels 不会自行建卡。",
	}
}

func wintunDLLPath() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "wintun.dll"))
	}
	if windir := os.Getenv("WINDIR"); windir != "" {
		candidates = append(candidates, filepath.Join(windir, "System32", "wintun.dll"))
	}
	candidates = append(candidates, "wintun.dll")
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// virtualAdapters lists existing virtual NICs (WinTun / TAP / WireGuard / …)
// so the operator can see what the machine already has. Purely observational.
func virtualAdapters() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return []string{}
	}
	keywords := []string{"wintun", "tap", "tun", "tailscale", "wireguard", "meta", "clash", "wg", "vpn"}
	out := []string{}
	for _, i := range ifs {
		name := strings.ToLower(i.Name)
		for _, k := range keywords {
			if strings.Contains(name, k) {
				out = append(out, i.Name)
				break
			}
		}
	}
	return out
}
