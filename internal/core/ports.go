package core

import (
	"context"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// ---------------------------------------------------------------------------
// Module ports. Per the project spec, every module is defined as an interface
// (with tests) before its implementation. Implementations live in
// internal/engine and internal/store. Core Manager (engine.Engine) is the only
// component allowed to call them; the REST/WS layer never reaches past it.
// ---------------------------------------------------------------------------

type EventBus interface {
	Publish(e contracts.Event)
	Subscribe(fn func(contracts.Event)) (unsubscribe func())
	Subscribers() int
}

type LogSink interface {
	Append(e contracts.LogEntry)
	Since(id contracts.CoreID, afterSeq uint64, limit int) []contracts.LogEntry
	Latest(id contracts.CoreID, limit int) []contracts.LogEntry
	Clear(id contracts.CoreID)
}

type ProcessHandle interface {
	PID() int
	Done() <-chan struct{}
	Err() error
}

type RestartPolicy struct {
	Enabled    bool
	MaxRetries int
	Backoff    time.Duration
}

// StartOptions is a full, self-contained description of one supervised start.
// Keeping it in one struct lets the Supervisor perform independent crash
// recovery without asking Core Manager to rebuild the command.
type StartOptions struct {
	ID        contracts.CoreID
	Binary    string
	Args      []string
	Env       []string
	WorkDir   string
	OnLine    func(stream, line string)
	OnExit    func(err error)
	OnRestart func(attempt int, err error)
	Policy    RestartPolicy
}

// Supervisor owns real OS processes. No fake state: IsRunning is derived from
// the actual OS process, not from a boolean flag.
type Supervisor interface {
	Start(ctx context.Context, o StartOptions) (ProcessHandle, error)
	Stop(ctx context.Context, id contracts.CoreID, graceful time.Duration) error
	IsRunning(id contracts.CoreID) bool
	PID(id contracts.CoreID) int
	SetRestartPolicy(id contracts.CoreID, p RestartPolicy)
	RunningIDs() []contracts.CoreID
	StopAll(graceful time.Duration)
}

// ConfigManager enforces the mandatory transaction:
//
//	backup active config -> write candidate -> native validate -> promote
//
// A failed validation must leave the active config untouched.
type ConfigManager interface {
	Read(id contracts.CoreID) ([]byte, error)
	Path(id contracts.CoreID) string
	Ensure(id contracts.CoreID) error
	Validate(ctx context.Context, id contracts.CoreID, content []byte) (output string, err error)
	Apply(ctx context.Context, id contracts.CoreID, content []byte, reason string) (contracts.ConfigBackup, error)
	Backups(id contracts.CoreID) ([]contracts.ConfigBackup, error)
	Rollback(id contracts.CoreID, backupID string) error
}

type VersionManager interface {
	InstalledVersion(ctx context.Context, id contracts.CoreID) (contracts.VersionInfo, error)
	AvailableVersions(ctx context.Context, id contracts.CoreID) ([]contracts.VersionInfo, error)
	Install(ctx context.Context, id contracts.CoreID, version string, progress func(string)) (contracts.VersionInfo, error)
	Uninstall(ctx context.Context, id contracts.CoreID) error
	Rollback(ctx context.Context, id contracts.CoreID, version string) error
	BinaryPath(id contracts.CoreID) string
	IsInstalled(id contracts.CoreID) bool
}

type HealthMonitor interface {
	Sample(id contracts.CoreID, pid int) (contracts.ProcessStats, error)
}

type PortAllocator interface {
	Allocate(id contracts.CoreID) (RuntimeParams, error)
	Get(id contracts.CoreID) (RuntimeParams, bool)
	Release(id contracts.CoreID)
	Check(ports []int) []int
	Allocations() []contracts.PortAllocation
}

type StateStore interface {
	GetDesired(id contracts.CoreID) (state contracts.CoreState, autoStart bool, ok bool, err error)
	SetDesired(id contracts.CoreID, state contracts.CoreState, autoStart bool, version string) error
	ListDesired() (map[contracts.CoreID]DesiredRecord, error)
	AddBackup(b contracts.ConfigBackup) error
	ListBackups(id contracts.CoreID) ([]contracts.ConfigBackup, error)
	GetBackup(id string) (contracts.ConfigBackup, error)
	AddAudit(action string, id contracts.CoreID, detail string) error
	Audit(limit int) ([]AuditRecord, error)
	Close() error
}

type DesiredRecord struct {
	CoreID    contracts.CoreID    `json:"core_id"`
	State     contracts.CoreState `json:"state"`
	AutoStart bool                `json:"auto_start"`
	Version   string              `json:"version"`
	UpdatedAt time.Time           `json:"updated_at"`
}

type AuditRecord struct {
	ID        int64            `json:"id"`
	Action    string           `json:"action"`
	CoreID    contracts.CoreID `json:"core_id"`
	Detail    string           `json:"detail"`
	CreatedAt time.Time        `json:"created_at"`
}

type ArchDetector interface {
	Detect() contracts.Platform
	SupportedBy(a CoreAdapter) bool
}
