package api

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/auth"
)

// The browser console is authenticated with an HttpOnly session cookie. The
// static API token from ngg.yaml stays valid for scripts / CI, but the web UI
// never sends it anymore: opening the console always shows either the
// first-run setup screen or the login form.
const sessionCookie = "ngg_session"

func isAuthPath(p string) bool {
	return strings.HasPrefix(p, "/api/v1/auth/")
}

// authenticated reports whether the request carries a valid session cookie or
// the configured API token.
func (s *Server) authenticated(r *http.Request) (string, bool) {
	if s.auth != nil {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if user, ok := s.auth.Lookup(c.Value); ok {
				return user, true
			}
		}
	}
	token := bearerToken(r)
	if token != "" && subtleEqual(token, s.cfg.Server.Token) {
		return "api-token", true
	}
	return "", false
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	ttl := int(time.Until(sess.ExpiresAt).Seconds())
	if ttl < 60 {
		ttl = 60
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   ttl,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// GET /api/v1/auth/state — public. Tells the SPA whether to show the first-run
// setup screen, the login form, or the console.
func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeErr(w, http.StatusServiceUnavailable, "auth store unavailable")
		return
	}
	user, ok := s.authenticated(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_required":  s.auth.SetupRequired(),
		"authenticated":   ok,
		"username":        user,
		"api_token_login": ok && user == "api-token",
		"policy": map[string]any{
			"min_password":    8,
			"session_hours":   int(auth.SessionTTL.Hours()),
			"remember_hours":  int(auth.RememberTTL.Hours()),
			"max_failures":    5,
			"lockout_seconds": 60,
		},
		"host": clientHost(r),
	})
}

// POST /api/v1/auth/setup — public, succeeds only once in the lifetime of an
// installation.
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeErr(w, http.StatusServiceUnavailable, "auth store unavailable")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.auth.Setup(body.Username, body.Password); err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, auth.ErrAlreadySetup):
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	sess, err := s.auth.Login(body.Username, body.Password, r.UserAgent(), true)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookie(w, r, sess)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":         true,
		"username":   sess.Username,
		"expires_at": sess.ExpiresAt,
	})
}

// POST /api/v1/auth/login
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeErr(w, http.StatusServiceUnavailable, "auth store unavailable")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if s.auth.SetupRequired() {
		writeErr(w, http.StatusConflict, auth.ErrSetupRequired.Error())
		return
	}
	sess, err := s.auth.Login(body.Username, body.Password, r.UserAgent(), body.Remember)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, auth.ErrLocked) || strings.Contains(err.Error(), "连续登录失败") {
			status = http.StatusTooManyRequests
		}
		writeErr(w, status, err.Error())
		return
	}
	s.setSessionCookie(w, r, sess)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"username":   sess.Username,
		"expires_at": sess.ExpiresAt,
	})
}

// POST /api/v1/auth/logout
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && s.auth != nil {
		s.auth.Logout(c.Value)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/auth/password — rotating the password revokes every session, so
// the caller must log in again with the new password.
func (s *Server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeErr(w, http.StatusServiceUnavailable, "auth store unavailable")
		return
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.auth.ChangePassword(body.OldPassword, body.NewPassword); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, auth.ErrInvalidCredential) {
			status = http.StatusForbidden
		}
		writeErr(w, status, err.Error())
		return
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "密码已更新，所有会话已失效，请用新密码重新登录",
	})
}

func clientHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
