// Package testcms provides deterministic fakes and helpers for testing against
// maestro-cms interfaces without real cloud services, databases, or model
// calls.
package testcms

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/store"
)

// MemoryStore is an in-memory store.ObjectStore for tests, with every
// optional capability the GCS adapter has (store.RangeReader, store.Composer,
// store.Lister), so a consumer's tests exercise the same contract production
// does. It is safe for concurrent use. Its not-found behavior mirrors a real
// object store: Get and Delete on an absent key return store.ErrObjectNotFound.
type MemoryStore struct {
	mu      sync.Mutex
	objects map[string]*memObject
}

// memObject is one stored object: its bytes and when they were written.
type memObject struct {
	data    []byte
	created time.Time
}

var (
	_ store.ObjectStore = (*MemoryStore)(nil)
	_ store.RangeReader = (*MemoryStore)(nil)
	_ store.Composer    = (*MemoryStore)(nil)
	_ store.Lister      = (*MemoryStore)(nil)
)

// NewMemoryStore returns an empty MemoryStore ready for use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: make(map[string]*memObject)}
}

// Get returns a reader over a private copy of the bytes stored at key, or
// store.ErrObjectNotFound if the key is absent.
func (s *MemoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	if !ok {
		return nil, store.ErrObjectNotFound
	}
	cp := make([]byte, len(o.data))
	copy(cp, o.data)
	return io.NopCloser(bytes.NewReader(cp)), nil
}

// GetRange implements store.RangeReader: a private copy of length bytes from
// offset (to the end when length < 0, shortened to the end when it runs past
// it), store.ErrObjectNotFound for an absent key, store.ErrRangeNotSatisfiable
// for an offset at or past the end, an error for a negative offset.
func (s *MemoryStore) GetRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 {
		return nil, fmt.Errorf("testcms: get range %q: negative offset %d", key, offset)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	if !ok {
		return nil, store.ErrObjectNotFound
	}
	b := o.data
	if offset >= int64(len(b)) {
		return nil, store.ErrRangeNotSatisfiable
	}
	// Compare before adding: an oversized length must not overflow into a
	// negative endpoint; it simply reads to the end.
	end := int64(len(b))
	if length >= 0 && length < end-offset {
		end = offset + length
	}
	cp := make([]byte, end-offset)
	copy(cp, b[offset:end])
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
	s.objects[key] = &memObject{data: b, created: time.Now()}
	return nil
}

// Compose implements store.Composer: dst becomes a private copy of the
// sources' bytes in order, replacing whatever dst held. The source count is
// bounded by store.MaxComposeSources exactly as the GCS adapter bounds it, so
// a consumer that needs a compose tree finds out here. A missing source →
// store.ErrObjectNotFound, and nothing is written. contentType is accepted
// and dropped: this store records bytes only.
func (s *MemoryStore) Compose(_ context.Context, dst string, srcs []string, _ string) error {
	if len(srcs) == 0 || len(srcs) > store.MaxComposeSources {
		return fmt.Errorf("testcms: compose %q: %d sources, want 1..%d", dst, len(srcs), store.MaxComposeSources)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []byte
	for _, k := range srcs {
		o, ok := s.objects[k]
		if !ok {
			return store.ErrObjectNotFound
		}
		out = append(out, o.data...)
	}
	s.objects[dst] = &memObject{data: out, created: time.Now()}
	return nil
}

// List implements store.Lister: fn for every object whose key begins with
// prefix, in ascending key order, over a snapshot taken under the lock (fn
// may call back into the store). fn's first error ends the listing and is
// returned unchanged.
func (s *MemoryStore) List(_ context.Context, prefix string, fn func(store.ObjectInfo) error) error {
	s.mu.Lock()
	infos := make([]store.ObjectInfo, 0, len(s.objects))
	for k, o := range s.objects {
		if strings.HasPrefix(k, prefix) {
			infos = append(infos, store.ObjectInfo{Key: k, Size: int64(len(o.data)), Created: o.created})
		}
	}
	s.mu.Unlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key < infos[j].Key })
	for i := range infos {
		if err := fn(infos[i]); err != nil {
			return err
		}
	}
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
