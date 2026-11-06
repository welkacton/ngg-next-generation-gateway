// Package engine implements the NGG Core Manager and its modules. Every kernel
// operation in the whole system flows through Engine -> CoreAdapter -> native
// binary. The HTTP layer has no other path to a process or a config file.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/adapters/builtin"
	"github.com/ngg-project/ngg/internal/adapters/generic"
	"github.com/ngg-project/ngg/internal/agent"
	"github.com/ngg-project/ngg/internal/clients"
	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/internal/gateway"
	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/internal/nodes"
	"github.com/ngg-project/ngg/internal/plugins"
	"github.com/ngg-project/ngg/internal/protocols"
	"github.com/ngg-project/ngg/internal/rulesets"
	"github.com/ngg-project/ngg/internal/sdwan"
	"github.com/ngg-project/ngg/internal/store"
	"github.com/ngg-project/ngg/internal/users"
	"github.com/ngg-project/ngg/internal/version"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// NGGVersion is the shipped product release; generation lives in
// internal/version and is exposed through /api/v1/update/status.
const NGGVersion = version.Release

type OpResult struct {
	ID      contracts.CoreID `json:"id"`
	OK      bool             `json:"ok"`
	Message string           `json:"message"`
}

type coreRuntime struct {
	state     contracts.CoreState
	version   string
	message   string
	restarts  int
	autoStart bool
	updatedAt time.Time
}

// Engine is the Core Manager facade exposed to the REST / WebSocket layer.
type Engine struct {
	cfg        *config.Config
	registry   *core.Registry
	store      *store.Store
	bus        *eventBus
	logs       *logSink
	ports      *portAllocator
	supervisor *supervisor
	versions   *versionManager
	configs    *configManager
	network    *netengine.Engine
	protocols  *protocols.Registry
	plugins    *plugins.Manager
	sdwan      *sdwan.Manager
	nodeReg    *nodes.Registry
	hostReg    *hosts.Registry
	userReg    *users.Registry
	ruleSets   *rulesets.Registry
	gateway    *gateway.Store
	agents     *agent.Store
	agentCfg   *agent.ServerStore
	clientReg  *clients.Store
	health     *healthMonitor
	arch       archDetector
	client     *http.Client

	mu        sync.RWMutex
	runtime   map[contracts.CoreID]*coreRuntime
	opsMu     sync.Mutex
	opLocks   map[contracts.CoreID]*sync.Mutex
	startedAt time.Time
	stopMon   chan struct{}
	monOnce   sync.Once
}

func New(cfg *config.Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	for _, d := range []string{cfg.Storage.Dir, filepath.Join(cfg.Storage.Dir, "cores"), cfg.Logging.Dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	// Runtime network config (managed from the Web UI) lives in a dedicated
	// JSON file so the main config — comments, token, formatting — is never
	// rewritten by the control plane.
	if blob, err := os.ReadFile(filepath.Join(cfg.Storage.Dir, "network-config.json")); err == nil {
		var saved netengine.Config
		if err := json.Unmarshal(blob, &saved); err == nil {
			cfg.Network = saved
		}
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	e := &Engine{
		cfg: cfg, store: st,
		registry:  core.NewRegistry(),
		arch:      archDetector{},
		client:    &http.Client{Timeout: 5 * time.Minute},
		runtime:   map[contracts.CoreID]*coreRuntime{},
		opLocks:   map[contracts.CoreID]*sync.Mutex{},
		startedAt: time.Now().UTC(),
		stopMon:   make(chan struct{}),
	}
	for _, a := range builtin.All() {
		if err := e.registry.Register(a); err != nil {
			st.Close()
			return nil, fmt.Errorf("register builtin kernel: %w", err)
		}
	}
	if n, problems := e.registry.LoadDescriptorDir(cfg.Cores.DescriptorDir, generic.NewFromFile); n > 0 || len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "NGG: loaded %d descriptor kernel(s), %d problem(s)\n", n, len(problems))
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "NGG descriptor: %s\n", p)
		}
	}

	e.bus = newEventBus()
	e.logs = newLogSink(cfg.Logging.RingCapacity, func(entry contracts.LogEntry) {
		e.bus.Publish(contracts.Event{Type: "core.log", CoreID: entry.CoreID, Payload: entry})
	})
	secret := sha256.Sum256([]byte(cfg.Server.Token))
	e.ports = newPortAllocator(cfg.Cores.PortBase, cfg.Cores.PortStride, hex.EncodeToString(secret[:])[:24])
	e.supervisor = newSupervisor()
	e.health = newHealthMonitor()
	e.versions = newVersionManager(e.registry, cfg.Storage.Dir, e.arch.Detect(), e.client, e.bus, st)
	e.configs = newConfigManager(e.registry, cfg.Storage.Dir, e.ports, e.versions, st, e.bus)
	netCfg := cfg.Network
	e.network = netengine.NewEngine(&netCfg, e.registry, e.ports, e.bus, e.GetCore)
	e.initStage45()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, d := range e.registry.List() {
		id := d.ID
		if _, err := e.ports.Allocate(id); err != nil {
			fmt.Fprintf(os.Stderr, "NGG: port allocation failed for %s: %v\n", id, err)
		}
		if err := os.MkdirAll(filepath.Join(cfg.Storage.Dir, "cores", string(id)), 0o755); err != nil {
			st.Close()
			return nil, err
		}
		_ = e.configs.Ensure(id)

		rt := &coreRuntime{state: contracts.StateNotInstalled, updatedAt: time.Now().UTC()}
		if e.versions.IsInstalled(id) {
			rt.state = contracts.StateInstalled
			if v, err := e.versions.InstalledVersion(ctx, id); err == nil {
				rt.version = v.Version
			} else {
				rt.message = fmt.Sprintf("installed but version detection failed: %v", err)
			}
		}
		if _, auto, ok, _ := e.store.GetDesired(id); ok {
			rt.autoStart = auto
		}
		e.runtime[id] = rt
		e.opLocks[id] = &sync.Mutex{}
	}
	go e.monitorLoop()
	return e, nil
}

