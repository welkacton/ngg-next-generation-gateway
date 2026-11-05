package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestInstallManagerLifecycle(t *testing.T) {
	m := newInstallManager()
	task, err := m.start("mihomo", "v1.19.33")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.ID == "" || task.CoreID != "mihomo" {
		t.Fatalf("unexpected task: %+v", task)
	}
	if _, err := m.start("mihomo", "v1.19.34"); err == nil {
		t.Fatal("a second install for the same core must be rejected while running")
	}
	if got, ok := m.get(task.ID); !ok || got.Status != "running" {
		t.Fatalf("get: %+v %v", got, ok)
	}
	if got, ok := m.forCore("mihomo"); !ok || got.ID != task.ID {
		t.Fatalf("forCore: %+v", got)
	}

	m.finish(task.ID, map[string]string{"version": "v1.19.33"}, nil)
	done, _ := m.get(task.ID)
	if done.Status != "succeeded" || done.FinishedAt == "" || done.Result == nil {
		t.Fatalf("finish success: %+v", done)
	}
	if _, err := m.start("mihomo", "v1.19.34"); err != nil {
		t.Fatalf("a new install must be allowed after the previous finished: %v", err)
	}
}

func TestInstallManagerReportsFailure(t *testing.T) {
	m := newInstallManager()
	task, _ := m.start("xray-core", "")
	m.finish(task.ID, nil, errors.New("GitHub API 限流"))
	got, _ := m.get(task.ID)
	if got.Status != "failed" || !strings.Contains(got.Error, "限流") {
		t.Fatalf("failure not surfaced: %+v", got)
	}
	if len(m.list()) != 1 {
		t.Fatalf("list should contain the failed task, got %d", len(m.list()))
	}
}

func TestInstallEndpointsAcceptWithoutBlockingAndReportErrors(t *testing.T) {
	ts, token := newTestServer(t)

	// Unknown kernel must fail fast with 404, not start a background task.
	resp, body := req(t, "POST", ts.URL+"/api/v1/cores/not-a-kernel/install", token, `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown kernel: %d %s", resp.StatusCode, body)
	}

	// Status endpoint is always readable and returns the documented shape.
	resp, body = req(t, "GET", ts.URL+"/api/v1/cores/install/status", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d %s", resp.StatusCode, body)
	}
	var list struct {
		Tasks []InstallTask `json:"tasks"`
		Total int           `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("status shape: %v %s", err, body)
	}

	// A real registered kernel starts a background task and returns 202 at
	// once. We do not wait for the download to finish or assert success —
	// this test only proves the request no longer blocks on the network.
	resp, body = req(t, "POST", ts.URL+"/api/v1/cores/mihomo/install", token, `{}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("install must be accepted, got %d %s", resp.StatusCode, body)
	}
	var accepted struct {
		OK      bool   `json:"ok"`
		TaskID  string `json:"task_id"`
		Status  string `json:"status"`
		CoreID  string `json:"core_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &accepted); err != nil {
		t.Fatal(err)
	}
	if !accepted.OK || accepted.TaskID == "" || accepted.Status != "running" || accepted.CoreID != "mihomo" {
		t.Fatalf("unexpected accepted payload: %+v", accepted)
	}

	// Duplicate install while running is a conflict, not a second download.
	resp, _ = req(t, "POST", ts.URL+"/api/v1/cores/mihomo/install", token, `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate install should conflict, got %d", resp.StatusCode)
	}

	resp, body = req(t, "GET", ts.URL+"/api/v1/cores/install/status?core=mihomo", token, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, accepted.TaskID) {
		t.Fatalf("core status: %d %s", resp.StatusCode, body)
	}
	resp, _ = req(t, "GET", ts.URL+"/api/v1/cores/install/status?id="+accepted.TaskID, token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("task status: %d", resp.StatusCode)
	}
	resp, _ = req(t, "GET", ts.URL+"/api/v1/cores/install/status?id=nope", token, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown task should be 404, got %d", resp.StatusCode)
	}
}
