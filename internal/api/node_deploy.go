package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/agent"
	"github.com/ngg-project/ngg/internal/hostmon"
	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/nodedeploy"
	"github.com/ngg-project/ngg/internal/profiles"
)

// Node deployment: the Remnawave-style "control plane → node" flow, with the
// Nezha-style report channel.
//
// A POST generates (or reuses) the deployment spec — port, mode, credentials,
// probe identity — renders the bash script and stores both, then optionally
// EXECUTES the script on the node over the SSH credentials the operator already
// saved for monitoring. Execution is asynchronous: the console polls the job
// and renders the step log while it runs, because installing a kernel on a
// 1-vCPU VPS takes long enough to outlive any HTTP client timeout.
//
// Reading the node afterwards does NOT use SSH. The script installs ngg-probe
// on the node, which POSTs metrics + node state to /v1/agent/report every 10s;
// the status endpoint reads that registry. The panel never dials into the node.
//
// Nothing here fabricates progress: every step in the response came from a
// NGG_STEP / NGG_OK / NGG_FAIL line the node printed.

const (
	// nodeDeployBudget bounds one execution. Downloading Xray + apt on a small
	// VPS takes about a minute; six minutes is generous without hanging forever.
	nodeDeployBudget = 6 * time.Minute
	// storedOutputKeep is how much raw output is persisted with a run.
	storedOutputKeep = 16 << 10
)

type nodeDeployRequest struct {
	Mode      string `json:"mode"`
	Port      int    `json:"port"`
	SNI       string `json:"sni"`
	ProfileID string `json:"profile_id"`
	// PanelURL is a pointer so "field omitted" (keep the stored value) and
	// "explicitly empty" (stop the probe) are different requests.
	PanelURL *string `json:"panel_url"`
	// Rotate re-mints the node credentials. Default false so re-deploying a
	// live node never silently invalidates every configured client.
	Rotate bool `json:"rotate"`
	// Execute runs the script over SSH instead of only returning it.
	Execute bool `json:"execute"`
}

func (s *Server) nodeDeployOrErr(w http.ResponseWriter) (*nodedeploy.Store, *nodeDeployJobs, bool) {
	if s.deploy == nil || s.deployJobs == nil {
		writeErr(w, http.StatusServiceUnavailable, "节点部署存储不可用")
		return nil, nil, false
	}
	return s.deploy, s.deployJobs, true
}