func (e *Engine) lockFor(id contracts.CoreID) *sync.Mutex {
	e.opsMu.Lock()
	defer e.opsMu.Unlock()
	m, ok := e.opLocks[id]
	if !ok {
		m = &sync.Mutex{}
		e.opLocks[id] = m
	}
	return m
}

func (e *Engine) getRuntime(id contracts.CoreID) *coreRuntime {
	e.mu.RLock()
	rt, ok := e.runtime[id]
	e.mu.RUnlock()
	if ok {
		return rt
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	rt = &coreRuntime{state: contracts.StateUnknown, updatedAt: time.Now().UTC()}
	e.runtime[id] = rt
	return rt
}

func (e *Engine) setState(id contracts.CoreID, s contracts.CoreState, msg string) {
	e.mu.Lock()
	rt := e.runtime[id]
	if rt == nil {
		rt = &coreRuntime{}
		e.runtime[id] = rt
	}
	rt.state, rt.message, rt.updatedAt = s, msg, time.Now().UTC()
	st := e.statusOf(id, rt)
	e.mu.Unlock()
	e.bus.Publish(contracts.Event{Type: "core.state", CoreID: id, Payload: st})
}

// statusOf must be called with e.mu held (or from setState which holds it).
func (e *Engine) statusOf(id contracts.CoreID, rt *coreRuntime) contracts.CoreStatus {
	a, err := e.registry.Get(id)
	if err != nil {
		return contracts.CoreStatus{ID: id, State: contracts.StateUnknown, UpdatedAt: time.Now().UTC()}
	}
	d := a.Descriptor()
	running := e.supervisor.IsRunning(id)
	installed := e.versions.IsInstalled(id)
	state := rt.state
	if !installed {
		state = contracts.StateNotInstalled
	} else if running && state != contracts.StateStarting {
		state = contracts.StateRunning
	} else if !running && (state == contracts.StateRunning || state == contracts.StateStarting) {
		state = contracts.StateFailed
	}
	params, _ := e.ports.Get(id)
	return contracts.CoreStatus{
		ID: id, DisplayName: d.DisplayName, State: state, Installed: installed,
		Version: rt.version, PID: e.supervisor.PID(id), BinaryPath: e.versions.BinaryPath(id),
		ConfigPath: e.configs.Path(id), Ports: params.Ports(), AutoStart: rt.autoStart,
		Message: rt.message, Restarts: rt.restarts, UpdatedAt: rt.updatedAt,
		Capabilities: d.Capability,
	}
}

func (e *Engine) SystemInfo() contracts.SystemInfo {
	p := e.arch.Detect()
	listen := fmt.Sprintf("%s:%d", e.cfg.Server.Listen, e.cfg.Server.Port)
	return contracts.SystemInfo{
		NGGVersion: NGGVersion, OS: p.OS, Arch: p.Arch, Platform: p.String(),
		GoVersion: runtime.Version(), StartedAt: e.startedAt, ListenAddr: listen,
		Stage: 1, Mode: "multi-core-parallel", DataDir: e.cfg.Storage.Dir,
	}
}

func (e *Engine) ListCores() []contracts.CoreStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]contracts.CoreStatus, 0, len(e.runtime))
	for _, d := range e.registry.List() {
		rt := e.runtime[d.ID]
		if rt == nil {
			rt = &coreRuntime{state: contracts.StateUnknown}
		}
		out = append(out, e.statusOf(d.ID, rt))
	}
	return out
}

