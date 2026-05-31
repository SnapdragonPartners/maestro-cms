package content

import (
	"errors"
	"testing"
)

func TestSourceValidate(t *testing.T) {
	tests := []struct {
		name string
		src  Source
		want error
	}{
		{"valid", Source{ID: "s1", MediaType: "text/plain"}, nil},
		{"missing id", Source{MediaType: "text/plain"}, ErrMissingID},
		{"missing media type", Source{ID: "s1"}, ErrMissingMediaType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.src.Validate(); !errors.Is(got, tt.want) {
				t.Fatalf("Validate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestArtifactValidate(t *testing.T) {
	tests := []struct {
		name string
		art  Artifact
		want error
	}{
		{"valid", Artifact{ID: "a1", MediaType: "text/plain", DerivedFrom: "s1"}, nil},
		{"missing id", Artifact{MediaType: "text/plain", DerivedFrom: "s1"}, ErrMissingID},
		{"missing media type", Artifact{ID: "a1", DerivedFrom: "s1"}, ErrMissingMediaType},
		{"missing parent", Artifact{ID: "a1", MediaType: "text/plain"}, ErrMissingParent},
		// Validate is structural/provenance-only: payload is not checked, so an
		// artifact with neither Text nor Blob is intentionally valid.
		{"no payload is valid", Artifact{ID: "a1", MediaType: "text/plain", DerivedFrom: "s1"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.art.Validate(); !errors.Is(got, tt.want) {
				t.Fatalf("Validate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSourceCloneIndependentMetadata(t *testing.T) {
	orig := Source{ID: "s1", MediaType: "text/plain", Metadata: map[string]string{"k": "v"}}
	clone := orig.Clone()
	clone.Metadata["k"] = "changed"
	clone.Metadata["new"] = "x"
	if orig.Metadata["k"] != "v" {
		t.Fatalf("clone mutation leaked into original: got %q, want v", orig.Metadata["k"])
	}
	if _, ok := orig.Metadata["new"]; ok {
		t.Fatal("clone insertion leaked into original")
	}
}

func TestSourceCloneNilMetadataStaysNil(t *testing.T) {
	if got := (Source{ID: "s1", MediaType: "text/plain"}).Clone(); got.Metadata != nil {
		t.Fatalf("Clone() Metadata = %v, want nil", got.Metadata)
	}
}

func TestArtifactCloneIndependentMetadataAndBlob(t *testing.T) {
	orig := Artifact{
		ID: "a1", MediaType: "image/png", DerivedFrom: "s1",
		Blob:     &StoreHandle{Backend: "fs", Key: "orig"},
		Metadata: map[string]string{"k": "v"},
	}
	clone := orig.Clone()
	clone.Metadata["k"] = "changed"
	clone.Blob.Key = "changed"
	if orig.Metadata["k"] != "v" {
		t.Fatalf("clone metadata mutation leaked: got %q, want v", orig.Metadata["k"])
	}
	if orig.Blob.Key != "orig" {
		t.Fatalf("clone blob mutation leaked: got %q, want orig", orig.Blob.Key)
	}
}

func TestArtifactCloneNilBlobStaysNil(t *testing.T) {
	if got := (Artifact{ID: "a1", MediaType: "text/plain", DerivedFrom: "s1"}).Clone(); got.Blob != nil {
		t.Fatalf("Clone() Blob = %v, want nil", got.Blob)
	}
}
