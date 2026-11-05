//go:build !windows

package netengine

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// Non-Windows hosts do not have a WinINET system proxy. NGG reports that
// honestly instead of pretending to attach; Linux uses the real TUN path when
// /dev/net/tun and CAP_NET_ADMIN are available, everything else uses the
// userspace ingress.

var ErrGlobalProxyUnsupported = fmt.Errorf("system-proxy takeover is only implemented on Windows")

func (e *Engine) AttachGlobalProxy(_ context.Context) error {
	return ErrGlobalProxyUnsupported
}

func (e *Engine) DetachGlobalProxy(_ context.Context) error {
	return ErrGlobalProxyUnsupported
}

func (e *Engine) GlobalProxyStatus() map[string]any {
	return map[string]any{
		"supported": false,
		"attached":  false,
		"mode":      "none",
		"error":     "当前平台没有 WinINET 系统代理；Linux 请使用 TUN 或 ingress",
	}
}

func (e *Engine) TunCapabilities() map[string]any {
	admin := os.Geteuid() == 0
	dev := "/dev/net/tun"
	_, err := os.Stat(dev)
	hasDev := err == nil
	supported := admin && hasDev
	reason := ""
	switch {
	case !admin && !hasDev:
		reason = "需要 root（CAP_NET_ADMIN）且 /dev/net/tun 不存在"
	case !admin:
		reason = "检测到 /dev/net/tun，但当前进程不是 root（需要 CAP_NET_ADMIN）"
	case !hasDev:
		reason = "/dev/net/tun 不存在；请加载 tun 内核模块"
	}
	return map[string]any{
		"os":               runtime.GOOS,
		"tun_supported":    supported,
		"admin":            admin,
		"wintun_dll":       "",
		"reason":           reason,
		"virtual_adapters": []string{},
		"note":             "Linux TUN 需要 /dev/net/tun 与 CAP_NET_ADMIN；其余平台使用用户态 ingress。",
	}
}

// InstallWintun is Windows-only: other platforms use the kernel TUN.
func InstallWintun(_ context.Context) (map[string]any, error) {
	return nil, fmt.Errorf("%w: 当前平台不需要 wintun 驱动", ErrTUNUnsupported)
}
