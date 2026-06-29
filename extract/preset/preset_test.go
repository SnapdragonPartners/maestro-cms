package preset_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/preset"
)

func TestNewDocumentRegistrySupportsAll(t *testing.T) {
	reg := preset.NewDocumentRegistry()
	for _, mt := range []content.MediaType{
		"text/plain", "text/html", "application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"text/markdown", "text/x-markdown",
	} {
		if !reg.Supports(mt) {
			t.Errorf("registry does not support %q", mt)
		}
	}
}

func TestSupportedDocumentMediaTypesSorted(t *testing.T) {
	got := preset.SupportedDocumentMediaTypes()
	want := []content.MediaType{
		"application/pdf",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"text/html",
		"text/markdown",
		"text/plain",
		"text/x-markdown",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("SupportedDocumentMediaTypes = %v, want %v", got, want)
	}
	// Delegation guarantee: the bundle list matches what a fresh registry reports.
	if !slices.Equal(got, preset.NewDocumentRegistry().SupportedMediaTypes()) {
		t.Fatal("SupportedDocumentMediaTypes drifted from the registry")
	}
}

func TestNewDocumentRegistryExtracts(t *testing.T) {
	reg := preset.NewDocumentRegistry()
	// Markdown is wired and preserved verbatim (structure intact).
	arts, err := reg.Extract(context.Background(), "text/markdown", strings.NewReader("# Title\n\nbody"), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := extract.SingleTextArtifact(arts)
	if err != nil {
		t.Fatalf("SingleTextArtifact: %v", err)
	}
	if got.MediaType != "text/markdown" || !strings.Contains(got.Text, "# Title") {
		t.Fatalf("unexpected artifact: %+v", got)
	}
}

func TestNewDocumentRegistryPassesOptions(t *testing.T) {
	// WithMaxBytes flows through to the registry: an oversize source is rejected.
	reg := preset.NewDocumentRegistry(extract.WithMaxBytes(4))
	_, err := reg.Extract(context.Background(), "text/plain", strings.NewReader("way too long"), "src-1")
	if err == nil {
		t.Fatal("expected ErrSourceTooLarge from WithMaxBytes, got nil")
	}
}