func (e *Engine) GetCore(id contracts.CoreID) (contracts.CoreStatus, error) {
	if !e.registry.Has(id) {
		return contracts.CoreStatus{}, fmt.Errorf("kernel %q is not registered", id)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.statusOf(id, e.getRuntimeLocked(id)), nil
}

func (e *Engine) getRuntimeLocked(id contracts.CoreID) *coreRuntime {
	rt := e.runtime[id]
	if rt == nil {
		rt = &coreRuntime{state: contracts.StateUnknown, updatedAt: time.Now().UTC()}
	}
	return rt
}

func (e *Engine) Registry() []contracts.CoreDescriptor { return e.registry.List() }

// ReloadRegistry picks up newly added capability descriptors. Existing entries
// are protected from replacement so a reload can never break a running kernel.
func (e *Engine) ReloadRegistry() (int, []string) {
	n, problems := e.registry.LoadDescriptorDir(e.cfg.Cores.DescriptorDir, generic.NewFromFile)
	if problems == nil {
		problems = []string{}
	}
	for _, d := range e.registry.List() {
		e.lockFor(d.ID)
		e.mu.Lock()
		if _, ok := e.runtime[d.ID]; !ok {
			e.runtime[d.ID] = &coreRuntime{state: contracts.StateNotInstalled, updatedAt: time.Now().UTC()}
		}
		e.mu.Unlock()
	}
	_ = e.store.AddAudit("registry.reload", "", fmt.Sprintf("loaded=%d problems=%d", n, len(problems)))
	return n, problems
}

func (e *Engine) Ports() []contracts.PortAllocation { return e.ports.Allocations() }
func (e *Engine) PortCheck(ports []int) []int       { return e.ports.Check(ports) }
func (e *Engine) Audit(limit int) ([]core.AuditRecord, error) {
	return e.store.Audit(limit)
}
func (e *Engine) Subscribe(fn func(contracts.Event)) func() { return e.bus.Subscribe(fn) }

func (e *Engine) Start(ctx context.Context, id contracts.CoreID) error {
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	return e.startLocked(ctx, id)
}

func (e *Engine) startLocked(ctx context.Context, id contracts.CoreID) error {
	a, err := e.registry.Get(id)
	if err != nil {
		return err
	}
	if !e.versions.IsInstalled(id) {
		return fmt.Errorf("kernel %q is not installed", id)
	}
	if e.supervisor.IsRunning(id) {
		return fmt.Errorf("kernel %q is already running (pid %d)", id, e.supervisor.PID(id))
	}
	if !e.arch.SupportedBy(a) {
		return fmt.Errorf("kernel %q does not declare support for %s", id, e.arch.Detect().String())
	}
	params, err := e.ports.Allocate(id)
	if err != nil {
		return err
	}
	if conflicts := e.ports.Check(params.Ports()); len(conflicts) > 0 {
		return fmt.Errorf("port conflict detection failed for %q: ports %v are occupied", id, conflicts)
	}
	if err := e.configs.Ensure(id); err != nil {
		return err
	}
	configPath := e.configs.Path(id)
	content, err := e.configs.Read(id)
	if err != nil {
		return err
	}
	// Drift guard: the config may still point at a port that a stale process
	// kept bound. Reconcile with the allocator BEFORE validating, so the kernel
	// never starts into a bind() failure it cannot explain.
	if note, rerr := e.reconcileWorkerPorts(ctx, id, params); rerr != nil {
		e.logLine(id, "ngg", "port reconcile failed: "+rerr.Error())
	} else if note != "" {
		e.logLine(id, "ngg", note)
		if content, err = e.configs.Read(id); err != nil {
			return err
		}
	}
	// Fail-Closed: never start a kernel with a config its own validator rejects.
	if _, err := e.configs.Validate(ctx, id, content); err != nil {
		return fmt.Errorf("refusing to start %q: native config validation failed: %w", id, err)
	}

	// Pre-flight: the binary must actually execute on THIS host. An antivirus
	// quarantine, a wrong architecture or a missing runtime fails here with a
	// clear message instead of an opaque start-then-crash.
	if err := e.preflightBinary(ctx, id, a.VersionArgs()); err != nil {
		e.logLine(id, "ngg", "preflight failed: "+err.Error())
		_ = e.store.AddAudit("core.start", id, "preflight failed: "+err.Error())
		e.setState(id, contracts.StateFailed, truncateText(err.Error(), 300))
		return err
	}

	e.setState(id, contracts.StateStarting, "starting native kernel")
	e.logLine(id, "ngg", fmt.Sprintf("starting: %s %v", e.versions.BinaryPath(id), a.RunArgs(configPath)))

	restartPolicy := core.RestartPolicy{
		Enabled: true, MaxRetries: e.cfg.Supervisor.RestartMax, Backoff: e.cfg.RestartBackoff(),
	}
	// Crash restarts stay OFF until the kernel survives the startup window: a
	// kernel that cannot bind its port must fail ONCE with the real reason
	// instead of looping and stacking notifications.
	handle, err := e.supervisor.Start(ctx, core.StartOptions{
		ID: id, Binary: e.versions.BinaryPath(id), Args: a.RunArgs(configPath),
		Env: a.Environment(configPath), WorkDir: filepath.Dir(configPath),
		Policy: core.RestartPolicy{
			Enabled: false, MaxRetries: restartPolicy.MaxRetries, Backoff: restartPolicy.Backoff,
		},
		OnLine: func(stream, line string) { e.logLine(id, stream, line) },
		OnRestart: func(attempt int, exitErr error) {
			msg := e.failureMessage(id, exitErr, "崩溃退出")
			e.mu.Lock()
			if rt := e.runtime[id]; rt != nil {
				rt.restarts++
				rt.state = contracts.StateStarting
				rt.message = msg
				rt.updatedAt = time.Now().UTC()
			}
			e.mu.Unlock()
			e.logLine(id, "ngg", fmt.Sprintf("crash recovery attempt %d after %v", attempt, exitErr))
			// A crash restart is a lifecycle fact of ONE kernel, not a global
			// system alert: publishing it as core.state keeps the UI from
			// stacking modal "系统告警" popups while still carrying the reason.
			e.bus.Publish(contracts.Event{Type: "core.state", CoreID: id, Payload: map[string]any{
				"id": id, "state": string(contracts.StateFailed),
				"reason": msg, "attempt": attempt, "event": "core.crash_restart",
			}})
			e.publishState(id)
		},
		OnExit: func(exitErr error) {
			e.mu.Lock()
			rt := e.runtime[id]
			stopping := rt != nil && rt.state == contracts.StateStopping
			if rt != nil {
				if stopping {
					rt.state, rt.message = contracts.StateStopped, "stopped by NGG"
				} else {
					rt.state = contracts.StateFailed
					rt.message = e.failureMessage(id, exitErr, "进程异常退出")
				}
				rt.updatedAt = time.Now().UTC()
			}
			e.mu.Unlock()
			_ = e.store.AddAudit("core.exit", id, fmt.Sprintf("%v", exitErr))
			e.publishState(id)
		},
	})
	if err != nil {
		e.setState(id, contracts.StateFailed, err.Error())
		return err
	}

	// Startup window: a kernel that dies within 3s is reported as a failed
	// start carrying its real stderr, never as {"ok":true,"message":"started"}.
	exited := false
	select {
	case <-handle.Done():
		exited = true
	case <-time.After(kernelStartupGrace):
		exited = !e.supervisor.IsRunning(id)
	}
	if exited {
		full := e.startupFailureMessage(id, handle.Err())
		e.mu.Lock()
		if rt := e.runtime[id]; rt != nil {
			rt.state = contracts.StateFailed
			rt.message = truncateText(full, 300)
			rt.updatedAt = time.Now().UTC()
		}
		e.mu.Unlock()
		e.logLine(id, "ngg", "start failed: "+full)
		_ = e.store.AddAudit("core.start", id, "failed after launch: "+full)
		e.publishState(id)
		return fmt.Errorf("%s", full)
	}
	e.supervisor.SetRestartPolicy(id, restartPolicy)

	version := ""
	if info, err := e.versions.InstalledVersion(ctx, id); err == nil {
		version = info.Version
	}
	e.mu.Lock()
	rt := e.runtime[id]
	if rt == nil {
		rt = &coreRuntime{}
		e.runtime[id] = rt
	}
	rt.version, rt.autoStart, rt.updatedAt = version, true, time.Now().UTC()
	e.mu.Unlock()
	e.health.MarkStarted(id)
	if e.supervisor.RestartCount(id) > 0 {
		// The kernel already crashed during the startup handshake and the
		// supervisor is restarting it. Reporting "running" here would erase the
		// real crash reason the UI must show; publish the captured state instead.
		e.publishState(id)
		_ = e.store.AddAudit("core.start", id, fmt.Sprintf("exited during startup, independent restart #%d", e.supervisor.RestartCount(id)))
		return nil
	}
	e.setState(id, contracts.StateRunning, "")
	_ = e.store.SetDesired(id, contracts.StateRunning, true, version)
	_ = e.store.AddAudit("core.start", id, fmt.Sprintf("pid=%d ports=%v version=%s", e.supervisor.PID(id), params.Ports(), version))
	return nil
}

func (e *Engine) Stop(ctx context.Context, id contracts.CoreID) error {
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	return e.stopLocked(ctx, id)
}

func (e *Engine) stopLocked(ctx context.Context, id contracts.CoreID) error {
	if !e.registry.Has(id) {
		return fmt.Errorf("kernel %q is not registered", id)
	}
	if !e.supervisor.IsRunning(id) {
		e.setState(id, contracts.StateStopped, "not running")
		return nil
	}
	e.setState(id, contracts.StateStopping, "stopping")
	if err := e.supervisor.Stop(ctx, id, e.cfg.StopTimeout()); err != nil {
		e.setState(id, contracts.StateFailed, err.Error())
		return err
	}
	e.setState(id, contracts.StateStopped, "stopped by NGG")
	e.mu.Lock()
	if rt := e.runtime[id]; rt != nil {
		rt.autoStart = false
	}
	e.mu.Unlock()
	_ = e.store.SetDesired(id, contracts.StateStopped, false, "")
	_ = e.store.AddAudit("core.stop", id, "stopped")
	return nil
}

func (e *Engine) Restart(ctx context.Context, id contracts.CoreID) error {
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	if e.supervisor.IsRunning(id) {
		if err := e.stopLocked(ctx, id); err != nil {
			return err
		}
	}
	return e.startLocked(ctx, id)
}

// BatchStart starts several kernels concurrently. exclusive=true implements
// the UI's "single core mode": all other running kernels are stopped first.
// The default (false) is the architecture-mandated multi-core parallel mode.
func (e *Engine) BatchStart(ctx context.Context, ids []contracts.CoreID, exclusive bool) []OpResult {
	if exclusive {
		keep := map[contracts.CoreID]bool{}
		for _, id := range ids {
			keep[id] = true
		}
		for _, running := range e.supervisor.RunningIDs() {
			if !keep[running] {
				if err := e.Stop(ctx, running); err != nil {
					e.logLine(running, "ngg", "exclusive mode stop failed: "+err.Error())
				}
			}
		}
	}
	// Starting an already-running kernel is an idempotent no-op at the
	// batch level (needed for exclusive mode, where the selected kernel may
	// already be the only one running). Single Start() stays strict.
	return e.parallel(ctx, ids, func(c context.Context, id contracts.CoreID) error {
		if e.supervisor.IsRunning(id) {
			return nil
		}
		return e.Start(c, id)
	})
}

func (e *Engine) BatchStop(ctx context.Context, ids []contracts.CoreID) []OpResult {
	return e.parallel(ctx, ids, func(c context.Context, id contracts.CoreID) error { return e.Stop(c, id) })
}

func (e *Engine) parallel(ctx context.Context, ids []contracts.CoreID, fn func(context.Context, contracts.CoreID) error) []OpResult {
	results := make([]OpResult, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id contracts.CoreID) {
			defer wg.Done()
			err := fn(ctx, id)
			results[i] = OpResult{ID: id, OK: err == nil, Message: "ok"}
			if err != nil {
				results[i].Message = err.Error()
			}
		}(i, id)
	}
	wg.Wait()
	return results
}

