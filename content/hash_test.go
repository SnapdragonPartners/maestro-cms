package content_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/content"
)

// Known SHA-256 vectors.
const (
	hashABC   = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" // "abc"
	hashEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // ""
)

func TestSHA256HexBytesAndString(t *testing.T) {
	if got := content.SHA256HexBytes([]byte("abc")); got != hashABC {
		t.Fatalf("SHA256HexBytes = %q, want %q", got, hashABC)
	}
	if got := content.SHA256HexString("abc"); got != hashABC {
		t.Fatalf("SHA256HexString = %q, want %q", got, hashABC)
	}
	if got := content.SHA256HexBytes(nil); got != hashEmpty {
		t.Fatalf("SHA256HexBytes(nil) = %q, want empty-hash %q", got, hashEmpty)
	}
}

func TestSHA256HexReader(t *testing.T) {
	h, n, err := content.SHA256HexReader(context.Background(), strings.NewReader("abc"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 3 {
		t.Fatalf("n = %d, want 3", n)
	}
	if h != hashABC {
		t.Fatalf("hash = %q, want %q", h, hashABC)
	}
}

func TestSHA256HexReaderAgreesWithBytes(t *testing.T) {
	const s = "the quick brown fox\n\nmultiple paragraphs"
	want := content.SHA256HexString(s)
	got, n, err := content.SHA256HexReader(context.Background(), strings.NewReader(s))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Fatalf("reader hash %q != string hash %q", got, want)
	}
	if n != int64(len(s)) {
		t.Fatalf("n = %d, want %d", n, len(s))
	}
}

func TestSHA256HexReaderCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := content.SHA256HexReader(ctx, strings.NewReader("abc"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
