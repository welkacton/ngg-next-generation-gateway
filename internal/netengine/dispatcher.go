package netengine

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// ProtocolDispatcher decides, PER CONNECTION, which kernel executes it.
// All registered kernels may run simultaneously; a failure in one never stops
// another. Fail-Closed is the default when no healthy execution path exists.
type protocolDispatcher struct {
	cfg       *Config
	registry  *core.Registry
	bridge    *CoreBridge
	rules     *ruleEngine
	resources *ResourceManager
	egress    *egressManager
	metrics   *Metrics
}

var _ ProtocolDispatcher = (*protocolDispatcher)(nil)

func newProtocolDispatcher(cfg *Config, reg *core.Registry, bridge *CoreBridge, rules *ruleEngine, res *ResourceManager, egress *egressManager, m *Metrics) *protocolDispatcher {
	return &protocolDispatcher{cfg: cfg, registry: reg, bridge: bridge, rules: rules, resources: res, egress: egress, metrics: m}
}

// Decide implements the per-connection routing decision.
func (d *protocolDispatcher) Decide(ctx context.Context, s Session) (RouteDecision, error) {
	decision, err := d.rules.Match(ctx, s)
	if err != nil {
		return RouteDecision{}, err
	}
	if d.cfg.DefaultCore != "" && decision.CoreID == "" && !decision.Direct && !decision.Block {
		decision.CoreID = contracts.CoreID(d.cfg.DefaultCore)
	}
	return decision, nil
}

// Dispatch satisfies the reserved Stage-2 interface. The returned adapter is
// a real StreamAdapter bound to the chosen kernel's Core Bridge.
func (d *protocolDispatcher) Dispatch(ctx context.Context, s Session) (StreamAdapter, error) {
	decision, err := d.Decide(ctx, s)
	if err != nil {
		return nil, err
	}
	if decision.Block || (decision.Direct && decision.CoreID == "") {
		return &StreamAdapterImpl{coreID: "", bridge: d.bridge}, nil
	}
	return &StreamAdapterImpl{coreID: decision.CoreID, bridge: d.bridge}, nil
}

// Open establishes the real data path for one session.
//
//	DIRECT  -> NGG dials the destination with the owned egress dialer
//	PROXY   -> Core Bridge dials the kernel's local SOCKS5/HTTP endpoint
//	CHAIN   -> nested SOCKS5 handshakes kernel A -> kernel B -> ... -> dst
func (d *protocolDispatcher) Open(ctx context.Context, s Session, dst string) (net.Conn, RouteDecision, error) {
	decision, err := d.Decide(ctx, s)
	if err != nil {
		return nil, decision, err
	}
	return d.OpenWithDecision(ctx, s, decision, dst)
}

// OpenWithDecision is used when a plugin has already adjusted the decision.
func (d *protocolDispatcher) OpenWithDecision(ctx context.Context, s Session, decision RouteDecision, dst string) (net.Conn, RouteDecision, error) {
	var err error
	if decision.Block {
		if d.metrics != nil {
			d.metrics.Dropped.Add(1)
		}
		return nil, decision, fmt.Errorf("%w: rule rejected the connection", ErrFailClosed)
	}
	if d.resources != nil {
		if addr, err := net.ResolveTCPAddr("tcp", dst); err == nil {
			if err := d.resources.AssertNoLoop(decision.CoreID, addr); err != nil {
				if d.metrics != nil {
					d.metrics.Dropped.Add(1)
				}
				return nil, decision, err
			}
		}
	}
	if decision.Direct {
		conn, err := d.openDirect(ctx, decision, dst)
		if err != nil {
			return nil, decision, err
		}
		if d.metrics != nil {
			d.metrics.Direct.Add(1)
		}
		return conn, decision, nil
	}

	candidates := d.candidates(decision)
	if len(candidates) == 0 {
		if d.metrics != nil {
			d.metrics.Dropped.Add(1)
		}
		return nil, decision, fmt.Errorf("%w: no kernel candidate available", ErrFailClosed)
	}
	var lastErr error
	for i, id := range candidates {
		var conn net.Conn
		conn, err = d.openViaKernel(ctx, id, decision, dst)
		if err == nil {
			if i > 0 && d.metrics != nil {
				d.metrics.Failovers.Add(1)
			}
			if d.metrics != nil {
				d.metrics.Proxied.Add(1)
			}
			decision.CoreID = id
			return conn, decision, nil
		}
		lastErr = err
		if !d.cfg.Failover {
			break
		}
	}
	if d.metrics != nil {
		d.metrics.KernelErrors.Add(1)
		d.metrics.Dropped.Add(1)
	}
	if d.cfg.DirectOnFailure && !d.cfg.FailClosed {
		// Explicit, opt-in leak path. Default is Fail-Closed (no DIRECT leak).
		if conn, derr := d.openDirect(ctx, decision, dst); derr == nil {
			if d.metrics != nil {
				d.metrics.Direct.Add(1)
			}
			return conn, decision, nil
		}
	}
	if lastErr == nil {
		lastErr = ErrKernelDown
	}
	return nil, decision, fmt.Errorf("%w: %v", ErrFailClosed, lastErr)
}

