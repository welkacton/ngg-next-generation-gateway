package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/nodeapi"
	"github.com/ngg-project/ngg/internal/nodedeploy"
	"github.com/ngg-project/ngg/internal/profiles"
)

// Node deployment: the Remnawave-style control-plane → node flow.
//
//	A POST renders the plan and a bash script the operator runs on the server
//	once. The script installs ngg-node (the control API), ngg-core (the kernel)
//	and the certificates the panel minted for this node.
//
//	After that the panel CONNECTS to the node over mutual TLS and does
//	everything else over the network: healthcheck → push the kernel config →
//	read the real host metrics. There is no SSH path here — not for execution,
//	not for reading state — and no background job runner, because a deploy is
//	now something the operator runs and the panel merely observes.
//
// Nothing is fabricated: a node is online only if it answered a real TLS
// request, and the numbers on the card are the ones it reported.

// nodeConnectBudget bounds one connect: healthcheck + config push + stats.
// Pushing a config restarts the kernel, so this is not a quick ping.
const nodeConnectBudget = 60 * time.Second

type nodeDeployRequest struct {
	Mode string `json:"mode"`
	Port int    `json:"port"`
	// NodePort is the API port the panel dials (NODE_PORT).
	NodePort  int    `json:"node_port"`
	SNI       string `json:"sni"`
	ProfileID string `json:"profile_id"`
	// Rotate re-mints the node's transport credentials. Default false so
	// re-deploying a live node never silently invalidates every configured
	// client. Certificates are always re-minted: the script carries them.
	Rotate bool `json:"rotate"`
}

func (s *Server) nodeDeployOrErr(w http.ResponseWriter) (*nodedeploy.Store, bool) {
	if s.deploy == nil {
		writeErr(w, http.StatusServiceUnavailable, "节点部署存储不可用")
		return nil, false
	}
	return s.deploy, true
}

// legacyDeployView keeps an old (v2.12 probe/SSH) plan visible in the console
// while making it impossible to mistake its script for a usable one.
type legacyDeployView struct {
	nodedeploy.View
	Legacy  bool   `json:"legacy"`
	Warning string `json:"warning"`
}

