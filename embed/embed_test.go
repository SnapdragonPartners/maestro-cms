package embed_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-llms/llms"

	"github.com/SnapdragonPartners/maestro-cms/chunk"
	"github.com/SnapdragonPartners/maestro-cms/embed"
	"github.com/SnapdragonPartners/maestro-cms/testcms"
)

func in(src, art string, idx int, text string) embed.Input {
	return embed.Input{SourceID: src, ArtifactID: art, Chunk: chunk.Chunk{Text: text, Index: idx}}
}

func inTok(src, art string, idx int, text string, tok int) embed.Input {
	return embed.Input{SourceID: src, ArtifactID: art, Chunk: chunk.Chunk{Text: text, Index: idx, TokenCount: tok}}
}

func TestRunHappyPath(t *testing.T) {
	fake := &testcms.FakeEmbedder{}
	inputs := []embed.Input{
		in("s1", "a1", 0, "alpha"),
		in("s1", "a1", 1, "beta"),
		in("s2", "a2", 0, "gamma"),
	}

	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("unexpected failures: %+v", res.Failures)
	}
	if len(res.Records) != 3 {
		t.Fatalf("got %d records, want 3", len(res.Records))
	}
	if fake.Calls() != 1 {
		t.Fatalf("got %d Embed calls, want 1 (single batch)", fake.Calls())
	}
	// Order and field mapping.
	want := []struct {
		src, art string
		idx      int
		text     string
	}{{"s1", "a1", 0, "alpha"}, {"s1", "a1", 1, "beta"}, {"s2", "a2", 0, "gamma"}}
	for i, w := range want {
		r := res.Records[i]
		if r.SourceID != w.src || r.ArtifactID != w.art || r.ChunkIndex != w.idx || r.Text != w.text {
			t.Fatalf("record %d = %+v, want %v", i, r, w)
		}
		if len(r.Vector) != testcms.DefaultFakeDimensions {
			t.Fatalf("record %d vector len %d, want %d", i, len(r.Vector), testcms.DefaultFakeDimensions)
		}
		if r.Model != (llms.ModelRef{Provider: "fake", Name: "fake-embedding-001"}) {
			t.Fatalf("record %d model = %+v", i, r.Model)
		}
		if r.TokenCount != llms.EstimateTextTokens(w.text) {
			t.Fatalf("record %d tokens = %d, want %d", i, r.TokenCount, llms.EstimateTextTokens(w.text))
		}
	}
}

func TestBatchingByInputCount(t *testing.T) {
	fake := &testcms.FakeEmbedder{}
	var inputs []embed.Input
	for i := range 5 {
		inputs = append(inputs, in("s", "a", i, "chunk"))
	}
	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{MaxBatchInputs: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Records) != 5 {
		t.Fatalf("got %d records, want 5", len(res.Records))
	}
	if fake.Calls() != 3 { // 2 + 2 + 1
		t.Fatalf("got %d Embed calls, want 3", fake.Calls())
	}
	for i, r := range res.Records {
		if r.ChunkIndex != i {
			t.Fatalf("record %d out of order: index %d", i, r.ChunkIndex)
		}
	}
}

func TestBatchingByTokenBudget(t *testing.T) {
	fake := &testcms.FakeEmbedder{}
	var inputs []embed.Input
	for i := range 5 {
		inputs = append(inputs, inTok("s", "a", i, "chunk", 100))
	}
	// 250-token budget packs two 100-token chunks per batch (300 > 250).
	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{MaxBatchTokens: 250})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Records) != 5 {
		t.Fatalf("got %d records, want 5", len(res.Records))
	}
	if fake.Calls() != 3 { // [2,2,1]
		t.Fatalf("got %d Embed calls, want 3", fake.Calls())
	}
	if res.Records[0].TokenCount != 100 {
		t.Fatalf("expected chunk TokenCount honored, got %d", res.Records[0].TokenCount)
	}
}

