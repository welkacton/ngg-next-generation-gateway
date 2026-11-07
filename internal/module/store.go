package module

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/atomicfile"
)

// ErrNotFound is returned when a module id is unknown.
var ErrNotFound = errors.New("模块不存在")

// Store persists all modules in <data>/modules.json (0600). It deliberately
// does NOT touch any of the existing domain stores.
type Store struct {
	mu   sync.RWMutex
	path string
	list []Module
	byID map[string]int
}

// NewStore opens (or creates) the module store under the NGG data directory.
func NewStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "modules.json"), byID: map[string]int{}}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("读取模块库失败: %w", err)
	}
	if len(b) == 0 {
		return s, nil
	}
	var list []Module
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("解析模块库失败: %w", err)
	}
	s.list = list
	for i := range s.list {
		s.byID[s.list[i].ID] = i
	}
	return s, nil
}

func (s *Store) flushLocked() error {
	return atomicfile.WriteJSON(s.path, s.list)
}

func (s *Store) indexLocked() {
	s.byID = make(map[string]int, len(s.list))
	for i := range s.list {
		s.byID[s.list[i].ID] = i
	}
}

// List returns copies so callers cannot mutate the in-memory store.
func (s *Store) List() []Module {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Module, len(s.list))
	copy(out, s.list)
	return out
}

// Get looks up one module by id.
func (s *Store) Get(id string) (Module, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byID[id]
	if !ok {
		return Module{}, false
	}
	return s.list[i], true
}

// Save inserts or replaces a module and persists atomically.
func (s *Store) Save(m Module) error {
	if m.ID == "" {
		return errors.New("模块 ID 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	m.UpdatedAt = now
	if i, ok := s.byID[m.ID]; ok {
		m.CreatedAt = s.list[i].CreatedAt
		s.list[i] = m
	} else {
		m.CreatedAt = now
		s.list = append(s.list, m)
	}
	s.indexLocked()
	if err := s.flushLocked(); err != nil {
		return fmt.Errorf("保存模块失败: %w", err)
	}
	return nil
}

// Delete removes a module by id.
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
