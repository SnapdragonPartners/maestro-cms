// Package testcms provides deterministic fakes and helpers for testing against
// maestro-cms interfaces without real cloud services, databases, or model
// calls.
package testcms

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/SnapdragonPartners/maestro-cms/store"
)

// MemoryStore is an in-memory store.ObjectStore for tests. It is safe for
// concurrent use. Its not-found behavior mirrors a real object store: Get and
// Delete on an absent key return store.ErrObjectNotFound.
type MemoryStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

// NewMemoryStore returns an empty MemoryStore ready for use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: make(map[string][]byte)}
}

// Get returns a reader over a private copy of the bytes stored at key, or
// store.ErrObjectNotFound if the key is absent.
func (s *MemoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, store.ErrObjectNotFound
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return io.NopCloser(bytes.NewReader(cp)), nil
}

// Put reads all bytes from r and stores a private copy at key, overwriting any
// existing object.
func (s *MemoryStore) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("testcms: read object for key %q: %w", key, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return nil
}

// Delete removes the object at key, returning store.ErrObjectNotFound if it is
// absent.
func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; !ok {
		return store.ErrObjectNotFound
	}
	delete(s.objects, key)
	return nil
}

// Exists reports whether an object exists at key.
func (s *MemoryStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}
