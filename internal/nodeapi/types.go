package nodeapi

// The wire contract of the node's internal API.
//
// Field names and the {"response": …} envelope follow remnawave/node so the
// two are recognisably the same interface (libs/contract/api/controllers and
// models). Fields marked "NGG" are additions: upstream has no disk usage, no
// CPU percentage and no config hash, and the console needs them. Additions are
// additive only — a stock reader of the upstream shape ignores them.
//
// The same structs are used by the server side (internal/nodesvc), so there is
// exactly one definition of what travels over the wire.

// Health is GET /node/xray/healthcheck.
type Health struct {
	// Upstream fields.
	IsAlive                  bool   `json:"isAlive"`
	XrayInternalStatusCached bool   `json:"xrayInternalStatusCached"`
	XrayVersion              string `json:"xrayVersion"`
	NodeVersion              string `json:"nodeVersion"`

	// NGG additions.
	NodeID       string `json:"nodeId"`
	CoreActive   bool   `json:"coreActive"`
	UptimeSec    int64  `json:"uptimeSec"`
	ConfigSHA256 string `json:"configSha256"`
	CheckedAt    string `json:"checkedAt"`
}

// SystemInfo is the static half of the node's system section.
type SystemInfo struct {
	Arch              string   `json:"arch"`
	CPUs              int      `json:"cpus"`
	CPUModel          string   `json:"cpuModel"`
	MemoryTotal       uint64   `json:"memoryTotal"`
	Hostname          string   `json:"hostname"`
	Platform          string   `json:"platform"`
	Release           string   `json:"release"`
	Type              string   `json:"type"`
	Version           string   `json:"version"`
	NetworkInterfaces []string `json:"networkInterfaces"`
}

// NetworkInterface is one interface's counters and rates.
type NetworkInterface struct {
	Interface     string  `json:"interface"`
	RxBytesPerSec float64 `json:"rxBytesPerSec"`
	TxBytesPerSec float64 `json:"txBytesPerSec"`
	RxTotal       uint64  `json:"rxTotal"`
	TxTotal       uint64  `json:"txTotal"`
}

// SystemStats is the live half.
type SystemStats struct {
	MemoryFree uint64            `json:"memoryFree"`
	MemoryUsed uint64            `json:"memoryUsed"`
	Uptime     uint64            `json:"uptime"`
	LoadAvg    []float64         `json:"loadAvg"`
	Interface  *NetworkInterface `json:"interface"`

	// NGG additions.
	MemoryTotal uint64             `json:"memoryTotal"`
	CPUPercent  float64            `json:"cpuPercent"`
	DiskTotal   uint64             `json:"diskTotal"`
	DiskUsed    uint64             `json:"diskUsed"`
	DiskMount   string             `json:"diskMount"`
	Interfaces  []NetworkInterface `json:"interfaces"`
	SampledAt   string             `json:"sampledAt"`
}

// System is what start and stats return.
type System struct {
	Info  SystemInfo  `json:"info"`
	Stats SystemStats `json:"stats"`
}

// StartResult is POST /node/xray/start and /node/xray/stop.
type StartResult struct {
	IsStarted bool    `json:"isStarted"`
	Version   *string `json:"version"`
	Error     *string `json:"error"`
	System    *System `json:"system"`
}