func (e *Engine) Install(ctx context.Context, id contracts.CoreID, version string) (contracts.VersionInfo, error) {
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	if !e.registry.Has(id) {
		return contracts.VersionInfo{}, fmt.Errorf("kernel %q is not registered", id)
	}
	wasRunning := e.supervisor.IsRunning(id)
	if wasRunning {
		e.logLine(id, "ngg", "stopping kernel before replacing its binary")
		if err := e.stopLocked(ctx, id); err != nil {
			return contracts.VersionInfo{}, err
		}
	}
	e.setState(id, contracts.StateStarting, "installing")
	info, err := e.versions.Install(ctx, id, version, func(msg string) { e.logLine(id, "ngg", "[install] "+msg) })
	if err != nil {
		e.setState(id, contracts.StateFailed, err.Error())
		return contracts.VersionInfo{}, err
	}
	e.mu.Lock()
	if rt := e.runtime[id]; rt != nil {
		rt.version = info.Version
		rt.message = ""
	}
	e.mu.Unlock()
	_ = e.store.AddAudit("core.install", id, fmt.Sprintf("version=%s", info.Version))
	e.setState(id, contracts.StateInstalled, "installed "+info.Version)
	if wasRunning {
		if err := e.startLocked(ctx, id); err != nil {
			return info, fmt.Errorf("installed %s but failed to restart: %w", info.Version, err)
		}
	}
	return info, nil
}

