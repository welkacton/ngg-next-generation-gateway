package nodedeploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
	"github.com/ngg-project/ngg/internal/nodeapi"
)

// Persistence: <data>/nodedeploy/<hostID>.json (0600).
//
// The record holds the spec (including the node's own transport secrets,
// because re-rendering the script must not rotate a live node's client
// credentials) and the script itself. It is never served raw to the browser:
// the API projects it through Spec.PublicSpec and strips the script unless the
// caller explicitly asked for it.

var hostIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ConnectResult is the outcome of one panel → node connection. It is the only
// "is it alive" evidence the panel has, so it records exactly what happened:
// when, how long it took, what failed, and what the node said about itself.
type ConnectResult struct {
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	DurationMS int64     `json:"duration_ms"`
	// Stage is which call failed: "health" / "start" / "stats".
	Stage string `json:"stage,omitempty"`
	Error string `json:"error,omitempty"`
	// Health / System are the node's own answers, kept so the console can show
	// the last known state while the node is unreachable.
	Health *nodeapi.Health `json:"health,omitempty"`
	System *nodeapi.System `json:"system,omitempty"`
	// ConfigPushed is true when this connection also pushed the kernel config.
	ConfigPushed bool `json:"config_pushed"`
}

// Record is everything the panel knows about one node's deployment.
type Record struct {
	HostID      string         `json:"host_id"`
	Spec        Spec           `json:"spec"`
	Script      string         `json:"script"`
	GeneratedAt time.Time      `json:"generated_at"`
	LastConnect *ConnectResult `json:"last_connect,omitempty"`
}

// View is the browser-safe projection: no private key, no profile source, and
// the script only when the caller asked for it.
type View struct {
	HostID      string         `json:"host_id"`
	Spec        Spec           `json:"spec"`
	Script      string         `json:"script,omitempty"`
	GeneratedAt time.Time      `json:"generated_at"`
	LastConnect *ConnectResult `json:"last_connect,omitempty"`
	Params      []string       `json:"params"`
	// Warning is an operator-facing caveat about this plan (for example a
	// management port that is usually occupied by another product).
	Warning string `json:"warning,omitempty"`
}

// View builds the browser-safe projection.
func (r Record) View(withScript bool) View {
	v := View{
		HostID:      r.HostID,
		Spec:        r.Spec.PublicSpec(),
		GeneratedAt: r.GeneratedAt,
		LastConnect: r.LastConnect,
		Params:      r.Spec.ClientParams(),
	}
	if withScript {
		v.Script = r.Script
	}
	return v
}

// Store persists deployment records.
type Store struct {
	mu  sync.Mutex
	dir string
}

// NewStore roots the store at the NGG data directory.
func NewStore(dataDir string) *Store {
	return &Store{dir: filepath.Join(dataDir, "nodedeploy")}
}

// Dir is the storage location (created on first write).
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(hostID string) (string, error) {
	if !hostIDRE.MatchString(hostID) {
		return "", ErrNotFound
	}
	return filepath.Join(s.dir, hostID+".json"), nil
}

// Save writes the record atomically with 0600.
func (s *Store) Save(rec Record) error {
	p, err := s.path(rec.HostID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return atomicfile.WriteJSON(p, rec)
}

// Load reads one record.
func (s *Store) Load(hostID string) (Record, error) {
	p, err := s.path(hostID)
	if err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, ErrNotFound
		}
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return Record{}, fmt.Errorf("parse %s: %w", p, err)
	}
	return rec, nil
}

// List returns every stored record. It is what the metrics poller iterates:
// a host the operator never deployed has no record and is never dialled.
func (s *Store) List() []Record {
	s.mu.Lock()
	entries, err := os.ReadDir(s.dir)
	s.mu.Unlock()
	if err != nil {
		return nil
	}
	out := make([]Record, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".json")]
		rec, lerr := s.Load(id)
		if lerr != nil {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Delete removes a record (used when a host is deleted).
func (s *Store) Delete(hostID string) error {
	p, err := s.path(hostID)
	if err != nil {
		return nil // nothing to delete for an invalid id
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
