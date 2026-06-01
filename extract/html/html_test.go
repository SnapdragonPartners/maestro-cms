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
	const doc = `<!DOCTYPE html><html><head><title>PAGETITLE</title>
		<meta name="description" content="METADESC">
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
	// Non-visible head content (title, meta), CSS, and scripts must be excluded.
	for _, unwanted := range []string{"PAGETITLE", "METADESC", "should not appear", "color:red", "var noise"} {
		if strings.Contains(a.Text, unwanted) {
			t.Fatalf("text contains non-visible content %q: %q", unwanted, a.Text)
		}
	}
}

// Block-level elements must become paragraph boundaries so the boundary-aware
// chunker sees structure rather than one flat line.
func TestExtractPreservesBlockStructure(t *testing.T) {
	const doc = `<h1>Title</h1><p>First para.</p><p>Second para.</p>`
	arts, err := html.New().Extract(context.Background(), strings.NewReader(doc), "s")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	text := arts[0].Text
	// Each block is separated by a blank line.
	for _, want := range []string{"Title\n\nFirst para.", "First para.\n\nSecond para."} {
		if !strings.Contains(text, want) {
			t.Fatalf("block structure not preserved: missing %q in %q", want, text)
		}
	}
	// Minified input (no whitespace between tags) must still split into 3 units.
	if got := strings.Count(text, "\n\n"); got < 2 {
		t.Fatalf("expected >=2 paragraph breaks, got %d in %q", got, text)
	}
}

// <br> is a line break, not a paragraph break.
func TestExtractBrIsLineBreak(t *testing.T) {
	arts, err := html.New().Extract(context.Background(), strings.NewReader("<p>line one<br>line two</p>"), "s")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(arts[0].Text, "line one\nline two") {
		t.Fatalf("br did not produce a line break: %q", arts[0].Text)
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