func (e *Engine) Uninstall(ctx context.Context, id contracts.CoreID) error {
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	if e.supervisor.IsRunning(id) {
		if err := e.stopLocked(ctx, id); err != nil {
			return err
		}
	}
	if err := e.versions.Uninstall(ctx, id); err != nil {
		return err
	}
	e.mu.Lock()
	if rt := e.runtime[id]; rt != nil {
		rt.version, rt.message, rt.autoStart = "", "", false
	}
	e.mu.Unlock()
	_ = e.store.SetDesired(id, contracts.StateNotInstalled, false, "")
	_ = e.store.AddAudit("core.uninstall", id, "binary removed, config and backups retained")
	e.setState(id, contracts.StateNotInstalled, "uninstalled")
	return nil
}

func (e *Engine) RollbackVersion(ctx context.Context, id contracts.CoreID, version string) error {
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	wasRunning := e.supervisor.IsRunning(id)
	if wasRunning {
		if err := e.stopLocked(ctx, id); err != nil {
			return err
		}
	}
	if err := e.versions.Rollback(ctx, id, version); err != nil {
		return err
	}
	info, err := e.versions.InstalledVersion(ctx, id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if rt := e.runtime[id]; rt != nil {
		rt.version = info.Version
	}
	e.mu.Unlock()
	_ = e.store.AddAudit("core.rollback", id, fmt.Sprintf("restored %s", info.Version))
	e.setState(id, contracts.StateInstalled, "rolled back to "+info.Version)
	if wasRunning {
		return e.startLocked(ctx, id)
	}
	return nil
}

func (e *Engine) AvailableVersions(ctx context.Context, id contracts.CoreID) ([]contracts.VersionInfo, error) {
	return e.versions.AvailableVersions(ctx, id)
}
func (e *Engine) InstalledVersion(ctx context.Context, id contracts.CoreID) (contracts.VersionInfo, error) {
	return e.versions.InstalledVersion(ctx, id)
}
func (e *Engine) StoredVersions(id contracts.CoreID) []string { return e.versions.storedVersions(id) }

func (e *Engine) Config(id contracts.CoreID) (map[string]any, error) {
	b, err := e.configs.Read(id)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(e.configs.Path(id))
	var mod time.Time
	if err == nil {
		mod = st.ModTime()
	}
	return map[string]any{
		"content": string(b), "path": e.configs.Path(id),
		"sha256": sha256Hex(b), "size": len(b), "updated_at": mod,
	}, nil
}

func (e *Engine) ValidateConfig(ctx context.Context, id contracts.CoreID, content []byte) (string, error) {
	return e.configs.Validate(ctx, id, content)
}
func (e *Engine) ApplyConfig(ctx context.Context, id contracts.CoreID, content []byte, reason string) (contracts.ConfigBackup, error) {
	return e.configs.Apply(ctx, id, content, reason)
}
func (e *Engine) ConfigBackups(id contracts.CoreID) ([]contracts.ConfigBackup, error) {
	return e.configs.Backups(id)
}
func (e *Engine) AllBackups() ([]contracts.ConfigBackup, error) { return e.store.ListBackups("") }
func (e *Engine) RollbackConfig(id contracts.CoreID, backupID string) error {
	return e.configs.Rollback(id, backupID)
}
func (e *Engine) ConfigPath(id contracts.CoreID) string { return e.configs.Path(id) }

func (e *Engine) Logs(id contracts.CoreID, after uint64, limit int) []contracts.LogEntry {
	return e.logs.Since(id, after, limit)
}
func (e *Engine) LatestLogs(id contracts.CoreID, limit int) []contracts.LogEntry {
	return e.logs.Latest(id, limit)
}
func (e *Engine) ClearLogs(id contracts.CoreID) { e.logs.Clear(id) }
func (e *Engine) Stats(id contracts.CoreID) (contracts.ProcessStats, error) {
	return e.health.Sample(id, e.supervisor.PID(id))
}

// Restore restarts every kernel whose last desired state was running. A failure
// on one kernel never prevents the others from being restored.
func (e *Engine) Restore(ctx context.Context) []OpResult {
	desired, err := e.store.ListDesired()
	if err != nil {
		e.bus.Publish(contracts.Event{Type: "system.alert", Payload: map[string]any{"message": "restore state read failed: " + err.Error()}})
		return nil
	}
	var ids []contracts.CoreID
	for id, rec := range desired {
		if (rec.State == contracts.StateRunning || rec.AutoStart) && e.registry.Has(id) && e.versions.IsInstalled(id) {
			ids = append(ids, id)
			e.mu.Lock()
			if rt := e.runtime[id]; rt != nil {
				rt.autoStart = true
			}
			e.mu.Unlock()
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return []OpResult{}
	}
	fmt.Fprintf(os.Stderr, "NGG: restoring %d kernel(s) from persisted desired state: %v\n", len(ids), ids)
	results := e.BatchStart(ctx, ids, false)
	_ = e.store.AddAudit("service.restore", "", fmt.Sprintf("restored=%v", ids))
	return results
}

func (e *Engine) Shutdown(ctx context.Context) error {
	e.monOnce.Do(func() { close(e.stopMon) })
	if e.network != nil {
		_ = e.network.Stop(ctx)
	}
	e.supervisor.StopAll(e.cfg.StopTimeout())
	return e.store.Close()
}

func (e *Engine) logLine(id contracts.CoreID, stream, line string) {
	e.logs.Append(contracts.LogEntry{CoreID: id, Stream: stream, Line: line})
}

// ---------------------------------------------------------------------------
// Crash explanation + read-only accessors used by the diagnostics endpoint.
// Every value below comes from the real OS / the real installed binary.
// ---------------------------------------------------------------------------

// portConflictSignals are the real error strings kernels emit when they cannot
// bind an allocated port. Matched case-insensitively against recent stderr.
var portConflictSignals = []string{
	"address already in use",
	"only one usage of each socket address",
	"address in use",
	"listen tcp",
	"listen udp",
	"bind: ",
}

// kernelStartupGrace is how long Start() watches a freshly launched kernel
// before declaring the start successful.
const kernelStartupGrace = 3 * time.Second

// preflightBinary executes the kernel's own version command before any service
// start. It catches the failures that make a binary un-runnable on this host
// (antivirus quarantine, wrong architecture, missing runtime, removed file)
// while the error can still name the cause.
func (e *Engine) preflightBinary(ctx context.Context, id contracts.CoreID, args []string) error {
	bin := e.versions.BinaryPath(id)
	if bin == "" {
		return fmt.Errorf("二进制无法执行：未找到内核 %q 的可执行文件路径", id)
	}
	if len(args) == 0 {
		// A descriptor without a version command must not be executed bare: for
		// a proxy binary "no arguments" means "run the server", which would hang
		// this check. The startup window still catches a broken launch.
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = filepath.Dir(bin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("二进制无法执行：3 秒内没有任何响应（可能被杀毒软件拦截）：%s", bin)
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = "没有任何输出（可能被杀毒软件隔离或缺少运行库）"
	}
	return fmt.Errorf("二进制无法执行：%v（%s）", err, truncateText(detail, 200))
}

// startupFailureMessage describes a kernel that exited inside the startup
// window, quoting the last stderr lines the kernel actually printed.
func (e *Engine) startupFailureMessage(id contracts.CoreID, exitErr error) string {
	reason := "无错误码"
	if exitErr != nil && strings.TrimSpace(exitErr.Error()) != "" {
		reason = strings.TrimSpace(exitErr.Error())
	}
	tail := e.supervisor.RecentStderr(id, 5)
	if len(tail) == 0 {
		if evidence := e.recentEvidence(id); len(evidence) > 0 {
			tail = evidence
			if len(tail) > 5 {
				tail = tail[len(tail)-5:]
			}
		}
	}
	msg := fmt.Sprintf("%s 启动后立即退出（%s）", id, reason)
	if advice := portConflictAdvice(tail, e.occupiedPorts(id)); advice != "" {
		msg += " · " + advice
	}
	if len(tail) == 0 {
		return truncateText(msg+"：内核没有任何输出", 900)
	}
	lines := make([]string, 0, len(tail))
	for _, line := range tail {
		lines = append(lines, truncateText(strings.TrimSpace(line), 160))
	}
	return truncateText(msg+"："+strings.Join(lines, " | "), 900)
}

// failureMessage turns a raw process exit into ONE readable line: exit status,
// the actionable cause when stderr shows a port conflict, and the last output
// line as evidence. Truncated to 300 characters because the UI renders it in
// the status column and in a single notification.
func (e *Engine) failureMessage(id contracts.CoreID, exitErr error, verb string) string {
	exitText := "进程已退出（无错误信息）"
	if exitErr != nil {
		exitText = strings.TrimSpace(exitErr.Error())
		if exitText == "" {
			exitText = "进程已退出（无错误信息）"
		}
	}
	parts := []string{verb + "：" + exitText}
	tail := e.recentEvidence(id)
	if advice := portConflictAdvice(tail, e.occupiedPorts(id)); advice != "" {
		parts = append(parts, advice)
	}
	if line := lastOutputLine(tail); line != "" {
		parts = append(parts, "最近输出："+line)
	}
	return truncateText(strings.Join(parts, " · "), 300)
}

// recentEvidence prefers the kernel's stderr ring (real error channel) and
// falls back to the log ring sink so a kernel that reports through stdout still
// yields the decisive line.
func (e *Engine) recentEvidence(id contracts.CoreID) []string {
	if tail := e.supervisor.RecentStderr(id, stderrTailLimit); len(tail) > 0 {
		return tail
	}
	entries := e.logs.Latest(id, 20)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Stream == "ngg" {
			continue
		}
		out = append(out, entry.Line)
	}
	return out
}

// occupiedPorts reports which of the kernel's allocated ports are bound right
// now by any OS listener.
func (e *Engine) occupiedPorts(id contracts.CoreID) []int {
	params, ok := e.ports.Get(id)
	if !ok {
		return nil
	}
	return e.ports.Check(params.Ports())
}

func portConflictAdvice(tail []string, occupied []int) string {
	ports := append([]int(nil), occupied...)
	seen := false
	for _, line := range tail {
		if !isPortConflictLine(line) {
			continue
		}
		seen = true
		// The kernel names the port it could not bind, which may not be one of
		// the currently allocated ports (e.g. a stale generated config).
		if p := portFromLine(line); p > 0 && !containsInt(ports, p) {
			ports = append(ports, p)
		}
	}
	if !seen {
		return ""
	}
	if len(ports) > 0 {
		shown := ports
		suffix := ""
		if len(shown) > 3 {
			shown, suffix = shown[:3], " 等"
		}
		numbers := make([]string, 0, len(shown))
		for _, p := range shown {
			numbers = append(numbers, fmt.Sprint(p))
		}
		return fmt.Sprintf("端口 %s%s 被占用，请停止占用该端口的进程，或修改内核端口后重启",
			strings.Join(numbers, "、"), suffix)
	}
	return "端口监听失败（疑似被其他进程占用），请停止占用端口的进程，或修改内核端口后重启"
}

func isPortConflictLine(line string) bool {
	lower := strings.ToLower(line)
	for _, s := range portConflictSignals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// portFromLine extracts the last ":<port>" occurrence of an error line.
func portFromLine(line string) int {
	best := 0
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		j := i + 1
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j == i+1 || j-(i+1) > 5 {
			continue
		}
		if n, err := strconv.Atoi(line[i+1 : j]); err == nil && n > 0 && n <= 65535 {
			best = n
		}
	}
	return best
}

func containsInt(values []int, want int) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// lastOutputLine picks the most recent non-empty stderr line.
func lastOutputLine(tail []string) string {
	for i := len(tail) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(tail[i]); line != "" {
			return truncateText(line, 180)
		}
	}
	return ""
}

// truncateText is rune-safe: messages are Chinese, byte slicing would emit
// broken UTF-8 into the WebSocket / JSON payloads.
func truncateText(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// BinaryPath is the active binary file of a kernel.
func (e *Engine) BinaryPath(id contracts.CoreID) string { return e.versions.BinaryPath(id) }

// VersionArgs is the adapter's native version invocation (e.g. ["version"]).
func (e *Engine) VersionArgs(id contracts.CoreID) []string {
	a, err := e.registry.Get(id)
	if err != nil {
		return nil
	}
	return a.VersionArgs()
}

// PortParams returns the isolated port block allocated to a kernel.
func (e *Engine) PortParams(id contracts.CoreID) (core.RuntimeParams, bool) { return e.ports.Get(id) }

// IsRunning reports whether the supervisor holds a live OS process.
func (e *Engine) IsRunning(id contracts.CoreID) bool { return e.supervisor.IsRunning(id) }

// CoreLogTail returns the most recent n log lines (oldest first) of a kernel.
func (e *Engine) CoreLogTail(id contracts.CoreID, n int) []string {
	entries := e.logs.Latest(id, n)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		line := entry.Line
		if entry.Stream != "" && entry.Stream != "ngg" {
			line = "[" + entry.Stream + "] " + line
		}
		if !entry.Time.IsZero() {
			line = entry.Time.Local().Format("15:04:05") + " " + line
		}
		out = append(out, line)
	}
	return out
}

// StderrTail returns up to n recent stderr lines of the kernel's process.
func (e *Engine) StderrTail(id contracts.CoreID, n int) []string {
	return e.supervisor.RecentStderr(id, n)
}

func (e *Engine) publishState(id contracts.CoreID) {
	e.mu.RLock()
	st := e.statusOf(id, e.getRuntimeLocked(id))
	e.mu.RUnlock()
	e.bus.Publish(contracts.Event{Type: "core.state", CoreID: id, Payload: st})
}

func (e *Engine) monitorLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-e.stopMon:
			return
		case <-t.C:
			for _, id := range e.supervisor.RunningIDs() {
				if st, err := e.health.Sample(id, e.supervisor.PID(id)); err == nil {
					e.bus.Publish(contracts.Event{Type: "core.stats", CoreID: id, Payload: st})
				}
			}
		}
	}
}