// handleNodeDeploy serves GET (last plan) and POST (render / re-render), and
// DELETE (forget the plan).
func (s *Server) handleNodeDeploy(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	store, ok := s.nodeDeployOrErr(w)
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
		if rec.Spec.NodePort < 1 {
			// v2.12 used an outbound probe and had no management port. Handing
			// that script to the operator would install a node this panel can
			// never talk to, so hide the script and say what to do instead.
			view.Script = ""
			view.LastConnect = nil
			writeJSON(w, http.StatusOK, legacyDeployView{
				View:    view,
				Legacy:  true,
				Warning: "这是旧版本（SSH / 探针）生成的部署方案，当前面板无法连接。请点「生成安装脚本」重新签发节点证书，然后在服务器上重新执行一次脚本。",
			})
			return
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
			if !errors.Is(err, nodedeploy.ErrInvalidSpec) && !errors.Is(err, nodedeploy.ErrProfileKernel) {
				code = http.StatusInternalServerError
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
			rec.LastConnect = prev.LastConnect
		}
		if err := store.Save(rec); err != nil {
			writeErr(w, http.StatusInternalServerError, "保存部署方案失败: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rec.View(true))

	case http.MethodDelete:
		if err := store.Delete(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.nodeMetrics != nil {
			s.nodeMetrics.drop(id)
		}
		writeOK(w, "部署方案已删除（节点上的服务不会被卸载，请在服务器上执行 nggctl uninstall）")

	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// buildNodeSpec merges the request with the stored plan. Re-deploying keeps the
// existing credentials unless rotate=true; switching mode drops the secrets that
// belong to the previous mode.
//
// The node's certificate is always re-minted: it is embedded in the script and
// nowhere else, so a fresh page of the plan must produce a script that works on
// its own.
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
		// Switching transport keeps the same machine and the same node id;
		// only the inbound changes.
		spec = nodedeploy.Spec{
			HostID: host.ID, HostName: host.Name, Address: host.Address,
			Mode:     mode,
			NodePort: spec.NodePort,
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
	if body.NodePort > 0 {
		spec.NodePort = body.NodePort
	} else if spec.NodePort == 0 {
		spec.NodePort = nodedeploy.DefaultNodePort
	}
	if sni := strings.TrimSpace(body.SNI); sni != "" {
		spec.SNI = sni
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

	spec, err := nodedeploy.Prepare(spec)
	if err != nil {
		return nodedeploy.Spec{}, err
	}

	// Certificates + the node daemon's provenance. Both are transient: they go
	// into the rendered script, not into the record.
	if s.pki == nil {
		return nodedeploy.Spec{}, errors.New("节点证书中心未初始化，无法生成部署方案")
	}
	leaf, err := s.pki.IssueNode(host.ID, host.Address)
	if err != nil {
		return nodedeploy.Spec{}, fmt.Errorf("签发节点证书失败: %w", err)
	}
	spec.NodeCertPEM, spec.NodeKeyPEM, spec.CAPEM = leaf.CertPEM, leaf.KeyPEM, s.pki.CAPEM()
	applyNodeBinary(&spec)
	return spec, nil
}

// applyNodeBinary points the script at the released ngg-node build and pins its
// digest when this panel was built with one (release builds always are).
//
// NGG_NODE_BIN_BASE overrides the download prefix for a mirror or an air-gapped
// install; it must still serve files named ngg-node-linux-<arch>.
func applyNodeBinary(spec *nodedeploy.Spec) {
	if base := strings.TrimSpace(envOr("NGG_NODE_BIN_BASE", "")); base != "" {
		spec.NodeBinBase = strings.TrimRight(base, "/")
	} else {
		spec.NodeBinBase = nodedeploy.DefaultNodeBinBase()
	}
	spec.NodeBinSHA256AMD64, spec.NodeBinSHA256ARM64 = nodedeploy.NodeDigests()
}

// handleNodeDeployScript returns the rendered script as a download. It contains
// node credentials, so it is only served on an authenticated request and is
// never embedded in the JSON list views.
func (s *Server) handleNodeDeployScript(w http.ResponseWriter, r *http.Request) {
	store, ok := s.nodeDeployOrErr(w)
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
	if rec.Spec.NodePort < 1 {
		writeErr(w, http.StatusConflict, "旧版本（SSH / 探针）脚本已作废：请在面板里重新生成安装脚本")
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "ngg-node-"+id+".sh"))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rec.Script))
}

// handleNodeConnect is the button the operator presses after running the
// script: the panel now dials the node.
//
// Order matters. healthcheck proves the node API is up and that it is the node
// we think it is (pinned certificate CN); start pushes the kernel config and
// waits for the kernel unit; stats closes the loop with the numbers the console
// renders. Every step can fail on its own, and the failure is reported at the
// stage it happened instead of as a generic "connect failed".
func (s *Server) handleNodeConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	store, ok := s.nodeDeployOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	rec, err := store.Load(id)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "该节点还没有生成部署方案，先生成方案并在服务器上执行脚本")
		return
	}

	var body struct {
		// PushConfig defaults to true: connecting is also how a node gets its
		// kernel config, exactly like Remnawave's start call.
		PushConfig *bool `json:"push_config"`
	}
	_ = decodeJSON(r, &body)
	push := body.PushConfig == nil || *body.PushConfig

	res := s.connectNode(r.Context(), rec, push)
	rec.LastConnect = &res
	if serr := store.Save(rec); serr != nil {
		res.Error = strings.TrimSpace(res.Error + " · 保存连接记录失败: " + serr.Error())
	}
	if s.nodeMetrics != nil && res.Health != nil {
		s.nodeMetrics.record(id, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"host_id":      id,
		"node_url":     nodeURL(rec.Spec),
		"last_connect": res,
		"spec":         rec.Spec.PublicSpec(),
		"params":       rec.Spec.ClientParams(),
	})
}

// connectNode performs the panel → node conversation and records what happened.
func (s *Server) connectNode(ctx context.Context, rec nodedeploy.Record, pushConfig bool) nodedeploy.ConnectResult {
	started := time.Now()
	res := nodedeploy.ConnectResult{At: started.UTC()}

	client, err := s.nodeClient(rec.Spec)
	if err != nil {
		res.Stage = "config"
		res.Error = err.Error()
		res.DurationMS = time.Since(started).Milliseconds()
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, nodeConnectBudget)
	defer cancel()

	health, _, err := client.Health(ctx)
	if err != nil {
		res.Stage = "health"
		res.Error = friendlyNodeError(err)
		res.DurationMS = time.Since(started).Milliseconds()
		return res
	}
	res.Health = &health

	if pushConfig {
		cfg, cerr := nodedeploy.ConfigJSON(rec.Spec)
		if cerr != nil {
			res.Stage = "config"
			res.Error = "内核配置无法渲染: " + cerr.Error()
			res.DurationMS = time.Since(started).Milliseconds()
			return res
		}
		start, _, serr := client.Start(ctx, cfg)
		if serr != nil {
			res.Stage = "start"
			res.Error = friendlyNodeError(serr)
			res.DurationMS = time.Since(started).Milliseconds()
			return res
		}
		res.ConfigPushed = true
		if start.Version != nil {
			health.XrayVersion = *start.Version
			res.Health = &health
		}
		if start.System != nil {
			res.System = start.System
		}
	}

	if res.System == nil {
		sys, _, serr := client.SystemStats(ctx)
		if serr != nil {
			res.Stage = "stats"
			res.Error = friendlyNodeError(serr)
			res.DurationMS = time.Since(started).Milliseconds()
			return res
		}
		res.System = &sys
	}

	res.OK = true
	res.DurationMS = time.Since(started).Milliseconds()
	return res
}

