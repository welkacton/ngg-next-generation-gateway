package nodedeploy

import (
	"fmt"
	"strings"
)

// Step log parsing.
//
// The script speaks a tiny line protocol (see script.go). Parsing it here — not
// in the frontend — means the console renders exactly what the node printed,
// and an unrecognised line is kept as raw output rather than dropped.

// Step status values.
const (
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
)

// Step is one entry of the deployment timeline.
type Step struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Ready is the terminal marker of a successful run.
type Ready struct {
	NodeID    string `json:"node_id"`
	Port      int    `json:"port"`
	Mode      string `json:"mode"`
	Listening bool   `json:"listening"`
	ConfigSHA string `json:"config_sha"`
	// Probe is "up" (reached the panel), "down" (running, panel unreachable)
	// or "off" (no panel URL configured, so no probe was started).
	Probe string `json:"probe"`
}

// LogScanner is an incremental parser: the same instance serves the live job
// view and the final stored record.
type LogScanner struct {
	steps []Step
	notes []string
	ready *Ready
}

// Feed consumes one stdout line.
func (l *LogScanner) Feed(line string) {
	line = strings.TrimRight(line, "\r")
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	switch fields[0] {
	case "NGG_STEP":
		if len(fields) < 3 {
			return
		}
		idx := atoiSafe(fields[1])
		if idx <= 0 {
			return
		}
		l.steps = append(l.steps, Step{
			Index:  idx,
			Title:  strings.TrimSpace(strings.TrimPrefix(line, "NGG_STEP "+fields[1])),
			Status: StepRunning,
		})
	case "NGG_OK":
		l.setStatus(fields[1], StepDone, "")
	case "NGG_FAIL":
		reason := ""
		if len(fields) > 2 {
			reason = strings.TrimSpace(strings.TrimPrefix(line, "NGG_FAIL "+fields[1]))
		}
		l.setStatus(fields[1], StepFailed, reason)
	case "NGG_NOTE":
		l.notes = append(l.notes, strings.TrimSpace(strings.TrimPrefix(line, "NGG_NOTE")))
	case "NGG_NODE_READY":
		r := &Ready{}
		for _, f := range fields[1:] {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "node":
				r.NodeID = v
			case "port":
				r.Port = atoiSafe(v)
			case "mode":
				r.Mode = v
			case "listen":
				r.Listening = v == "1"
			case "config_sha":
				r.ConfigSHA = v
			case "probe":
				r.Probe = v
			}
		}
		l.ready = r
	}
}

func (l *LogScanner) setStatus(raw, status, detail string) {
	idx := atoiSafe(raw)
	for i := range l.steps {
		if l.steps[i].Index == idx {
			l.steps[i].Status = status
			if detail != "" {
				l.steps[i].Detail = detail
			}
			return
		}
	}
	// A marker without its NGG_STEP line (truncated stdout): keep it visible
	// instead of silently dropping the failure.
	l.steps = append(l.steps, Step{Index: idx, Title: fmt.Sprintf("步骤 %d", idx), Status: status, Detail: detail})
}

// Steps returns a copy of the timeline so far.
func (l *LogScanner) Steps() []Step {
	out := make([]Step, len(l.steps))
	copy(out, l.steps)
	return out
}

// Notes returns a copy of the operator-facing notes.
func (l *LogScanner) Notes() []string {
	out := make([]string, len(l.notes))
	copy(out, l.notes)
	return out
}

// Ready returns the terminal marker, or nil if the run never reached it.
func (l *LogScanner) Ready() *Ready {
	if l.ready == nil {
		return nil
	}
	cp := *l.ready
	return &cp
}

// ParseLog parses a completed stdout in one go.
func ParseLog(stdout string) (steps []Step, notes []string, ready *Ready) {
	l := &LogScanner{}
	for _, line := range strings.Split(stdout, "\n") {
		l.Feed(line)
	}
	return l.Steps(), l.Notes(), l.Ready()
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 1<<20 {
			return 0
		}
	}
	return n
}
