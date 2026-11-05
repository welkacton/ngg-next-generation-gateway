package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// Kernel installs download tens of megabytes, verify SHA256, extract and run
// the binary's own version command. That routinely takes longer than the Web
// console's 30s HTTP timeout. The frontend already listens for "core.install"
// WebSocket progress events, so installs are real background tasks: the POST
// returns immediately with a task id and the UI polls /cores/install/status.
// Nothing is faked — the task runs the exact same engine.Install path.

type InstallTask struct {
	ID         string `json:"id"`
	CoreID     string `json:"core_id"`
	Version    string `json:"version"`
	Status     string `json:"status"` // running | succeeded | failed
	Message    string `json:"message"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

type installManager struct {
	mu    sync.RWMutex
	tasks map[string]*InstallTask
	byKey map[string]string // core id -> latest task id
	seq   int
}

func newInstallManager() *installManager {
	return &installManager{tasks: map[string]*InstallTask{}, byKey: map[string]string{}}
}

func (m *installManager) start(coreID, version string) (*InstallTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[coreID]; ok {
		if t := m.tasks[id]; t != nil && t.Status == "running" {
			return nil, fmt.Errorf("内核 %s 的安装任务仍在进行中（%s）", coreID, id)
		}
	}
	m.seq++
	now := time.Now().UTC()
	t := &InstallTask{
		ID:     fmt.Sprintf("install-%d-%d", now.UnixNano(), m.seq),
		CoreID: coreID, Version: version, Status: "running",
		Message:   "安装任务已提交，正在查询官方 Release…",
		StartedAt: now.Format(time.RFC3339),
	}
	m.tasks[t.ID] = t
	m.byKey[coreID] = t.ID
	return t, nil
}

func (m *installManager) finish(id string, result any, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return
	}
	t.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		t.Status, t.Error, t.Message = "failed", err.Error(), "安装失败: "+err.Error()
	} else {
		t.Status, t.Result, t.Message = "succeeded", result, "安装完成"
	}
}

func (m *installManager) get(id string) (InstallTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if t := m.tasks[id]; t != nil {
		return *t, true
	}
	return InstallTask{}, false
}

func (m *installManager) forCore(coreID string) (InstallTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byKey[coreID]
	if !ok {
		return InstallTask{}, false
	}
	if t := m.tasks[id]; t != nil {
		return *t, true
	}
	return InstallTask{}, false
}

func (m *installManager) list() []InstallTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]InstallTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, *t)
	}
	return out
}

// POST /cores/{id}/install — start a background install and return at once.
// The synchronous behaviour is still available with ?wait=1 for CLI/scripts.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var body versionBody
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	id := s.id(r)
	version := strings.TrimSpace(body.Version)
	registered := false
	for _, d := range s.engine.Registry() {
		if d.ID == id {
			registered = true
			break
		}
	}
	if !registered {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("kernel %q is not registered", id))
		return
	}

	// Legacy synchronous mode kept for scripts and tests.
	if r.URL.Query().Get("wait") == "1" {
		info, err := s.engine.Install(r.Context(), id, version)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, contracts.OperationResult{OK: true, Message: "installed " + info.Version, Data: info})
		return
	}

	task, err := s.installs.start(string(id), version)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	// Detach from the request context: the browser will disconnect as soon as
	// it gets the response, but the install must keep going.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		info, installErr := s.engine.Install(ctx, id, version)
		if installErr != nil {
			s.installs.finish(task.ID, nil, installErr)
			return
		}
		s.installs.finish(task.ID, info, nil)
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "accepted": true, "task_id": task.ID,
		"status": "running", "core_id": string(id),
		"message": "安装任务已在后台启动；进度通过 WebSocket 实时推送，可轮询 /cores/install/status 获取结果。",
	})
}

// GET /cores/install/status[?id=task-id][&core=mihomo] — poll task results.
func (s *Server) handleInstallStatus(w http.ResponseWriter, r *http.Request) {
	if id := r.URL.Query().Get("id"); id != "" {
		t, ok := s.installs.get(id)
		if !ok {
			writeErr(w, http.StatusNotFound, "install task "+id+" not found")
			return
		}
		writeJSON(w, http.StatusOK, t)
		return
	}
	if core := r.URL.Query().Get("core"); core != "" {
		t, ok := s.installs.forCore(core)
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"tasks": []InstallTask{}, "total": 0})
			return
		}
		writeJSON(w, http.StatusOK, t)
		return
	}
	tasks := s.installs.list()
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "total": len(tasks)})
}
