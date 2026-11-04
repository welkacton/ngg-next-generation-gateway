// Package builtin wires the three Phase-1 kernels into the registry.
// These are registrations, not a whitelist: any descriptor in configs/cores.d
// is registered alongside them through the same CoreAdapter interface.
package builtin

import (
	"github.com/ngg-project/ngg/internal/adapters/mihomo"
	"github.com/ngg-project/ngg/internal/adapters/singbox"
	"github.com/ngg-project/ngg/internal/adapters/xray"
	"github.com/ngg-project/ngg/internal/core"
)

func All() []core.CoreAdapter {
	return []core.CoreAdapter{
		mihomo.New(),
		xray.New(),
		singbox.New(),
	}
}
