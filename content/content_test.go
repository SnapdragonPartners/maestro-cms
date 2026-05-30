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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.art.Validate(); !errors.Is(got, tt.want) {
				t.Fatalf("Validate() = %v, want %v", got, tt.want)
			}
		})
	}
}
