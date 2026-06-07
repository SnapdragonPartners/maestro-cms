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
	client     *storage.Client
	bucket     string
	ownsClient bool
}

var _ store.ObjectStore = (*Store)(nil)

// New constructs a Store over bucket, creating a GCS client with the given
// options. In normal operation pass no options and the client authenticates via
// Application Default Credentials; for tests set STORAGE_EMULATOR_HOST, which the
// SDK honors without credentials.
//
// ctx is used only to construct the client (auth and connection setup) and is
// not retained: canceling it later does not close the client — call Close for
// that. A Store created by New owns its client and closes it on Close.
func New(ctx context.Context, bucket string, opts ...option.ClientOption) (*Store, error) {
	if bucket == "" {
		return nil, errors.New("gcs: bucket must not be empty")
	}
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gcs: new client: %w", err)
	}
	return &Store{client: client, bucket: bucket, ownsClient: true}, nil
}

// NewWithClient wraps an existing *storage.Client as a Store over bucket — the
// seam for callers that build and share their own client (one client across
// several buckets, or a client pointed at an emulator in tests).
//
// Ownership stays with the caller: Store.Close does NOT close a client passed
// here, so sharing one client across multiple Stores is safe and the caller
// closes it once when done. (A Store from New, by contrast, owns and closes the
// client it created.)
//
// It panics if bucket is empty or client is nil — both are wiring bugs that
// would otherwise produce a Store that panics on first use.
func NewWithClient(bucket string, client *storage.Client) *Store {
	if bucket == "" {
		panic("gcs: NewWithClient requires a non-empty bucket")
	}
	if client == nil {
		panic("gcs: NewWithClient requires a non-nil client")
	}
	return &Store{client: client, bucket: bucket, ownsClient: false}
}

// Bucket returns the bucket name this store is scoped to.
func (s *Store) Bucket() string { return s.bucket }

// Close releases the client's connection pool, but only if this Store created
// the client (via New). A client supplied to NewWithClient is owned by the
// caller and left open, so Close is a no-op for such a Store.
func (s *Store) Close() error {
	if !s.ownsClient || s.client == nil {
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
//
// Aborting matters: a GCS Writer buffers and uploads on Close, so calling Close
// after a partial io.Copy would finalize a truncated object. To prevent that we
// give the Writer a child context and cancel it on copy failure, so Close aborts
// the upload (the Writer also has no CloseWithError abort path — the SDK directs
// callers to cancel the context instead).
func (s *Store) Put(ctx context.Context, key string, r io.Reader) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()

	w := s.client.Bucket(s.bucket).Object(key).NewWriter(wctx)
	if _, err := io.Copy(w, r); err != nil {
		cancel()      // abort the upload before Close so no partial object commits
		_ = w.Close() // returns the cancellation error; the copy error is the real cause
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
