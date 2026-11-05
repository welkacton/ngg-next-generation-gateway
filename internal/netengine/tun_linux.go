//go:build linux

package netengine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	tunSETIFF   = 0x400454ca
	iffTUN      = 0x0001
	iffNO_PI    = 0x1000
	iffVNET_HDR = 0x0400
)

type ifreq struct {
	Name  [16]byte
	Flags uint16
	_     [22]byte
}

// TunDevice is the REAL Linux TUN implementation. NGG is the only owner of
// this device; kernels never create a system-level TUN.
type TunDevice struct {
	mu    sync.Mutex
	file  *os.File
	name  string
	cfg   TunConfig
	stats TunStats
}

var _ TunManager = (*TunDevice)(nil)

func NewTunDevice() *TunDevice { return &TunDevice{} }

func (t *TunDevice) Open(_ context.Context, cfg TunConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.file != nil {
		return fmt.Errorf("TUN already open")
	}
	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("%w: open /dev/net/tun: %v", ErrTUNUnsupported, err)
	}
	var req ifreq
	copy(req.Name[:], cfg.Name)
	req.Flags = iffTUN | iffNO_PI
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(tunSETIFF), uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		_ = f.Close()
		return fmt.Errorf("%w: TUNSETIFF: %v (CAP_NET_ADMIN required)", ErrTUNUnsupported, errno)
	}
	name := string(req.Name[:])
	for i, b := range req.Name {
		if b == 0 {
			name = string(req.Name[:i])
			break
		}
	}
	if cfg.MTU > 0 {
		if err := setLinkMTU(name, cfg.MTU); err != nil {
			_ = f.Close()
			return fmt.Errorf("set MTU on %s: %w", name, err)
		}
	}
	if cfg.IPv4 != "" {
		if err := runIP("addr", "add", cfg.IPv4, "dev", name); err != nil {
			_ = f.Close()
			return fmt.Errorf("assign IPv4 to %s: %w", name, err)
		}
	}
	if cfg.IPv6 != "" {
		_ = runIP("-6", "addr", "add", cfg.IPv6, "dev", name)
	}
	if err := runIP("link", "set", name, "up"); err != nil {
		_ = f.Close()
		return fmt.Errorf("bring up %s: %w", name, err)
	}
	if cfg.AutoRoute {
		// NGG owns the global route. Kernels are explicitly forbidden from
		// doing this themselves (architecture revision §2/§8).
		_ = runIP("route", "replace", "default", "dev", name, "table", "17890")
		_ = runIP("rule", "add", "fwmark", "0x4E47", "lookup", "17890")
	}
	t.file, t.name, t.cfg = f, name, cfg
	t.stats = TunStats{Since: nowUTC()}
	return nil
}

func (t *TunDevice) Close(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.file == nil {
		return nil
	}
	err := t.file.Close()
	t.file = nil
	return err
}

func (t *TunDevice) Read(_ context.Context, buf []byte) (int, error) {
	t.mu.Lock()
	f := t.file
	t.mu.Unlock()
	if f == nil {
		return 0, fmt.Errorf("TUN not open")
	}
	n, err := f.Read(buf)
	if n > 0 {
		t.mu.Lock()
		t.stats.PacketsIn++
		t.stats.BytesIn += uint64(n)
		t.mu.Unlock()
	}
	return n, err
}

func (t *TunDevice) Write(_ context.Context, p []byte) (int, error) {
	t.mu.Lock()
	f := t.file
	t.mu.Unlock()
	if f == nil {
		return 0, fmt.Errorf("TUN not open")
	}
	n, err := f.Write(p)
	if n > 0 {
		t.mu.Lock()
		t.stats.PacketsOut++
		t.stats.BytesOut += uint64(n)
		t.mu.Unlock()
	}
	return n, err
}

func (t *TunDevice) Stats() TunStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

func (t *TunDevice) AttachEngine(_ *Engine) {}

func (t *TunDevice) Status() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{
		"open": t.file != nil, "adapter": t.name, "mtu": t.cfg.MTU, "ipv4": t.cfg.IPv4,
		"os": "linux", "stack": "kernel TUN", "stats": t.stats,
	}
}

// SetDefaultRoute moves the global default into the NGG routing table. Linux
// uses a dedicated table + fwmark so kernels can never compete for it.
func (t *TunDevice) SetDefaultRoute(enable bool) error {
	t.mu.Lock()
	name := t.name
	t.mu.Unlock()
	if name == "" {
		return fmt.Errorf("TUN not open")
	}
	if enable {
		if err := runIP("route", "replace", "default", "dev", name, "table", "17890"); err != nil {
			return err
		}
		return runIP("rule", "add", "fwmark", "0x4E47", "lookup", "17890")
	}
	_ = runIP("rule", "del", "fwmark", "0x4E47", "lookup", "17890")
	// Flushing an already empty table exits non-zero on some distros; the
	// route is gone either way, so this is deliberately best-effort.
	_ = exec.Command("ip", "route", "flush", "table", "17890").Run()
	return nil
}

func (t *TunDevice) Name() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.name
}

func setLinkMTU(name string, mtu int) error {
	f, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return runIP("link", "set", "dev", name, "mtu", fmt.Sprintf("%d", mtu))
	}
	defer unix.Close(f)
	// IFNAMSIZ(16) + ifr_mtu(int32) — SIOCSIFMTU = 0x8922
	type mtuReq struct {
		Name [16]byte
		MTU  int32
		_    [20]byte
	}
	m := mtuReq{MTU: int32(mtu)}
	copy(m.Name[:], name)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), 0x8922, uintptr(unsafe.Pointer(&m)))
	if errno != 0 {
		return fmt.Errorf("SIOCSIFMTU: %v", errno)
	}
	return nil
}

func runIP(args ...string) error {
	cmd := exec.Command("ip", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip %v: %v: %s", args, err, string(out))
	}
	return nil
}

func nowUTC() (t time.Time) { return time.Now().UTC() }
