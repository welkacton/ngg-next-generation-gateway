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

// GET /protocols/catalog — text-only directory of protocols each kernel
// supports, synced from the kernels' official documentation. Never editable.
func (s *Server) handleProtocolCatalog(w http.ResponseWriter, r *http.Request) {
	snap := s.catalog.Current()
	// Lazy refresh: if the snapshot is stale and we have not tried recently,
	// kick off one background refresh. The current (cached) data is still
	// returned immediately so the page never blocks on the network.
	if snap.Stale && !strings.EqualFold(r.URL.Query().Get("refresh"), "false") {
		go s.catalog.Refresh(context.Background())
	}
	writeJSON(w, http.StatusOK, snap)
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
