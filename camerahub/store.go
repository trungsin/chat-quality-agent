package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var (
	ErrNotFound = errors.New("không tìm thấy nguồn")
	ErrExists   = errors.New("id đã tồn tại")
)

// Store keeps sources in a single JSON file. Enough for a few thousand cameras;
// swap for PostgreSQL when the registry grows.
type Store struct {
	mu      sync.RWMutex
	path    string
	sources map[string]Source
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, sources: map[string]Source{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Source
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	for _, src := range list {
		s.sources[src.ID] = src
	}
	return s, nil
}

func (s *Store) List() []Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Source, 0, len(s.sources))
	for _, src := range s.sources {
		out = append(out, src)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Site != out[j].Site {
			return out[i].Site < out[j].Site
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Store) Get(id string) (Source, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.sources[id]
	return src, ok
}

// Create adds all sources atomically: either every ID is new and all are saved, or none.
func (s *Store) Create(srcs ...Source) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, src := range srcs {
		if _, ok := s.sources[src.ID]; ok {
			return ErrExists
		}
	}
	for _, src := range srcs {
		s.sources[src.ID] = src
	}
	if err := s.saveLocked(); err != nil {
		for _, src := range srcs {
			delete(s.sources, src.ID)
		}
		return err
	}
	return nil
}

func (s *Store) Update(src Source) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.sources[src.ID]
	if !ok {
		return ErrNotFound
	}
	s.sources[src.ID] = src
	if err := s.saveLocked(); err != nil {
		s.sources[src.ID] = old
		return err
	}
	return nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.sources[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.sources, id)
	if err := s.saveLocked(); err != nil {
		s.sources[id] = old
		return err
	}
	return nil
}

func (s *Store) saveLocked() error {
	list := make([]Source, 0, len(s.sources))
	for _, src := range s.sources {
		list = append(list, src)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
