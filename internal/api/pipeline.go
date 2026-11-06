package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/pipeline"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// inputs assembles a snapshot of every Config Engine page that feeds the chain.
func (s *Server) pipelineInputs(cfg pipeline.Config) pipeline.Inputs {
	in := pipeline.Inputs{}
	if e := s.engine; e != nil {
		if reg := e.Hosts(); reg != nil {
			in.Hosts = reg.List("")
		}
		if reg := e.RuleSets(); reg != nil {
			in.RuleSets = reg.List()
		}
		if reg := e.Users(); reg != nil {
			in.ActiveUsers = reg.Stats().Active
		}
		if ne := e.Network(); ne != nil {
			ncfg := ne.Config()
			in.DNSUpstream = ncfg.DNS.Upstreams
			in.DNSEnabled = ncfg.DNS.Enabled
		}
		st, err := e.GetCore(contracts.CoreID(cfg.Kernel))
		if err == nil {
			in.KernelReady = st.Installed
			in.KernelState = string(st.State)
			// A running kernel already owns the port; do not report its own
			// listener as "occupied by someone else".
			running := st.State == contracts.StateRunning || st.State == contracts.StateStarting
			in.PortFree = func(addr string) bool {
				if running {
					return true
				}
				ln, err := net.Listen("tcp", addr)
				if err != nil {
					return false
				}
				_ = ln.Close()
				return true
			}
		}
	}
	if in.PortFree == nil {
		in.PortFree = func(string) bool { return true }
	}
	return in
}

type pipelineView struct {
	Config  pipeline.Config `json:"config"`
	Steps   []pipeline.Step `json:"steps"`
	Blocked bool            `json:"blocked"`
	Warning int             `json:"warning_count"`
	Path    string          `json:"store_path"`
}

func (s *Server) pipelineView() pipelineView {
	cfg := s.pipeline.Get()
	steps := pipeline.Validate(cfg, s.pipelineInputs(cfg))
	warn := 0
	for _, st := range steps {
		if st.Status == "warn" {
			warn++
		}
	}
	_, blocked := pipeline.HasBlockingFailure(steps)
	return pipelineView{
		Config: cfg, Steps: steps, Blocked: blocked,
		Warning: warn, Path: s.pipeline.Path(),
	}
}

