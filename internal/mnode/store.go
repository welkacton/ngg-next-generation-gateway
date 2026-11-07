// Package mnode is 规划 1 of the module-driven architecture: BLANK NODES.
//
// The operator enters ONLY an IP (or hostname) and a port. API password,
// post-quantum key material and the mTLS certificate are generated here,
// never typed by hand. The node can be deployed and then probed for real host
// information. It is intentionally independent of every existing domain.
package mnode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
	"github.com/ngg-project/ngg/internal/module"
)

// ErrNotFound is returned for an unknown node id.
var ErrNotFound = errors.New("节点不存在")

// Record is the full local state of one node. It lives in a 0600 file and is
// NEVER served raw to the browser: the API projects it through View(), and the
// installer is only exposed through an authenticated download endpoint.
type Record struct {
	Node        module.NodeSpec    `json:"node"`
	Secrets     module.NodeSecrets `json:"secrets"`
	Script      string             `json:"script"`
	GeneratedAt time.Time          `json:"generated_at"`
}

// View is the browser-safe projection: no API password, no private keys.
type View struct {
	Node        module.NodeSpec `json:"node"`
	HasScript   bool            `json:"has_script"`
	GeneratedAt time.Time       `json:"generated_at"`
}

func (r Record) View() View {
	return View{Node: r.Node, HasScript: r.Script != "", GeneratedAt: r.GeneratedAt}
}

// Store persists nodes in <data>/mnodes.json (0600).
type Store struct {
	mu   sync.RWMutex
	path string
	list []Record
	byID map[string]int
}

// NewStore opens (or creates) the node store.
func NewStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "mnodes.json"), byID: map[string]int{}}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("读取节点库失败: %w", err)
	}
	if len(b) == 0 {
		return s, nil
	}
	var list []Record
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("解析节点库失败: %w", err)
	}
	s.list = list
	s.indexLocked()
	return s, nil
}

func (s *Store) indexLocked() {
	s.byID = make(map[string]int, len(s.list))
	for i := range s.list {
		s.byID[s.list[i].Node.ID] = i
	}
}

func (s *Store) flushLocked() error {
	return atomicfile.WriteJSON(s.path, s.list)
}

// List returns the safe projection of every node.
func (s *Store) List() []View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]View, 0, len(s.list))
	for _, r := range s.list {
		out = append(out, r.View())
	}
	return out
}

// Get returns one public node view.
func (s *Store) Get(id string) (View, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byID[id]
	if !ok {
		return View{}, false
	}
	return s.list[i].View(), true
}

// Record returns the full record (internal use only: probe, script download).
func (s *Store) Record(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byID[id]
	if !ok {
		return Record{}, false
	}
	return s.list[i], true
}

// Put inserts or replaces a full record and persists atomically.
func (s *Store) Put(r Record) error {
	if r.Node.ID == "" {
		return errors.New("节点 ID 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.byID[r.Node.ID]; ok {
		s.list[i] = r
	} else {
		s.list = append(s.list, r)
	}
	s.indexLocked()
	return s.flushLocked()
}

// SetProbe attaches the latest probe result to a node.
func (s *Store) SetProbe(id string, p *module.NodeProbe) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.byID[id]
	if !ok {
		return ErrNotFound
	}
	s.list[i].Node.Probe = p
	return s.flushLocked()
}

// Delete removes a node by id.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.byID[id]
	if !ok {
		return ErrNotFound
	}
	s.list = append(s.list[:i], s.list[i+1:]...)
	s.indexLocked()
	return s.flushLocked()
}

// CreateBlankNode is the ONLY way to add a node: address + port in,
// fully generated credentials and a ready-to-run installer out.
func CreateBlankNode(s *Store, address string, port int, pkiDir string) (View, module.NodeSecrets, error) {
	n, secrets, err := module.NewNodeCredentials("", address, port, pkiDir)
	if err != nil {
		return View{}, module.NodeSecrets{}, err
	}
	script, err := Script(n, secrets)
	if err != nil {
		return View{}, module.NodeSecrets{}, err
	}
	n.Probe = &module.NodeProbe{
		CollectedAt: time.Now().UTC(),
		Error:       "尚未部署或尚未连接",
	}
	rec := Record{Node: n, Secrets: secrets, Script: script, GeneratedAt: time.Now().UTC()}
	if err := s.Put(rec); err != nil {
		return View{}, module.NodeSecrets{}, err
	}
	return rec.View(), secrets, nil
}