func (d *protocolDispatcher) candidates(decision RouteDecision) []contracts.CoreID {
	if len(decision.Chain) > 0 {
		return append([]contracts.CoreID(nil), decision.Chain...)
	}
	var out []contracts.CoreID
	if decision.CoreID != "" {
		out = append(out, decision.CoreID)
	}
	if !d.cfg.Failover {
		return out
	}
	// Failover order is deterministic: registered descriptor order.
	for _, desc := range d.registry.List() {
		if desc.ID == decision.CoreID {
			continue
		}
		out = append(out, desc.ID)
	}
	return out
}

func (d *protocolDispatcher) openViaKernel(ctx context.Context, id contracts.CoreID, decision RouteDecision, dst string) (net.Conn, error) {
	if len(decision.Chain) > 1 {
		return d.openChain(ctx, decision.Chain, dst)
	}
	return d.bridge.Connect(ctx, id, "tcp", dst)
}

// openChain builds a REAL multi-kernel proxy chain: each hop is a SOCKS5
// CONNECT to the next hop's local endpoint, performed over the previous hop's
// already-established tunnel.
func (d *protocolDispatcher) openChain(ctx context.Context, chain []contracts.CoreID, dst string) (net.Conn, error) {
	if len(chain) < 2 {
		return nil, fmt.Errorf("chain needs at least two kernels")
	}
	var conn net.Conn
	for i, id := range chain {
		ep, err := d.bridge.endpoint(id)
		if err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, err
		}
		if i == 0 {
			dialer := &net.Dialer{Timeout: 8 * time.Second}
			conn, err = dialer.DialContext(ctx, "tcp", ep.Address)
			if err != nil {
				return nil, fmt.Errorf("chain hop %s unreachable: %w", id, err)
			}
		}
		next := dst
		if i+1 < len(chain) {
			nextEp, e := d.bridge.endpoint(chain[i+1])
			if e != nil {
				_ = conn.Close()
				return nil, e
			}
			next = nextEp.Address
		}
		_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
		if err := handshakeSOCKS5(conn, next, "", ""); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("chain hop %s handshake: %w", id, err)
		}
		_ = conn.SetDeadline(time.Time{})
	}
	return conn, nil
}

func (d *protocolDispatcher) openDirect(ctx context.Context, decision RouteDecision, dst string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if d.egress != nil {
		id := decision.CoreID
		if id == "" {
			id = contracts.CoreID("ngg-direct")
		}
		dialer = d.egress.Dialer(id)
	}
	return dialer.DialContext(ctx, "tcp", dst)
}

// HealthyKernels is used by API/UI; it performs real endpoint dials.
func (d *protocolDispatcher) HealthyKernels() []contracts.CoreID {
	var out []contracts.CoreID
	for _, desc := range d.registry.List() {
		ep, err := d.bridge.endpoint(desc.ID)
		if err == nil && core.IsHealthyEndpoint(ep) {
			out = append(out, desc.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
