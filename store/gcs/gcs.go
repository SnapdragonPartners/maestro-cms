// Package gcs implements store.ObjectStore over a Google Cloud Storage bucket.
//
// It is an opt-in subpackage so the core store package stays standard-library
// only: importing this package pulls in the Google Cloud Storage SDK and its
// transitive tree (gRPC, OpenTelemetry, genproto). A depguard rule keeps core
// packages from importing it (see
// docs/adr/0006-optional-adapters-as-subpackages.md). Wire it in with:
//
//	st, err := gcs.New(ctx, "my-bucket")
//	defer st.Close()
//
// Keys are used verbatim as GCS object names: no prefix is prepended and no
// normalization happens, matching store.ObjectStore's opaque-key contract. The
// caller owns naming (see store's path-convention notes). Per-bucket encryption,
// lifecycle, and access policy are provisioned outside this package.
//
// For tests and local development, set the STORAGE_EMULATOR_HOST environment
// variable (e.g. to a fsouza/fake-gcs-server instance); the underlying SDK
// routes to it and skips authentication automatically.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/SnapdragonPartners/maestro-cms/store"
)

// Store is a store.ObjectStore backed by a single GCS bucket. All operations are
// scoped to the bucket passed at construction.
type Store struct {
	client *storage.Client
	bucket string
}

var _ store.ObjectStore = (*Store)(nil)

// New constructs a Store over bucket, creating a GCS client with the given
// options. In normal operation pass no options and the client authenticates via
// Application Default Credentials; for tests set STORAGE_EMULATOR_HOST, which the
// SDK honors without credentials. Pass the long-lived context the client should
// live under; release it with Close.
func New(ctx context.Context, bucket string, opts ...option.ClientOption) (*Store, error) {
	if bucket == "" {
		return nil, errors.New("gcs: bucket must not be empty")
	}
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gcs: new client: %w", err)
	}
	return &Store{client: client, bucket: bucket}, nil
}

// NewWithClient wraps an existing *storage.Client as a Store over bucket. It is
// the seam for callers that construct and share their own client (and for tests
// that build a client against an emulator). The caller owns the client's
// lifecycle; Store.Close will still close it.
func NewWithClient(bucket string, client *storage.Client) *Store {
	return &Store{client: client, bucket: bucket}
}

// Bucket returns the bucket name this store is scoped to.
func (s *Store) Bucket() string { return s.bucket }

// Close releases the underlying client's connection pool.
func (s *Store) Close() error {
	if s.client == nil {
		return nil
	}
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("gcs: close client: %w", err)
	}
	return nil
}

// Get returns a reader for the object at key, or store.ErrObjectNotFound if it
// does not exist. The caller must close the returned reader.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := s.client.Bucket(s.bucket).Object(key).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, store.ErrObjectNotFound
		}
		return nil, fmt.Errorf("gcs: get %q: %w", key, err)
	}
	return rc, nil
}

// Put writes the bytes read from r to key, replacing any existing object. The
// upload is finalized atomically on success; a copy failure aborts it without
// committing a partial object.
func (s *Store) Put(ctx context.Context, key string, r io.Reader) error {
	w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
	if _, err := io.Copy(w, r); err != nil {
		// The resumable upload is already aborted; the Close error after a failed
		// copy is uninteresting, so surface the original cause.
		_ = w.Close()
		return fmt.Errorf("gcs: put %q: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcs: put %q: close: %w", key, err)
	}
	return nil
}

// Delete removes the object at key. It returns store.ErrObjectNotFound if the
// key does not exist, per the store.ObjectStore contract. (This differs from a
// GCS-idempotent delete: the interface distinguishes "deleted" from "was not
// there" so callers can detect a missing object.)
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.client.Bucket(s.bucket).Object(key).Delete(ctx); err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return store.ErrObjectNotFound
		}
		return fmt.Errorf("gcs: delete %q: %w", key, err)
	}
	return nil
}

// Exists reports whether an object is present at key. A nil error with false is
// the not-found outcome; a non-nil error indicates a transport or auth failure,
// which the caller should surface rather than read as absence.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	if _, err := s.client.Bucket(s.bucket).Object(key).Attrs(ctx); err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("gcs: exists %q: %w", key, err)
	}
	return true, nil
}
