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
)

// Persistence: <data>/nodedeploy/<hostID>.json (0600).
//
// The record holds the spec (including the node's secrets, because re-rendering
// the script must not rotate a live node's credentials) and the script itself.
// It is never served raw to the browser: the API projects it through
// Spec.PublicSpec and strips the script unless explicitly requested.

var hostIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Run states.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
)

// Run is one execution of the deployment script on the node.
type Run struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   int        `json:"exit_code"`
	DurationMS int64      `json:"duration_ms"`
	Steps      []Step     `json:"steps"`
	Notes      []string   `json:"notes,omitempty"`
	Ready      *Ready     `json:"ready,omitempty"`
	Stdout     string     `json:"stdout,omitempty"`
	Stderr     string     `json:"stderr,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Record is everything the panel knows about one node's deployment.
type Record struct {
	HostID      string    `json:"host_id"`
	Spec        Spec      `json:"spec"`
	Script      string    `json:"script"`
	GeneratedAt time.Time `json:"generated_at"`
	LastRun     *Run      `json:"last_run,omitempty"`
}

// View is the browser-safe projection: no private key, no node token, no
// profile source, and the script only when the caller asked for it.
type View struct {
	HostID      string    `json:"host_id"`
	Spec        Spec      `json:"spec"`
	Script      string    `json:"script,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
	LastRun     *Run      `json:"last_run,omitempty"`
	Params      []string  `json:"params"`
}

// View builds the browser-safe projection.
func (r Record) View(withScript bool) View {
	v := View{
		HostID:      r.HostID,
		Spec:        r.Spec.PublicSpec(),
		GeneratedAt: r.GeneratedAt,
		LastRun:     r.LastRun,
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
