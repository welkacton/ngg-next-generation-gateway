package gateway

import (
	"strings"
	"testing"
)

// A zero adapter metric means "highest priority" to the operator, but
// "automatic" to netsh. The plan must never emit metric=0, otherwise the
// physical NIC can keep the default route and the takeover silently fails.
func TestWindowsPlanNeverEmitsAutomaticMetric(t *testing.T) {
	m := DefaultMaster()
	m.AdapterName = "NGG"
	m.AdapterMetric = 0
	m.CaptureAll = true

	cmds := planCaptureFor("windows", m, "10.89.0.1")
	if len(cmds) != 2 {
		t.Fatalf("expected interface + route commands, got %d: %v", len(cmds), displayCommands(cmds))
	}
	for _, c := range cmds {
		line := c.String()
		if strings.Contains(line, "metric=0") {
			t.Fatalf("metric=0 means AUTOMATIC to netsh: %s", line)
		}
	}
	if !strings.Contains(cmds[0].String(), "metric=1") {
		t.Fatalf("interface metric should be pinned to 1: %s", cmds[0].String())
	}
	if !strings.Contains(cmds[1].String(), "metric=1") {
		t.Fatalf("route metric should be pinned to 1: %s", cmds[1].String())
	}

	// An explicit metric is preserved.
	m.AdapterMetric = 7
	cmds = planCaptureFor("windows", m, "10.89.0.1")
	if !strings.Contains(cmds[0].String(), "metric=7") {
		t.Fatalf("explicit metric lost: %s", cmds[0].String())
	}
}

// Without a nexthop the route step must be skipped: a guessed gateway would
// create a black hole.
func TestWindowsPlanSkipsRouteWithoutNexthop(t *testing.T) {
	m := DefaultMaster()
	m.AdapterName = "NGG"
	cmds := planCaptureFor("windows", m, "")
	if len(cmds) != 1 {
		t.Fatalf("expected only the interface metric command, got %v", displayCommands(cmds))
	}
}
