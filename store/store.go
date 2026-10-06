// Package store defines a minimal, provider-neutral object-store interface for
// maestro-cms. Keys are opaque, adapter-defined strings; the interface carries
// no path conventions and no domain knowledge. Optional adapters such as
// store/gcs live in subpackages so the core stays dependency-free. See
// docs/adr/0006-optional-adapters-as-subpackages.md.
package store

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrObjectNotFound is returned by adapters when a key does not exist.
var ErrObjectNotFound = errors.New("store: object not found")

// ErrRangeNotSatisfiable is returned by RangeReader.GetRange when offset is
// at or past the end of the object, so no byte of the requested range exists.
var ErrRangeNotSatisfiable = errors.New("store: range not satisfiable")

// ObjectStore is a byte-level object store. Implementations read and write
// opaque bytes addressed by an adapter-defined key.
type ObjectStore interface {
	// Get returns a reader for the object at key. It returns ErrObjectNotFound
	// if the key does not exist. The caller must close the returned reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Put writes the bytes read from r to key, overwriting any existing object.
	Put(ctx context.Context, key string, r io.Reader) error
	// Delete removes the object at key. It returns ErrObjectNotFound if the key
	// does not exist.
	Delete(ctx context.Context, key string) error
	// Exists reports whether an object exists at key.
	Exists(ctx context.Context, key string) (bool, error)
}

// RangeReader is an ObjectStore that can read part of an object without
// reading the rest — what serving a large media file to a browser needs,
// since a video element asks for byte ranges to seek. Adapters implement it
// when the backing store serves ranges natively; a caller discovers it by
// type assertion and falls back to Get when it is absent.
type RangeReader interface {
	// GetRange returns a reader over the bytes of key starting at offset:
	// length bytes, or every byte to the end of the object when length < 0.
	// A range that runs past the end is shortened to the end, not an error.
	// It returns ErrObjectNotFound if the key does not exist and
	// ErrRangeNotSatisfiable if offset is at or past the object's end; a
	// negative offset is an error. The caller must close the returned reader.
	GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
}

// MaxComposeSources is the most objects one Compose call may join. It is
// Cloud Storage's limit, and the fake enforces it too, so a caller that must
// join more composes a tree (sources → intermediates → result) and finds out
// in its tests, not in production.
const MaxComposeSources = 32

// Composer is an ObjectStore that can join objects into one without the
// bytes passing through the caller — what assembling a large upload from
// parts needs, since reading a gigabyte back to write it again is the cost
// the parts were meant to avoid. Adapters implement it when the backing
// store composes natively; a caller discovers it by type assertion.
type Composer interface {
	// Compose writes to dst the concatenation of the objects at srcs, in
	// order, replacing any object at dst; contentType is recorded on dst
	// when the store records one (the fake does not). Between 1 and
	// MaxComposeSources sources, else an error before anything is written.
	// It returns ErrObjectNotFound if any source does not exist.
	Compose(ctx context.Context, dst string, srcs []string, contentType string) error
}

// ObjectInfo describes one listed object.
type ObjectInfo struct {
	// Key is the object's key, as Get would take it.
	Key string
	// Size is the object's length in bytes.
	Size int64
	// Created is when the object was written (its current generation).
	Created time.Time
}

// Lister is an ObjectStore that can enumerate the objects under a key
// prefix — what a sweep that reconciles a store against a database needs.
// Adapters implement it when the backing store lists natively; a caller
// discovers it by type assertion.
type Lister interface {
	// List calls fn once for every object whose key begins with prefix, in
	// ascending key order, and stops at the first error fn returns, which it
	// returns unchanged. An empty prefix lists every object.
	List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error
}