func TestValidationFailures(t *testing.T) {
	fake := &testcms.FakeEmbedder{}
	inputs := []embed.Input{
		in("s", "a", 0, "good one"),
		in("s", "", 1, "no artifact"),
		in("", "a", 2, "no source"),
		in("s", "a", 3, "   "),
		in("s", "a", -1, "negative index"),
		in("s", "a", 4, "good two"),
	}
	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("got %d records, want 2 (the valid ones)", len(res.Records))
	}
	if res.Records[0].Text != "good one" || res.Records[1].Text != "good two" {
		t.Fatalf("valid records wrong/out of order: %+v", res.Records)
	}
	wantErrs := []error{embed.ErrMissingArtifactID, embed.ErrMissingSourceID, embed.ErrEmptyText, embed.ErrNegativeIndex}
	if len(res.Failures) != len(wantErrs) {
		t.Fatalf("got %d failures, want %d", len(res.Failures), len(wantErrs))
	}
	for i, we := range wantErrs {
		f := res.Failures[i]
		if !errors.Is(f.Err, we) {
			t.Fatalf("failure %d err = %v, want %v", i, f.Err, we)
		}
		if f.Retryable {
			t.Fatalf("failure %d should not be retryable", i)
		}
		if len(f.Inputs) != 1 {
			t.Fatalf("failure %d covers %d inputs, want 1", i, len(f.Inputs))
		}
	}
}

const poison = "POISON"

func failOnPoison(req llms.EmbeddingRequest) error {
	for _, i := range req.Inputs {
		if strings.Contains(i.Text, poison) {
			return &llms.ProviderError{Provider: "fake", Kind: llms.ErrorKindBadRequest, Message: "poison input"}
		}
	}
	return nil
}

func TestBisectionIsolatesPoison(t *testing.T) {
	fake := &testcms.FakeEmbedder{FailFunc: failOnPoison}
	inputs := []embed.Input{
		in("s", "a", 0, "good"),
		in("s", "a", 1, "good"),
		in("s", "a", 2, poison),
		in("s", "a", 3, "good"),
	}
	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Records) != 3 {
		t.Fatalf("got %d records, want 3 (poison isolated)", len(res.Records))
	}
	// Surviving records preserve input order, skipping the poison position.
	gotIdx := []int{res.Records[0].ChunkIndex, res.Records[1].ChunkIndex, res.Records[2].ChunkIndex}
	if gotIdx[0] != 0 || gotIdx[1] != 1 || gotIdx[2] != 3 {
		t.Fatalf("record order = %v, want [0 1 3]", gotIdx)
	}
	if len(res.Failures) != 1 {
		t.Fatalf("got %d failures, want 1", len(res.Failures))
	}
	if got := res.Failures[0].Inputs; len(got) != 1 || got[0].Chunk.Index != 2 {
		t.Fatalf("failure should isolate the single poison input, got %+v", got)
	}
	if fake.Calls() <= 1 {
		t.Fatalf("expected multiple Embed calls from bisection, got %d", fake.Calls())
	}
}

func TestBisectionDisabledFailsWholeBatch(t *testing.T) {
	fake := &testcms.FakeEmbedder{FailFunc: failOnPoison}
	inputs := []embed.Input{
		in("s", "a", 0, "good"),
		in("s", "a", 1, poison),
		in("s", "a", 2, "good"),
	}
	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{DisableBisect: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Records) != 0 {
		t.Fatalf("got %d records, want 0 (whole batch failed)", len(res.Records))
	}
	if len(res.Failures) != 1 || len(res.Failures[0].Inputs) != 3 {
		t.Fatalf("want one failure covering all 3 inputs, got %+v", res.Failures)
	}
}

