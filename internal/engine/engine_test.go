package engine

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

func TestEventBusPublishSubscribe(t *testing.T) {
	b := newEventBus()
	var mu sync.Mutex
	var got []string
	unsub := b.Subscribe(func(e contracts.Event) {
		mu.Lock()
		got = append(got, e.Type)
		mu.Unlock()
	})
	if b.Subscribers() != 1 {
		t.Fatal("subscriber not registered")
	}
	b.Publish(contracts.Event{Type: "core.state"})
	b.Publish(contracts.Event{Type: "core.log"})
	unsub()
	b.Publish(contracts.Event{Type: "never"})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "core.state" || got[1] != "core.log" {
		t.Fatalf("events = %v", got)
	}
}

func TestLogSinkRingAndFilter(t *testing.T) {
	l := newLogSink(100, nil)
	for i := 0; i < 150; i++ {
		l.Append(contracts.LogEntry{CoreID: "a", Stream: "stdout", Line: fmt.Sprintf("line-%d", i)})
	}
	l.Append(contracts.LogEntry{CoreID: "b", Stream: "stderr", Line: "other"})
	latest := l.Latest("a", 10)
	if len(latest) != 10 || latest[0].Line != "line-140" || latest[9].Line != "line-149" {
		t.Fatalf("ring buffer wrong: %+v", latest)
	}
	since := l.Since("a", latest[8].Seq, 10)
	if len(since) != 1 || since[0].Line != "line-149" {
		t.Fatalf("since filter wrong: %+v", since)
	}
	l.Clear("a")
	if len(l.Latest("a", 10)) != 0 || len(l.Latest("b", 10)) != 1 {
		t.Fatal("clear affected the wrong core")
	}
}

func TestPortAllocatorIsolatesKernels(t *testing.T) {
	p := newPortAllocator(27890, 10, "secret")
	a, err := p.Allocate("core-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Allocate("core-b")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, port := range append(a.Ports(), b.Ports()...) {
		if seen[port] {
			t.Fatalf("port %d allocated twice", port)
		}
		seen[port] = true
	}
	if len(seen) != 10 {
		t.Fatalf("expected 10 disjoint ports, got %d", len(seen))
	}
	if again, _ := p.Get("core-a"); again.MixedPort != a.MixedPort {
		t.Fatal("allocation is not stable")
	}
	if len(p.Allocations()) != 2 {
		t.Fatal("allocations report wrong")
	}
	p.Release("core-a")
	if _, ok := p.Get("core-a"); ok {
		t.Fatal("release did not free the allocation")
	}
}

func TestPortCheckDetectsRealListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	p := newPortAllocator(27890, 10, "s")
	if conflicts := p.Check([]int{port}); len(conflicts) != 1 || conflicts[0] != port {
		t.Fatalf("occupied port not detected: %v", conflicts)
	}
}

// TestSupervisorHelper is launched as a REAL child OS process by the test
// below. It is not a mock: Start/Stop/IsRunning operate on a genuine PID.
func TestSupervisorHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Println("helper stdout line")
	fmt.Fprintln(os.Stderr, "helper stderr line")
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func TestSupervisorRunsAndStopsRealProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := newSupervisor()
	var mu sync.Mutex
	var lines []string
	ctx := context.Background()
	h, err := s.Start(ctx, core.StartOptions{
		ID:     "helper-core",
		Binary: exe,
		Args:   []string{"-test.run=TestSupervisorHelper", "--", "child"},
		Env:    []string{"GO_WANT_HELPER_PROCESS=1"},
		OnLine: func(stream, line string) {
			mu.Lock()
			lines = append(lines, stream+":"+line)
			mu.Unlock()
		},
		Policy: core.RestartPolicy{Enabled: false},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.PID() <= 0 {
		t.Fatal("no real PID")
	}
	if !s.IsRunning("helper-core") {
		t.Fatal("real process not reported running")
	}
	if _, err := s.Start(ctx, core.StartOptions{ID: "helper-core", Binary: exe}); err == nil {
		t.Fatal("double start must be rejected")
	}
	time.Sleep(600 * time.Millisecond)
	mu.Lock()
	joined := strings.Join(lines, "|")
	mu.Unlock()
	if !strings.Contains(joined, "helper stdout line") {
		t.Fatalf("stdout was not captured: %s", joined)
	}
	if err := s.Stop(ctx, "helper-core", 3*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s.IsRunning("helper-core") {
		t.Fatal("process still reported running after Stop")
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("process handle Done() not closed")
	}
}

func TestVersionMatchingAndDigest(t *testing.T) {
	if !versionMatches("v1.19.33", "1.19.33") || !versionMatches("v26.10.31", "26.10.31") {
		t.Fatal("tag/version normalization failed")
	}
	if versionMatches("v1.19.33", "9.9.9") {
		t.Fatal("mismatched versions must be rejected")
	}
	if got := normalizeDigest("sha256:6AB2180875F9F6FFF8B19386D760DAEC257F66B45F72B3AFC650634272FA837A"); len(got) != 64 {
		t.Fatalf("digest normalization: %q", got)
	}
	if got := normalizeDigest("not-a-digest"); got != "" {
		t.Fatal("invalid digest must be rejected")
	}
}

func TestTrustedDownloadURL(t *testing.T) {
	if err := assertTrustedDownloadURL("https://github.com/SagerNet/sing-box/releases/download/v1/sb.zip"); err != nil {
		t.Fatalf("official URL rejected: %v", err)
	}
	for _, bad := range []string{"http://github.com/a/b", "https://evil.example.com/a.zip", "https://github.com.evil.com/a"} {
		if err := assertTrustedDownloadURL(bad); err == nil {
			t.Fatalf("untrusted URL accepted: %s", bad)
		}
	}
}

func TestArchDetectorUsesRealPlatform(t *testing.T) {
	p := archDetector{}.Detect()
	if p.OS == "" || p.Arch == "" {
		t.Fatalf("bad platform: %+v", p)
	}
}
