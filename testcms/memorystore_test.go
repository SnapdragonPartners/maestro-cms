package testcms

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"time"

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
		{"oversized length reads to the end, no overflow", 1, math.MaxInt64, "123456789"},
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

func TestMemoryStoreCompose(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	for k, v := range map[string]string{"p/0": "abc", "p/1": "de", "p/2": "f"} {
		if err := s.Put(ctx, k, bytes.NewReader([]byte(v))); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	if err := s.Compose(ctx, "out", []string{"p/0", "p/1", "p/2"}, "video/mp4"); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	rc, err := s.Get(ctx, "out")
	if err != nil {
		t.Fatalf("Get out: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "abcdef" {
		t.Fatalf("composed = %q, want abcdef", got)
	}
	// Order is the caller's, and dst is replaced, not appended to.
	if err := s.Compose(ctx, "out", []string{"p/2", "p/0"}, ""); err != nil {
		t.Fatalf("Compose again: %v", err)
	}
	rc, _ = s.Get(ctx, "out")
	got, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "fabc" {
		t.Fatalf("recomposed = %q, want fabc", got)
	}
	// A missing source is ErrObjectNotFound and writes nothing.
	if err := s.Compose(ctx, "none", []string{"p/0", "missing"}, ""); !errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("missing source: err = %v, want ErrObjectNotFound", err)
	}
	if ok, _ := s.Exists(ctx, "none"); ok {
		t.Fatal("a failed compose wrote its destination")
	}
	// The source count is bounded as the GCS adapter bounds it.
	many := make([]string, store.MaxComposeSources+1)
	for i := range many {
		many[i] = "p/0"
	}
	if err := s.Compose(ctx, "many", many, ""); err == nil || errors.Is(err, store.ErrObjectNotFound) {
		t.Fatalf("%d sources: err = %v, want a plain error", len(many), err)
	}
	if err := s.Compose(ctx, "many", many[:store.MaxComposeSources], ""); err != nil {
		t.Fatalf("%d sources: %v", store.MaxComposeSources, err)
	}
	if err := s.Compose(ctx, "zero", nil, ""); err == nil {
		t.Fatal("zero sources: want an error")
	}
	// The destination holds a private copy: a later overwrite of a source does
	// not change it.
	if err := s.Put(ctx, "p/0", bytes.NewReader([]byte("ZZZ"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, _ = s.Get(ctx, "out")
	got, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "fabc" {
		t.Fatalf("after source overwrite, composed = %q, want fabc", got)
	}
}

func TestMemoryStoreList(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	// A stepping clock: each write is stamped one second after the last, so
	// Created is deterministic and age-based logic can be tested without
	// sleeping.
	epoch := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tick := 0
	s.Now = func() time.Time { tick++; return epoch.Add(time.Duration(tick) * time.Second) }
	for _, k := range []string{"u/a/1", "u/a/0", "u/b/0", "v/0"} {
		v := map[string]string{"u/a/1": "x", "u/a/0": "xy", "u/b/0": "", "v/0": "xyz"}[k]
		if err := s.Put(ctx, k, bytes.NewReader([]byte(v))); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	var got []store.ObjectInfo
	if err := s.List(ctx, "u/a/", func(o store.ObjectInfo) error { got = append(got, o); return nil }); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Key != "u/a/0" || got[0].Size != 2 || got[1].Key != "u/a/1" || got[1].Size != 1 {
		t.Fatalf("List(u/a/) = %+v, want u/a/0 (2 bytes) then u/a/1 (1 byte)", got)
	}
	// u/a/1 was the first write (epoch+1s), u/a/0 the second (epoch+2s).
	if !got[1].Created.Equal(epoch.Add(time.Second)) || !got[0].Created.Equal(epoch.Add(2*time.Second)) {
		t.Fatalf("Created = %v / %v, want the clock's stamps", got[0].Created, got[1].Created)
	}
	// Compose stamps with the same clock.
	if err := s.Compose(ctx, "u/c", []string{"u/a/0", "u/a/1"}, ""); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	_ = s.List(ctx, "u/c", func(o store.ObjectInfo) error {
		if !o.Created.Equal(epoch.Add(5 * time.Second)) {
			t.Fatalf("composed Created = %v, want epoch+5s", o.Created)
		}
		return nil
	})
	// A broader prefix sees more, an empty one sees everything, a stranger none.
	count := func(prefix string) int {
		n := 0
		_ = s.List(ctx, prefix, func(store.ObjectInfo) error { n++; return nil })
		return n
	}
	if count("u/") != 4 || count("") != 5 || count("w") != 0 {
		t.Fatalf("counts: u/=%d (want 4), \"\"=%d (want 5), w=%d (want 0)", count("u/"), count(""), count("w"))
	}
	// fn's error stops the listing and comes back unchanged.
	sentinel := errors.New("stop")
	seen := 0
	err := s.List(ctx, "", func(store.ObjectInfo) error { seen++; return sentinel })
	if !errors.Is(err, sentinel) || seen != 1 {
		t.Fatalf("List with a failing fn: err = %v, seen = %d; want the sentinel after one", err, seen)
	}
	// fn may call back into the store (the listing runs over a snapshot).
	if err := s.List(ctx, "v/", func(o store.ObjectInfo) error { return s.Delete(ctx, o.Key) }); err != nil {
		t.Fatalf("List with a deleting fn: %v", err)
	}
	if ok, _ := s.Exists(ctx, "v/0"); ok {
		t.Fatal("v/0 still exists after the deleting listing")
	}
}
