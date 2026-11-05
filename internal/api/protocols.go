package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/ngg-project/ngg/internal/protocols"
)

// Protocol center extensions: custom protocols, JSON editing, config editing,
// validation and batch enable/disable. The base GET /protocols and
// POST /protocols/{id}/{enable,config} live in stage45.go.

func (s *Server) handleProtocolMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": []map[string]string{
			{"value": string(protocols.GroupEncrypted), "label": "加密协议"},
			{"value": string(protocols.GroupPlaintext), "label": "普通未加密协议"},
			{"value": string(protocols.GroupPlainProxy), "label": "普通代理协议"},
		},
		"categories": []map[string]string{
			{"value": string(protocols.CatVPN), "label": "VPN"},
			{"value": string(protocols.CatEncryptedProxy), "label": "加密代理"},
			{"value": string(protocols.CatPQ), "label": "后量子"},
			{"value": string(protocols.CatPlaintext), "label": "明文协议"},
			{"value": string(protocols.CatPlainProxy), "label": "普通代理"},
		},
		"config_kinds": []string{
			string(protocols.KindOutbound), string(protocols.KindPolicy),
			string(protocols.KindListener), string(protocols.KindHelper),
		},
		"kernels": []string{"mihomo", "sing-box", "xray-core", "ngg-native", "external-helper"},
		"formats": protocols.Formats(),
	})
}

func (s *Server) handleProtocolCreate(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var spec protocols.Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	// A newly added protocol is usable immediately unless the caller explicitly
	// asked for it to be disabled. Go's zero value cannot tell "absent" from
	// "false", so inspect the raw object for the key.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err == nil {
		if _, present := fields["enabled"]; !present {
			spec.Enabled = true
		}
	}
	if err := s.engine.Protocols().Register(spec); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": spec})
}

func (s *Server) handleProtocolUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var spec protocols.Spec
	if err := decodeJSON(r, &spec); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if spec.ID == "" {
		spec.ID = id
	}
	if spec.ID != id {
		writeErr(w, http.StatusBadRequest, "id in body does not match URL")
		return
	}
	if err := s.engine.Protocols().Update(spec); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	updated, _ := s.engine.Protocols().Get(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": updated})
}

func (s *Server) handleProtocolDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.engine.Protocols().Delete(id); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// handleProtocolsToggle flips one, a selection, or every protocol in one
// persisted write. It never rewrites the whole catalog or touches the network.
func (s *Server) handleProtocolsToggle(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusBadRequest, "ids or all is required")
		return
	}
	changed, err := s.engine.Protocols().SetEnabledBatch(body.IDs, body.All, body.Enabled)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	scope := "selected"
	if body.All {
		scope = "all"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "changed": changed, "enabled": body.Enabled, "scope": scope,
	})
}

// handleProtocolValidate checks hand-edited content without persisting it, so
// the JSON/YAML editor can give immediate feedback.
func (s *Server) handleProtocolValidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := protocols.ValidateConfig([]byte(body.Content), body.Format); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "valid": false, "format": body.Format, "error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "valid": true, "format": body.Format,
	})
}

// handleProtocolSaveConfig stores the user's edited content as an override;
// generation will then return it until it is cleared or regenerated.
func (s *Server) handleProtocolSaveConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	spec, err := s.engine.Protocols().SaveConfig(id, body.Content, body.Format)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": spec})
}

func (s *Server) handleProtocolClearConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	spec, err := s.engine.Protocols().ClearConfig(id)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": spec})
}
