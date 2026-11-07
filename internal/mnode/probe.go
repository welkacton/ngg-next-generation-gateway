package mnode

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/module"
)

// probeResponse is the node-side /health payload. Keep it aligned with the
// Python service embedded in deploy.go.
type probeResponse struct {
	Online      bool    `json:"online"`
	Hostname    string  `json:"hostname"`
	OS          string  `json:"os"`
	Arch        string  `json:"arch"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryTotal uint64  `json:"memory_total"`
	MemoryUsed  uint64  `json:"memory_used"`
	DiskTotal   uint64  `json:"disk_total"`
	DiskUsed    uint64  `json:"disk_used"`
	UptimeSec   uint64  `json:"uptime_sec"`
	NetRxBytes  uint64  `json:"net_rx_bytes"`
	NetTxBytes  uint64  `json:"net_tx_bytes"`
	CollectedAt string  `json:"collected_at"`
	Error       string  `json:"error"`
}

// Probe is 规划 1's "连接探测主机信息": dial the node over mutual TLS,
// present the generated API password and post-quantum fingerprint, and read
// back REAL host metrics. Nothing is fabricated — a failed dial is an error.
func Probe(ctx context.Context, rec Record, clientCertPEM, clientKeyPEM []byte) (module.NodeProbe, error) {
	n := rec.Node
	result := module.NodeProbe{CollectedAt: time.Now().UTC()}

	cert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		result.Error = "面板客户端证书不可用: " + err.Error()
		return result, fmt.Errorf("%s", result.Error)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(n.CACertPEM)) {
		result.Error = "节点 CA 证书无法解析"
		return result, fmt.Errorf("%s", result.Error)
	}
	addr := net.JoinHostPort(n.Address, strconv.Itoa(n.Port))
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pool,
			ServerName:   n.Address,
			MinVersion:   tls.VersionTLS12,
		},
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
	defer transport.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/health", nil)
	if err != nil {
		result.Error = "构造探测请求失败: " + err.Error()
		return result, err
	}
	req.Header.Set("X-NGG-API-Password", rec.Secrets.APIPassword)
	req.Header.Set("X-NGG-PQ-Fingerprint", rec.Secrets.PQFingerprint)

	started := time.Now()
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		result.Error = friendlyProbeError(err)
		return result, fmt.Errorf("%s", result.Error)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		result.Error = "读取节点响应失败: " + err.Error()
		return result, fmt.Errorf("%s", result.Error)
	}
	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("节点拒绝探测（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(body)))
		return result, fmt.Errorf("%s", result.Error)
	}
	parsed, perr := parseProbe(body, time.Since(started).Milliseconds())
	if perr != nil {
		result.Error = perr.Error()
		return result, perr
	}
	return parsed, nil
}

// parseProbe is kept separate so the response contract has a direct unit test
// without needing a live TLS server.
func parseProbe(body []byte, latencyMS int64) (module.NodeProbe, error) {
	var got probeResponse
	if err := json.Unmarshal(body, &got); err != nil {
		return module.NodeProbe{}, fmt.Errorf("节点响应格式不符: %w", err)
	}
	if !got.Online {
		return module.NodeProbe{}, fmt.Errorf("节点自报离线：%s", got.Error)
	}
	return module.NodeProbe{
		Online: got.Online, Hostname: got.Hostname, OS: got.OS, Arch: got.Arch,
		CPUPercent: got.CPUPercent, MemoryTotal: got.MemoryTotal, MemoryUsed: got.MemoryUsed,
		DiskTotal: got.DiskTotal, DiskUsed: got.DiskUsed,
		NetRxBytes: got.NetRxBytes, NetTxBytes: got.NetTxBytes,
		UptimeSec: got.UptimeSec, LatencyMS: latencyMS,
		CollectedAt: time.Now().UTC(),
	}, nil
}

func friendlyProbeError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "节点拒绝连接：安装脚本未执行完成，或管理端口未放通"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
		return "连接节点超时：确认云安全组 / 防火墙已放通该端口"
	case strings.Contains(msg, "no such host"):
		return "节点地址无法解析"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls:"):
		return "双向 TLS 握手失败：节点证书与面板 CA 不匹配，请重新生成节点凭据并重新执行安装脚本"
	default:
		return "无法连接节点：" + msg
	}
}
