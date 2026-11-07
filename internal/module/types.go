// Package module is the NEW aggregation root of NGG's module-driven design.
//
// A Module is one complete, downloadable network capability:
//
//	nodes + kernels + protocol groups + rules/rule sets + DNS + policy groups
//	+ the NGG master kernel that regulates all of the above
//
// A user binds ONE module, gets ONE subscription token, and the client
// receives the whole bundle. Sub-domains (hosts, cores, profiles, rulesets,
// users, subscription) keep their existing implementations unchanged; a module
// only stores value objects and ID references to them.
package module

import "time"

// Kernel identifiers available in 内核管理. Exactly three, by design.
const (
	KernelMihomo  = "mihomo"
	KernelXray    = "xray-core"
	KernelSingbox = "sing-box"
)

// KernelIDs is the canonical order used by the console and by validation.
var KernelIDs = []string{KernelMihomo, KernelXray, KernelSingbox}

// StartMode describes how 内核管理 starts the three kernels.
const (
	StartModeSingle   = "single"   // exactly one kernel
	StartModeMultiple = "multiple" // an explicit subset
	StartModeAll      = "all"      // all three at once
)

// DNS encryption modes for 规划 5.
const (
	DNSEncryptNone = "none"
	DNSEncryptDoH  = "doh"
	DNSEncryptDoT  = "dot"
	DNSEncryptDoQ  = "doq"
)

// MasterConfig is 规划 10: the NGG master kernel as the total controller.
type MasterConfig struct {
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"` // auto | manual
	AutoRegulate    bool   `json:"auto_regulate"`
	PollIntervalSec int    `json:"poll_interval_sec"`
	EgressNodeID    string `json:"egress_node_id,omitempty"`
}

// NodeProbe is the host information read back after connecting to a node.
// Zero values mean "not collected", never "collected as zero".
type NodeProbe struct {
	Online      bool      `json:"online"`
	Hostname    string    `json:"hostname,omitempty"`
	OS          string    `json:"os,omitempty"`
	Arch        string    `json:"arch,omitempty"`
	CPUPercent  float64   `json:"cpu_percent,omitempty"`
	MemoryTotal uint64    `json:"memory_total,omitempty"`
	MemoryUsed  uint64    `json:"memory_used,omitempty"`
	DiskTotal   uint64    `json:"disk_total,omitempty"`
	DiskUsed    uint64    `json:"disk_used,omitempty"`
	NetRxBytes  uint64    `json:"net_rx_bytes,omitempty"`
	NetTxBytes  uint64    `json:"net_tx_bytes,omitempty"`
	UptimeSec   uint64    `json:"uptime_sec,omitempty"`
	LatencyMS   int64     `json:"latency_ms,omitempty"`
	Error       string    `json:"error,omitempty"`
	CollectedAt time.Time `json:"collected_at,omitempty"`
}

// NodeSpec is 规划 1: a BLANK node. The operator only types Address + Port;
// API password, post-quantum key and mTLS certificate are generated.
// Private material is NEVER stored here — only hashes and fingerprints.
type NodeSpec struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Address         string     `json:"address"`
	Port            int        `json:"port"`
	APIPasswordHash string     `json:"api_password_hash"`
	PQSuite         string     `json:"pq_suite"`
	PQPublic        string     `json:"pq_public"`      // base64(ML-KEM ek || X25519 pk)
	PQFingerprint   string     `json:"pq_fingerprint"` // sha256 of PQPublic, hex
	MTLSCommonName  string     `json:"mtls_common_name"`
	MTLSFingerprint string     `json:"mtls_fingerprint"`
	CACertPEM       string     `json:"ca_cert_pem"`
	NodeCertPEM     string     `json:"node_cert_pem"`
	Probe           *NodeProbe `json:"probe,omitempty"`
}

// NodeSecrets is returned ONCE at creation time. The console must show it
// immediately; it is not persisted and cannot be recovered from the store.
type NodeSecrets struct {
	APIPassword   string `json:"api_password"`
	PQPublic      string `json:"pq_public"`
	PQFingerprint string `json:"pq_fingerprint"`
	NodeKeyPEM    string `json:"node_key_pem"`
	NodeCertPEM   string `json:"node_cert_pem"`
	CACertPEM     string `json:"ca_cert_pem"`
	Notice        string `json:"notice"`
}

