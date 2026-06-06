// Package embed is an orchestration runner that turns chunks into persist-ready
// embedded records over a maestro-llms embedding client. It is a layer *over* a
// pure chunker, not a competing contract: chunking stays deterministic and
// network-free, while the parts that are easy to get wrong — cross-document
// batching, partial-failure handling, defensive ID/order matching, and
// token-budget-aware batch sizing — live here (see
// docs/adr/0004-embed-is-a-runner-over-a-pure-chunker.md). The embedding
// vocabulary is maestro-llms' own (llms.EmbeddingClient / EmbeddingRequest /
// EmbeddingResponse); this package defines no embedding contract of its own (see
// docs/adr/0001-consume-maestro-llms-embedding-interface.md).
//
// Retry is NOT the runner's job. llms.EmbeddingClient.Embed is all-or-nothing
// per call; the runner packs chunks into batches and relies on the caller having
// wrapped the client with retry/backoff middleware
// (llms/middleware.RecommendedEmbeddings). When a batch call still returns an
// error, the runner optionally bisects the batch to isolate a durable "poison"
// input (config DisableBisect turns this off). Successful records are always
// preserved and returned in input order; each unrecoverable batch is reported as
// a BatchFailure carrying the exact inputs it covered, the error, and whether the
// error is retryable (llms.Retryable). The runner never persists anything — the
// caller decides whether to store the records and re-run the failures.
package embed

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/SnapdragonPartners/maestro-llms/llms"

	"github.com/SnapdragonPartners/maestro-cms/chunk"
)

// Defaults for Config. Batch limits are intentionally conservative; set them to
// your provider/model's actual per-request limits for best throughput.
const (
	// DefaultMaxBatchInputs caps the number of inputs in one Embed call.
	DefaultMaxBatchInputs = 96
	// DefaultMaxBatchTokens caps the summed estimated tokens in one Embed call.
	DefaultMaxBatchTokens = 20_000
)

// Sentinel errors. Validation errors are reported per input as BatchFailures
// (never panics); ErrResponseMismatch flags a provider response that does not
// correspond one-to-one with the request.
var (
	// ErrMissingArtifactID indicates an input had no ArtifactID (its provenance).
	ErrMissingArtifactID = errors.New("embed: input missing ArtifactID")
	// ErrMissingSourceID indicates an input had no SourceID.
	ErrMissingSourceID = errors.New("embed: input missing SourceID")
	// ErrEmptyText indicates an input's chunk text was empty or whitespace-only.
	ErrEmptyText = errors.New("embed: input chunk has empty text")
	// ErrNegativeIndex indicates an input's chunk index was negative.
	ErrNegativeIndex = errors.New("embed: input chunk has negative index")
	// ErrResponseMismatch indicates the embedding response did not match the
	// request inputs (missing, duplicate, or unknown vector IDs). It is matched
	// with errors.Is.
	ErrResponseMismatch = errors.New("embed: embedding response did not match request")
)

// Input is one chunk plus the provenance needed to persist its vector. A flat
// list of Inputs may span many artifacts and sources; the runner batches across
// all of them, which is where embedding batching efficiency comes from.
type Input struct {
	// SourceID and ArtifactID locate the chunk in the content provenance tree.
	SourceID   string
	ArtifactID string
	// Chunk is the segment to embed (its Text, Index, and TokenCount are used).
	Chunk chunk.Chunk
	// Title is an optional document title, passed through to the embedding
	// request; advisory and only meaningful with a retrieval-document task.
	Title string
	// Metadata is a neutral, app-owned label carrier copied onto the resulting
	// Record (or failure). The runner never interprets it.
	Metadata map[string]string
}

// Record is one chunk plus its vector, ready for the application to persist. The
// runner owns no storage; it returns records and lets the app write them.
type Record struct {
	SourceID   string
	ArtifactID string
	// ChunkIndex is the chunk's 0-based position within its source artifact.
	ChunkIndex int
	Text       string
	// Vector is the embedding; it is a fresh copy, not aliased to the response.
	Vector []float32
	// Model is the model the embedding client targets.
	Model llms.ModelRef
	// TokenCount is the chunk's token count used for batch budgeting.
	TokenCount int
	// Metadata is an independent copy of the originating Input.Metadata.
	Metadata map[string]string
}

// BatchFailure reports an embedding batch that could not be completed. Inputs
// holds the exact chunks the batch covered (a chunk index alone is ambiguous
// across documents, so the inputs themselves are the retry unit). Retryable
// reflects llms.Retryable on the underlying error where one applies.
type BatchFailure struct {
	Inputs    []Input
	Err       error
	Retryable bool
}

// RunResult is the outcome of a run: the successful records (in input order) and
// the per-batch failures (also ordered by their first input's position).
type RunResult struct {
	Records  []Record
	Failures []BatchFailure
}

