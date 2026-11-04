// Package contracts defines the stable data contracts shared by the NGG control
// plane, Core Adapters, the Web UI and the (future) Unified Network Engine.
//
// Nothing in this package may import an internal package. Keeping this layer
// dependency-free is what allows new kernels to be registered through a
// descriptor file without recompiling NGG.
package contracts

import "time"

// CoreID is the registry key of a kernel. It is NOT an enum: new kernels may
// use any ID, which is a hard requirement of the Core Plugin Registry.
type CoreID string

// Well-known IDs. These are conveniences only, never a whitelist.
const (
	CoreMihomo  CoreID = "mihomo"
	CoreXray    CoreID = "xray-core"
	CoreSingBox CoreID = "sing-box"
)

type CoreState string

const (
	StateNotInstalled CoreState = "not_installed"
	StateInstalled    CoreState = "installed"
	StateStarting     CoreState = "starting"
	StateRunning      CoreState = "running"
	StateStopping     CoreState = "stopping"
	StateStopped      CoreState = "stopped"
	StateFailed       CoreState = "failed"
	StateUnknown      CoreState = "unknown"
)

// Platform is a GOOS/GOARCH pair.
type Platform struct {
	OS   string `json:"os" yaml:"os"`
	Arch string `json:"arch" yaml:"arch"`
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// Capability describes what a kernel can execute. This is the protocol
// registration surface required by the architecture revision (section 5).
// Stage 2 fields describe local TCP/UDP access points that the Protocol
// Dispatcher will use; Stage 1 only declares them, it does not bind them.
type Capability struct {
	Protocols    []string `json:"protocols" yaml:"protocols"`
	InboundTypes []string `json:"inbound_types" yaml:"inbound_types"`
	Features     []string `json:"features" yaml:"features"`

	// Stage 2 reserved: local access endpoints used by Core Adapter Bridge.
	LocalTCPEndpoint string `json:"local_tcp_endpoint,omitempty" yaml:"local_tcp_endpoint,omitempty"`
	LocalUDPEndpoint string `json:"local_udp_endpoint,omitempty" yaml:"local_udp_endpoint,omitempty"`
	LocalProxyKind   string `json:"local_proxy_kind,omitempty" yaml:"local_proxy_kind,omitempty"` // socks5 | http-connect | mixed | transparent
	NativeStream     bool   `json:"native_stream" yaml:"native_stream"`
	NativeDatagram   bool   `json:"native_datagram" yaml:"native_datagram"`

	// WorkerMode declares that the kernel must NOT create a system-level TUN,
	// hijack DNS, or modify global routing. NGG owns all of that (Stage 2).
	WorkerMode bool `json:"worker_mode" yaml:"worker_mode"`
}

type CoreDescriptor struct {
	ID          CoreID     `json:"id" yaml:"id"`
	DisplayName string     `json:"display_name" yaml:"display_name"`
	Description string     `json:"description" yaml:"description"`
	Homepage    string     `json:"homepage" yaml:"homepage"`
	Repository  string     `json:"repository" yaml:"repository"`
	Capability  Capability `json:"capability" yaml:"capability"`
	Builtin     bool       `json:"builtin" yaml:"builtin"`
	Source      string     `json:"source" yaml:"source"` // "builtin" | "descriptor"
}

type VersionInfo struct {
	Version   string    `json:"version"`
	Raw       string    `json:"raw,omitempty"`
	Channel   string    `json:"channel"` // "installed" | "available"
	Detected  time.Time `json:"detected_at"`
	AssetURL  string    `json:"asset_url,omitempty"`
	DigestURL string    `json:"digest_url,omitempty"`
}

type PortAllocation struct {
	CoreID CoreID         `json:"core_id"`
	Ports  []int          `json:"ports"`
	Names  map[int]string `json:"names"`
}

type CoreStatus struct {
	ID           CoreID     `json:"id"`
	DisplayName  string     `json:"display_name"`
	State        CoreState  `json:"state"`
	Installed    bool       `json:"installed"`
	Version      string     `json:"version,omitempty"`
	PID          int        `json:"pid,omitempty"`
	BinaryPath   string     `json:"binary_path,omitempty"`
	ConfigPath   string     `json:"config_path,omitempty"`
	Ports        []int      `json:"ports"`
	AutoStart    bool       `json:"auto_start"`
	Message      string     `json:"message,omitempty"`
	Restarts     int        `json:"restarts"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Capabilities Capability `json:"capabilities"`
}

type ProcessStats struct {
	PID        int32     `json:"pid"`
	CPUPercent float64   `json:"cpu_percent"`
	MemoryRSS  uint64    `json:"memory_rss"`
	MemoryVMS  uint64    `json:"memory_vms"`
	Threads    int32     `json:"threads"`
	UptimeSec  int64     `json:"uptime_sec"`
	SampledAt  time.Time `json:"sampled_at"`
}

type LogEntry struct {
	Seq    uint64    `json:"seq"`
	CoreID CoreID    `json:"core_id"`
	Stream string    `json:"stream"` // stdout | stderr | ngg
	Line   string    `json:"line"`
	Time   time.Time `json:"time"`
}

type ConfigBackup struct {
	ID        string    `json:"id"`
	CoreID    CoreID    `json:"core_id"`
	Path      string    `json:"path"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// Event is the WebSocket envelope.
type Event struct {
	Type      string    `json:"type"` // core.state | core.log | core.stats | system.alert
	CoreID    CoreID    `json:"core_id,omitempty"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

type SystemInfo struct {
	NGGVersion string    `json:"ngg_version"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	Platform   string    `json:"platform"`
	GoVersion  string    `json:"go_version"`
	StartedAt  time.Time `json:"started_at"`
	ListenAddr string    `json:"listen_addr"`
	Stage      int       `json:"stage"`
	Mode       string    `json:"mode"`
	DataDir    string    `json:"data_dir"`
}

type OperationResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
