package netengine

import (
	"errors"
	"testing"
)

// The console used to show a drop count roughly twice the real one: the
// dispatcher incremented Dropped AND Engine.reject() incremented it again for
// the same failed session. Every drop is now counted exactly once, under a
// reason the UI can display.
func TestRejectCountsEachDropExactlyOnce(t *testing.T) {
	e := newTestEngine(t, freePort(t), freePort(t))

	cases := []struct {
		name   string
		err    error
		bucket func() int64
		want   int64
	}{
		{"block", &DropError{Reason: "block", Err: errors.New("rule rejected")}, func() int64 { return e.metrics.DropBlocked.Load() }, 1},
		{"loop", &DropError{Reason: "loop", Err: errors.New("loop detected")}, func() int64 { return e.metrics.DropLoop.Load() }, 1},
		{"no_kernel", &DropError{Reason: "no_kernel", Err: errors.New("no kernel")}, func() int64 { return e.metrics.DropNoKernel.Load() }, 1},
		{"session", &DropError{Reason: "session", Err: errors.New("session")}, func() int64 { return e.metrics.DropSession.Load() }, 1},
		{"rule", &DropError{Reason: "rule", Err: errors.New("rule")}, func() int64 { return e.metrics.DropRule.Load() }, 1},
		{"unclassified", errors.New("dial tcp: connection refused"), func() int64 { return e.metrics.DropDial.Load() }, 1},
	}

	for i, c := range cases {
		_ = e.reject(ConnRequest{Dst: "192.0.2.10:80"}, c.err)
		if got := c.bucket(); got != c.want {
			t.Fatalf("%s: bucket=%d want %d", c.name, got, c.want)
		}
		if got := e.metrics.Dropped.Load(); got != int64(i+1) {
			t.Fatalf("%s: total Dropped=%d want %d (double counted?)", c.name, got, i+1)
		}
	}

	snap := e.metrics.Snapshot()
	reasons, ok := snap["drop_reasons"].(map[string]int64)
	if !ok {
		t.Fatalf("snapshot has no drop_reasons: %v", snap)
	}
	for _, key := range []string{"blocked", "loop", "no_kernel", "session", "rule", "dial"} {
		if reasons[key] != 1 {
			t.Fatalf("drop_reasons[%s]=%d want 1 (all=%v)", key, reasons[key], reasons)
		}
	}
}

// A blocked decision must carry its reason out of the dispatcher, otherwise the
// caller can only guess why the connection died.
func TestDispatchBlockCarriesDropReason(t *testing.T) {
	e := newTestEngine(t, freePort(t), freePort(t))
	_, _, err := e.dispatcher.OpenWithDecision(
		t.Context(),
		Session{Network: "tcp"},
		RouteDecision{Block: true},
		"192.0.2.10:80",
	)
	if err == nil {
		t.Fatal("blocked decision must fail")
	}
	if got := DropReasonOf(err); got != "block" {
		t.Fatalf("reason=%q want block", got)
	}
	// The dispatcher itself must not have counted it: reject() owns the count.
	if e.metrics.Dropped.Load() != 0 {
		t.Fatalf("dispatcher counted a drop on its own: %d", e.metrics.Dropped.Load())
	}
}
