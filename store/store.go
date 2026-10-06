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
