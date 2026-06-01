package extract

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/content"
)

// stubExtractor returns a fixed artifact text, recording the parentID it saw.
type stubExtractor struct {
	gotParent string
}

func (s *stubExtractor) Extract(_ context.Context, _ io.Reader, parentID string) ([]content.Artifact, error) {
	s.gotParent = parentID
	return []content.Artifact{{MediaType: MediaTypeText, DerivedFrom: parentID, Text: "stub"}}, nil
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("text/plain"); ok {
		t.Fatal("Get on empty registry returned ok=true")
	}
	stub := &stubExtractor{}
	r.Register("text/plain", stub)
	got, ok := r.Get("text/plain")
	if !ok {
		t.Fatal("Get after Register returned ok=false")
	}
	if got != stub {
		t.Fatal("Get returned a different extractor than registered")
	}
}

func TestRegistryExtractDispatch(t *testing.T) {
	r := NewRegistry()
	stub := &stubExtractor{}
	r.Register("text/plain", stub)

	arts, err := r.Extract(context.Background(), "text/plain", strings.NewReader("ignored"), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 1 || arts[0].DerivedFrom != "src-1" {
		t.Fatalf("unexpected artifacts: %+v", arts)
	}
	if stub.gotParent != "src-1" {
		t.Fatalf("extractor saw parentID %q, want src-1", stub.gotParent)
	}
}

func TestRegistryExtractUnsupported(t *testing.T) {
	r := NewRegistry()
	_, err := r.Extract(context.Background(), "application/pdf", strings.NewReader("x"), "src-1")
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf("Extract err = %v, want ErrUnsupportedMediaType", err)
	}
}

func TestRegistryCanonicalizesMediaType(t *testing.T) {
	r := NewRegistry()
	r.Register("text/plain", &stubExtractor{})

	// Casing and parameters must resolve to the same registration.
	for _, mt := range []content.MediaType{"text/plain", "Text/Plain", "text/plain; charset=utf-8", "  text/plain  "} {
		if _, ok := r.Get(mt); !ok {
			t.Fatalf("Get(%q) = false, want true after registering text/plain", mt)
		}
	}
}

func TestRegistryRegisterCanonicalForm(t *testing.T) {
	// Registering a parameterized/cased value should be found via the bare type.
	r := NewRegistry()
	r.Register("Text/Plain; charset=utf-8", &stubExtractor{})
	if _, ok := r.Get("text/plain"); !ok {
		t.Fatal("Get(text/plain) = false after registering Text/Plain; charset=utf-8")
	}
}

func TestRegistryRegisterNilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register(nil) did not panic")
		}
	}()
	NewRegistry().Register("text/plain", nil)
}

func TestRegistryRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register did not panic")
		}
	}()
	r := NewRegistry()
	r.Register("text/plain", &stubExtractor{})
	// Canonicalizes to the same key as the first — must panic, not clobber.
	r.Register("Text/Plain; charset=utf-8", &stubExtractor{})
}

func TestRegistryRegisterInvalidMediaTypePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register with invalid media type did not panic")
		}
	}()
	NewRegistry().Register("not a media type", &stubExtractor{})
}

func TestRegistryExtractInvalidMediaType(t *testing.T) {
	r := NewRegistry()
	r.Register("text/plain", &stubExtractor{})
	_, err := r.Extract(context.Background(), "not a media type", strings.NewReader("x"), "src-1")
	if !errors.Is(err, ErrInvalidMediaType) {
		t.Fatalf("Extract err = %v, want ErrInvalidMediaType", err)
	}
}

func TestRegistryExtractEmptyParentID(t *testing.T) {
	r := NewRegistry()
	r.Register("text/plain", &stubExtractor{})
	_, err := r.Extract(context.Background(), "text/plain", strings.NewReader("x"), "")
	if !errors.Is(err, ErrMissingParentID) {
		t.Fatalf("Extract err = %v, want ErrMissingParentID", err)
	}
}

// The empty-parentID guard runs before media-type lookup, so even an
// unregistered/invalid media type still reports the missing parent first.
func TestRegistryExtractEmptyParentIDTakesPrecedence(t *testing.T) {
	r := NewRegistry()
	_, err := r.Extract(context.Background(), "not a media type", strings.NewReader("x"), "")
	if !errors.Is(err, ErrMissingParentID) {
		t.Fatalf("Extract err = %v, want ErrMissingParentID (precedence)", err)
	}
}

func TestWithMaxBytesNonPositivePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("WithMaxBytes(0) did not panic")
		}
	}()
	WithMaxBytes(0)
}

func TestRegistryExtractEnforcesMaxBytes(t *testing.T) {
	r := NewRegistry(WithMaxBytes(8))
	r.Register("text/plain", NewTextExtractor())
	_, err := r.Extract(context.Background(), "text/plain", strings.NewReader("123456789"), "src-1") // 9 > 8
	if !errors.Is(err, ErrSourceTooLarge) {
		t.Fatalf("Extract err = %v, want ErrSourceTooLarge", err)
	}
	// The typed error carries the limit for diagnostics.
	var tooLarge *SourceTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("Extract err = %v, want *SourceTooLargeError", err)
	}
	if tooLarge.Limit != 8 {
		t.Fatalf("SourceTooLargeError.Limit = %d, want 8", tooLarge.Limit)
	}
}

func TestRegistryExtractAtMaxBytesIsAllowed(t *testing.T) {
	r := NewRegistry(WithMaxBytes(5))
	r.Register("text/plain", NewTextExtractor())
	arts, err := r.Extract(context.Background(), "text/plain", strings.NewReader("hello"), "src-1") // exactly 5
	if err != nil {
		t.Fatalf("Extract at exactly MaxBytes: %v", err)
	}
	if arts[0].Text != "hello" {
		t.Fatalf("Text = %q, want hello", arts[0].Text)
	}
}

func TestRegistryExtractPerCallMaxBytesOverride(t *testing.T) {
	r := NewRegistry(WithMaxBytes(2)) // registry default too small
	r.Register("text/plain", NewTextExtractor())
	arts, err := r.Extract(context.Background(), "text/plain", strings.NewReader("hello"), "src-1", WithMaxBytes(100))
	if err != nil {
		t.Fatalf("per-call override failed: %v", err)
	}
	if arts[0].Text != "hello" {
		t.Fatalf("Text = %q, want hello", arts[0].Text)
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"trims lines", "  a  \n  b  ", "a\nb"},
		{"collapses blank runs", "a\n\n\n\nb", "a\n\nb"},
		{"trims document edges", "\n\n  hi  \n\n", "hi"},
		{"all blank", "\n  \n\t\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeWhitespace(tt.in); got != tt.want {
				t.Fatalf("normalizeWhitespace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
