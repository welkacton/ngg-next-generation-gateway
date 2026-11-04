package netengine

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// DialSOCKS5 is a real SOCKS5 client used by NGG's own self-test and by the
// transparent ingress. It is exported so tests can exercise the data path.
func DialSOCKS5(ctx context.Context, proxyAddr, dst, user, pass string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	if err := handshakeSOCKS5(conn, dst, user, pass); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// SelfTest performs a GENUINE end-to-end network test: it starts a local echo
// server, opens a SOCKS5 connection through the NGG ingress, pushes real bytes
// through the selected kernel (or the DIRECT path), and verifies the exact
// bytes come back. No mocked responses.
func (e *Engine) SelfTest(ctx context.Context, coreID string) (map[string]any, error) {
	if !e.IsRunning() {
		return nil, ErrNotRunning
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start echo server: %w", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				// An explicit echo loop is required: io.Copy(c, c) on a single
				// TCPConn recurses through TCPConn.WriteTo/genericWriteTo.
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	target := ln.Addr().String()

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(nonce)

	ruleID := ""
	if coreID != "" {
		host, _, _ := net.SplitHostPort(target)
		ruleID = "selftest-" + token[:8]
		_ = e.ruleSet.Remove(ruleID)
		err = e.ruleSet.Add(Rule{
			ID: ruleID, Type: RuleDomain, Value: host, Action: ActProxy,
			CoreID: contracts.CoreID(coreID), Priority: 100000, Enabled: true,
			Comment: "temporary self-test rule",
		})
		if err != nil {
			return nil, fmt.Errorf("install temporary rule: %w", err)
		}
		defer e.ruleSet.Remove(ruleID)
	}

	proxy := e.ingress.Addr()
	if proxy == "" {
		return nil, fmt.Errorf("ingress is not listening")
	}
	start := time.Now()
	conn, err := DialSOCKS5(ctx, proxy, target, e.cfg.Ingress.AuthUser, e.cfg.Ingress.AuthPass)
	if err != nil {
		return map[string]any{
			"ok": false, "target": target, "proxy": proxy, "core_id": coreID,
			"error": err.Error(), "fail_closed": e.cfg.FailClosed,
		}, nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	payload := "NGG-SELFTEST:" + token + "\n"
	if _, err := io.WriteString(conn, payload); err != nil {
		return nil, fmt.Errorf("write through NGG path: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("read echo through NGG path: %w", err)
	}
	ok := strings.TrimSpace(line) == strings.TrimSpace(payload)
	if !ok {
		return map[string]any{"ok": false, "error": "echo payload mismatch", "got": line, "want": payload}, nil
	}
	return map[string]any{
		"ok": true, "target": target, "proxy": proxy, "core_id": coreID,
		"bytes": len(payload), "latency_ms": float64(elapsed.Microseconds()) / 1000.0,
		"path": func() string {
			if coreID != "" {
				return "NGG ingress -> " + coreID + " -> echo"
			}
			return "NGG ingress -> DIRECT egress -> echo"
		}(),
		"echo":     strings.TrimSpace(line)[:min(24, len(strings.TrimSpace(line)))],
		"sessions": e.sessions.Count(),
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
