//go:build integration

// Integration tests for the GCS adapter. They run only under the `integration`
// build tag and require a GCS-compatible endpoint named by STORAGE_EMULATOR_HOST
// (e.g. a fsouza/fake-gcs-server container). `make test-integration` starts that
// container, sets the variable, and runs these; without it they are skipped.
package gcs_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strings"
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

func TestGCSNewEmulatorRoundTrip(t *testing.T) {
	_ = newStore(t) // skips if no emulator, and ensures the bucket exists
	ctx := context.Background()
	st, err := gcs.NewEmulator(ctx, testBucket, os.Getenv("STORAGE_EMULATOR_HOST"))
	if err != nil {
		t.Fatalf("NewEmulator: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() }) // New-constructed: owns and closes its client

	const key = "emulator/obj.bin"
	if err := st.Put(ctx, key, strings.NewReader("via emulator")); err != nil {
		t.Fatalf("Put: %v", err)
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
	if string(got) != "via emulator" {
		t.Fatalf("Get = %q, want %q", got, "via emulator")
	}
	_ = st.Delete(ctx, key)
}

func TestGCSGetRange(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	const key = "range/object.bin"
	payload := []byte("0123456789abcdef")
	if err := st.Put(ctx, key, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	read := func(offset, length int64) (string, error) {
		rc, err := st.GetRange(ctx, key, offset, length)
		if err != nil {
			return "", err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		return string(b), err
	}
	for _, c := range []struct {
		name           string
		offset, length int64
		want           string
	}{
		{"middle", 2, 3, "234"},
		{"to the end", 10, -1, "abcdef"},
		{"runs past the end, shortened", 14, 10, "ef"},
		{"whole object", 0, -1, string(payload)},
		// The SDK's inclusive endpoint (offset+length-1) would overflow;
		// the adapter reads to the end instead.
		{"oversized length reads to the end", 2, math.MaxInt64, "23456789abcdef"},
	} {
		got, err := read(c.offset, c.length)
		if err != nil || got != c.want {
			t.Errorf("%s: GetRange(%d, %d) = %q, %v; want %q", c.name, c.offset, c.length, got, err, c.want)
		}
	}
	if _, err := read(int64(len(payload)), 1); !errors.Is(err, store.ErrRangeNotSatisfiable) {
		t.Errorf("offset at the end: err = %v, want ErrRangeNotSatisfiable", err)
	}
	// A zero-length read is a metadata-only request the service answers
	// even past the end; the contract still says unsatisfiable.
	if _, err := read(int64(len(payload))+5, 0); !errors.Is(err, store.ErrRangeNotSatisfiable) {
		t.Errorf("zero-length read past the end: err = %v, want ErrRangeNotSatisfiable", err)
	}
	if got, err := read(3, 0); err != nil || got != "" {
		t.Errorf("zero-length read inside the object = %q, %v; want empty, nil", got, err)
	}
	if _, err := st.GetRange(ctx, key, -1, 1); err == nil {
		t.Error("negative offset: want an error")
	}
	if _, err := st.GetRange(ctx, "range/missing", 0, 1); !errors.Is(err, store.ErrObjectNotFound) {
		t.Errorf("missing object: err = %v, want ErrObjectNotFound", err)
	}
}

// An empty object has no byte to serve: offset 0 is already at the end.
func TestGCSGetRangeEmptyObject(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	const key = "range/empty.bin"
	if err := st.Put(ctx, key, bytes.NewReader(nil)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if rc, err := st.GetRange(ctx, key, 0, -1); !errors.Is(err, store.ErrRangeNotSatisfiable) {
		if rc != nil {
			_ = rc.Close()
		}
		t.Errorf("empty object, offset 0: err = %v, want ErrRangeNotSatisfiable (as MemoryStore)", err)
	}
}

// A range addresses the STORED bytes: an object stored gzip-encoded is
// served as its compressed bytes, not transcoded (which could hand back
// the whole decompressed object under a range request).
func TestGCSGetRangeReadsStoredBytesOfAGzipObject(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	const key = "range/encoded.txt"
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(bytes.Repeat([]byte("the same line over and over\n"), 200)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	w := client.Bucket(testBucket).Object(key).NewWriter(ctx)
	w.ContentType, w.ContentEncoding = "text/plain", "gzip"
	if _, err := w.Write(compressed.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("write gzip object: %v", err)
	}
	rc, err := st.GetRange(ctx, key, 4, 16)
	if err != nil {
		t.Fatalf("GetRange: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if want := compressed.Bytes()[4:20]; !bytes.Equal(got, want) {
		t.Fatalf("GetRange over a gzip-encoded object = %d bytes %q, want the stored bytes 4..19 %q", len(got), got, want)
	}
}

func TestGCSCompose(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	parts := []string{"compose/p0", "compose/p1", "compose/p2"}
	want := ""
	for i, k := range parts {
		chunk := strings.Repeat(string(rune('a'+i)), 1000+i)
		want += chunk
		if err := st.Put(ctx, k, strings.NewReader(chunk)); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	if err := st.Compose(ctx, "compose/out", parts, "video/mp4"); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	rc, err := st.Get(ctx, "compose/out")
	if err != nil {
		t.Fatalf("Get out: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || string(got) != want {
		t.Fatalf("composed bytes differ (len %d vs %d, err %v)", len(got), len(want), err)
	}
	// The content type the caller named is the destination's (read through
	// the SDK directly; the Store has no attributes accessor).
	client, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer func() { _ = client.Close() }()
	attrs, err := client.Bucket(testBucket).Object("compose/out").Attrs(ctx)
	if err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.ContentType != "video/mp4" {
		t.Fatalf("content type = %q, want video/mp4", attrs.ContentType)
	}
	// A missing source fails and writes nothing. The service answers 404,
	// which the adapter maps to ErrObjectNotFound (TestComposeMapsNotFound,
	// against a stand-in server); the emulator answers 500 instead, so here
	// only the failure and the absence of the destination are asserted.
	if err := st.Compose(ctx, "compose/none", []string{parts[0], "compose/missing"}, ""); err == nil {
		t.Fatal("missing source: Compose succeeded")
	}
	if ok, _ := st.Exists(ctx, "compose/none"); ok {
		t.Fatal("a failed compose wrote its destination")
	}
	// Too many sources is refused before any request.
	many := make([]string, store.MaxComposeSources+1)
	for i := range many {
		many[i] = parts[0]
	}
	if err := st.Compose(ctx, "compose/many", many, ""); err == nil || errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("%d sources: err = %v, want a plain error", len(many), err)
	}
}

func TestGCSList(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	for k, v := range map[string]string{"list/a/1": "x", "list/a/0": "xy", "list/b": "xyz"} {
		if err := st.Put(ctx, k, strings.NewReader(v)); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	var got []store.ObjectInfo
	if err := st.List(ctx, "list/a/", func(o store.ObjectInfo) error { got = append(got, o); return nil }); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Key != "list/a/0" || got[0].Size != 2 || got[1].Key != "list/a/1" || got[1].Size != 1 {
		t.Fatalf("List(list/a/) = %+v, want list/a/0 (2) then list/a/1 (1)", got)
	}
	if got[0].Created.IsZero() {
		t.Fatal("Created is zero")
	}
	sentinel := errors.New("stop")
	seen := 0
	if err := st.List(ctx, "list/", func(store.ObjectInfo) error { seen++; return sentinel }); !errors.Is(err, sentinel) || seen != 1 {
		t.Fatalf("failing fn: err = %v, seen = %d; want the sentinel after one", err, seen)
	}
	n := 0
	if err := st.List(ctx, "list/nothing/", func(store.ObjectInfo) error { n++; return nil }); err != nil || n != 0 {
		t.Fatalf("empty prefix listing: err = %v, n = %d", err, n)
	}
}
