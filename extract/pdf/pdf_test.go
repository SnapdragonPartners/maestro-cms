package pdf_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

var (
	_ extract.Extractor = pdf.Extractor{}
	_ extract.Extractor = pdf.New()
)

// fakeEngine returns canned pages/error, exercising the Extractor without a real
// parser or external binary.
type fakeEngine struct {
	name  string
	pages []pdf.Page
	err   error
}

func (f fakeEngine) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}

func (f fakeEngine) Pages(context.Context, []byte) ([]pdf.Page, error) { return f.pages, f.err }

func TestExtractNoEngineErrors(t *testing.T) {
	_, err := pdf.New().Extract(context.Background(), strings.NewReader("%PDF-1.4"), "s")
	if !errors.Is(err, pdf.ErrNoEngine) {
		t.Fatalf("err = %v, want ErrNoEngine", err)
	}
}

func TestExtractPerPageArtifacts(t *testing.T) {
	eng := fakeEngine{name: "fake", pages: []pdf.Page{
		{Number: 1, Text: "page one"},
		{Number: 2, Text: "   "}, // blank → dropped
		{Number: 3, Text: "page three"},
	}}
	arts, err := pdf.New(pdf.WithEngine(eng)).Extract(context.Background(), strings.NewReader("x"), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 2 {
		t.Fatalf("got %d artifacts, want 2 (blank page dropped)", len(arts))
	}
	want := []struct {
		text, page string
	}{{"page one", "1"}, {"page three", "3"}}
	for i, w := range want {
		a := arts[i]
		if a.Text != w.text {
			t.Fatalf("artifact %d Text = %q, want %q", i, a.Text, w.text)
		}
		if a.MediaType != extract.MediaTypeText {
			t.Fatalf("artifact %d MediaType = %q, want %q", i, a.MediaType, extract.MediaTypeText)
		}
		if a.DerivedFrom != "src-1" || a.ID != "" {
			t.Fatalf("artifact %d provenance: DerivedFrom=%q ID=%q", i, a.DerivedFrom, a.ID)
		}
		if a.Metadata[pdf.MetaPage] != w.page {
			t.Fatalf("artifact %d page = %q, want %q", i, a.Metadata[pdf.MetaPage], w.page)
		}
		if a.Metadata[pdf.MetaEngine] != "fake" {
			t.Fatalf("artifact %d engine = %q, want fake", i, a.Metadata[pdf.MetaEngine])
		}
	}
}

// modeEngine is a fakeEngine that also reports a text mode.
type modeEngine struct {
	fakeEngine
	mode string
}

func (m modeEngine) Mode() string { return m.mode }

var _ pdf.ModeReporter = modeEngine{}

func TestExtractRecordsMode(t *testing.T) {
	eng := modeEngine{fakeEngine: fakeEngine{pages: []pdf.Page{
		{Number: 1, Text: "Ending balance   1,150.00"},
		{Number: 2, Text: "Contributions    250.00"},
	}}, mode: pdf.ModeLayout}
	arts, err := pdf.New(pdf.WithEngine(eng)).Extract(context.Background(), strings.NewReader("x"), "s")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 2 {
		t.Fatalf("got %d artifacts, want 2", len(arts))
	}
	for i, a := range arts {
		if got := a.Metadata[pdf.MetaMode]; got != pdf.ModeLayout {
			t.Fatalf("artifact %d mode = %q, want %q", i, got, pdf.ModeLayout)
		}
	}
	// Gaps inside a line survive whitespace normalization.
	if arts[0].Text != "Ending balance   1,150.00" {
		t.Fatalf("text = %q, want inner gap preserved", arts[0].Text)
	}
}

func TestExtractNoModeKeyWithoutReporter(t *testing.T) {
	eng := fakeEngine{pages: []pdf.Page{{Number: 1, Text: "text"}}}
	arts, err := pdf.New(pdf.WithEngine(eng)).Extract(context.Background(), strings.NewReader("x"), "s")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if v, ok := arts[0].Metadata[pdf.MetaMode]; ok {
		t.Fatalf("unexpected %s = %q for an engine without Mode()", pdf.MetaMode, v)
	}
}

func TestExtractEmptyModeOmitted(t *testing.T) {
	eng := modeEngine{fakeEngine: fakeEngine{pages: []pdf.Page{{Number: 1, Text: "text"}}}}
	arts, err := pdf.New(pdf.WithEngine(eng)).Extract(context.Background(), strings.NewReader("x"), "s")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if v, ok := arts[0].Metadata[pdf.MetaMode]; ok {
		t.Fatalf("unexpected %s = %q for an empty Mode()", pdf.MetaMode, v)
	}
}

func TestExtractAllBlankIsNoContent(t *testing.T) {
	eng := fakeEngine{pages: []pdf.Page{{Number: 1, Text: "  "}, {Number: 2, Text: ""}}}
	_, err := pdf.New(pdf.WithEngine(eng)).Extract(context.Background(), strings.NewReader("x"), "s")
	if !errors.Is(err, extract.ErrNoContent) {
		t.Fatalf("err = %v, want ErrNoContent", err)
	}
}

func TestExtractEngineErrorPropagates(t *testing.T) {
	eng := fakeEngine{err: &extract.MalformedSourceError{MediaType: pdf.MediaType, Err: errors.New("boom")}}
	_, err := pdf.New(pdf.WithEngine(eng)).Extract(context.Background(), strings.NewReader("x"), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

func TestExtractContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pdf.New(pdf.WithEngine(fakeEngine{})).Extract(ctx, strings.NewReader("x"), "s")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