// HealthSummary is used by GET /health.
func (e *Engine) HealthSummary() map[string]any {
	running := e.supervisor.RunningIDs()
	if running == nil {
		running = []contracts.CoreID{}
	}
	return map[string]any{
		"status": "ok", "cores_running": len(running), "running": running,
		"uptime_sec":  int64(time.Since(e.startedAt).Seconds()),
		"registered":  e.registry.Count(),
		"subscribers": e.bus.Subscribers(),
		"stage":       1,
		"unified_tun": "reserved-stage-2",
	}
}

func (e *Engine) Adapter(id contracts.CoreID) (core.CoreAdapter, error) { return e.registry.Get(id) }

func (e *Engine) StringPlatform() string {
	return strings.ReplaceAll(e.arch.Detect().String(), "/", "-")
}

// ---------------------------------------------------------------------------
// Stage 2-5: Unified Network Engine facade. Every network operation is exposed
// through Core Manager so the REST layer has a single control point.
// ---------------------------------------------------------------------------

func (e *Engine) Network() *netengine.Engine { return e.network }

func (e *Engine) NetworkStatus() map[string]any {
	if e.network == nil {
		return map[string]any{"running": false, "stage": 1}
	}
	return e.network.Status()
}

func (e *Engine) NetworkStart(ctx context.Context) error {
	if e.network == nil {
		return fmt.Errorf("network engine not initialized")
	}
	return e.network.Start(ctx)
}

func (e *Engine) NetworkStop(ctx context.Context) error {
	if e.network == nil {
		return nil
	}
	return e.network.Stop(ctx)
}
