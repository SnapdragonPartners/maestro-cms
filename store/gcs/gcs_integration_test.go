//go:build integration

// Integration tests for the GCS adapter. They run only under the `integration`
// build tag and require a GCS-compatible endpoint named by STORAGE_EMULATOR_HOST
// (e.g. a fsouza/fake-gcs-server container). `make test-integration` starts that
// container, sets the variable, and runs these; without it they are skipped.
package gcs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/SnapdragonPartners/maestro-cms/store"
	"github.com/SnapdragonPartners/maestro-cms/store/gcs"
)

const testBucket = "maestro-cms-it"

// newStore returns a Store backed by the emulator, creating the test bucket if
// needed. It skips the test when no emulator endpoint is configured.
func newStore(t *testing.T) *gcs.Store {
	t.Helper()
	if os.Getenv("STORAGE_EMULATOR_HOST") == "" {
		t.Skip("STORAGE_EMULATOR_HOST not set; run `make test-integration`")
	}
	ctx := context.Background()
	// STORAGE_EMULATOR_HOST routes the client to the emulator; WithoutAuthentication
	// stops it from attempting Application Default Credentials, which do not exist
	// in CI/dev.
	client, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	// Create the bucket; tolerate "already exists" since the emulator persists for
	// the life of the container across tests.
	if err := client.Bucket(testBucket).Create(ctx, "maestro-cms-test", nil); err != nil {
		if _, aerr := client.Bucket(testBucket).Attrs(ctx); aerr != nil {
			t.Fatalf("create bucket %q: %v", testBucket, err)
		}
	}
	st := gcs.NewWithClient(testBucket, client)
	// NewWithClient does not take ownership, so the test closes the client it
	// created (st.Close would be a no-op here).
	t.Cleanup(func() { _ = client.Close() })
	return st
}

// errReader yields data once, then fails — to exercise a mid-stream reader error.
type errReader struct {
	data []byte
	err  error
	done bool
}

func (e *errReader) Read(p []byte) (int, error) {
	if !e.done {
		e.done = true
		return copy(p, e.data), nil
	}
	return 0, e.err
}

func TestGCSRoundTrip(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	const key = "roundtrip/object.bin"
	payload := []byte("hello, gcs adapter")

	if err := st.Put(ctx, key, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	ok, err := st.Exists(ctx, key)
	if err != nil || !ok {
		t.Fatalf("Exists after Put = (%v, %v), want (true, nil)", ok, err)
	}

	rc, err := st.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get returned %q, want %q", got, payload)
	}

	if err := st.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ok, err = st.Exists(ctx, key)
	if err != nil || ok {
		t.Fatalf("Exists after Delete = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestGCSOverwrite(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	const key = "overwrite/object.bin"

	if err := st.Put(ctx, key, bytes.NewReader([]byte("first"))); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if err := st.Put(ctx, key, bytes.NewReader([]byte("second"))); err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	rc, err := st.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("after overwrite Get = %q, want %q", got, "second")
	}
	_ = st.Delete(ctx, key)
}

// TestGCSPutAbortsOnReaderError verifies that a reader error mid-stream aborts
// the upload instead of finalizing a truncated object: Put must fail and leave
// no object behind.
func TestGCSPutAbortsOnReaderError(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	const key = "abort/partial.bin"

	r := &errReader{data: []byte("partial data"), err: errors.New("reader blew up")}
	if err := st.Put(ctx, key, r); err == nil {
		t.Fatal("Put with an erroring reader returned nil, want error")
	}
	ok, err := st.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Fatal("Put aborted but an object was still committed; want no object")
	}
}

func TestGCSGetMissingIsNotFound(t *testing.T) {
	st := newStore(t)
	if _, err := st.Get(context.Background(), "missing/nope.bin"); !errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("Get missing err = %v, want store.ErrObjectNotFound", err)
	}
}

func TestGCSDeleteMissingIsNotFound(t *testing.T) {
	// Per the store.ObjectStore contract (unlike a GCS-idempotent delete),
	// deleting an absent key reports ErrObjectNotFound.
	st := newStore(t)
	if err := st.Delete(context.Background(), "missing/nope.bin"); !errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("Delete missing err = %v, want store.ErrObjectNotFound", err)
	}
}

func TestGCSExistsMissingIsFalse(t *testing.T) {
	st := newStore(t)
	ok, err := st.Exists(context.Background(), "missing/nope.bin")
	if err != nil || ok {
		t.Fatalf("Exists missing = (%v, %v), want (false, nil)", ok, err)
	}
}
