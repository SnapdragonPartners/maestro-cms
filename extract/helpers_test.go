package extract_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
)

func TestRegistrySupports(t *testing.T) {
	r := extract.NewRegistry()
	r.Register("text/plain", extract.NewTextExtractor())

	if !r.Supports("text/plain") {
		t.Fatal("Supports(text/plain) = false")
	}
	// Same canonicalization as Extract: casing + parameters are normalized.
	if !r.Supports("Text/Plain; charset=utf-8") {
		t.Fatal("Supports with casing/params = false")
	}
	if r.Supports("application/pdf") {
		t.Fatal("Supports(application/pdf) = true for empty registry entry")
	}
}

func TestRegistrySupportedMediaTypesSorted(t *testing.T) {
	r := extract.NewRegistry()
	r.Register("text/plain", extract.NewTextExtractor())
	r.Register("application/json", extract.NewTextExtractor())

	got := r.SupportedMediaTypes()
	want := []content.MediaType{"application/json", "text/plain"}
	if !slices.Equal(got, want) {
		t.Fatalf("SupportedMediaTypes = %v, want %v (sorted)", got, want)
	}
}

func TestTextArtifacts(t *testing.T) {
	in := []content.Artifact{
		{Text: "first"},
		{Text: ""}, // empty inline text: excluded
		{Blob: &content.StoreHandle{Backend: "gcs"}}, // blob payload: excluded
		{Text: "second", Blob: nil},
	}
	got := extract.TextArtifacts(in)
	if len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
		t.Fatalf("TextArtifacts = %+v, want the two non-empty inline-text artifacts", got)
	}
}

func TestSingleTextArtifact(t *testing.T) {
	one, err := extract.SingleTextArtifact([]content.Artifact{{Text: "only"}})
	if err != nil || one.Text != "only" {
		t.Fatalf("single = (%+v, %v), want the one artifact", one, err)
	}

	_, err = extract.SingleTextArtifact([]content.Artifact{{Blob: &content.StoreHandle{}}})
	if !errors.Is(err, extract.ErrNoTextArtifact) {
		t.Fatalf("no-text err = %v, want ErrNoTextArtifact", err)
	}

	_, err = extract.SingleTextArtifact([]content.Artifact{{Text: "a"}, {Text: "b"}})
	if !errors.Is(err, extract.ErrMultipleTextArtifacts) {
		t.Fatalf("multi err = %v, want ErrMultipleTextArtifacts", err)
	}
}
