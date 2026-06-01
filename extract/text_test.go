package extract

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var _ Extractor = TextExtractor{}

func TestTextExtractor(t *testing.T) {
	arts, err := NewTextExtractor().Extract(context.Background(), strings.NewReader("  hello  \n\n\n  world  "), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(arts))
	}
	a := arts[0]
	if a.Text != "hello\n\nworld" {
		t.Fatalf("Text = %q, want %q", a.Text, "hello\n\nworld")
	}
	if a.MediaType != MediaTypeText {
		t.Fatalf("MediaType = %q, want %q", a.MediaType, MediaTypeText)
	}
	if a.DerivedFrom != "src-1" {
		t.Fatalf("DerivedFrom = %q, want src-1", a.DerivedFrom)
	}
	if a.ID != "" {
		t.Fatalf("ID = %q, want empty (caller assigns)", a.ID)
	}
}

func TestTextExtractorEmptyIsNoContent(t *testing.T) {
	_, err := NewTextExtractor().Extract(context.Background(), strings.NewReader("   \n\t\n"), "src-1")
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("Extract err = %v, want ErrNoContent", err)
	}
}

func TestTextExtractorInvalidUTF8(t *testing.T) {
	arts, err := NewTextExtractor().Extract(context.Background(), strings.NewReader("a\xffb"), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(arts[0].Text, "�") {
		t.Fatalf("invalid UTF-8 not replaced with U+FFFD: %q", arts[0].Text)
	}
}

func TestTextExtractorHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewTextExtractor().Extract(ctx, strings.NewReader("hello"), "src-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract err = %v, want context.Canceled", err)
	}
}
