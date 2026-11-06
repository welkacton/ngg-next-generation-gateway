package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// stderrTailLimit is how many recent stderr lines are kept per kernel so the
// crash notification and the diagnostics endpoint can quote the REAL reason
// instead of just "exit status 1".
const stderrTailLimit = 30

// unit is one supervised kernel process family (the initial process plus any
// automatic crash restarts). Done() closes only when the whole unit is final.
type unit struct {
	opts core.StartOptions

	mu         sync.Mutex
	cmd        *exec.Cmd
	err        error
	stopping   bool
	attempt    int
	policy     core.RestartPolicy
	done       chan struct{}
	stopReq    chan struct{}
	firstErr   chan error
	finishOnce sync.Once
	stopOnce   sync.Once

	tailMu     sync.Mutex
	stderrTail []string
}

// pushStderr records one stderr line in the bounded diagnostic ring.
func (u *unit) pushStderr(line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	u.tailMu.Lock()
	defer u.tailMu.Unlock()
	u.stderrTail = append(u.stderrTail, line)
	if len(u.stderrTail) > stderrTailLimit {
		u.stderrTail = append([]string(nil), u.stderrTail[len(u.stderrTail)-stderrTailLimit:]...)
	}
}

// recentStderr returns the last n stderr lines, oldest first.
func (u *unit) recentStderr(n int) []string {
	u.tailMu.Lock()
	defer u.tailMu.Unlock()
	if n <= 0 || n > len(u.stderrTail) {
		n = len(u.stderrTail)
	}
	out := make([]string, n)
	copy(out, u.stderrTail[len(u.stderrTail)-n:])
	return out
}

var _ core.ProcessHandle = (*unit)(nil)

func newUnit(o core.StartOptions) *unit {
	return &unit{
		opts: o, policy: o.Policy,
		done: make(chan struct{}), stopReq: make(chan struct{}),
		firstErr: make(chan error, 1),
	}
}

func (u *unit) PID() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cmd != nil && u.cmd.Process != nil {
		return u.cmd.Process.Pid
	}
	return 0
}
func (u *unit) Done() <-chan struct{} { return u.done }
func (u *unit) Err() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err
}
func (u *unit) isStopping() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stopping
}
func (u *unit) currentPolicy() core.RestartPolicy {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.policy
}
func (u *unit) finish(err error) {
	u.finishOnce.Do(func() {
		u.mu.Lock()
		u.err = err
		u.mu.Unlock()
		close(u.done)
		if u.opts.OnExit != nil {
			u.opts.OnExit(err)
		}
	})
}
func (u *unit) requestStop() {
	u.stopOnce.Do(func() {
		u.mu.Lock()
		u.stopping = true
		u.mu.Unlock()
		close(u.stopReq)
	})
}

// supervisor runs real OS processes. It never reports a process as running
// unless the OS process is actually alive.
type supervisor struct {
	mu    sync.Mutex
	procs map[contracts.CoreID]*unit
}

var _ core.Supervisor = (*supervisor)(nil)

func newSupervisor() *supervisor {
	return &supervisor{procs: map[contracts.CoreID]*unit{}}
}

func (s *supervisor) Start(ctx context.Context, o core.StartOptions) (core.ProcessHandle, error) {
	if o.Binary == "" {
		return nil, fmt.Errorf("no binary path for kernel %q", o.ID)
	}
	if st, err := os.Stat(o.Binary); err != nil || st.IsDir() {
		return nil, fmt.Errorf("kernel %q binary not found: %s", o.ID, o.Binary)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	u := newUnit(o)
	s.mu.Lock()
	if old, exists := s.procs[o.ID]; exists {
		select {
		case <-old.done:
			// previous unit has fully terminated; safe to replace
		default:
			s.mu.Unlock()
			return nil, fmt.Errorf("kernel %q is already running (pid %d)", o.ID, old.PID())
		}
	}
	s.procs[o.ID] = u
	s.mu.Unlock()

	// The supervised process intentionally OUTLIVES the API request context;
	// ctx only cancels the synchronous startup handshake.
	go s.runUnit(u)
	select {
	case err := <-u.firstErr:
		if err != nil {
			s.remove(o.ID, u)
			return nil, fmt.Errorf("start kernel %q: %w", o.ID, err)
		}
		return u, nil
	case <-time.After(10 * time.Second):
		u.requestStop()
		s.remove(o.ID, u)
		return nil, fmt.Errorf("start kernel %q: timed out waiting for process launch", o.ID)
	case <-ctx.Done():
		u.requestStop()
		s.remove(o.ID, u)
		return nil, ctx.Err()
	}
}

func (s *supervisor) remove(id contracts.CoreID, u *unit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.procs[id] == u {
		delete(s.procs, id)
	}
}

func (s *supervisor) runUnit(u *unit) {
	first := true
	var lastErr error
	for {
		cmd := exec.Command(u.opts.Binary, u.opts.Args...)
		cmd.Dir = u.opts.WorkDir
		cmd.Env = append(os.Environ(), u.opts.Env...)
		// Writers instead of StdoutPipe/StderrPipe: with pipes cmd.Wait closes
		// the read end as soon as the process exits, which silently drops the
		// final — and diagnostically decisive — crash lines.
		stdout := &lineWriter{stream: "stdout", onLine: u.opts.OnLine}
		stderr := &lineWriter{stream: "stderr", onLine: u.opts.OnLine, onStderr: u.pushStderr}
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			lastErr = err
			if first {
				u.firstErr <- err
				close(u.firstErr)
			}
			break
		}
		u.mu.Lock()
		u.cmd = cmd
		u.mu.Unlock()
		if first {
			close(u.firstErr) // a nil value signals successful launch
		}
		lastErr = cmd.Wait()
		stdout.Flush()
		stderr.Flush()
		first = false
		if u.opts.OnLine != nil {
			u.opts.OnLine("ngg", fmt.Sprintf("process exited: %v", lastErr))
		}
		if u.isStopping() {
			break
		}
		pol := u.currentPolicy()
		u.mu.Lock()
		attempt := u.attempt
		u.mu.Unlock()
		if !pol.Enabled || attempt >= pol.MaxRetries {
			break
		}
		if u.opts.OnRestart != nil {
			u.opts.OnRestart(attempt+1, lastErr)
		}
		u.mu.Lock()
		u.attempt++
		u.mu.Unlock()
		backoff := pol.Backoff * time.Duration(attempt+1)
		if backoff <= 0 {
			backoff = time.Second
		}
		select {
		case <-u.stopReq:
			u.finish(lastErr)
			return
		case <-time.After(backoff):
		}
	}
	u.finish(lastErr)
}

