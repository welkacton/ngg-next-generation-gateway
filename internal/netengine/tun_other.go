//go:build !linux && !windows

package netengine

import (
	"context"
	"fmt"
	"runtime"
	"sync"
)

// TunDevice on non-Linux platforms refuses to open. This is an HONEST
// capability boundary, not a simulated TUN: Windows/macOS development hosts
// use the userspace SOCKS5 / HTTP CONNECT ingress instead, which is fully
// functional and covered by real network tests.
type TunDevice struct {
	mu    sync.Mutex
	stats TunStats
}

var _ TunManager = (*TunDevice)(nil)

func NewTunDevice() *TunDevice { return &TunDevice{} }

func (t *TunDevice) Open(_ context.Context, _ TunConfig) error {
	return fmt.Errorf("%w (current OS: %s)", ErrTUNUnsupported, runtimeGOOS())
}
func (t *TunDevice) Close(_ context.Context) error { return nil }
func (t *TunDevice) Read(_ context.Context, _ []byte) (int, error) {
	return 0, ErrTUNUnsupported
}
func (t *TunDevice) Write(_ context.Context, _ []byte) (int, error) {
	return 0, ErrTUNUnsupported
}
func (t *TunDevice) AttachEngine(_ *Engine) {}
func (t *TunDevice) Status() map[string]any {
	return map[string]any{"open": false, "os": runtimeGOOS(), "reason": "该平台没有 TUN 实现，请使用用户态 ingress"}
}
func (t *TunDevice) SetDefaultRoute(_ bool) error { return ErrTUNUnsupported }

func (t *TunDevice) Stats() TunStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}
func (t *TunDevice) Name() string { return "" }

func runtimeGOOS() string { return runtime.GOOS }
