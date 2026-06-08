package gcs_test

import (
	"context"
	"testing"

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