// lineWriter turns a kernel output stream into complete lines. Writes never
// fail: the kernel must not be affected by NGG's log handling.
type lineWriter struct {
	stream   string
	onLine   func(stream, line string)
	onStderr func(line string)

	mu  sync.Mutex
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.emit(line)
	}
	// A single unterminated line must not grow without bound.
	if len(w.buf) > 1<<20 {
		w.emit(string(w.buf))
		w.buf = nil
	}
	return len(p), nil
}

// Flush emits a trailing line that had no newline before the process exited.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return
	}
	line := string(w.buf)
	w.buf = nil
	w.emit(line)
}

func (w *lineWriter) emit(line string) {
	line = strings.TrimRight(line, "\r")
	if w.onStderr != nil {
		w.onStderr(line)
	}
	if w.onLine != nil {
		w.onLine(w.stream, line)
	}
}

func (s *supervisor) Stop(_ context.Context, id contracts.CoreID, graceful time.Duration) error {
	s.mu.Lock()
	u := s.procs[id]
	s.mu.Unlock()
	if u == nil {
		return nil
	}
	u.requestStop()
	u.mu.Lock()
	cmd := u.cmd
	u.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = terminate(cmd.Process)
	}
	if graceful <= 0 {
		graceful = 5 * time.Second
	}
	select {
	case <-u.done:
	case <-time.After(graceful):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-u.done:
		case <-time.After(2 * time.Second):
			u.finish(fmt.Errorf("process did not exit after kill"))
		}
	}
	s.remove(id, u)
	return nil
}

func (s *supervisor) IsRunning(id contracts.CoreID) bool {
	s.mu.Lock()
	u := s.procs[id]
	s.mu.Unlock()
	if u == nil {
		return false
	}
	select {
	case <-u.done:
		return false
	default:
		return true
	}
}

func (s *supervisor) PID(id contracts.CoreID) int {
	s.mu.Lock()
	u := s.procs[id]
	s.mu.Unlock()
	if u == nil {
		return 0
	}
	return u.PID()
}

// RecentStderr returns up to n of the most recent stderr lines the supervised
// process wrote. It is diagnostic only and never affects lifecycle decisions.
func (s *supervisor) RecentStderr(id contracts.CoreID, n int) []string {
	s.mu.Lock()
	u := s.procs[id]
	s.mu.Unlock()
	if u == nil {
		return []string{}
	}
	return u.recentStderr(n)
}

// RestartCount reports how many independent crash restarts the current unit
// has performed. It is used to avoid reporting a crash-looping kernel as
// freshly "running" during the startup handshake.
func (s *supervisor) RestartCount(id contracts.CoreID) int {
	s.mu.Lock()
	u := s.procs[id]
	s.mu.Unlock()
	if u == nil {
		return 0
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.attempt
}

func (s *supervisor) SetRestartPolicy(id contracts.CoreID, p core.RestartPolicy) {
	s.mu.Lock()
	u := s.procs[id]
	s.mu.Unlock()
	if u != nil {
		u.mu.Lock()
		u.policy = p
		u.mu.Unlock()
	}
}

func (s *supervisor) RunningIDs() []contracts.CoreID {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contracts.CoreID
	for id, u := range s.procs {
		select {
		case <-u.done:
		default:
			out = append(out, id)
		}
	}
	return out
}

func (s *supervisor) StopAll(graceful time.Duration) {
	ids := s.RunningIDs()
	for _, id := range ids {
		_ = s.Stop(context.Background(), id, graceful)
	}
}