// GET /api/v1/pipeline
func (s *Server) handlePipelineGet(w http.ResponseWriter, r *http.Request) {
	if s.pipeline == nil {
		writeErr(w, http.StatusServiceUnavailable, "pipeline store unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.pipelineView())
}

// PUT /api/v1/pipeline
func (s *Server) handlePipelinePut(w http.ResponseWriter, r *http.Request) {
	if s.pipeline == nil {
		writeErr(w, http.StatusServiceUnavailable, "pipeline store unavailable")
		return
	}
	var body pipeline.Config
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	saved, err := s.pipeline.Set(body)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": saved, "view": s.pipelineView()})
}

// POST /api/v1/pipeline/validate
func (s *Server) handlePipelineValidate(w http.ResponseWriter, r *http.Request) {
	if s.pipeline == nil {
		writeErr(w, http.StatusServiceUnavailable, "pipeline store unavailable")
		return
	}
	cfg := s.pipeline.Get()
	var body struct {
		Config *pipeline.Config `json:"config"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if body.Config != nil {
			cfg = *body.Config
		}
	}
	steps := pipeline.Validate(cfg, s.pipelineInputs(cfg))
	blocking, blocked := pipeline.HasBlockingFailure(steps)
	writeJSON(w, http.StatusOK, map[string]any{
		"steps": steps, "blocked": blocked, "blocking_step": blocking,
	})
}

// POST /api/v1/pipeline/render — dry run: full mihomo YAML, or an honest
// official-syntax JSON skeleton for sing-box / xray-core.
func (s *Server) handlePipelineRender(w http.ResponseWriter, r *http.Request) {
	if s.pipeline == nil {
		writeErr(w, http.StatusServiceUnavailable, "pipeline store unavailable")
		return
	}
	cfg := s.pipeline.Get()
	content, warnings, err := pipeline.Render(cfg, s.pipelineInputs(cfg))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "content": content, "warnings": warnings, "lines": strings.Count(content, "\n"),
	})
}

// POST /api/v1/pipeline/apply — validate → render → native check → replace →
// (re)start the kernel. Refuses to touch the running kernel on a hard failure.
func (s *Server) handlePipelineApply(w http.ResponseWriter, r *http.Request) {
	if s.pipeline == nil {
		writeErr(w, http.StatusServiceUnavailable, "pipeline store unavailable")
		return
	}
	ctx := r.Context()
	cfg := s.pipeline.Get()
	in := s.pipelineInputs(cfg)
	steps := pipeline.Validate(cfg, in)
	if bad, blocked := pipeline.HasBlockingFailure(steps); blocked {
		writeJSON(w, http.StatusConflict, map[string]any{
			"ok": false, "steps": steps, "blocked": true,
			"error": "链路校验未通过：" + bad.Title + " — " + bad.Detail,
		})
		return
	}
	content, warnings, err := pipeline.Render(cfg, in)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	id := contracts.CoreID(cfg.Kernel)
	backup, err := s.engine.ApplyConfig(ctx, id, []byte(content), "pipeline: "+cfg.Name)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"ok": false, "error": err.Error(), "steps": steps,
		})
		return
	}

	// A running kernel keeps the OLD config in memory, so it must be cycled.
	restarted := false
	if st, err := s.engine.GetCore(id); err == nil {
		if st.State == contracts.StateRunning || st.State == contracts.StateStarting {
			if err := s.engine.Stop(ctx, id); err == nil {
				restarted = true
			}
		}
	}
	if err := s.engine.Start(ctx, id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"ok": false, "error": "配置已写入，但内核启动失败：" + err.Error(),
			"backup": backup, "steps": steps, "warnings": warnings,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "kernel": cfg.Kernel, "backup": backup,
		"steps": steps, "warnings": warnings, "restarted": restarted,
		"applied_at": time.Now().UTC(),
		"listen":     net.JoinHostPort(cfg.Inbound.Listen, strconv.Itoa(cfg.Inbound.Port)),
	})
}

// POST /api/v1/pipeline/test — real SOCKS5 CONNECT through the chain.
func (s *Server) handlePipelineTest(w http.ResponseWriter, r *http.Request) {
	if s.pipeline == nil {
		writeErr(w, http.StatusServiceUnavailable, "pipeline store unavailable")
		return
	}
	cfg := s.pipeline.Get()
	target := cfg.TestTarget
	var body struct {
		Target  string `json:"target"`
		Timeout int    `json:"timeout_ms"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if strings.TrimSpace(body.Target) != "" {
			target = strings.TrimSpace(body.Target)
		}
	}
	timeout := 8 * time.Second
	if body.Timeout > 0 {
		timeout = time.Duration(body.Timeout) * time.Millisecond
	}
	listen := net.JoinHostPort(cfg.Inbound.Listen, strconv.Itoa(cfg.Inbound.Port))
	if cfg.Inbound.Listen == "0.0.0.0" || cfg.Inbound.Listen == "::" {
		listen = net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Inbound.Port))
	}

	// The kernel may still be binding: retry briefly instead of a false failure.
	var lastErr error
	var latency time.Duration
	deadline := time.Now().Add(6 * time.Second)
	for {
		latency, lastErr = pipeline.SocksDial(listen, target, timeout)
		if lastErr == nil {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if lastErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok": false, "listen": listen, "target": target, "error": lastErr.Error(),
			"message": "端到端测试失败：入口已监听但隧道没有建立，请检查出口节点与内核日志",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "listen": listen, "target": target,
		"latency_ms": float64(latency.Microseconds()) / 1000.0,
		"message":    "SOCKS5 隧道建立成功（真实握手，非模拟）",
	})
}