// Config tunes Run. The zero value is usable: it batches to the Default limits,
// estimates tokens with llms.EstimateTextTokens, labels calls
// llms.PurposeEmbedding, and bisects failed batches.
type Config struct {
	// MaxBatchInputs caps inputs per Embed call; <=0 means DefaultMaxBatchInputs.
	MaxBatchInputs int
	// MaxBatchTokens caps summed estimated tokens per Embed call; <=0 means
	// DefaultMaxBatchTokens.
	MaxBatchTokens int
	// Estimate counts tokens for budgeting a chunk whose TokenCount is unset
	// (<=0). Nil means llms.EstimateTextTokens.
	Estimate func(string) int
	// Task is an advisory, provider-neutral embedding task hint.
	Task llms.EmbeddingTask
	// Purpose is an app label on the request; "" means llms.PurposeEmbedding.
	Purpose llms.Purpose
	// Dimensions overrides the model's default vector size when the provider
	// supports it; 0 means the model default.
	Dimensions int
	// DisableBisect turns off splitting a failed batch to isolate a poison
	// input. With it set, the whole failed batch is reported as one failure.
	DisableBisect bool
}

func (c Config) withDefaults() Config {
	if c.MaxBatchInputs <= 0 {
		c.MaxBatchInputs = DefaultMaxBatchInputs
	}
	if c.MaxBatchTokens <= 0 {
		c.MaxBatchTokens = DefaultMaxBatchTokens
	}
	if c.Estimate == nil {
		c.Estimate = llms.EstimateTextTokens
	}
	if c.Purpose == "" {
		c.Purpose = llms.PurposeEmbedding
	}
	return c
}

// indexedInput carries a validated input with its original position (for stable
// output ordering and collision-free request IDs) and its token estimate.
type indexedInput struct {
	pos    int
	in     Input
	tokens int
}

// posFailure tags a BatchFailure with the smallest original position it covers,
// so failures can be ordered consistently with the input list.
type posFailure struct {
	pos int
	bf  BatchFailure
}

// Run embeds inputs and returns persist-ready records plus per-batch failures.
//
// Each input is validated first; invalid inputs are reported as failures and
// never sent to the client. Valid inputs are packed into batches (bounded by
// cfg.MaxBatchInputs and cfg.MaxBatchTokens) and embedded sequentially. Records
// are returned in input order even when bisection partially succeeds.
//
// Run returns a non-nil error only if the context is canceled mid-run; in that
// case it returns the records and failures gathered so far, and inputs not yet
// processed appear in neither slice (re-run them). All other problems are
// reported as BatchFailures, not as the returned error.
func Run(ctx context.Context, client llms.EmbeddingClient, inputs []Input, cfg Config) (RunResult, error) {
	cfg = cfg.withDefaults()

	var failures []posFailure
	valid := make([]indexedInput, 0, len(inputs))
	for i := range inputs {
		in := &inputs[i]
		if err := in.validate(); err != nil {
			failures = append(failures, posFailure{
				pos: i,
				bf:  BatchFailure{Inputs: []Input{cloneInput(*in)}, Err: err, Retryable: false},
			})
			continue
		}
		valid = append(valid, indexedInput{pos: i, in: *in, tokens: tokensFor(*in, cfg.Estimate)})
	}

	recordsByPos := make(map[int]Record, len(valid))
	var runErr error
	for _, batch := range packBatches(valid, cfg.MaxBatchInputs, cfg.MaxBatchTokens) {
		if err := ctx.Err(); err != nil {
			runErr = fmt.Errorf("embed: run canceled: %w", err)
			break
		}
		embedBatch(ctx, client, batch, cfg, recordsByPos, &failures)
	}

	return assemble(len(inputs), recordsByPos, failures), runErr
}

// validate checks an input is well-formed enough to embed and persist.
func (in Input) validate() error {
	switch {
	case in.ArtifactID == "":
		return ErrMissingArtifactID
	case in.SourceID == "":
		return ErrMissingSourceID
	case strings.TrimSpace(in.Chunk.Text) == "":
		return ErrEmptyText
	case in.Chunk.Index < 0:
		return ErrNegativeIndex
	}
	return nil
}

// tokensFor returns the chunk's own token count when set, else estimates it.
func tokensFor(in Input, est func(string) int) int {
	if in.Chunk.TokenCount > 0 {
		return in.Chunk.TokenCount
	}
	return est(in.Chunk.Text)
}