// handleNodeDeploy serves GET (last plan) and POST (generate / deploy).
func (s *Server) handleNodeDeploy(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	store, jobs, ok := s.nodeDeployOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	host, found := reg.Get(id)
	if !found {
		writeErr(w, http.StatusNotFound, "节点不存在: "+id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rec, err := store.Load(id)
		if err != nil {
			if errors.Is(err, nodedeploy.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "该节点尚未生成部署方案")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		view := rec.View(true)
		if job, running := jobs.get(id); running {
			run := job.snapshot()
			view.LastRun = &run
		}
		writeJSON(w, http.StatusOK, view)

	case http.MethodPost:
		var body nodeDeployRequest
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		spec, err := s.buildNodeSpec(w, host, body, store)
		if err != nil {
			code := http.StatusUnprocessableEntity
			if errors.Is(err, nodedeploy.ErrInvalidSpec) || errors.Is(err, nodedeploy.ErrProfileKernel) {
				code = http.StatusUnprocessableEntity
			}
			writeErr(w, code, err.Error())
			return
		}
		script, err := nodedeploy.Script(spec)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		rec := nodedeploy.Record{HostID: host.ID, Spec: spec, Script: script, GeneratedAt: spec.GeneratedAt}
		if prev, perr := store.Load(host.ID); perr == nil {
			rec.LastRun = prev.LastRun
		}
		if err := store.Save(rec); err != nil {
			writeErr(w, http.StatusInternalServerError, "保存部署方案失败: "+err.Error())
			return
		}

		if !body.Execute {
			writeJSON(w, http.StatusOK, rec.View(true))
			return
		}

		if s.monitor == nil {
			writeErr(w, http.StatusServiceUnavailable, "SSH 监控未初始化，无法在线部署")
			return
		}
		cfg, ok := s.monitor.Config(host.ID)
		if !ok {
			writeErr(w, http.StatusUnprocessableEntity,
				"该节点还没有保存 SSH 凭据，无法在线部署；先配置 SSH 监控或改用脚本方式部署")
			return
		}
		job, started := jobs.begin(host.ID, spec)
		if !started {
			writeErr(w, http.StatusConflict, "该节点已有部署任务在运行")
			return
		}
		go s.runNodeDeploy(host.ID, cfg, script, job)
		run := job.snapshot()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"host_id": host.ID,
			"spec":    spec.PublicSpec(),
			"params":  spec.ClientParams(),
			"job":     run,
		})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// buildNodeSpec merges the request with the stored plan. Re-deploying keeps the
// existing credentials unless rotate=true; switching mode drops the secrets that
// belong to the previous mode but keeps the node token that binds it to us.
func (s *Server) buildNodeSpec(w http.ResponseWriter, host hosts.Host, body nodeDeployRequest, store *nodedeploy.Store) (nodedeploy.Spec, error) {
	prev, prevErr := store.Load(host.ID)
	hasPrev := prevErr == nil

	spec := nodedeploy.Spec{HostID: host.ID, HostName: host.Name, Address: host.Address}
	if hasPrev {
		spec = prev.Spec
		spec.HostID = host.ID
		spec.HostName = host.Name
		spec.Address = host.Address
	}

	if mode := strings.ToLower(strings.TrimSpace(body.Mode)); mode != "" && mode != spec.Mode {
		// Switching transport keeps the reporting identity: it is the same
		// machine and the same probe, only the inbound changed.
		spec = nodedeploy.Spec{
			HostID: host.ID, HostName: host.Name, Address: host.Address,
			Mode:       mode,
			AgentID:    spec.AgentID,
			AgentToken: spec.AgentToken,
			PanelURL:   spec.PanelURL,
		}
	}
	if spec.Mode == "" {
		spec.Mode = nodedeploy.ModeReality
	}
	if body.Rotate {
		spec.UUID, spec.RealityPrivate, spec.RealityPublic = "", "", ""
		spec.ShortID, spec.Password = "", ""
	}
	if body.Port > 0 {
		spec.Port = body.Port
	} else if spec.Port == 0 {
		spec.Port = host.NodePort
		if spec.Port == 0 {
			spec.Port = nodedeploy.DefaultPort
		}
	}
	if sni := strings.TrimSpace(body.SNI); sni != "" {
		spec.SNI = sni
	}
	if body.PanelURL != nil {
		spec.PanelURL = strings.TrimRight(strings.TrimSpace(*body.PanelURL), "/")
	}
	// Profile binding: the profile's rendered content becomes the kernel config.
	if pid := strings.TrimSpace(body.ProfileID); pid != "" {
		profStore, ok := s.profilesOrErr(w)
		if !ok {
			return nodedeploy.Spec{}, errors.New("配置文件存储不可用")
		}
		prof, found := profStore.Get(pid)
		if !found {
			return nodedeploy.Spec{}, fmt.Errorf("配置文件不存在: %s", pid)
		}
		content, _, rerr := profiles.Render(prof)
		if rerr != nil {
			return nodedeploy.Spec{}, fmt.Errorf("配置文件无法渲染: %w", rerr)
		}
		spec.Mode = nodedeploy.ModeProfile
		spec.ProfileID = prof.ID
		spec.ProfileName = prof.Name
		spec.Kernel = prof.Kernel
		spec.ProfileContent = content
	} else if spec.Mode == nodedeploy.ModeProfile && strings.TrimSpace(spec.ProfileContent) == "" {
		return nodedeploy.Spec{}, fmt.Errorf("部署方案绑定的文件内容已丢失，请重新选择配置文件")
	}

	// Report identity: the node must be able to push its state to us without
	// us ever dialling in, so every plan carries an agent id + token.
	// An explicitly empty panel_url (operator cleared the field) must stay
	// empty instead of being refilled from the agent-server public_url.
	allowDefaultURL := body.PanelURL == nil || strings.TrimSpace(*body.PanelURL) != ""
	if err := s.ensureNodeAgent(&spec, body.Rotate, allowDefaultURL); err != nil {
		return nodedeploy.Spec{}, err
	}

	return nodedeploy.Prepare(spec)
}

// ensureNodeAgent makes sure the spec holds a probe identity that exists in the
// agent registry, and that the registry record matches this node (name, address,
// data port) so the 远程节点 list and the node card cannot drift apart.
//
// The plaintext token is only ever returned by Create / RotateToken, so it is
// kept in the deployment record (0600, same trust boundary as the REALITY
// private key). An existing token is reused unless rotate is requested —
// rotating it silently would cut a healthy node off from the panel.
func (s *Server) ensureNodeAgent(spec *nodedeploy.Spec, rotate, allowDefaultURL bool) error {
	reg := s.engine.Agents()
	if reg == nil {
		return errors.New("远程代理注册表不可用，无法为节点分配上报身份")
	}
	if spec.PanelURL == "" && allowDefaultURL {
		if cfg := s.engine.AgentServerConfig(); cfg != nil {
			spec.PanelURL = cfg.PublicBase()
		}
	}

	if spec.AgentID != "" {
		if view, found := reg.Get(spec.AgentID); found {
			if rotate || spec.AgentToken == "" {
				token, err := reg.RotateToken(spec.AgentID)
				if err != nil {
					return fmt.Errorf("轮换探针令牌失败: %w", err)
				}
				spec.AgentToken = token
			}
			name := spec.HostName
			if strings.TrimSpace(name) == "" {
				name = spec.HostID
			}
			if view.Port != spec.Port || view.Address != spec.Address || view.Name != name {
				port := spec.Port
				_, _ = reg.Update(spec.AgentID, agent.Patch{
					Name: &name, Address: &spec.Address, Port: &port,
				})
			}
			return nil
		}
	}

	name := strings.TrimSpace(spec.HostName)
	if name == "" {
		name = spec.HostID
	}
	view, token, err := reg.Create(name, spec.Address, spec.Port, "节点部署自动创建（ngg-probe）", agent.RoleBoth)
	if err != nil {
		return fmt.Errorf("创建探针身份失败: %w", err)
	}
	spec.AgentID, spec.AgentToken = view.ID, token
	return nil
}

// handleNodeDeployScript returns the rendered script as a download. It contains
// node credentials, so it is only served on an authenticated request and is
// never embedded in the JSON list views.
func (s *Server) handleNodeDeployScript(w http.ResponseWriter, r *http.Request) {
	store, _, ok := s.nodeDeployOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	rec, err := store.Load(id)
	if err != nil {
		if errors.Is(err, nodedeploy.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "该节点尚未生成部署方案")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "ngg-node-"+id+".sh"))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rec.Script))
}

