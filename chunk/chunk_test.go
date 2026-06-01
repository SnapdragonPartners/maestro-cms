package chunk

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// assertChunksCoverAndAlign verifies the core invariants every Split result must
// satisfy: text matches its byte offsets, offsets are within range and rune
// aligned, indices are sequential, and chunks advance.
func assertChunksInvariants(t *testing.T, source string, chunks []Chunk) {
	t.Helper()
	for i, c := range chunks {
		if c.Index != i {
			t.Fatalf("chunk %d has Index %d", i, c.Index)
		}
		if c.StartByte < 0 || c.EndByte > len(source) || c.StartByte >= c.EndByte {
			t.Fatalf("chunk %d has bad span [%d,%d) for source len %d", i, c.StartByte, c.EndByte, len(source))
		}
		if got := source[c.StartByte:c.EndByte]; got != c.Text {
			t.Fatalf("chunk %d Text %q != source[%d:%d] %q", i, c.Text, c.StartByte, c.EndByte, got)
		}
		if !utf8.ValidString(c.Text) {
			t.Fatalf("chunk %d Text is not valid UTF-8: %q", i, c.Text)
		}
		if i > 0 && c.StartByte <= chunks[i-1].StartByte {
			t.Fatalf("chunk %d start %d did not advance past chunk %d start %d", i, c.StartByte, i-1, chunks[i-1].StartByte)
		}
	}
}

func TestSplitEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t\n"} {
		if got := Split(in, Config{}); got != nil {
			t.Fatalf("Split(%q) = %v, want nil", in, got)
		}
	}
}

func TestSplitSmallInputIsSingleChunk(t *testing.T) {
	in := "a short paragraph that fits well within the budget"
	chunks := Split(in, Config{MaxTokens: 512})
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Text != in {
		t.Fatalf("Text = %q, want %q", chunks[0].Text, in)
	}
	assertChunksInvariants(t, in, chunks)
}

// Boundary-first: paragraphs are packed up to budget, and a chunk boundary
// should fall on a paragraph break, not mid-paragraph.
func TestSplitPacksParagraphsToBudget(t *testing.T) {
	// 4 paragraphs, ~10 tokens each under the default estimator. Budget of ~22
	// tokens should pack 2 paragraphs per chunk.
	p := func(n int) string { return strings.Repeat("word ", n) } // 5*n chars ≈ (5n/4) tokens
	para := strings.TrimSpace(p(16))                              // ~20 tokens
	in := para + "\n\n" + para + "\n\n" + para + "\n\n" + para
	chunks := Split(in, Config{MaxTokens: 45, NoOverlap: true})
	assertChunksInvariants(t, in, chunks)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// Each chunk (except possibly the last) should end at a paragraph break, so
	// its trimmed text should not contain a dangling partial — check each chunk
	// is composed of whole "word " repetitions plus separators.
	for _, c := range chunks {
		for line := range strings.SplitSeq(c.Text, "\n\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			for w := range strings.FieldsSeq(line) {
				if w != "word" {
					t.Fatalf("chunk split mid-word: found %q in %q", w, c.Text)
				}
			}
		}
	}
}

func TestSplitOverlapReincludesTrailingUnit(t *testing.T) {
	paras := []string{"alpha alpha", "bravo bravo", "charlie charlie", "delta delta"}
	in := strings.Join(paras, "\n\n")
	// Small budget so each chunk is ~1-2 paragraphs; overlap should re-include a
	// trailing paragraph in the next chunk.
	withOverlap := Split(in, Config{MaxTokens: 8, OverlapTokens: 4})
	noOverlap := Split(in, Config{MaxTokens: 8, NoOverlap: true})
	assertChunksInvariants(t, in, withOverlap)
	assertChunksInvariants(t, in, noOverlap)
	if len(withOverlap) <= len(noOverlap) {
		t.Fatalf("overlap should produce >= chunks than no-overlap: overlap=%d noOverlap=%d", len(withOverlap), len(noOverlap))
	}
	// Adjacent overlapping chunks should share some byte range.
	shared := false
	for i := 1; i < len(withOverlap); i++ {
		if withOverlap[i].StartByte < withOverlap[i-1].EndByte {
			shared = true
		}
	}
	if !shared {
		t.Fatal("expected overlapping chunks to share a byte range")
	}
}

// Last resort: a single paragraph larger than the budget is hard-split.
func TestSplitHardSplitsOversizeUnit(t *testing.T) {
	// One paragraph, no blank lines, far over budget.
	in := strings.TrimSpace(strings.Repeat("sentence here. ", 60)) // ~225 tokens
	chunks := Split(in, Config{MaxTokens: 40, NoOverlap: true})
	assertChunksInvariants(t, in, chunks)
	if len(chunks) < 3 {
		t.Fatalf("expected oversize unit to hard-split into several chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.TokenCount > 60 { // budget 40 + slack; must not be wildly over
			t.Fatalf("hard-split chunk far exceeds budget: %d tokens", c.TokenCount)
		}
	}
}

