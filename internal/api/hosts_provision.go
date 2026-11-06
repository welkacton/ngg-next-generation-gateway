package api

import (
	"errors"
	"net/http"

	"github.com/ngg-project/ngg/internal/hosts"
)

// VPS onboarding: GET returns the deployment script already generated for a
// host, POST generates (or re-generates) it. Both are presence-scoped to the
// host registry, so a missing host is 404 and a bad protocol/port is 422.
func (s *Server) handleHostProvision(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.hostsOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")

	switch r.Method {
	case http.MethodGet:
		p, err := reg.LoadProvision(id)
		if err != nil {
			if errors.Is(err, hosts.ErrProvisionNotFound) {
				writeErr(w, http.StatusNotFound, "该节点尚未生成部署脚本")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)

	case http.MethodPost:
		var body struct {
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
			SNI      string `json:"sni"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		p, err := reg.Provision(id, body.Protocol, body.Port, body.SNI)
		if err != nil {
			if errors.Is(err, hosts.ErrUnknownHost) {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)

	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}