// handleNodeDeployJob returns the live (or last) run of the deployment script.
func (s *Server) handleNodeDeployJob(w http.ResponseWriter, r *http.Request) {
	store, jobs, ok := s.nodeDeployOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if job, running := jobs.get(id); running {
		writeJSON(w, http.StatusOK, job.snapshot())
		return
	}
	rec, err := store.Load(id)
	if err != nil || rec.LastRun == nil {
		writeJSON(w, http.StatusOK, nodedeploy.Run{State: "idle", Steps: []nodedeploy.Step{}})
		return
	}
	writeJSON(w, http.StatusOK, *rec.LastRun)
}

// handleNodeStatus answers the node's live state WITHOUT touching the node.
//
// Two sources, both real and both labelled as such in the response:
//   - the stored plan (what the panel rendered and shipped: mode, port, URL)
//   - the agent registry, filled by the node's own probe reports
//
// There is deliberately no SSH path here. If the probe never reported, the
// response says so ("never") instead of guessing a state.
func (s *Server) handleNodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	store, _, ok := s.nodeDeployOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	resp := map[string]any{
		"host_id":    id,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
		"installed":  false,
		"source":     "agent",
	}

	rec, err := store.Load(id)
	if err != nil {
		resp["error"] = "该节点还没有生成部署方案，先执行一次部署"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	spec := rec.Spec.PublicSpec()
	resp["installed"] = true
	resp["spec"] = spec
	resp["report_url"] = spec.PanelURL
	resp["agent_id"] = spec.AgentID

	reg := s.engine.Agents()
	if reg == nil || spec.AgentID == "" {
		resp["verified"] = false
		resp["error"] = "该方案还没有上报身份（旧版本生成的方案），重新生成一次即可"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	view, found := reg.Get(spec.AgentID)
	if !found {
		resp["verified"] = false
		resp["error"] = "探针身份已不在远程节点列表（可能被删除）：重新生成方案会分配新的身份"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp["verified"] = view.Bound
	resp["online"] = view.Online
	resp["status"] = view.Status
	resp["last_seen"] = view.LastSeen
	resp["hostname"] = view.Hostname
	resp["os"] = view.OS
	resp["arch"] = view.Arch
	resp["agent_version"] = view.AgentVersion
	resp["metrics"] = view.Metrics
	resp["node"] = view.Node

	switch {
	case spec.PanelURL == "":
		resp["warning"] = "方案里没有面板上报地址：探针不会启动。填一个节点能访问到的地址后重新部署即可"
	case !view.Bound:
		resp["warning"] = "探针从未上报：确认节点上 ngg-probe.service 正在运行，且节点能访问 " + spec.PanelURL
	case !view.Online:
		resp["warning"] = "探针已离线（最后上报 " + view.LastSeen + "）"
	case view.Node == nil:
		resp["warning"] = "探针已上报，但没带节点段：节点清单 /etc/ngg/node.json 可能被删除了"
	case view.Node.UnitState != "active":
		resp["warning"] = "节点单元状态为 " + view.Node.UnitState + "，探针仍在运行"
	case !view.Node.Listening:
		resp["warning"] = "探针报告端口未监听：节点的内核可能没有在跑"
	}
	writeJSON(w, http.StatusOK, resp)
}

/* ------------------------------------------------------------------ *
 * 后台执行
 * ------------------------------------------------------------------ */

// nodeDeployJobs tracks one running deployment per host. The finished job stays
// in the map so a poll after completion still returns the final step log.
type nodeDeployJobs struct {
	mu sync.Mutex
	m  map[string]*nodeDeployJob
}

func newNodeDeployJobs() *nodeDeployJobs {
	return &nodeDeployJobs{m: map[string]*nodeDeployJob{}}
}

func (js *nodeDeployJobs) begin(hostID string, spec nodedeploy.Spec) (*nodeDeployJob, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	if cur, ok := js.m[hostID]; ok && !cur.done() {
		return nil, false
	}
	j := &nodeDeployJob{
		run: nodedeploy.Run{
			ID:        fmt.Sprintf("%s-%d", hostID, time.Now().UnixNano()),
			State:     nodedeploy.RunRunning,
			StartedAt: time.Now().UTC(),
			Steps:     []nodedeploy.Step{},
			Notes:     []string{},
		},
		scanner: &nodedeploy.LogScanner{},
		spec:    spec,
	}
	js.m[hostID] = j
	return j, true
}

func (js *nodeDeployJobs) get(hostID string) (*nodeDeployJob, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	j, ok := js.m[hostID]
	return j, ok
}

type nodeDeployJob struct {
	mu      sync.Mutex
	run     nodedeploy.Run
	scanner *nodedeploy.LogScanner
	spec    nodedeploy.Spec
	cancel  context.CancelFunc
	running bool
}

func (j *nodeDeployJob) attach(cancel context.CancelFunc) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cancel = cancel
	j.running = true
}

func (j *nodeDeployJob) done() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return !j.running && j.run.State != nodedeploy.RunRunning
}