// packBatches greedily groups items into batches under the input-count and
// token-sum limits. An item whose own token count exceeds maxTokens still gets a
// batch (alone) rather than being dropped — the provider, not the runner, is the
// authority on whether a single input is too large.
func packBatches(items []indexedInput, maxInputs, maxTokens int) [][]indexedInput {
	var batches [][]indexedInput
	cur := make([]indexedInput, 0, maxInputs)
	curTok := 0
	for i := range items {
		it := &items[i]
		if len(cur) > 0 && (len(cur) >= maxInputs || curTok+it.tokens > maxTokens) {
			batches = append(batches, cur)
			cur = make([]indexedInput, 0, maxInputs)
			curTok = 0
		}
		cur = append(cur, *it)
		curTok += it.tokens
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// embedBatch embeds one batch, recording successes into recordsByPos and
// appending failures. On a client error it bisects (unless disabled or the batch
// is a single input) so a durable poison input is isolated while its batch-mates
// still succeed. A response that does not match the request one-to-one is a
// non-retryable batch failure.
func embedBatch(ctx context.Context, client llms.EmbeddingClient, batch []indexedInput, cfg Config, recordsByPos map[int]Record, failures *[]posFailure) {
	req := llms.EmbeddingRequest{
		Inputs:     make([]llms.EmbeddingInput, len(batch)),
		Purpose:    cfg.Purpose,
		Dimensions: cfg.Dimensions,
		Task:       cfg.Task,
	}
	for i := range batch {
		it := &batch[i]
		req.Inputs[i] = llms.EmbeddingInput{ID: requestID(it.pos), Text: it.in.Chunk.Text, Title: it.in.Title}
	}

	resp, err := client.Embed(ctx, req)
	if err != nil {
		if !cfg.DisableBisect && len(batch) > 1 {
			mid := len(batch) / 2
			embedBatch(ctx, client, batch[:mid], cfg, recordsByPos, failures)
			embedBatch(ctx, client, batch[mid:], cfg, recordsByPos, failures)
			return
		}
		addBatchFailure(failures, batch, fmt.Errorf("embed: batch of %d failed: %w", len(batch), err), llms.Retryable(err))
		return
	}

	byID := make(map[string][]float32, len(resp.Vectors))
	for i := range resp.Vectors {
		v := &resp.Vectors[i]
		if _, dup := byID[v.ID]; dup {
			addBatchFailure(failures, batch, fmt.Errorf("%w: duplicate vector ID %q", ErrResponseMismatch, v.ID), false)
			return
		}
		byID[v.ID] = v.Values
	}
	// A one-to-one mapping requires equal counts (catches both extra/unknown
	// vectors and shortfalls) and a vector for every requested input.
	if len(byID) != len(batch) {
		addBatchFailure(failures, batch, fmt.Errorf("%w: got %d vectors for %d inputs", ErrResponseMismatch, len(byID), len(batch)), false)
		return
	}
	for i := range batch {
		it := &batch[i]
		vals, ok := byID[requestID(it.pos)]
		if !ok {
			addBatchFailure(failures, batch, fmt.Errorf("%w: no vector for input at position %d", ErrResponseMismatch, it.pos), false)
			return
		}
		recordsByPos[it.pos] = Record{
			SourceID:   it.in.SourceID,
			ArtifactID: it.in.ArtifactID,
			ChunkIndex: it.in.Chunk.Index,
			Text:       it.in.Chunk.Text,
			Vector:     slices.Clone(vals),
			Model:      client.Model(),
			TokenCount: it.tokens,
			Metadata:   maps.Clone(it.in.Metadata),
		}
	}
}

// requestID is an opaque, collision-free per-input ID built from the input's
// position in the run (never from app IDs, which could collide or contain
// delimiters). The runner maps responses back to inputs by this ID.
func requestID(pos int) string {
	return "in-" + strconv.Itoa(pos)
}

// addBatchFailure records a failed batch, cloning each input's mutable metadata
// so the failure report shares no state with the caller's inputs, and tagging it
// with the smallest position it covers for stable ordering.
func addBatchFailure(failures *[]posFailure, batch []indexedInput, err error, retryable bool) {
	ins := make([]Input, len(batch))
	minPos := batch[0].pos
	for i := range batch {
		it := &batch[i]
		ins[i] = cloneInput(it.in)
		if it.pos < minPos {
			minPos = it.pos
		}
	}
	*failures = append(*failures, posFailure{pos: minPos, bf: BatchFailure{Inputs: ins, Err: err, Retryable: retryable}})
}

// cloneInput returns a copy of in whose Metadata is an independent map, so
// carried-through inputs never alias the caller's maps.
func cloneInput(in Input) Input {
	in.Metadata = maps.Clone(in.Metadata)
	return in
}

// assemble builds the result: records in input order, failures ordered by their
// first covered input's position.
func assemble(n int, recordsByPos map[int]Record, failures []posFailure) RunResult {
	records := make([]Record, 0, len(recordsByPos))
	for pos := range n {
		if r, ok := recordsByPos[pos]; ok {
			records = append(records, r)
		}
	}
	sort.SliceStable(failures, func(i, j int) bool { return failures[i].pos < failures[j].pos })
	bfs := make([]BatchFailure, len(failures))
	for i := range failures {
		bfs[i] = failures[i].bf
	}
	return RunResult{Records: records, Failures: bfs}
}
