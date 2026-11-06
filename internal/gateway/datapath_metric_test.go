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

// Field bug: "一键启动" reported 第 2 条命令执行失败：exit status 1；输出：对象已存在。
// Windows returns exit code 1 for `netsh ... add route` when the route is
// already installed — which is the desired state, not a failure.
func TestRouteCommandsTolerateAlreadyAppliedState(t *testing.T) {
	m := DefaultMaster()
	m.AdapterName = "NGG"
	m.CaptureAll = true

	cmds := planCaptureFor("windows", m, "10.89.0.1")
	add := cmds[len(cmds)-1]
	if !strings.Contains(add.String(), "add route") {
		t.Fatalf("last capture command should be the route add: %s", add.String())
	}
	for _, out := range []string{
		"对象已存在。",
		"对象已存在",
		"The object already exists.",
		"Object already exists.",
	} {
		if !add.tolerated(out) {
			t.Fatalf("route add must tolerate %q (already applied)", out)
		}
	}
	if add.tolerated("参数错误。") {
		t.Fatal("a genuine failure must never be swallowed")
	}

	revert := planRevertFor("windows", m, "10.89.0.1")
	if len(revert) == 0 || !strings.Contains(revert[0].String(), "delete route") {
		t.Fatalf("revert should start by deleting the route: %v", displayCommands(revert))
	}
	for _, out := range []string{"找不到元素。", "The system cannot find the file specified.", "元素不存在"} {
		if !revert[0].tolerated(out) {
			t.Fatalf("route delete must tolerate %q (already gone)", out)
		}
	}
	if revert[0].tolerated("拒绝访问。") {
		t.Fatal("access denied must still be a failure")
	}
}
