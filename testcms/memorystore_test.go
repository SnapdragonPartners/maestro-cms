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
var _ store.RangeReader = (*MemoryStore)(nil)

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

func TestMemoryStoreGetRange(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Put(ctx, "k", bytes.NewReader([]byte("0123456789"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	cases := []struct {
		name           string
		offset, length int64
		want           string
	}{
		{"middle", 2, 3, "234"},
		{"to the end", 7, -1, "789"},
		{"runs past the end, shortened", 8, 10, "89"},
		{"zero length", 4, 0, ""},
		{"whole object", 0, -1, "0123456789"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc, err := s.GetRange(ctx, "k", c.offset, c.length)
			if err != nil {
				t.Fatalf("GetRange(%d, %d): %v", c.offset, c.length, err)
			}
			defer rc.Close()
			got, err := io.ReadAll(rc)
			if err != nil || string(got) != c.want {
				t.Fatalf("GetRange(%d, %d) = %q, %v; want %q", c.offset, c.length, got, err, c.want)
			}
		})
	}
	if _, err := s.GetRange(ctx, "k", 10, 1); !errors.Is(err, store.ErrRangeNotSatisfiable) {
		t.Errorf("offset at the end: err = %v, want ErrRangeNotSatisfiable", err)
	}
	if _, err := s.GetRange(ctx, "k", -1, 1); err == nil || errors.Is(err, store.ErrRangeNotSatisfiable) {
		t.Errorf("negative offset: err = %v, want a plain error", err)
	}
	if _, err := s.GetRange(ctx, "missing", 0, 1); !errors.Is(err, store.ErrObjectNotFound) {
		t.Errorf("absent key: err = %v, want ErrObjectNotFound", err)
	}
	// The returned bytes are a private copy: mutating them does not touch the store.
	rc, _ := s.GetRange(ctx, "k", 0, 2)
	got, _ := io.ReadAll(rc)
	got[0] = 'x'
	rc2, _ := s.Get(ctx, "k")
	all, _ := io.ReadAll(rc2)
	if string(all) != "0123456789" {
		t.Errorf("GetRange handed out the store's own bytes: %q", all)
	}
}