func TestSplitInjectedEstimatorIsUsed(t *testing.T) {
	// An estimator that reports every string as 1000 tokens forces even a small
	// input to be treated as oversize and hard-split.
	huge := func(string) int { return 1000 }
	in := strings.TrimSpace(strings.Repeat("abcd efgh ", 20))
	chunks := Split(in, Config{MaxTokens: 100, Estimate: huge, NoOverlap: true})
	assertChunksInvariants(t, in, chunks)
	if len(chunks) < 2 {
		t.Fatalf("custom estimator should have forced multiple chunks, got %d", len(chunks))
	}
}

func TestSplitRuneSafeOnMultibyte(t *testing.T) {
	// CJK text with no ASCII boundaries; small budget forces hard splits that
	// must land on rune boundaries.
	in := strings.Repeat("日本語のテキスト", 40)
	chunks := Split(in, Config{MaxTokens: 10, NoOverlap: true})
	assertChunksInvariants(t, in, chunks) // includes UTF-8 validity check
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long CJK input, got %d", len(chunks))
	}
}

func TestSplitConcatenationReconstructsWithoutOverlap(t *testing.T) {
	in := "para one here.\n\npara two here.\n\npara three is a bit longer than the rest."
	chunks := Split(in, Config{MaxTokens: 8, NoOverlap: true})
	assertChunksInvariants(t, in, chunks)
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString(c.Text)
	}
	if sb.String() != in {
		t.Fatalf("no-overlap chunks did not reconstruct source:\n got %q\nwant %q", sb.String(), in)
	}
}

func TestParagraphsCoverText(t *testing.T) {
	in := "first\n\nsecond\n\n\nthird"
	units := Paragraphs(in)
	if len(units) != 3 {
		t.Fatalf("got %d units, want 3", len(units))
	}
	// Units must be contiguous and cover the whole text.
	if units[0].Start != 0 || units[len(units)-1].End != len(in) {
		t.Fatalf("units do not cover text: %+v for len %d", units, len(in))
	}
	for i := 1; i < len(units); i++ {
		if units[i].Start != units[i-1].End {
			t.Fatalf("gap/overlap between unit %d and %d: %+v", i-1, i, units)
		}
	}
}

func TestDefaultEstimateRuneCounted(t *testing.T) {
	// 8 ASCII chars → 8 runes → 2 tokens. 8 CJK chars → 8 runes → 2 tokens too
	// (rune-counted, not byte-counted: byte-counting would give 24/4 = 6).
	if got := defaultEstimate("abcdefgh"); got != 2 {
		t.Fatalf("defaultEstimate(ascii) = %d, want 2", got)
	}
	if got := defaultEstimate("日本語日本語日本"); got != 2 {
		t.Fatalf("defaultEstimate(cjk) = %d, want 2 (rune-counted)", got)
	}
	if got := defaultEstimate(""); got != 0 {
		t.Fatalf("defaultEstimate(empty) = %d, want 0", got)
	}
	// Ceiling division, matching llms.EstimateTextTokens: 5 runes → 2, not 1.
	if got := defaultEstimate("abcde"); got != 2 {
		t.Fatalf("defaultEstimate(5 runes) = %d, want 2 (ceiling div)", got)
	}
	if got := defaultEstimate("a"); got != 1 {
		t.Fatalf("defaultEstimate(1 rune) = %d, want 1", got)
	}
}

// P1: offsets must index the caller's source, not a trimmed copy, when the
// source has leading/trailing whitespace.
func TestSplitOffsetsIndexOriginalSource(t *testing.T) {
	source := "   hello world   "
	chunks := Split(source, Config{MaxTokens: 512})
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	c := chunks[0]
	if got := source[c.StartByte:c.EndByte]; got != c.Text {
		t.Fatalf("contract broken: source[%d:%d] = %q, Text = %q", c.StartByte, c.EndByte, got, c.Text)
	}
	if c.StartByte != 3 {
		t.Fatalf("StartByte = %d, want 3 (after leading spaces)", c.StartByte)
	}
}

// P1 with multiple chunks and leading whitespace: every chunk's offsets must
// index the original source.
func TestSplitOffsetsWithLeadingWhitespaceMultiChunk(t *testing.T) {
	source := "\n\n  " + strings.Join([]string{"alpha", "bravo", "charlie", "delta"}, "\n\n") + "  \n"
	chunks := Split(source, Config{MaxTokens: 4, NoOverlap: true})
	assertChunksInvariants(t, source, chunks) // checks source[Start:End] == Text for each
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
}

