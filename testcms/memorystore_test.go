package testcms

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/store"
)

var _ store.ObjectStore = (*MemoryStore)(nil)

func TestMemoryStorePutGet(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	want := []byte("hello world")
	if err := s.Put(ctx, "k1", bytes.NewReader(want)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := s.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get = %q, want %q", got, want)
	}
}

func TestMemoryStoreExists(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	ok, err := s.Exists(ctx, "missing")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Fatal("Exists(missing) = true, want false")
	}
	if err = s.Put(ctx, "k", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ok, err = s.Exists(ctx, "k")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Fatal("Exists(k) = false, want true")
	}
}

func TestMemoryStoreGetMissing(t *testing.T) {
	_, err := NewMemoryStore().Get(context.Background(), "nope")
	if !errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrObjectNotFound", err)
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Put(ctx, "k", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.Delete(ctx, "k"); !errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("Delete(absent) err = %v, want ErrObjectNotFound", err)
	}
}

func TestMemoryStoreGetReturnsCopy(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Put(ctx, "k", bytes.NewReader([]byte("abc"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	b[0] = 'X' // mutate the caller's copy

	rc2, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc2.Close()
	b2, _ := io.ReadAll(rc2)
	if string(b2) != "abc" {
		t.Fatalf("stored bytes were mutated through the returned copy: got %q, want abc", b2)
	}
}
