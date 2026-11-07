// Package nodeapi is the panel's client for the node's internal API.
//
// The contract is Remnawave's (remnawave/node · libs/contract/api/routes.ts):
//
//	GET  /node/xray/healthcheck          is the node alive, which kernel version
//	POST /node/xray/start                push {xrayConfig} and start the kernel
//	POST /node/xray/stop                 stop the kernel
//	GET  /node/stats/get-system-stats    real host metrics
//
// Every response is wrapped in {"response": …} exactly as upstream does, and
// every request is made over mutual TLS with the panel's client certificate.
// The node's certificate is pinned to the expected node id, so a mis-pointed
// address fails loudly instead of pushing config onto the wrong machine.
package nodeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/nodepki"
)

// DefaultPort is the node API port — Remnawave's NODE_PORT default. It is the
// port ngg-node listens on and the port the panel dials.
const DefaultPort = 2222

// Timeouts: health and stats are cheap, start has to validate the config with
// the kernel and restart the unit, which on a 1-vCPU VPS takes seconds.
const (
	healthTimeout = 8 * time.Second
	statsTimeout  = 10 * time.Second
	startTimeout  = 45 * time.Second
	stopTimeout   = 20 * time.Second
	// maxBody caps a response so a hostile node cannot exhaust panel memory.
	maxBody = 1 << 20
)

// Kind classifies a failure so the console can say something true about it.
type Kind string

const (
	// KindUnreachable: TCP connect failed — node down, wrong port, firewall.
	KindUnreachable Kind = "unreachable"
	// KindTLS: handshake failed — certificate mismatch, wrong CA, expired.
	KindTLS Kind = "tls"
	// KindAuth: the node rejected our client certificate.
	KindAuth Kind = "auth"
	// KindNode: the node answered but the operation failed on its side.
	KindNode Kind = "node"
	// KindProtocol: the answer was not the shape we agreed on.
	KindProtocol Kind = "protocol"
)

