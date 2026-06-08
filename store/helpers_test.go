package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/store"
	"github.com/SnapdragonPartners/maestro-cms/testcms"
)

func TestIsNotFound(t *testing.T) {
	if !store.IsNotFound(store.ErrObjectNotFound) {
		t.Fatal("IsNotFound(ErrObjectNotFound) = false")
	}
	if !store.IsNotFound(errwrap(store.ErrObjectNotFound)) {
		t.Fatal("IsNotFound(wrapped) = false")
	}
	if store.IsNotFound(nil) {
		t.Fatal("IsNotFound(nil) = true")
	}
	if store.IsNotFound(errors.New("other")) {
		t.Fatal("IsNotFound(other) = true")
	}
}

func errwrap(err error) error { return errors.Join(errors.New("ctx"), err) }

func TestDeleteIfExistsMissingIsNil(t *testing.T) {
	ms := testcms.NewMemoryStore()
	if err := store.DeleteIfExists(context.Background(), ms, "absent"); err != nil {
		t.Fatalf("DeleteIfExists(absent) = %v, want nil", err)
	}
}

func TestDeleteIfExistsPresentDeletes(t *testing.T) {
	ms := testcms.NewMemoryStore()
	ctx := context.Background()
	if err := ms.Put(ctx, "k", strings.NewReader("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.DeleteIfExists(ctx, ms, "k"); err != nil {
		t.Fatalf("DeleteIfExists: %v", err)
	}
	if ok, _ := ms.Exists(ctx, "k"); ok {
		t.Fatal("object still present after DeleteIfExists")
	}
}

// failStore returns a non-not-found error from Delete, to confirm DeleteIfExists
// passes real errors through.
type failStore struct {
	store.ObjectStore
	err error
}

func (f failStore) Delete(context.Context, string) error { return f.err }

func TestDeleteIfExistsPassesOtherErrors(t *testing.T) {
	boom := errors.New("transport failure")
	err := store.DeleteIfExists(context.Background(), failStore{err: boom}, "k")
	if !errors.Is(err, boom) {
		t.Fatalf("DeleteIfExists = %v, want %v", err, boom)
	}
}
