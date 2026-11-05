package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/profiles"
	"github.com/ngg-project/ngg/internal/protocols"
)

// --------------------------------------------------------- protocol catalog ---

// GET /protocols/catalog — pure read of the cached directory. It NEVER hits
// GitHub: the frontend polls this endpoint every 60s, and a lazy refresh here
// used to fire 3 GitHub API calls a minute, exhausting the anonymous 60/hour
// quota and making kernel installs fail with HTTP 403. Refreshes are owned by
// the 6h background timer (StartCatalogSync) and the explicit refresh button.
func (s *Server) handleProtocolCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.catalog.Current())
}

// POST /protocols/catalog/refresh — force an immediate sync.
func (s *Server) handleProtocolCatalogRefresh(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	snap := s.catalog.Refresh(ctx)
	writeJSON(w, http.StatusOK, snap)
}

// StartCatalogSync starts the periodic official-docs sync. Called once by the
// daemon with a lifecycle context; tests never start it.
func (s *Server) StartCatalogSync(ctx context.Context) {
	s.catalog.Start(ctx)
}

// --------------------------------------------------------------- profiles ---

func (s *Server) profilesOrErr(w http.ResponseWriter) (*profiles.Store, bool) {
	if s.profileStore == nil {
		writeErr(w, http.StatusInternalServerError, "配置文件存储未初始化")
		return nil, false
	}
	return s.profileStore, true
}

// GET /profiles/options — kernel choices and the six section definitions.
func (s *Server) handleProfileOptions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"kernels":      profiles.KernelOptions(),
		"section_keys": profiles.SectionKeys,
		"section_defs": profiles.SectionDefs("mihomo"),
		"doc_urls":     map[string]string{"mihomo": protocols.DocMihomo, "sing-box": protocols.DocSingbox, "xray-core": protocols.DocXray},
	})
}

// POST /profiles/toggle — enable/disable one, a selection, or all profiles.
// Each profile keeps its own kernel; several kernels can be enabled at once.
func (s *Server) handleProfilesToggle(w http.ResponseWriter, r *http.Request) {
	st, ok := s.profilesOrErr(w)
	if !ok {
		return
	}
	var body struct {
		IDs     []string `json:"ids"`
		All     bool     `json:"all"`
		Enabled bool     `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !body.All && len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "请提供 ids 或设置 all=true")
		return
	}
	changed, err := st.SetEnabled(body.IDs, body.All, body.Enabled)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	list, active := st.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "changed": changed, "enabled": body.Enabled,
		"scope":    map[bool]string{true: "all", false: "selected"}[body.All],
		"profiles": list, "active": active,
	})
}

// GET /profiles — list of cards plus the active profile id.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	st, ok := s.profilesOrErr(w)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var p profiles.Profile
		if err := decodeJSON(r, &p); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if len(p.Sections) == 0 {
			p.Sections = profiles.Template(p.Kernel, p.Name).Sections
		}
		out, err := st.Create(p)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profile": out})
		return
	}
	list, active := st.List()
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list, "active": active, "total": len(list)})
}

// POST /profiles/validate — syntax check without writing anything.
func (s *Server) handleProfileValidate(w http.ResponseWriter, r *http.Request) {
	var p profiles.Profile
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := profiles.Validate(p); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	content, format, err := profiles.Render(p)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "validated": true, "content": content, "format": format,
		"message": "语法校验通过（注释已在校验时剥离，不会写入内核）",
	})
}

// POST /profiles/template — return a fresh starter configuration for a kernel.
func (s *Server) handleProfileTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kernel string `json:"kernel"`
		Name   string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name == "" {
		body.Name = "New-Profile"
	}
	writeJSON(w, http.StatusOK, profiles.Template(body.Kernel, body.Name))
}

func (s *Server) handleProfileByID(w http.ResponseWriter, r *http.Request) {
	st, ok := s.profilesOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing profile id")
		return
	}

	// Sub-actions -------------------------------------------------------
	switch {
	case strings.HasSuffix(r.URL.Path, "/duplicate"):
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if body.Name == "" {
			src, found := st.Get(id)
			if !found {
				writeErr(w, http.StatusNotFound, "配置文件 "+id+" 不存在")
				return
			}
			body.Name = src.Name + "-copy"
		}
		out, err := st.Duplicate(id, body.Name)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profile": out})
		return

	case strings.HasSuffix(r.URL.Path, "/activate"):
		if err := st.SetActive(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "active": id})
		return

	case strings.HasSuffix(r.URL.Path, "/render"):
		p, found := st.Get(id)
		if !found {
			writeErr(w, http.StatusNotFound, "配置文件 "+id+" 不存在")
			return
		}
		content, format, err := profiles.Render(p)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "content": content, "format": format, "name": p.Name, "kernel": p.Kernel,
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		p, found := st.Get(id)
		if !found {
			writeErr(w, http.StatusNotFound, fmt.Sprintf("配置文件 %s 不存在", id))
			return
		}
		writeJSON(w, http.StatusOK, p)

	case http.MethodPut:
		var p profiles.Profile
		if err := decodeJSON(r, &p); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		out, err := st.Update(id, p)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profile": out})

	case http.MethodDelete:
		if err := st.Delete(id); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": id})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method "+r.Method)
	}
}