// Error carries the classification plus a message that is safe to show.
type Error struct {
	Kind Kind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(kind Kind, msg string, err error) *Error { return &Error{Kind: kind, Msg: msg, Err: err} }

// Endpoint is everything needed to reach one node.
type Endpoint struct {
	// Address is the node's IP or DNS name as registered in the host list.
	Address string
	// Port is the node API port (NODE_PORT on the node side).
	Port int
	// NodeID is the expected certificate CN.
	NodeID string
	// CAPEM / CertPEM / KeyPEM are the panel's mTLS material.
	CAPEM   []byte
	CertPEM []byte
	KeyPEM  []byte
}

// Client talks to one node. It is cheap to build; one per request is fine.
type Client struct {
	base   string
	nodeID string
	hc     *http.Client
}

// New validates the endpoint and builds a pinned mutual-TLS client.
func New(ep Endpoint) (*Client, error) {
	addr := strings.TrimSpace(ep.Address)
	if addr == "" {
		return nil, newErr(KindProtocol, "节点地址为空", nil)
	}
	port := ep.Port
	if port == 0 {
		port = DefaultPort
	}
	if port < 1 || port > 65535 {
		return nil, newErr(KindProtocol, fmt.Sprintf("节点管理端口 %d 不合法", port), nil)
	}
	tlsCfg, err := nodepki.ClientConfig(ep.CAPEM, ep.CertPEM, ep.KeyPEM, addr, ep.NodeID)
	if err != nil {
		return nil, newErr(KindTLS, "面板客户端证书不可用", err)
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsCfg,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		ForceAttemptHTTP2:   false,
	}
	return &Client{
		base:   "https://" + net.JoinHostPort(addr, strconv.Itoa(port)),
		nodeID: ep.NodeID,
		hc:     &http.Client{Transport: transport},
	}, nil
}

// BaseURL is the endpoint the console shows for troubleshooting.
func (c *Client) BaseURL() string { return c.base }

/* ------------------------------------------------------------------ *
 * 调用
 * ------------------------------------------------------------------ */

// Health reports whether the node API is up and which kernel it runs.
func (c *Client) Health(ctx context.Context) (Health, time.Duration, error) {
	var out Health
	d, err := c.call(ctx, http.MethodGet, "/node/xray/healthcheck", nil, healthTimeout, &out)
	return out, d, err
}

// SystemStats reads the node's real host metrics.
func (c *Client) SystemStats(ctx context.Context) (System, time.Duration, error) {
	var out System
	d, err := c.call(ctx, http.MethodGet, "/node/stats/get-system-stats", nil, statsTimeout, &out)
	return out, d, err
}

// Start pushes a kernel config and starts (or restarts) the kernel.
//
// configJSON is the already-rendered configuration; it is wrapped in the
// upstream request shape so a stock Remnawave node would understand the call.
func (c *Client) Start(ctx context.Context, configJSON string) (StartResult, time.Duration, error) {
	body := map[string]any{
		"internals":  map[string]any{"forceRestart": true},
		"xrayConfig": json.RawMessage(configJSON),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return StartResult{}, 0, newErr(KindProtocol, "内核配置无法序列化", err)
	}
	var out StartResult
	d, cerr := c.call(ctx, http.MethodPost, "/node/xray/start", raw, startTimeout, &out)
	if cerr != nil {
		return StartResult{}, d, cerr
	}
	if !out.IsStarted {
		msg := "节点未能启动内核"
		if out.Error != nil && strings.TrimSpace(*out.Error) != "" {
			msg = strings.TrimSpace(*out.Error)
		}
		return out, d, newErr(KindNode, msg, nil)
	}
	return out, d, nil
}

// Stop stops the kernel and leaves the node API running.
func (c *Client) Stop(ctx context.Context) error {
	_, err := c.call(ctx, http.MethodPost, "/node/xray/stop", []byte(`{}`), stopTimeout, nil)
	return err
}

// call performs one request and unwraps the {"response": …} envelope.
func (c *Client) call(ctx context.Context, method, path string, body []byte, timeout time.Duration, out any) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return 0, newErr(KindProtocol, "构造请求失败", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ngg-panel")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.hc.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return elapsed, classify(err)
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if readErr != nil {
		return elapsed, newErr(KindUnreachable, "读取节点响应失败", readErr)
	}

	envelope := struct {
		Response json.RawMessage `json:"response"`
		Error    string          `json:"error"`
	}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &envelope)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return elapsed, newErr(KindAuth, "节点拒绝了面板的客户端证书（401/403）", nil)
	case resp.StatusCode >= 400:
		msg := firstNonEmpty(envelope.Error, strings.TrimSpace(string(raw)))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return elapsed, newErr(KindNode, fmt.Sprintf("节点返回 HTTP %d：%s", resp.StatusCode, msg), nil)
	}

	if out == nil {
		return elapsed, nil
	}
	if len(envelope.Response) == 0 || string(envelope.Response) == "null" {
		return elapsed, newErr(KindProtocol, "节点响应缺少 response 字段", nil)
	}
	if err := json.Unmarshal(envelope.Response, out); err != nil {
		return elapsed, newErr(KindProtocol, "节点响应格式与约定不符", err)
	}
	return elapsed, nil
}

// classify turns transport errors into something an operator can act on.
func classify(err error) *Error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newErr(KindUnreachable, "连接节点超时：确认节点在线、管理端口已放通", err)
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "certificate required"),
		strings.Contains(msg, "bad certificate"),
		strings.Contains(msg, "unknown certificate"),
		strings.Contains(msg, "tls:"):
		return newErr(KindTLS, "与节点握手失败：证书不匹配或已过期，重新生成方案并部署即可", err)
	case strings.Contains(msg, "no such host"):
		return newErr(KindUnreachable, "节点地址无法解析", err)
	case strings.Contains(msg, "connection refused"):
		return newErr(KindUnreachable, "节点拒绝连接：ngg-node.service 没有在跑，或管理端口不对", err)
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "connection timed out"):
		return newErr(KindUnreachable, "连接节点超时：确认安全组 / 防火墙放通了管理端口", err)
	}
	return newErr(KindUnreachable, "无法连接节点", err)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
