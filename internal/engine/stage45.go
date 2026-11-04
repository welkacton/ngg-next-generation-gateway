package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/internal/nodes"
	"github.com/ngg-project/ngg/internal/plugins"
	"github.com/ngg-project/ngg/internal/protocols"
	"github.com/ngg-project/ngg/internal/rulesets"
	"github.com/ngg-project/ngg/internal/sdwan"
	"github.com/ngg-project/ngg/internal/tenants"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// Stage 4/5 subsystems owned by Core Manager. They are composed into the
// Unified Network Engine through the extension points reserved in Stage 2.

type pluginHookAdapter struct{ m *plugins.Manager }

var _ netengine.PluginHook = (*pluginHookAdapter)(nil)

func (a *pluginHookAdapter) OnSession(ctx context.Context, s *netengine.Session, d *netengine.RouteDecision) error {
	out, err := a.m.RunSessionHooks(ctx, map[string]any{
		"hook": "session", "domain": s.Domain, "network": s.Network,
		"tenant": s.TenantID, "dst": fmt.Sprint(s.Dst), "core_id": string(d.CoreID),
		"direct": d.Direct, "block": d.Block, "rule": d.Reason,
	})
	if err != nil {
		return err
	}
	if v, ok := out["core_id"].(string); ok && v != "" {
		d.CoreID = contracts.CoreID(v)
		d.Direct = false
		d.Reason += " | plugin:" + v
	}
	if v, ok := out["direct"].(bool); ok && v {
		d.Direct, d.CoreID = true, ""
	}
	if v, ok := out["block"].(bool); ok && v {
		d.Block = true
	}
	if tags, ok := out["tags"].([]any); ok {
		for _, t := range tags {
			s.Tags = append(s.Tags, fmt.Sprint(t))
		}
	}
	return nil
}

func (a *pluginHookAdapter) OnDNS(ctx context.Context, name string) error {
	blocked, err := a.m.RunDNSHooks(ctx, name)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("domain %q blocked by plugin DNS policy", name)
	}
	return nil
}

func (a *pluginHookAdapter) Count() int { return a.m.Count() }

func (e *Engine) initStage45() {
	e.protocols = protocols.NewRegistry()
	e.plugins = plugins.NewManager(filepath.Join(e.cfg.Storage.Dir, "plugins"))
	e.sdwan = sdwan.NewManager(sdwan.PolicyLowestLatency, 3*time.Second)
	e.tenants = tenants.NewRegistry()
	e.ruleSets = rulesets.NewRegistry(filepath.Join(e.cfg.Storage.Dir, "rulesets-config.json"))
	if err := e.ruleSets.Load(); err != nil {
		fmt.Printf("NGG: could not load persisted rule sets: %v\n", err)
	}
	e.nodeReg = nodes.NewRegistry(filepath.Join(e.cfg.Storage.Dir, "nodes-config.json"))
	if err := e.nodeReg.Load(); err != nil {
		fmt.Printf("NGG: could not load persisted nodes: %v\n", err)
	}
	if e.network != nil {
		e.network.SetPluginHook(&pluginHookAdapter{m: e.plugins})
		e.network.SetTenantResolver(e.tenants)
		e.network.SetLinkSelector(e.sdwan)
	}
	if n, problems := e.plugins.LoadManifestDir(filepath.Join(e.cfg.Cores.DescriptorDir, "..", "plugins.d")); n > 0 || len(problems) > 0 {
		fmt.Printf("NGG: loaded %d plugin(s), %d problem(s)\n", n, len(problems))
	}
	_ = e.store.AddAudit("stage45.init", "", "protocols/plugins/sdwan/tenants initialized")
}

func (e *Engine) Protocols() *protocols.Registry { return e.protocols }
func (e *Engine) Plugins() *plugins.Manager      { return e.plugins }
func (e *Engine) SDWAN() *sdwan.Manager          { return e.sdwan }
func (e *Engine) Tenants() *tenants.Registry     { return e.tenants }
func (e *Engine) Nodes() *nodes.Registry         { return e.nodeReg }
func (e *Engine) RuleSets() *rulesets.Registry   { return e.ruleSets }

// Stages reports the honest implementation status of every stage.
func (e *Engine) Stages() map[string]any {
	return map[string]any{
		"stage1": map[string]any{"name": "统一管理中心 / 多内核引擎", "status": "complete", "kernels": len(e.Registry())},
		"stage2": map[string]any{"name": "统一 TUN / 会话 / Packet Processor / Dispatcher / Core Bridge", "status": "implemented", "note": "Linux TUN requires CAP_NET_ADMIN; userspace ingress is fully active on all platforms", "active": e.network != nil && e.network.IsRunning()},
		"stage3": map[string]any{"name": "统一规则引擎 / DNS / 出入口策略", "status": "implemented", "rules": len(e.network.Rules().List())},
		"stage4": map[string]any{"name": "VPN 协议中心 / 后量子 / 插件运行时", "status": "implemented", "protocols": len(e.protocols.List("")), "plugins": e.plugins.Count()},
		"stage5": map[string]any{"name": "SD-WAN / 多租户 / 跨平台骨架", "status": "implemented", "tenants": len(e.tenants.List()), "links": len(e.sdwan.Links())},
	}
}
