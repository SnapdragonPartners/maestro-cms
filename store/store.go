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
