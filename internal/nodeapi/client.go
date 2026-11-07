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
	"crypto/tls"
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

// DefaultPort is the node API port (NODE_PORT). Remnawave itself defaults to
// 2222, but that port is very often already taken by remnanode on the very VPS
// an operator is adding — pointing NGG at it produces a baffling "certificate
// mismatch" because the panel is then talking to another product's mTLS API.
// 2244 keeps the shape of the upstream contract without colliding with it.
const DefaultPort = 2244

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

// TLS failure reasons. A handshake failure is not one problem: a port taken
// by another mTLS product, an expired certificate and a wrong node id need
// three completely different fixes. Keeping the reason lets the console give
// the actionable one instead of a generic "regenerate the plan".
const (
	ReasonForeignCA      = "foreign-ca"      // server cert is not signed by our CA
	ReasonCertExpired    = "cert-expired"    // our cert, but outside its validity
	ReasonWrongNode      = "wrong-node"      // our CA, but a different node id
	ReasonClientRejected = "client-rejected" // server refused the panel cert
	ReasonOtherTLS       = "other-tls"
)

// Error carries the classification plus a message that is safe to show.
type Error struct {
	Kind   Kind
	Reason string
	Msg    string
	Err    error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(kind Kind, msg string, err error) *Error { return &Error{Kind: kind, Msg: msg, Err: err} }

func (e *Error) withReason(reason string) *Error { e.Reason = reason; return e }

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
//
// A TLS error used to be reported as "证书不匹配或已过期，重新生成方案" no
// matter what happened. That is actively harmful when the management port is
// occupied by a DIFFERENT product (very common: Remnawave's remnanode owns
// 2222): regenerating the plan a hundred times cannot fix a port collision.
func classify(err error) *Error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newErr(KindUnreachable, "连接节点超时：确认节点在线、管理端口已放通", err)
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unknown authority"),
		strings.Contains(msg, "unknown certificate authority"),
		strings.Contains(msg, "self-signed certificate in certificate chain"):
		return newErr(KindTLS, "该端口上的服务不是本面板签发的节点（服务端证书由其它 CA 签发）", err).
			withReason(ReasonForeignCA)
	case strings.Contains(msg, "has expired"),
		strings.Contains(msg, "not yet valid"),
		strings.Contains(msg, "certificate expired"):
		return newErr(KindTLS, "节点证书已过期或尚未生效（检查服务器时间）", err).
			withReason(ReasonCertExpired)
	case strings.Contains(msg, "与期望的"),
		strings.Contains(msg, "节点证书 CN="):
		return newErr(KindTLS, "该地址上的节点证书属于另一台节点", err).
			withReason(ReasonWrongNode)
	case strings.Contains(msg, "certificate required"),
		strings.Contains(msg, "bad certificate"),
		strings.Contains(msg, "client didn't provide a certificate"):
		return newErr(KindTLS, "节点拒绝了面板的客户端证书（节点装的是别的面板签发的 CA）", err).
			withReason(ReasonClientRejected)
	case strings.Contains(msg, "tls:"):
		return newErr(KindTLS, "与节点 TLS 握手失败（证书或协议不匹配）", err).
			withReason(ReasonOtherTLS)
	case strings.Contains(msg, "no such host"):
		return newErr(KindUnreachable, "节点地址无法解析", err)
	case strings.Contains(msg, "connection refused"):
		return newErr(KindUnreachable, "节点拒绝连接：ngg-node.service 没有在跑，或管理端口不对", err)
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "connection timed out"):
		return newErr(KindUnreachable, "连接节点超时：确认安全组 / 防火墙放通了管理端口", err)
	}
	return newErr(KindUnreachable, "无法连接节点", err)
}

// InspectPeer reports the certificate actually served on address:port, even
// when it is not ours. It is DIAGNOSTIC ONLY: verification is skipped on
// purpose because we are reading an unauthenticated endpoint's identity, not
// trusting it. A server that demands a client certificate still sends its own
// certificate before rejecting us, so we ignore the handshake error and read
// whatever peer certificates arrived.
func InspectPeer(ctx context.Context, address string, port int) string {
	if strings.TrimSpace(address) == "" || port < 1 || port > 65535 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	addr := net.JoinHostPort(address, strconv.Itoa(port))
	raw, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return ""
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(4 * time.Second))
	conn := tls.Client(raw, &tls.Config{
		// #nosec G402 -- read-only diagnosis of a foreign certificate; no trust decision.
		InsecureSkipVerify: true,
		ServerName:         address,
		MinVersion:         tls.VersionTLS12,
	})
	_ = conn.HandshakeContext(ctx)
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return ""
	}
	c := certs[0]
	issuer := c.Issuer.CommonName
	if issuer == "" {
		issuer = c.Issuer.String()
	}
	return fmt.Sprintf("CN=%s，签发者=%s，有效期至 %s",
		c.Subject.CommonName, issuer, c.NotAfter.UTC().Format("2006-01-02"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
