package api

import (
	"context"
	"net/http"
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
// The repository is public, so the panel talks to GitHub Releases directly and
// automatically — there is no token field in the UI and no X-GitHub-Token
// header anymore. Headless deployments may still set NGG_GITHUB_TOKEN /
// GITHUB_TOKEN to raise the anonymous API rate limit; that is optional and is
// never required for the update panel to work.

func (s *Server) handleUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.updater.Status())
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	res, err := s.updater.Check(r.Context())
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
	ctx := r.Context()
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
	ctx := r.Context()
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