// KernelSlot is 规划 2. One slot per kernel; any subset can be started.
type KernelSlot struct {
	Kernel          string `json:"kernel"`
	Enabled         bool   `json:"enabled"`
	AutoStart       bool   `json:"auto_start"`
	ProtocolGroupID string `json:"protocol_group_id,omitempty"`
}

// Protocol is an edited protocol entry inside a group (规划 3).
type Protocol struct {
	Type   string         `json:"type"`
	Tag    string         `json:"tag"`
	Config map[string]any `json:"config,omitempty"`
}

// ProtocolGroup is 规划 3: one group may target 1..3 kernels at once.
type ProtocolGroup struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	KernelIDs []string   `json:"kernel_ids"`
	Protocols []Protocol `json:"protocols"`
	Enabled   bool       `json:"enabled"`
}

// RuleSpec is 规划 4: a single, switchable rule.
type RuleSpec struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Pattern  string `json:"pattern"`
	Outbound string `json:"outbound"`
	Enabled  bool   `json:"enabled"`
}

// RuleSetRef is 规划 4: a rule set referenced by rules and policy groups.
type RuleSetRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Source  string `json:"source,omitempty"`
	Enabled bool   `json:"enabled"`
}

// DNSConfig is 规划 5: leak protection, encrypted upstreams and geo split.
type DNSConfig struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	LeakProtection  bool     `json:"leak_protection"`
	Encrypt         string   `json:"encrypt"` // none | doh | dot | doq
	Domestic        []string `json:"domestic"`
	Foreign         []string `json:"foreign"`
	Strategy        string   `json:"strategy"` // geo | all-domestic | all-foreign
	DomesticIPCIDRs []string `json:"domestic_ip_cidrs,omitempty"`
}

// PolicyGroup is 规划 6: binds rule sets into one policy.
type PolicyGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Mode       string   `json:"mode"` // select | url-test | fallback | load-balance
	RuleSetIDs []string `json:"rule_set_ids"`
	Enabled    bool     `json:"enabled"`
}

// LinkSpec is 规划 9: a client-specific, recognisable link derived from a token.
type LinkSpec struct {
	Client string `json:"client"`
	Target string `json:"target"`
	URL    string `json:"url"`
}

// Module is 规划 7: the aggregation root. Everything a user needs lives here.
type Module struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Note           string          `json:"note,omitempty"`
	Enabled        bool            `json:"enabled"`
	Master         MasterConfig    `json:"master"`
	Nodes          []NodeSpec      `json:"nodes"`
	Kernels        []KernelSlot    `json:"kernels"`
	ProtocolGroups []ProtocolGroup `json:"protocol_groups"`
	Rules          []RuleSpec      `json:"rules"`
	RuleSets       []RuleSetRef    `json:"rule_sets"`
	DNS            DNSConfig       `json:"dns"`
	PolicyGroups   []PolicyGroup   `json:"policy_groups"`
	Links          []LinkSpec      `json:"links,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// Summary is the projection the console shows in the module list/cards.
type Summary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	NodeCount     int    `json:"node_count"`
	KernelCount   int    `json:"kernel_count"`
	ActiveKernels int    `json:"active_kernels"`
	GroupCount    int    `json:"protocol_group_count"`
	RuleCount     int    `json:"rule_count"`
	RuleSetCount  int    `json:"rule_set_count"`
	PolicyCount   int    `json:"policy_group_count"`
	DNSEnabled    bool   `json:"dns_enabled"`
	MasterEnabled bool   `json:"master_enabled"`
	UpdatedAt     string `json:"updated_at"`
}

// View is the browser-safe projection: node private material is never present
// in NodeSpec anyway, but hashes are stripped too.
type View struct {
	Module
	Secrets map[string]NodeSecrets `json:"-"`
}

// Summarize builds the list projection without leaking any credential hashes.
func (m Module) Summarize() Summary {
	active := 0
	for _, k := range m.Kernels {
		if k.Enabled {
			active++
		}
	}
	return Summary{
		ID: m.ID, Name: m.Name, Enabled: m.Enabled,
		NodeCount: len(m.Nodes), KernelCount: len(m.Kernels), ActiveKernels: active,
		GroupCount: len(m.ProtocolGroups), RuleCount: len(m.Rules),
		RuleSetCount: len(m.RuleSets), PolicyCount: len(m.PolicyGroups),
		DNSEnabled: m.DNS.Enabled, MasterEnabled: m.Master.Enabled,
		UpdatedAt: m.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