// nodeClient builds the pinned mTLS client for a stored plan.
// nodeClient builds a panel → node dialer from the CA-level material.
//
// It must NOT depend on the node's own certificate bytes: those are transient
// (Spec keeps them with json:"-" so only the rendered script carries them). A
// record loaded from disk after a panel restart has no such bytes, and the
// metrics poller loads records from disk on every tick. What the client really
// needs is the panel's client certificate, the CA, the address and the expected
// server CN — all of which live in the PKI store or the persisted spec.
func (s *Server) nodeClient(spec nodedeploy.Spec) (*nodeapi.Client, error) {
	if s.pki == nil {
		return nil, errors.New("节点证书中心未初始化")
	}
	// A v2.12 (probe/SSH) record has no management port at all. Refuse it
	// explicitly instead of silently dialling port 0 or claiming a cert is
	// missing when the real problem is an obsolete install script.
	if spec.NodePort < 1 || spec.NodePort > 65535 {
		return nil, errors.New("该方案是旧版本（SSH / 探针）生成的，没有节点管理端口：重新生成方案并在服务器上重新执行脚本")
	}
	certPEM, keyPEM := s.pki.ClientMaterial()
	return nodeapi.New(nodeapi.Endpoint{
		Address: spec.Address,
		Port:    spec.NodePort,
		NodeID:  spec.HostID,
		CAPEM:   s.pki.CAPEM(),
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})
}

// friendlyNodeError keeps the node's own words and adds what to do about them.
func friendlyNodeError(err error) string {
	var ne *nodeapi.Error
	if !errors.As(err, &ne) {
		return err.Error()
	}
	switch ne.Kind {
	case nodeapi.KindUnreachable:
		return ne.Msg + "。确认脚本已执行完成（nggctl status）、云安全组放通了管理端口"
	case nodeapi.KindTLS:
		return ne.Msg + "。重新生成方案并在服务器上重新执行脚本即可换发证书"
	case nodeapi.KindAuth:
		return ne.Msg + "。重新生成方案并在服务器上重新执行脚本即可恢复面板信任"
	case nodeapi.KindNode:
		return ne.Msg
	}
	return ne.Error()
}

// nodeURL is the endpoint the console shows next to the connect button.
func nodeURL(spec nodedeploy.Spec) string {
	if spec.Address == "" || spec.NodePort == 0 {
		return ""
	}
	if strings.Contains(spec.Address, ":") {
		return fmt.Sprintf("https://[%s]:%d", spec.Address, spec.NodePort)
	}
	return fmt.Sprintf("https://%s:%d", spec.Address, spec.NodePort)
}

// handleNodeStatus answers the node's live state FROM THE POLL CACHE.
//
// It never dials: the console polls this endpoint on a timer and must not wait
// for a dead node. The cache is filled by the background poller (node_metrics.go)
// and by every explicit connect, so what this returns is always a real answer
// the node gave at a recorded time.
func (s *Server) handleNodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	store, ok := s.nodeDeployOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	resp := map[string]any{
		"host_id":    id,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
		"installed":  false,
		"online":     false,
		"source":     "node-api",
	}

	rec, err := store.Load(id)
	if err != nil {
		resp["error"] = "该节点还没有生成部署方案，先生成方案并在服务器上执行脚本"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["installed"] = true
	resp["spec"] = rec.Spec.PublicSpec()
	resp["node_url"] = nodeURL(rec.Spec)
	resp["last_connect"] = rec.LastConnect

	if s.nodeMetrics != nil {
		if entry, found := s.nodeMetrics.get(id); found {
			resp["online"] = entry.Online
			resp["checked_at"] = entry.CollectedAt
			resp["latency_ms"] = entry.LatencyMS
			resp["metrics"] = entry
			if entry.Error != "" {
				resp["error"] = entry.Error
			}
			if entry.Health != nil {
				resp["health"] = entry.Health
			}
		}
	}
	if rec.LastConnect != nil && !rec.LastConnect.OK {
		resp["warning"] = "最近一次连接失败（" + rec.LastConnect.Stage + " 阶段）：" + rec.LastConnect.Error
	}
	if _, hasMetrics := resp["metrics"]; !hasMetrics {
		resp["warning"] = "还没有连上过该节点：执行脚本后点「连接节点并下发配置」"
	}
	writeJSON(w, http.StatusOK, resp)
}

// envOr reads an override without leaving an empty value in place.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
