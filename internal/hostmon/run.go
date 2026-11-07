package hostmon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Remote command execution for the node deployment pipeline.
//
// The collection path (collect.go) is deliberately read-only and capped at 15
// seconds. Deployment needs the opposite: a long-running, state-changing script
// whose stdout the console renders as a step log. Both share the same SSH
// credentials and the same TOFU host-key policy, so a machine the operator has
// already trusted for monitoring does not need to be trusted twice.

const (
	// DefaultRunBudget bounds one remote run when the caller brings no
	// deadline. Installing Xray on a 1-vCPU VPS takes ~60 s; five minutes
	// leaves room for apt, the GitHub download and a kernel restart.
	DefaultRunBudget = 5 * time.Minute

	// maxRunOutput caps each stream. A runaway script (e.g. a kernel that
	// prints its whole config) must not be able to exhaust panel memory, and
	// the console only renders the tail anyway.
	maxRunOutput = 256 << 10
)

// RunResult is the outcome of one remote script execution.
type RunResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	// Truncated is true when either stream hit maxRunOutput.
	Truncated bool `json:"truncated,omitempty"`
}

// Config returns the stored SSH credentials for a host id (or for an entry
// keyed by the host's address, in "match any node by IP" mode).
func (m *Monitor) Config(hostID string) (SSHConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, cfg, ok := m.resolveLocked(hostID)
	return cfg, ok
}

// RunScript feeds script to `bash -s` on the host and returns its output.
//
// The script travels over stdin, never as a command-line argument: a deploy
// script contains multi-line heredocs and credentials, and argv is visible in
// the remote process table. A non-zero remote exit status is reported in
// RunResult.ExitCode with a nil error; only transport/auth failures are errors.
func (m *Monitor) RunScript(ctx context.Context, hostID, script string) (RunResult, error) {
	if strings.TrimSpace(script) == "" {
		return RunResult{}, fmt.Errorf("脚本内容为空")
	}
	cfg, ok := m.Config(hostID)
	if !ok {
		return RunResult{}, ErrNotFound
	}
	return m.RunScriptWith(ctx, cfg, script)
}

// RunScriptWith runs a script against an explicit config. It exists so the
// deploy pipeline can retry against a credential the caller just edited
// without re-reading the registry.
func (m *Monitor) RunScriptWith(ctx context.Context, cfg SSHConfig, script string) (RunResult, error) {
	return m.run(ctx, cfg, script, nil)
}

// RunScriptStream is RunScriptWith plus a per-line callback fired as the remote
// process writes to stdout. The deployment pipeline uses it to render a live
// step log; the callback must be cheap and thread-safe (it runs on the SSH
// reader goroutine).
func (m *Monitor) RunScriptStream(ctx context.Context, cfg SSHConfig, script string, onLine func(string)) (RunResult, error) {
	return m.run(ctx, cfg, script, onLine)
}

func (m *Monitor) run(ctx context.Context, cfg SSHConfig, script string, onLine func(string)) (RunResult, error) {
	started := time.Now()
	auth, err := buildAuth(cfg)
	if err != nil {
		return RunResult{}, err
	}

	captured := make(chan string, 1)
	cc := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback(cfg.HostKeyFingerprint, captured),
	}

	runCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, DefaultRunBudget)
		defer cancel()
	}

	client, err := dialSSH(runCtx, cfg, cc)
	if err != nil {
		return RunResult{}, err
	}
	defer client.Close()

	// Same watchdog as the collector: closing the client unblocks a stalled
	// read when the budget expires, so the panel never hangs on a dead link.
	defused := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			client.Close()
		case <-defused:
		}
	}()
	defer close(defused)

	session, err := client.NewSession()
	if err != nil {
		return RunResult{}, err
	}
	defer session.Close()

	stdout := newCappedBuffer(maxRunOutput)
	stderr := newCappedBuffer(64 << 10)
	session.Stdin = strings.NewReader(script)
	session.Stdout = io.Writer(stdout)
	if onLine != nil {
		session.Stdout = io.MultiWriter(stdout, newLineEmitter(onLine))
	}
	session.Stderr = stderr

	runErr := session.Run("bash -s")
	res := RunResult{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMS: time.Since(started).Milliseconds(),
		Truncated:  stdout.Overflowed() || stderr.Overflowed(),
	}
	if runErr != nil {
		var exitErr *ssh.ExitError
		if ok := asExitError(runErr, &exitErr); ok {
			res.ExitCode = exitErr.ExitStatus()
		} else {
			// Transport-level failure: the connection died mid-run.
			if res.ExitCode == 0 {
				res.ExitCode = -1
			}
			return res, runErr
		}
	}

	// TOFU, exactly as the collector does it: the first successful run pins the
	// key so a later change is rejected.
	select {
	case fp := <-captured:
		m.trustFingerprint(hostIDFor(cfg), fp)
	default:
	}
	return res, nil
}

func hostIDFor(cfg SSHConfig) string {
	if cfg.HostID != "" {
		return cfg.HostID
	}
	return cfg.Host
}

// trustFingerprint persists a newly observed key only while the stored value is
// still empty, so a concurrent Set is never overwritten with an older key.
func (m *Monitor) trustFingerprint(hostID, fp string) {
	if fp == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key, cfg, ok := m.resolveLocked(hostID)
	if !ok || cfg.HostKeyFingerprint != "" {
		return
	}
	cfg.HostKeyFingerprint = fp
	m.configs[key] = cfg
	_ = m.persistLocked() // best-effort: a read-only run must not fail on write
}

func asExitError(err error, target **ssh.ExitError) bool {
	for err != nil {
		if e, ok := err.(*ssh.ExitError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// newLineEmitter splits the stream into lines for the live log. A partial line
// is held until its newline arrives, so a step marker is never delivered in
// two pieces.
type lineEmitter struct {
	mu  sync.Mutex
	buf []byte
	on  func(string)
}

func newLineEmitter(on func(string)) *lineEmitter { return &lineEmitter{on: on} }

func (e *lineEmitter) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.buf = append(e.buf, p...)
	for {
		i := bytes.IndexByte(e.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(e.buf[:i]), "\r")
		e.buf = append(e.buf[:0], e.buf[i+1:]...)
		e.on(line)
	}
	return len(p), nil
}

// cappedBuffer collects up to limit bytes and then keeps the NEWEST bytes:
// a deploy log ends with the failing step, so the tail is what the operator
// needs. Overflowing is reported so the console can say the head was dropped.
type cappedBuffer struct {
	mu       sync.Mutex
	buf      []byte
	limit    int
	overflow bool
}

func newCappedBuffer(limit int) *cappedBuffer { return &cappedBuffer{limit: limit} }

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= c.limit {
		c.buf = append(c.buf[:0], p[len(p)-c.limit:]...)
		c.overflow = true
		return len(p), nil
	}
	if excess := len(c.buf) + len(p) - c.limit; excess > 0 {
		c.buf = append(c.buf[:0], c.buf[excess:]...)
		c.overflow = true
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

func (c *cappedBuffer) Overflowed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.overflow
}

var _ io.Writer = (*cappedBuffer)(nil)
