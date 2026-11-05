package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ngg-project/ngg/pkg/contracts"
)

func (s *Server) id(r *http.Request) contracts.CoreID {
	return contracts.CoreID(r.PathValue("id"))
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.SystemInfo())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.HealthSummary())
}

func (s *Server) handleListCores(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.ListCores())
}

func (s *Server) handleGetCore(w http.ResponseWriter, r *http.Request) {
	st, err := s.engine.GetCore(s.id(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Start(r.Context(), s.id(r)); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w, "started")
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Stop(r.Context(), s.id(r)); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w, "stopped")
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Restart(r.Context(), s.id(r)); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w, "restarted")
}

type versionBody struct {
	Version string `json:"version"`
}

// handleInstall now lives in installs.go as an asynchronous background task;
// the synchronous path remains available via ?wait=1 for scripts and tests.

func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Uninstall(r.Context(), s.id(r)); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w, "uninstalled (config and backups retained)")
}

func (s *Server) handleVersionOp(w http.ResponseWriter, r *http.Request) {
	var body versionBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Version) == "" {
		writeErr(w, http.StatusBadRequest, "version is required")
		return
	}
	id := s.id(r)
	if strings.HasSuffix(r.URL.Path, "/rollback") {
		if err := s.engine.RollbackVersion(r.Context(), id, body.Version); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeOK(w, "rolled back to "+body.Version)
		return
	}
	info, err := s.engine.Install(r.Context(), id, body.Version)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, contracts.OperationResult{OK: true, Message: "upgraded to " + info.Version, Data: info})
}

func (s *Server) handleAvailableVersions(w http.ResponseWriter, r *http.Request) {
	v, err := s.engine.AvailableVersions(r.Context(), s.id(r))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleInstalledVersion(w http.ResponseWriter, r *http.Request) {
	v, err := s.engine.InstalledVersion(r.Context(), s.id(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type batchBody struct {
	IDs       []contracts.CoreID `json:"ids"`
	Exclusive bool               `json:"exclusive"`
}

func (s *Server) handleBatchStart(w http.ResponseWriter, r *http.Request) {
	var body batchBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": s.engine.BatchStart(r.Context(), body.IDs, body.Exclusive)})
}

func (s *Server) handleBatchStop(w http.ResponseWriter, r *http.Request) {
	var body batchBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": s.engine.BatchStop(r.Context(), body.IDs)})
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.engine.Config(s.id(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type configBody struct {
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	var body configBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	b, err := s.engine.ApplyConfig(r.Context(), s.id(r), []byte(body.Content), body.Reason)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "配置已备份、原生校验通过并替换生效", "backup": b})
}

func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	var body configBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	out, err := s.engine.ValidateConfig(r.Context(), s.id(r), []byte(body.Content))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "output": out, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (s *Server) handleConfigBackups(w http.ResponseWriter, r *http.Request) {
	v, err := s.engine.ConfigBackups(s.id(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BackupID string `json:"backup_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.BackupID == "" {
		writeErr(w, http.StatusBadRequest, "backup_id is required")
		return
	}
	if err := s.engine.RollbackConfig(s.id(r), body.BackupID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, "配置已回滚并通过原生校验")
}

func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	b, err := s.engine.Config(s.id(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	name := filepath.Base(fmt.Sprint(b["path"]))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, fmt.Sprint(b["content"]))
}

func (s *Server) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "multipart parse: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "form field 'file' is required")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := s.engine.ApplyConfig(r.Context(), s.id(r), content, "api.import")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "导入已备份、原生校验通过并替换生效", "backup": b})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, _ := strconv.ParseUint(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	id := s.id(r)
	if q.Get("id") != "" {
		id = contracts.CoreID(q.Get("id"))
	}
	writeJSON(w, http.StatusOK, s.engine.Logs(id, after, limit))
}

func (s *Server) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	s.engine.ClearLogs(s.id(r))
	writeOK(w, "logs cleared")
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.engine.Stats(s.id(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleRegistry(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Registry())
}

func (s *Server) handleRegistryReload(w http.ResponseWriter, r *http.Request) {
	n, problems := s.engine.ReloadRegistry()
	writeJSON(w, http.StatusOK, map[string]any{"loaded": n, "problems": problems})
}

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Ports())
}

func (s *Server) handlePortCheck(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("ports")
	var ports []int
	for _, part := range strings.Split(raw, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n > 0 {
			ports = append(ports, n)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": s.engine.PortCheck(ports)})
}

func (s *Server) handleAllBackups(w http.ResponseWriter, r *http.Request) {
	v, err := s.engine.AllBackups()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v, err := s.engine.Audit(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleStatic serves the built Vue app. If web/dist has not been built yet,
// it serves an honest status page instead of a fake dashboard.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	root := s.cfg.Server.WebDir
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8">
<title>NGG — Next-Generation Gateway</title>
<body style="background:#101418;color:#d8e2ea;font:15px/1.7 system-ui;padding:48px;max-width:760px">
<h1 style="color:#36adcb">NGG · 次世代网关</h1>
<p>Phase 1 后端服务已运行。Web 管理界面尚未构建。</p>
<pre style="background:#182028;padding:16px;border-radius:8px;overflow:auto">
cd web
npm install
npm run build
</pre>
<p>构建完成后刷新本页。API 文档见 <code>docs/02-api.md</code>，健康检查：
<a style="color:#36adcb" href="/api/v1/health">/api/v1/health</a></p>
<p style="opacity:.65">Stage 1：统一管理中心 + 内核引擎（Mihomo / Xray-core / sing-box）。
统一 TUN、规则引擎、DNS 引擎属于 Stage 2/3 接口预留。</p>
</body></html>`)
		return
	}
	path := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
	if r.URL.Path == "/" {
		path = filepath.Join(root, "index.html")
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		path = filepath.Join(root, "index.html") // SPA fallback
	}
	http.ServeFile(w, r, path)
}
