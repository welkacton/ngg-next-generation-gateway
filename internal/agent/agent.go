// Package agent implements the "remote agent node" plugin: a registry of
// machines that report their own real metrics to NGG, plus the public HTTP
// endpoint those machines talk to.
//
// Nothing here is simulated. An agent is only "online" when it has actually
// reported within 3x its interval; metrics are stored exactly as the agent
// sent them and are nil (not zero) before the first report, so the console can
// show "never reported" instead of a fabricated resource graph.
package agent

import (
	"strings"
	"time"
)

// DefaultIntervalSec is the reporting interval NGG asks every agent to use.
const DefaultIntervalSec = 10

// onlineWindowFactor is how many intervals may be missed before an agent is
// considered offline. 3x keeps a single lost packet from flapping the state.
const onlineWindowFactor = 3

// maxHistory is the in-memory sample ring per agent. It is deliberately NOT
// persisted: after a restart the console must start empty rather than show
// samples that no longer match the machine's current state.
const maxHistory = 240

// Roles a remote node can take. The role is a console-side label; the agent
// itself only reports metrics.
const (
	RoleOutbound = "outbound"
	RoleInbound  = "inbound"
	RoleBoth     = "both"
)

// Status values of AgentView.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
	StatusNever   = "never"
)

// ValidRole reports whether r is one of the three documented roles.
func ValidRole(r string) bool {
	return r == RoleOutbound || r == RoleInbound || r == RoleBoth
}

// AgentDisk is one mounted filesystem as reported by the agent.
type AgentDisk struct {
	Mount  string `json:"mount"`
	FSType string `json:"fstype"`
	Total  uint64 `json:"total"`
	Used   uint64 `json:"used"`
}

// AgentInterface is one network interface with its cumulative counters.
type AgentInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	MTU       int      `json:"mtu"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"`
	RxBytes   uint64   `json:"rx_bytes"`
	TxBytes   uint64   `json:"tx_bytes"`
	RxRate    float64  `json:"rx_rate"`
	TxRate    float64  `json:"tx_rate"`
}

// AgentMetrics is the payload an agent reports. Field names are part of the
// v2.7.0 contract and must match web/src/api/ngg.ts exactly.
type AgentMetrics struct {
	CPUPct    float64 `json:"cpu_pct"`
	CPUCores  int     `json:"cpu_cores"`
	Load1     float64 `json:"load1"`
	MemTotal  uint64  `json:"mem_total"`
	MemUsed   uint64  `json:"mem_used"`
	SwapTotal uint64  `json:"swap_total"`
	SwapUsed  uint64  `json:"swap_used"`
	DiskTotal uint64  `json:"disk_total"`
	DiskUsed  uint64  `json:"disk_used"`

	Disks      []AgentDisk      `json:"disks"`
	Interfaces []AgentInterface `json:"interfaces"`

	RxBytes uint64  `json:"rx_bytes"`
	TxBytes uint64  `json:"tx_bytes"`
	RxRate  float64 `json:"rx_rate"`
	TxRate  float64 `json:"tx_rate"`

	UptimeSec uint64 `json:"uptime_sec"`
	SampledAt string `json:"sampled_at"`
	Source    string `json:"source"`
	Note      string `json:"note,omitempty"`
}

// AgentSample is one point of the in-memory history ring.
type AgentSample struct {
	SampledAt string  `json:"sampled_at"`
	CPUPct    float64 `json:"cpu_pct"`
	MemUsed   uint64  `json:"mem_used"`
	DiskUsed  uint64  `json:"disk_used"`
	RxBytes   uint64  `json:"rx_bytes"`
	TxBytes   uint64  `json:"tx_bytes"`
	RxRate    float64 `json:"rx_rate"`
	TxRate    float64 `json:"tx_rate"`
}

// Agent is the persisted record. It is never marshalled to an API client: the
// token hash would leak. Handlers must always use AgentView.
type Agent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Note    string `json:"note"`
	Role    string `json:"role"`

	// TokenSHA256 is the hex SHA-256 of the agent token. The plaintext token
	// is only returned once, at creation / rotation time.
	TokenSHA256 string `json:"token_sha256"`

	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Hostname     string `json:"hostname"`
	AgentVersion string `json:"agent_version"`

	// IntervalSec is the interval the agent last reported with; 0 means it has
	// never reported and DefaultIntervalSec is assumed.
	IntervalSec int `json:"interval_sec"`

	// LastSeen is the zero time when the agent never reported.
	LastSeen time.Time `json:"last_seen"`
	Created  time.Time `json:"created_at"`
	Updated  time.Time `json:"updated_at"`

	Metrics *AgentMetrics `json:"metrics,omitempty"`
}

// Bound reports whether the agent has ever authenticated successfully.
func (a *Agent) Bound() bool { return !a.LastSeen.IsZero() }

func (a *Agent) interval() time.Duration {
	sec := a.IntervalSec
	if sec <= 0 {
		sec = DefaultIntervalSec
	}
	return time.Duration(sec) * time.Second
}

// statusAt derives online/offline/never from the real last-seen timestamp.
func (a *Agent) statusAt(now time.Time) (status string, online bool) {
	if a.LastSeen.IsZero() {
		return StatusNever, false
	}
	if now.Sub(a.LastSeen) <= onlineWindowFactor*a.interval() {
		return StatusOnline, true
	}
	return StatusOffline, false
}

// AgentView is Agent plus derived liveness, safe to hand to the UI.
type AgentView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Note    string `json:"note"`
	Role    string `json:"role"`

	Status string `json:"status"`
	Online bool   `json:"online"`
	Bound  bool   `json:"bound"`

	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Hostname     string `json:"hostname"`
	AgentVersion string `json:"agent_version"`

	LastSeen  string `json:"last_seen"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	// Metrics is nil until the agent reports for the first time. It is never
	// substituted with a zero value.
	Metrics *AgentMetrics `json:"metrics"`
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// view builds the API representation. Callers must hold at least a read lock.
func (a *Agent) view(now time.Time) AgentView {
	status, online := a.statusAt(now)
	return AgentView{
		ID: a.ID, Name: a.Name, Address: a.Address, Port: a.Port, Note: a.Note, Role: a.Role,
		Status: status, Online: online, Bound: a.Bound(),
		OS: a.OS, Arch: a.Arch, Hostname: a.Hostname, AgentVersion: a.AgentVersion,
		LastSeen: fmtTime(a.LastSeen), CreatedAt: fmtTime(a.Created), UpdatedAt: fmtTime(a.Updated),
		Metrics: a.Metrics,
	}
}

// sampleOf flattens a metrics payload into one history point.
func sampleOf(m *AgentMetrics) AgentSample {
	at := strings.TrimSpace(m.SampledAt)
	if at == "" {
		at = fmtTime(time.Now())
	}
	return AgentSample{
		SampledAt: at,
		CPUPct:    m.CPUPct,
		MemUsed:   m.MemUsed,
		DiskUsed:  m.DiskUsed,
		RxBytes:   m.RxBytes,
		TxBytes:   m.TxBytes,
		RxRate:    m.RxRate,
		TxRate:    m.TxRate,
	}
}