// feed consumes one stdout line while the script runs. It is called from the
// SSH reader goroutine, so it must stay under the job mutex.
func (j *nodeDeployJob) feed(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.scanner.Feed(line)
	j.run.Steps = j.scanner.Steps()
	j.run.Notes = j.scanner.Notes()
	if ready := j.scanner.Ready(); ready != nil {
		j.run.Ready = ready
	}
}

func (j *nodeDeployJob) snapshot() nodedeploy.Run {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := j.run
	cp.Steps = j.copySteps()
	cp.Notes = append([]string{}, j.run.Notes...)
	return cp
}

func (j *nodeDeployJob) copySteps() []nodedeploy.Step {
	out := make([]nodedeploy.Step, len(j.run.Steps))
	copy(out, j.run.Steps)
	return out
}

// finish settles the run. A script that exits 0 without the ready marker is a
// failure: the panel must not call a node "deployed" on the strength of an exit
// code alone.
func (j *nodeDeployJob) finish(res hostmon.RunResult, err error) nodedeploy.Run {
	j.mu.Lock()
	defer j.mu.Unlock()

	finished := time.Now().UTC()
	j.run.FinishedAt = &finished
	j.run.DurationMS = res.DurationMS
	j.run.ExitCode = res.ExitCode
	j.run.Stdout = clip(res.Stdout, storedOutputKeep)
	j.run.Stderr = clip(res.Stderr, 4<<10)
	j.running = false

	switch {
	case err != nil:
		j.run.State = nodedeploy.RunFailed
		j.run.Error = err.Error()
	case res.ExitCode != 0:
		j.run.State = nodedeploy.RunFailed
		j.run.Error = fmt.Sprintf("脚本以退出码 %d 结束", res.ExitCode)
	case j.run.Ready == nil:
		j.run.State = nodedeploy.RunFailed
		j.run.Error = "脚本结束但没有输出就绪标记 NGG_NODE_READY"
	default:
		j.run.State = nodedeploy.RunDone
	}
	return j.snapshotLocked()
}

