package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/ngg-project/ngg/internal/update"
)

// Repository update endpoints. All of them require the same loopback +
// Bearer Token middleware as every other /api/ route.
//
// GET  /update/status    - generation, release, repo, platform, script readiness
// POST /update/check     - REAL GitHub Releases lookup (cached in the checker)
// POST /update/download  - download + mandatory SHA256SUMS verification
// POST /update/apply     - launch the platform update script (Windows), or
//                          return the exact command on systemd hosts
//
// Private repositories need a GitHub PAT. It can be supplied either as the
// NGG_GITHUB_TOKEN / GITHUB_TOKEN environment variable on the server, or from
// the Web UI via the X-GitHub-Token request header. UI-supplied tokens are
// scoped to a single request through the context: they are never persisted,
// never logged and never written into the response.

const githubTokenHeader = "X-GitHub-Token"

func updateCtx(r *http.Request) context.Context {
	tok := strings.TrimSpace(r.Header.Get(githubTokenHeader))
	return update.WithToken(r.Context(), tok)
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.updater.Status())
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	res, err := s.updater.Check(updateCtx(r))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type updateTargetBody struct {
	Tag   string `json:"tag"`
	Asset string `json:"asset"`
}

func (s *Server) resolveUpdateTarget(ctx context.Context, body updateTargetBody) (string, string) {
	if body.Tag != "" && body.Asset != "" {
		return body.Tag, body.Asset
	}
	if cached := s.updater.Cached(); cached != nil && cached.Release != nil && cached.Asset != nil {
		return cached.Release.Tag, cached.Asset.Name
	}
	res, err := s.updater.Check(ctx)
	if err != nil || res == nil || res.Release == nil || res.Asset == nil {
		return "", ""
	}
	return res.Release.Tag, res.Asset.Name
}

func (s *Server) handleUpdateDownload(w http.ResponseWriter, r *http.Request) {
	var body updateTargetBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ctx := updateCtx(r)
	tag, asset := s.resolveUpdateTarget(ctx, body)
	if tag == "" || asset == "" {
		writeErr(w, http.StatusBadRequest, "tag/asset missing and no matching release asset could be resolved (private repo needs a GitHub Token)")
		return
	}
	res, err := s.updater.Download(ctx, tag, asset)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	var body updateTargetBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ctx := updateCtx(r)
	tag, asset := s.resolveUpdateTarget(ctx, body)
	if tag == "" || asset == "" {
		writeErr(w, http.StatusBadRequest, "tag/asset missing and no matching release asset could be resolved (private repo needs a GitHub Token)")
		return
	}
	res, err := s.updater.Apply(ctx, tag, asset)
	if err != nil {
		writeErr(w, http.StatusPreconditionFailed, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
