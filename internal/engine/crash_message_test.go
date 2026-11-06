package engine

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ngg-project/ngg/internal/adapters/builtin"
	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// diagnosticsEngine builds just enough Engine for preflight/message tests.
func diagnosticsEngine(t *testing.T, root string) (*Engine, *versionManager) {
	t.Helper()
	reg := core.NewRegistry()
	for _, a := range builtin.All() {
		if err := reg.Register(a); err != nil {
			t.Fatalf("register builtin: %v", err)
		}
	}
	vm := newVersionManager(reg, root,
		contracts.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		http.DefaultClient, newEventBus(), nil)
	return &Engine{
		registry:   reg,
		versions:   vm,
		supervisor: newSupervisor(),
		logs:       newLogSink(100, nil),
		ports:      newPortAllocator(17890, 10, "test-secret"),
		runtime:    map[contracts.CoreID]*coreRuntime{},
	}, vm
}

func TestPreflightRejectsUnrunnableBinary(t *testing.T) {
	root := t.TempDir()
	e, vm := diagnosticsEngine(t, root)
	bin := vm.BinaryPath(contracts.CoreSingBox)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file the OS cannot execute: what an antivirus-quarantined or corrupt
	// binary looks like to exec.
	if err := os.WriteFile(bin, []byte("not a real executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := e.preflightBinary(context.Background(), contracts.CoreSingBox, []string{"version"})
	if err == nil {
		t.Fatal("preflight accepted a non-executable binary")
	}
	if !strings.Contains(err.Error(), "二进制无法执行") {
		t.Fatalf("error %q does not explain that the binary cannot run", err)
	}
}

func TestStartupFailureMessageQuotesExitStatus(t *testing.T) {
	root := t.TempDir()
	e, _ := diagnosticsEngine(t, root)
	msg := e.startupFailureMessage(contracts.CoreSingBox, os.ErrPermission)
	if !strings.Contains(msg, "启动后立即退出") {
		t.Fatalf("message %q does not describe an immediate exit", msg)
	}
	if !strings.Contains(msg, "permission denied") {
		t.Fatalf("message %q does not carry the exit error", msg)
	}
	if !utf8.ValidString(msg) {
		t.Fatalf("message is not valid UTF-8")
	}
}

func TestPortConflictAdviceNamesThePortFromStderr(t *testing.T) {
	tail := []string{
		`FATAL[0000] start service: listen tcp 127.0.0.1:17890: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.`,
	}
	advice := portConflictAdvice(tail, nil)
	if !strings.Contains(advice, "17890") {
		t.Fatalf("advice %q does not name the port reported by the kernel", advice)
	}
	if !strings.Contains(advice, "被占用") {
		t.Fatalf("advice %q does not classify the conflict", advice)
	}
}

func TestPortConflictAdviceIgnoresUnrelatedStderr(t *testing.T) {
	tail := []string{"FATAL[0000] decode config at config.json: unknown field \"foo\""}
	if advice := portConflictAdvice(tail, []int{17910}); advice != "" {
		t.Fatalf("advice = %q, want empty for a non-port failure", advice)
	}
}

func TestPortConflictAdviceFallsBackToAllocatedPorts(t *testing.T) {
	tail := []string{"listen tcp: address already in use"}
	advice := portConflictAdvice(tail, []int{17900, 17901})
	for _, port := range []string{"17900", "17901"} {
		if !strings.Contains(advice, port) {
			t.Fatalf("advice %q does not mention allocated port %s", advice, port)
		}
	}
}

func TestLastOutputLineSkipsBlankLines(t *testing.T) {
	tail := []string{"first", "  ", "last line", ""}
	if got := lastOutputLine(tail); got != "last line" {
		t.Fatalf("lastOutputLine = %q, want %q", got, "last line")
	}
	if got := lastOutputLine(nil); got != "" {
		t.Fatalf("lastOutputLine(nil) = %q, want empty", got)
	}
}

func TestTruncateTextKeepsValidUTF8(t *testing.T) {
	long := strings.Repeat("内核崩溃原因", 100)
	got := truncateText(long, 300)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateText returned invalid UTF-8")
	}
	if n := len([]rune(got)); n != 301 { // 300 runes + ellipsis
		t.Fatalf("rune length = %d, want 301", n)
	}
	if short := truncateText("exit status 1", 300); short != "exit status 1" {
		t.Fatalf("truncateText changed a short string: %q", short)
	}
}

func TestPortFromLine(t *testing.T) {
	cases := map[string]int{
		"listen tcp 127.0.0.1:17890: bind: Only one usage":    17890,
		"listen udp 0.0.0.0:53: bind: address already in use": 53,
		"no port here":                         0,
		"2026/11/06 10:00:00 something failed": 0,
	}
	for line, want := range cases {
		if got := portFromLine(line); got != want {
			t.Fatalf("portFromLine(%q) = %d, want %d", line, got, want)
		}
	}
}
