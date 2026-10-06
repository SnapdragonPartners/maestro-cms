package gcs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/SnapdragonPartners/maestro-cms/store"
	"github.com/SnapdragonPartners/maestro-cms/store/gcs"
)

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: expected panic, got none", name)
		}
	}()
	fn()
}

var (
	_ store.RangeReader = (*gcs.Store)(nil)
	_ store.Composer    = (*gcs.Store)(nil)
	_ store.Lister      = (*gcs.Store)(nil)
)

func TestNewWithClientPanics(t *testing.T) {
	// Bucket is checked before client, so an empty bucket panics even with a nil
	// client; a nil client panics with a valid bucket.
	mustPanic(t, "empty bucket", func() { gcs.NewWithClient("", nil) })
	mustPanic(t, "nil client", func() { gcs.NewWithClient("bucket", nil) })
}

func TestNewEmulatorValidates(t *testing.T) {
	ctx := context.Background()
	if _, err := gcs.NewEmulator(ctx, "bucket", ""); err == nil {
		t.Fatal("NewEmulator with empty endpoint = nil error, want error")
	}
	// Empty bucket is rejected by New before any client/network work.
	if _, err := gcs.NewEmulator(ctx, "", "http://localhost:4443"); err == nil {
		t.Fatal("NewEmulator with empty bucket = nil error, want error")
	}
}

// TestComposeMapsNotFound: the service answers a compose whose source does not
// exist with 404, and the adapter must surface that as store.ErrObjectNotFound
// rather than a generic error. The emulator answers 500 for the same case, so
// the mapping is exercised against a stand-in server that answers 404 to
// everything.
func TestComposeMapsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"No such object","errors":[{"message":"No such object","reason":"notFound"}]}}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer func() { _ = client.Close() }()
	st := gcs.NewWithClient("bucket", client)
	if err := st.Compose(ctx, "dst", []string{"a", "b"}, "video/mp4"); !errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("Compose against a 404 server: err = %v, want ErrObjectNotFound", err)
	}
	// The source-count check needs no server at all.
	if err := st.Compose(ctx, "dst", nil, ""); err == nil || errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("zero sources: err = %v, want a plain error", err)
	}
}
