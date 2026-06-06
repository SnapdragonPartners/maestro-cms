package testcms_test

import (
	"context"
	"slices"
	"testing"

	"github.com/SnapdragonPartners/maestro-llms/llms"

	"github.com/SnapdragonPartners/maestro-cms/testcms"
)

func TestFakeEmbedderDeterministicAndOrdered(t *testing.T) {
	f := &testcms.FakeEmbedder{}
	req := llms.EmbeddingRequest{Inputs: []llms.EmbeddingInput{
		{ID: "a", Text: "hello"},
		{ID: "b", Text: "world"},
	}}

	r1, err := f.Embed(context.Background(), req)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(r1.Vectors) != 2 {
		t.Fatalf("got %d vectors, want 2", len(r1.Vectors))
	}
	if r1.Vectors[0].ID != "a" || r1.Vectors[1].ID != "b" {
		t.Fatalf("vector IDs not preserved in order: %q, %q", r1.Vectors[0].ID, r1.Vectors[1].ID)
	}
	for _, v := range r1.Vectors {
		if len(v.Values) != testcms.DefaultFakeDimensions {
			t.Fatalf("vector len %d, want %d", len(v.Values), testcms.DefaultFakeDimensions)
		}
	}
	// Same text -> identical vector (determinism); different text -> different.
	r2, _ := f.Embed(context.Background(), req)
	if !slices.Equal(r1.Vectors[0].Values, r2.Vectors[0].Values) {
		t.Fatal("same input produced different vectors across calls")
	}
	if slices.Equal(r1.Vectors[0].Values, r1.Vectors[1].Values) {
		t.Fatal("distinct inputs produced identical vectors")
	}
	if f.Calls() != 2 {
		t.Fatalf("Calls() = %d, want 2", f.Calls())
	}
}

func TestFakeEmbedderDefaultsAndOverrides(t *testing.T) {
	def := &testcms.FakeEmbedder{}
	if got := def.Model(); got != (llms.ModelRef{Provider: "fake", Name: "fake-embedding-001"}) {
		t.Fatalf("default Model() = %+v", got)
	}
	if def.DefaultDimensions() != testcms.DefaultFakeDimensions {
		t.Fatalf("default dims = %d", def.DefaultDimensions())
	}

	custom := &testcms.FakeEmbedder{ModelRef: llms.ModelRef{Provider: "p", Name: "m"}, Dims: 16}
	if custom.Model() != (llms.ModelRef{Provider: "p", Name: "m"}) {
		t.Fatalf("custom Model() = %+v", custom.Model())
	}
	r, _ := custom.Embed(context.Background(), llms.EmbeddingRequest{Inputs: []llms.EmbeddingInput{{ID: "x", Text: "t"}}})
	if len(r.Vectors[0].Values) != 16 {
		t.Fatalf("custom dims vector len = %d, want 16", len(r.Vectors[0].Values))
	}
}

func TestFakeEmbedderContextCanceled(t *testing.T) {
	f := &testcms.FakeEmbedder{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Embed(ctx, llms.EmbeddingRequest{}); err == nil {
		t.Fatal("expected error on canceled context")
	}
	if f.Calls() != 0 {
		t.Fatalf("Calls() = %d, want 0 (canceled before work)", f.Calls())
	}
}
