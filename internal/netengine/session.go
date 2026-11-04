package netengine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// SessionManager tracks every connection independently. Each connection's
// execution path is decided by NGG, never by a kernel.
type sessionManager struct {
	mu       sync.RWMutex
	next     atomic.Uint64
	sessions map[SessionID]*SessionRec
	max      int
	ttl      time.Duration
}

var _ SessionManager = (*sessionManager)(nil)

func newSessionManager(max int, ttl time.Duration) *sessionManager {
	if max <= 0 {
		max = 8192
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &sessionManager{sessions: map[SessionID]*SessionRec{}, max: max, ttl: ttl}
}

func (m *sessionManager) Create(_ context.Context, s Session) (SessionID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= m.max {
		// Evict expired sessions before refusing new ones.
		m.evictLocked()
		if len(m.sessions) >= m.max {
			return 0, ErrFailClosed
		}
	}
	id := SessionID(m.next.Add(1))
	if id == 0 {
		id = SessionID(m.next.Add(1))
	}
	s.ID = id
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	m.sessions[id] = &SessionRec{Session: s, State: "establishing"}
	return id, nil
}

func (m *sessionManager) Lookup(id SessionID) (Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.sessions[id]
	if !ok {
		return Session{}, false
	}
	return r.Session, true
}

func (m *sessionManager) Get(id SessionID) (*SessionRec, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.sessions[id]
	return r, ok
}

func (m *sessionManager) Close(id SessionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.sessions[id]; ok {
		r.Close("closed", "")
		delete(m.sessions, id)
	}
	return nil
}

func (m *sessionManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

func (m *sessionManager) List(limit int) []SessionRec {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out := make([]SessionRec, 0, limit)
	for _, r := range m.sessions {
		r.mu.Lock()
		out = append(out, SessionRec{
			Session: r.Session, TenantID: r.TenantID, Tags: append([]string(nil), r.Tags...),
			BytesIn: r.BytesIn, BytesOut: r.BytesOut, State: r.State, LastError: r.LastError,
		})
		r.mu.Unlock()
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (m *sessionManager) Tag(id SessionID, tenant string, tags ...string) {
	m.mu.RLock()
	r, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return
	}
	r.mu.Lock()
	if tenant != "" {
		r.TenantID = tenant
	}
	r.Tags = append(r.Tags, tags...)
	r.mu.Unlock()
}

func (m *sessionManager) SetPath(id SessionID, coreID string, rule string) {
	m.mu.RLock()
	r, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return
	}
	r.mu.Lock()
	r.Session.CoreID = contracts.CoreID(coreID)
	r.Session.Rule = rule
	r.mu.Unlock()
}

func (m *sessionManager) evictLocked() {
	now := time.Now()
	for id, r := range m.sessions {
		if now.Sub(r.Session.CreatedAt) > m.ttl {
			delete(m.sessions, id)
		}
	}
}