func TestRetryableReflectsProviderError(t *testing.T) {
	cases := []struct {
		name string
		kind llms.ErrorKind
		want bool
	}{
		{"rate_limited", llms.ErrorKindRateLimited, true},
		{"bad_request", llms.ErrorKindBadRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &testcms.FakeEmbedder{FailFunc: func(llms.EmbeddingRequest) error {
				return &llms.ProviderError{Provider: "fake", Kind: tc.kind}
			}}
			// Single input so bisection cannot fan out; the leaf failure carries
			// the classification.
			res, err := embed.Run(context.Background(), fake, []embed.Input{in("s", "a", 0, "x")}, embed.Config{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(res.Failures) != 1 {
				t.Fatalf("got %d failures, want 1", len(res.Failures))
			}
			if res.Failures[0].Retryable != tc.want {
				t.Fatalf("Retryable = %v, want %v", res.Failures[0].Retryable, tc.want)
			}
		})
	}
}

func TestResponseMismatchDroppedVector(t *testing.T) {
	fake := &testcms.FakeEmbedder{Corrupt: func(resp llms.EmbeddingResponse) llms.EmbeddingResponse {
		resp.Vectors = resp.Vectors[:len(resp.Vectors)-1] // drop one
		return resp
	}}
	res, err := embed.Run(context.Background(), fake, []embed.Input{
		in("s", "a", 0, "x"), in("s", "a", 1, "y"),
	}, embed.Config{DisableBisect: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Records) != 0 || len(res.Failures) != 1 {
		t.Fatalf("want 0 records, 1 failure; got %d records, %d failures", len(res.Records), len(res.Failures))
	}
	if !errors.Is(res.Failures[0].Err, embed.ErrResponseMismatch) {
		t.Fatalf("err = %v, want ErrResponseMismatch", res.Failures[0].Err)
	}
}

func TestResponseMismatchDuplicateVector(t *testing.T) {
	fake := &testcms.FakeEmbedder{Corrupt: func(resp llms.EmbeddingResponse) llms.EmbeddingResponse {
		resp.Vectors[1] = resp.Vectors[0] // duplicate the first ID
		return resp
	}}
	res, err := embed.Run(context.Background(), fake, []embed.Input{
		in("s", "a", 0, "x"), in("s", "a", 1, "y"),
	}, embed.Config{DisableBisect: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Failures) != 1 || !errors.Is(res.Failures[0].Err, embed.ErrResponseMismatch) {
		t.Fatalf("want one ErrResponseMismatch failure, got %+v", res.Failures)
	}
}

func TestOrderPreservedThroughBisection(t *testing.T) {
	fake := &testcms.FakeEmbedder{FailFunc: failOnPoison}
	inputs := []embed.Input{
		in("s", "a", 0, "good"),
		in("s", "a", 1, "good"),
		in("s", "a", 2, poison),
		in("s", "a", 3, "good"),
		in("s", "a", 4, poison),
		in("s", "a", 5, "good"),
	}
	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	gotRec := make([]int, len(res.Records))
	for i, r := range res.Records {
		gotRec[i] = r.ChunkIndex
	}
	wantRec := []int{0, 1, 3, 5}
	if len(gotRec) != len(wantRec) {
		t.Fatalf("records = %v, want %v", gotRec, wantRec)
	}
	for i := range wantRec {
		if gotRec[i] != wantRec[i] {
			t.Fatalf("records = %v, want %v", gotRec, wantRec)
		}
	}
	// Failures ordered by position: poison at 2 then 4.
	if len(res.Failures) != 2 {
		t.Fatalf("got %d failures, want 2", len(res.Failures))
	}
	if res.Failures[0].Inputs[0].Chunk.Index != 2 || res.Failures[1].Inputs[0].Chunk.Index != 4 {
		t.Fatalf("failures out of order: %d then %d", res.Failures[0].Inputs[0].Chunk.Index, res.Failures[1].Inputs[0].Chunk.Index)
	}
}

func TestContextCanceled(t *testing.T) {
	fake := &testcms.FakeEmbedder{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := embed.Run(ctx, fake, []embed.Input{in("s", "a", 0, "x")}, embed.Config{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(res.Records) != 0 {
		t.Fatalf("got %d records, want 0", len(res.Records))
	}
	if fake.Calls() != 0 {
		t.Fatalf("Embed should not be called after cancel, got %d", fake.Calls())
	}
}

func TestMetadataClonedIntoRecordsAndFailures(t *testing.T) {
	fake := &testcms.FakeEmbedder{FailFunc: failOnPoison}
	goodMD := map[string]string{"k": "v"}
	badMD := map[string]string{"k": "v"}
	inputs := []embed.Input{
		{SourceID: "s", ArtifactID: "a", Chunk: chunk.Chunk{Text: "good", Index: 0}, Metadata: goodMD},
		{SourceID: "s", ArtifactID: "a", Chunk: chunk.Chunk{Text: poison, Index: 1}, Metadata: badMD},
	}
	res, err := embed.Run(context.Background(), fake, inputs, embed.Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Mutate the caller's maps after the run; copies inside the result must not change.
	goodMD["k"] = "mutated"
	badMD["k"] = "mutated"

	if len(res.Records) != 1 || res.Records[0].Metadata["k"] != "v" {
		t.Fatalf("record metadata not cloned: %+v", res.Records)
	}
	if len(res.Failures) != 1 || res.Failures[0].Inputs[0].Metadata["k"] != "v" {
		t.Fatalf("failure metadata not cloned: %+v", res.Failures)
	}
}
