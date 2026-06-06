package markdown_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/markdown"
)

var _ extract.Extractor = markdown.Extractor{}

func extractText(t *testing.T, in string) string {
	t.Helper()
	arts, err := markdown.Extractor{}.Extract(context.Background(), strings.NewReader(in), "src-1")
	if err != nil {
		t.Fatalf("Extract(%q): %v", in, err)
	}
	if len(arts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(arts))
	}
	a := arts[0]
	if a.MediaType != markdown.MediaType {
		t.Fatalf("MediaType = %q, want %q", a.MediaType, markdown.MediaType)
	}
	if a.DerivedFrom != "src-1" {
		t.Fatalf("DerivedFrom = %q, want %q", a.DerivedFrom, "src-1")
	}
	if a.ID != "" {
		t.Fatalf("ID = %q, want empty (caller assigns)", a.ID)
	}
	return a.Text
}

func TestExtractVerbatimPreservesStructure(t *testing.T) {
	// Indented code, nested-list indentation, and a two-space hard break must all
	// survive — this is the whole reason Markdown does not use the prose path.
	in := "# Title\n\n    indented code line\n\n- a\n  - nested\n\nline one  \nline two\n"
	got := extractText(t, in)
	if got != in {
		t.Fatalf("verbatim mismatch:\n got %q\nwant %q", got, in)
	}
}

func TestExtractFencedCodeWithHashIsPreserved(t *testing.T) {
	in := "intro\n\n```sh\n# this is a shell comment, not a heading\necho hi\n```\n"
	got := extractText(t, in)
	if got != in {
		t.Fatalf("verbatim mismatch:\n got %q\nwant %q", got, in)
	}
}

func TestStripYAMLFrontMatter(t *testing.T) {
	in := "---\ntitle: Hello\ntags: [a, b]\n---\n# Body\n\ntext\n"
	want := "# Body\n\ntext\n"
	if got := extractText(t, in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStripTOMLFrontMatter(t *testing.T) {
	in := "+++\ntitle = \"Hello\"\n+++\nbody\n"
	want := "body\n"
	if got := extractText(t, in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFrontMatterWithoutClosingDelimiterIsContent(t *testing.T) {
	// No closing "---": the leading delimiter is content, not front matter.
	in := "---\nthis looks like front matter but never closes\nmore text\n"
	if got := extractText(t, in); got != in {
		t.Fatalf("got %q, want unchanged %q", got, in)
	}
}

func TestThematicBreakNotTreatedAsFrontMatter(t *testing.T) {
	// A "---" that is not at byte 0 is a thematic break and must be preserved.
	in := "para one\n\n---\n\npara two\n"
	if got := extractText(t, in); got != in {
		t.Fatalf("got %q, want unchanged %q", got, in)
	}
}

func TestLeadingDelimiterRunIsNotFrontMatter(t *testing.T) {
	// "----" is not exactly "---", so it is not a front-matter opener.
	in := "----\nnot front matter\n----\nbody\n"
	if got := extractText(t, in); got != in {
		t.Fatalf("got %q, want unchanged %q", got, in)
	}
}

func TestBOMStripped(t *testing.T) {
	in := "\ufeff# Heading\n\nbody\n"
	want := "# Heading\n\nbody\n"
	if got := extractText(t, in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmptyAndWhitespaceYieldNoContent(t *testing.T) {
	for _, in := range []string{"", "   \n\t\n", "---\n---\n", "---\n---\n   \n"} {
		_, err := markdown.Extractor{}.Extract(context.Background(), strings.NewReader(in), "src-1")
		if !errors.Is(err, extract.ErrNoContent) {
			t.Fatalf("Extract(%q) err = %v, want ErrNoContent", in, err)
		}
	}
}

func TestInvalidUTF8Coerced(t *testing.T) {
	got := extractText(t, "# ok\n\xff\xfe bad bytes\n")
	if !strings.Contains(got, "�") {
		t.Fatalf("expected U+FFFD replacement in %q", got)
	}
}

func TestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := markdown.Extractor{}.Extract(ctx, strings.NewReader("# x"), "src-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRegistryWiring(t *testing.T) {
	reg := extract.NewRegistry()
	reg.Register(markdown.MediaType, markdown.New())
	reg.Register(markdown.MediaTypeX, markdown.New())

	for _, mt := range []content.MediaType{"text/markdown", "text/x-markdown", "text/markdown; charset=utf-8"} {
		arts, err := reg.Extract(context.Background(), mt, strings.NewReader("# h\n\nbody\n"), "src-1")
		if err != nil {
			t.Fatalf("registry Extract(%q): %v", mt, err)
		}
		if len(arts) != 1 || arts[0].MediaType != markdown.MediaType {
			t.Fatalf("registry Extract(%q) = %+v", mt, arts)
		}
	}
}
