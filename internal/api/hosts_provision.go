package api

import (
	"errors"
	"net/http"

	"github.com/ngg-project/ngg/internal/hosts"
	"github.com/ngg-project/ngg/internal/profiles"
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
			// profile_id binds a 协议配置 profile: when present the node runs
			// exactly that file and protocol/port/sni are only overrides.
			ProfileID string `json:"profile_id"`
			Protocol  string `json:"protocol"`
			Port      int    `json:"port"`
			SNI       string `json:"sni"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		var p hosts.HostProvision
		var err error
		if pid := body.ProfileID; pid != "" {
			store, ok := s.profilesOrErr(w)
			if !ok {
				return
			}
			prof, found := store.Get(pid)
			if !found {
				writeErr(w, http.StatusNotFound, "配置文件不存在: "+pid)
				return
			}
			content, format, rerr := profiles.Render(prof)
			if rerr != nil {
				writeErr(w, http.StatusUnprocessableEntity, "配置文件无法渲染: "+rerr.Error())
				return
			}
			p, err = reg.ProvisionWithProfile(id, hosts.ProvisionProfile{
				ID: prof.ID, Name: prof.Name, Kernel: prof.Kernel, Format: format,
				Content: content, Protocol: body.Protocol, Port: body.Port, SNI: body.SNI,
			})
		} else {
			p, err = reg.Provision(id, body.Protocol, body.Port, body.SNI)
		}
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