// P3: a packed unit larger than the overlap budget must not be duplicated into
// the next chunk.
func TestSplitOverlapDoesNotDuplicateOversizeUnit(t *testing.T) {
	// Units roughly [1, big, 1] tokens. With a small overlap budget, the big
	// middle unit must not reappear in the following chunk.
	small := "x"
	big := strings.TrimSpace(strings.Repeat("word ", 200)) // ~250 tokens
	source := small + "\n\n" + big + "\n\n" + small
	chunks := Split(source, Config{MaxTokens: 300, OverlapTokens: 2})
	assertChunksInvariants(t, source, chunks)
	// The big unit's byte range should appear in exactly one chunk.
	bigStart := strings.Index(source, big)
	covering := 0
	for _, c := range chunks {
		if c.StartByte <= bigStart && bigStart < c.EndByte {
			covering++
		}
	}
	if covering != 1 {
		t.Fatalf("oversize unit covered by %d chunks, want 1 (no duplication)", covering)
	}
}

func TestSplitCustomBoundaries(t *testing.T) {
	// A caller-provided segmenter that splits on "|" — proves Boundaries is
	// pluggable and drives chunk edges.
	in := "aaaa|bbbb|cccc|dddd"
	pipe := func(text string) []Unit {
		var units []Unit
		start := 0
		for start < len(text) {
			rel := strings.IndexByte(text[start:], '|')
			if rel < 0 {
				units = append(units, Unit{Start: start, End: len(text)})
				break
			}
			units = append(units, Unit{Start: start, End: start + rel + 1})
			start = start + rel + 1
		}
		return units
	}
	chunks := Split(in, Config{MaxTokens: 3, NoOverlap: true, Boundaries: pipe})
	assertChunksInvariants(t, in, chunks)
	// Each ~4-char unit is ~1 token; budget 3 packs a few per chunk, but every
	// chunk boundary must fall on a "|".
	for _, c := range chunks {
		if c.EndByte != len(in) && !strings.HasSuffix(c.Text, "|") {
			t.Fatalf("custom-boundary chunk did not end on a unit boundary: %q", c.Text)
		}
	}
}

// Exercises the default MaxTokens/OverlapTokens paths (Config{} with content
// large enough to actually chunk) rather than the early-return empty cases.
func TestSplitUsesDefaultsWithBareConfig(t *testing.T) {
	para := strings.TrimSpace(strings.Repeat("word ", 400)) // ~500 tokens, ~ default budget
	in := para + "\n\n" + para + "\n\n" + para
	chunks := Split(in, Config{}) // all defaults
	assertChunksInvariants(t, in, chunks)
	if len(chunks) < 2 {
		t.Fatalf("expected default budget to split ~1500-token input, got %d chunks", len(chunks))
	}
}

// A single oversize unit whose overlap budget exceeds the whole unit must still
// make forward progress (overlapStart's i+1 fallback) and a too-large overlap
// inside hard-split must not stall.
func TestSplitOversizeUnitWithLargeOverlapTerminates(t *testing.T) {
	in := strings.TrimSpace(strings.Repeat("alpha bravo charlie ", 50)) // one big paragraph
	chunks := Split(in, Config{MaxTokens: 20, OverlapTokens: 19})
	assertChunksInvariants(t, in, chunks)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
}

// Overlap budget large enough to want to re-include several trailing units, but
// must still advance past the first unit (overlapStart clamp to i+1).
func TestSplitOverlapClampsToForwardProgress(t *testing.T) {
	paras := make([]string, 6)
	for i := range paras {
		paras[i] = "tiny"
	}
	in := strings.Join(paras, "\n\n")
	// Small budget so packing stops mid-list and overlapStart runs; the large
	// overlap budget would, unclamped, restart at the same unit and stall.
	chunks := Split(in, Config{MaxTokens: 2, OverlapTokens: 100})
	assertChunksInvariants(t, in, chunks)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
}

// CJK input with a tiny budget forces snapEndToRuneBoundary's forward-extension
// path (idealEnd lands mid-rune extremely close to start).
func TestSplitTinyBudgetMultibyteForwardExtends(t *testing.T) {
	in := strings.Repeat("語", 20) // 3 bytes each, no ASCII boundary anywhere
	chunks := Split(in, Config{MaxTokens: 1, NoOverlap: true})
	assertChunksInvariants(t, in, chunks)
	// Every chunk must contain at least one whole rune.
	for _, c := range chunks {
		if utf8.RuneCountInString(c.Text) < 1 {
			t.Fatalf("chunk has no complete rune: %q", c.Text)
		}
	}
}
