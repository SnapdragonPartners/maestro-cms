package testcms

import (
	"context"
	"hash/fnv"
	"strconv"
	"sync"

	"github.com/SnapdragonPartners/maestro-llms/llms"
)

// FakeEmbedder is a deterministic, offline llms.EmbeddingClient for tests. Each
// input maps to a stable vector derived from its text, so assertions are
// reproducible and no network or model is involved. It is safe for concurrent
// use.
//
// Two optional hooks drive failure-path testing without bespoke clients:
//
//   - FailFunc, if set, is consulted on every Embed call; returning a non-nil
//     error makes that call fail. Use it to simulate provider errors or a
//     "poison" input (fail any batch that contains it) to exercise the embed
//     runner's bisection.
//   - Corrupt, if set, post-processes a successful response, e.g. dropping,
//     duplicating, or relabeling a vector to exercise the runner's defensive
//     ID matching.
//
// The zero value is a usable client: 8-dimensional vectors, model {fake,
// fake-embedding-001}, no injected failures.
type FakeEmbedder struct {
	// ModelRef is what Model() reports; the zero value gets a default.
	ModelRef llms.ModelRef
	// Dims is the produced vector length; <=0 means DefaultFakeDimensions.
	Dims int
	// FailFunc, when non-nil, can fail a call by returning a non-nil error.
	FailFunc func(req llms.EmbeddingRequest) error
	// Corrupt, when non-nil, transforms a successful response before it returns.
	Corrupt func(resp llms.EmbeddingResponse) llms.EmbeddingResponse

	mu        sync.Mutex
	callCount int
}

// DefaultFakeDimensions is the vector length a FakeEmbedder produces when Dims
// is unset.
const DefaultFakeDimensions = 8

var _ llms.EmbeddingClient = (*FakeEmbedder)(nil)

// Embed returns one deterministic vector per input, preserving order and tagging
// each vector with its input ID. It honors context cancellation, consults
// FailFunc, and applies Corrupt to the response when set.
func (f *FakeEmbedder) Embed(ctx context.Context, req llms.EmbeddingRequest) (llms.EmbeddingResponse, error) {
	if err := ctx.Err(); err != nil {
		return llms.EmbeddingResponse{}, err //nolint:wrapcheck // sentinel ctx error for the caller to classify
	}

	f.mu.Lock()
	f.callCount++
	f.mu.Unlock()

	if f.FailFunc != nil {
		if err := f.FailFunc(req); err != nil {
			return llms.EmbeddingResponse{}, err
		}
	}

	dims := f.dims()
	resp := llms.EmbeddingResponse{Vectors: make([]llms.EmbeddingVector, len(req.Inputs))}
	tokens := 0
	for i := range req.Inputs {
		in := &req.Inputs[i]
		resp.Vectors[i] = llms.EmbeddingVector{ID: in.ID, Values: fakeVector(in.Text, dims)}
		tokens += llms.EstimateTextTokens(in.Text)
	}
	resp.Usage = llms.Usage{InputTokens: tokens, TotalTokens: tokens, EmbeddingTokens: tokens}

	if f.Corrupt != nil {
		resp = f.Corrupt(resp)
	}
	return resp, nil
}

// Model reports the model this fake targets.
func (f *FakeEmbedder) Model() llms.ModelRef {
	if f.ModelRef == (llms.ModelRef{}) {
		return llms.ModelRef{Provider: "fake", Name: "fake-embedding-001"}
	}
	return f.ModelRef
}

// DefaultDimensions reports the vector length the fake produces.
func (f *FakeEmbedder) DefaultDimensions() int { return f.dims() }

// Calls reports how many times Embed has been invoked, useful for asserting
// batching and bisection behavior.
func (f *FakeEmbedder) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

func (f *FakeEmbedder) dims() int {
	if f.Dims > 0 {
		return f.Dims
	}
	return DefaultFakeDimensions
}

// fakeVector deterministically maps text to a dims-length vector in [-1, 1).
// Each component hashes the text together with its index, so different positions
// vary and identical text always yields the identical vector.
func fakeVector(text string, dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		h := fnv.New64a()
		_, _ = h.Write([]byte(text))
		_, _ = h.Write([]byte(strconv.Itoa(i)))
		v[i] = float32(h.Sum64()%2000)/1000.0 - 1.0
	}
	return v
}