func (j *nodeDeployJob) snapshotLocked() nodedeploy.Run {
	cp := j.run
	cp.Steps = j.copySteps()
	cp.Notes = append([]string{}, j.run.Notes...)
	return cp
}

func (s *Server) runNodeDeploy(hostID string, cfg hostmon.SSHConfig, script string, job *nodeDeployJob) {
	ctx, cancel := context.WithTimeout(context.Background(), nodeDeployBudget)
	defer cancel()
	job.attach(cancel)

	res, err := s.monitor.RunScriptStream(ctx, cfg, script, job.feed)
	run := job.finish(res, err)

	if rec, lerr := s.deploy.Load(hostID); lerr == nil {
		rec.LastRun = &run
		if serr := s.deploy.Save(rec); serr != nil {
			// Not fatal for the deployment itself, but the console would lose
			// the log, so surface it in the job error.
			run.Error = strings.TrimSpace(run.Error + " 保存运行记录失败: " + serr.Error())
		}
	}

	// Keep the host registry in step with what is actually running, so the node
	// card and the subscription renderer show the deployed protocol.
	if run.State == nodedeploy.RunDone {
		if reg := s.engine.Hosts(); reg != nil {
			if host, ok := reg.Get(hostID); ok {
				host.Protocol = job.spec.Label()
				if job.spec.Mode == nodedeploy.ModeProfile {
					host.Profile = job.spec.ProfileID
				}
				if _, uerr := reg.Update(hostID, host); uerr != nil {
					_ = uerr // the deployment succeeded; metadata can be fixed later
				}
			}
		}
	}
}

// clip keeps the tail of s: the beginning of a kernel install log is noise, the
// end is where the failure is.
func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return "…(前段已截断)\n" + s[len(s)-limit:]
}
