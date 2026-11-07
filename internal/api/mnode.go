package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ngg-project/ngg/internal/mnode"
)

// 规划 1 的 HTTP 契约：新建空白节点（只传 IP+端口）、下载安装脚本、
// 连接探测主机信息。凭据一律由后端自动生成，接口不接受任何私钥入参。

func (s *Server) mnodesOrErr(w http.ResponseWriter) (*mnode.Store, bool) {
	if s.mnodes == nil {
		writeErr(w, http.StatusServiceUnavailable, "模块节点存储不可用")
		return nil, false
	}
	return s.mnodes, true
}

// handleMNodes serves list (GET) and create (POST).
func (s *Server) handleMNodes(w http.ResponseWriter, r *http.Request) {
	store, ok := s.mnodesOrErr(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"nodes": store.List()})

	case http.MethodPost:
		var body struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		body.Address = strings.TrimSpace(body.Address)
		if body.Address == "" {
			writeErr(w, http.StatusUnprocessableEntity, "只需填写服务器 IP（或域名）")
			return
		}
		if body.Port == 0 {
			body.Port = 6691
		}
		pkiDir := s.cfg.Storage.Dir + "/nodepki"
		view, secrets, err := mnode.CreateBlankNode(store, body.Address, body.Port, pkiDir)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"node":    view,
			"secrets": secrets,
			"notice":  "API 密码、后量子公钥、mTLS 私钥只在此处显示一次；安装脚本已可在服务器上执行。",
		})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// handleMNode serves GET (one node) and DELETE.
func (s *Server) handleMNode(w http.ResponseWriter, r *http.Request) {
	store, ok := s.mnodesOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		view, found := store.Get(id)
		if !found {
			writeErr(w, http.StatusNotFound, "节点不存在: "+id)
			return
		}
		writeJSON(w, http.StatusOK, view)
	case http.MethodDelete:
		if err := store.Delete(id); err != nil {
			if errors.Is(err, mnode.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "节点不存在: "+id)
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeOK(w, "节点已删除（服务器上的服务不会被卸载，请在服务器上停止 ngg-mnode.service）")
	default:
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
	}
}

// handleMNodeScript returns the self-contained installer. It contains the
// node's private key, so it is only served on an authenticated request.
func (s *Server) handleMNodeScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	store, ok := s.mnodesOrErr(w)
	if !ok {
		return
	}
	rec, found := store.Record(r.PathValue("id"))
	if !found {
		writeErr(w, http.StatusNotFound, "节点没有生成安装脚本")
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "ngg-mnode-"+rec.Node.ID+".sh"))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(rec.Script))
}

// handleMNodeProbe connects over mutual TLS and reads REAL host information.
func (s *Server) handleMNodeProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "unsupported method")
		return
	}
	store, ok := s.mnodesOrErr(w)
	if !ok {
		return
	}
	if s.pki == nil {
		writeErr(w, http.StatusServiceUnavailable, "面板 CA 未初始化")
		return
	}
	id := r.PathValue("id")
	rec, found := store.Record(id)
	if !found {
		writeErr(w, http.StatusNotFound, "节点不存在: "+id)
		return
	}
	certPEM, keyPEM := s.pki.ClientMaterial()
	probe, err := mnode.Probe(r.Context(), rec, certPEM, keyPEM)
	_ = store.SetProbe(id, &probe)
	if err != nil {
		writeErr(w, http.StatusBadGateway, probe.Error)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"node_id": id, "probe": probe})
}
