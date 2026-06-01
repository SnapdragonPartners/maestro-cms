package html_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/html"
)

// Compile-time check that the HTML extractor satisfies the Extractor interface.
var _ extract.Extractor = html.Extractor{}

func TestExtractVisibleText(t *testing.T) {
	const doc = `<!DOCTYPE html><html><head><title>T</title>
		<style>.x{color:red}</style></head>
		<body><h1>Hello</h1><p>world <b>bold</b></p>
		<script>var noise = "should not appear";</script></body></html>`
	arts, err := html.New().Extract(context.Background(), strings.NewReader(doc), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(arts))
	}
	a := arts[0]
	if a.MediaType != extract.MediaTypeText {
		t.Fatalf("MediaType = %q, want %q", a.MediaType, extract.MediaTypeText)
	}
	if a.DerivedFrom != "src-1" {
		t.Fatalf("DerivedFrom = %q, want src-1", a.DerivedFrom)
	}
	if a.ID != "" {
		t.Fatalf("ID = %q, want empty (caller assigns)", a.ID)
	}
	for _, want := range []string{"Hello", "world", "bold"} {
		if !strings.Contains(a.Text, want) {
			t.Fatalf("text missing %q: %q", want, a.Text)
		}
	}
	for _, unwanted := range []string{"should not appear", "color:red", "var noise"} {
		if strings.Contains(a.Text, unwanted) {
			t.Fatalf("text contains script/style content %q: %q", unwanted, a.Text)
		}
	}
}

func TestExtractInlineTextDoesNotFuse(t *testing.T) {
	arts, err := html.New().Extract(context.Background(), strings.NewReader("<p><b>Hello</b>world</p>"), "s")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// A space between text nodes keeps the words separate.
	if !strings.Contains(arts[0].Text, "Hello world") {
		t.Fatalf("inline text fused: %q", arts[0].Text)
	}
}

func TestExtractEmptyIsNoContent(t *testing.T) {
	for _, doc := range []string{
		"",
		"<html><head><style>.x{}</style></head><body></body></html>",
		"<script>only();</script>",
	} {
		_, err := html.New().Extract(context.Background(), strings.NewReader(doc), "s")
		if !errors.Is(err, extract.ErrNoContent) {
			t.Fatalf("Extract(%q) err = %v, want ErrNoContent", doc, err)
		}
	}
}

func TestExtractHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := html.New().Extract(ctx, strings.NewReader("<p>hi</p>"), "s")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract err = %v, want context.Canceled", err)
	}
}

// Via a Registry: registration under text/html dispatches here, and the
// registry's size limit applies through the subpackage.
func TestExtractThroughRegistry(t *testing.T) {
	reg := extract.NewRegistry()
	reg.Register("text/html", html.New())

	arts, err := reg.Extract(context.Background(), "text/html; charset=utf-8",
		strings.NewReader("<p>registered</p>"), "src-1")
	if err != nil {
		t.Fatalf("registry Extract: %v", err)
	}
	if !strings.Contains(arts[0].Text, "registered") {
		t.Fatalf("unexpected text: %q", arts[0].Text)
	}

	_, err = reg.Extract(context.Background(), "text/html",
		strings.NewReader("<p>too big for the tiny cap here</p>"), "src-1", extract.WithMaxBytes(8))
	if !errors.Is(err, extract.ErrSourceTooLarge) {
		t.Fatalf("registry size limit not applied to subpackage: err = %v", err)
	}
}
