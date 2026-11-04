//go:build !linux

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
func (t *TunDevice) Stats() TunStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}
func (t *TunDevice) Name() string { return "" }

func runtimeGOOS() string { return runtime.GOOS }
