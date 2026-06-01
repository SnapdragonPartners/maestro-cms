// Package chunk splits text into retrieval-sized segments using boundary-aware
// chunking with optional token-budget enforcement.
//
// Chunking is boundary-first: text is segmented at semantic boundaries
// (paragraphs by default; pluggable via Config.Boundaries for headings, pages,
// sections, code functions, transcript segments, or caller-supplied units), and
// consecutive units are packed together. Token estimation is a downstream
// budget constraint, not the chunking strategy: it decides only whether a
// candidate chunk fits a model budget, and, as a last resort, drives the
// hard-split of a single semantic unit that alone exceeds the budget.
//
// The package is pure: Split is deterministic, does no I/O, and never imports a
// provider or model SDK. Token counting is injected as Config.Estimate
// (func(string) int); when nil it falls back to a local rune-counted estimate
// (~4 chars/token). The standard injection is maestro-llms's
// llms.EstimateTextTokens, wired in by the consumer — chunk itself stays
// dependency-light (see docs/adr/0002 and docs/adr/0004).
//
// When a unit must be hard-split, the fallback is preference-ordered:
// sentence-ending punctuation (". " / "! " / "? "), then a word boundary, then a
// UTF-8 rune boundary as the final cut. Overlap is back-prepended so content
// spanning a boundary stays retrievable from a single chunk.
package chunk

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Defaults for Config, in tokens.
const (
	// DefaultMaxTokens is the per-chunk token budget.
	DefaultMaxTokens = 512
	// DefaultOverlapTokens is the trailing-token overlap carried into the next
	// chunk.
	DefaultOverlapTokens = 64
	// approxCharsPerToken backs the default rune-counted estimator.
	approxCharsPerToken = 4
)

// Estimate reports the approximate token count of s. It is the seam that keeps
// chunk model-agnostic: callers inject llms.EstimateTextTokens (or any counter),
// and a nil Estimate in Config uses defaultEstimate.
type Estimate func(s string) int

// Unit is a semantic segment of the source, given as a half-open byte span
// [Start, End) into the original text. Boundaries functions return units in
// ascending order that together cover the text without overlapping.
type Unit struct {
	Start int
	End   int
}

// Boundaries segments text into semantic units. The default is Paragraphs;
// callers can supply their own (code-aware, transcript, page, or section
// segmenters) without changing the packing or budget logic.
type Boundaries func(text string) []Unit

// Chunk is one segment produced by Split. StartByte/EndByte are byte offsets
// into the source string, a half-open interval [StartByte, EndByte), so
// Text == source[StartByte:EndByte].
type Chunk struct {
	// Text is the chunk's content.
	Text string
	// Index is the 0-based position of this chunk within the source.
	Index int
	// StartByte is the inclusive byte offset of Text within the source.
	StartByte int
	// EndByte is the exclusive byte offset of Text within the source.
	EndByte int
	// TokenCount is the approximate token count per the configured estimator.
	TokenCount int
}

// Config tunes Split. The zero value is usable: it segments by Paragraphs, packs
// to DefaultMaxTokens with DefaultOverlapTokens, and uses the default estimator.
// A zero OverlapTokens means "use the default"; set NoOverlap to disable overlap.
type Config struct {
	// MaxTokens is the per-chunk token budget. Zero or negative means
	// DefaultMaxTokens.
	MaxTokens int
	// OverlapTokens is the trailing-token overlap carried into the next chunk.
	// Zero or negative means DefaultOverlapTokens; use NoOverlap to disable.
	OverlapTokens int
	// NoOverlap disables overlap entirely, distinct from OverlapTokens == 0
	// (which means "use the default") so a bare Config{} does not silently drop
	// overlap.
	NoOverlap bool
	// Estimate counts tokens for budget decisions. Nil means the default
	// rune-counted estimate; the standard injection is llms.EstimateTextTokens.
	Estimate Estimate
	// Boundaries segments the text. Nil means Paragraphs.
	Boundaries Boundaries
}

func (c *Config) maxTokens() int {
	if c.MaxTokens <= 0 {
		return DefaultMaxTokens
	}
	return c.MaxTokens
}

func (c *Config) overlapTokens() int {
	if c.NoOverlap {
		return 0
	}
	if c.OverlapTokens <= 0 {
		return DefaultOverlapTokens
	}
	return c.OverlapTokens
}

func (c *Config) estimate() Estimate {
	if c.Estimate != nil {
		return c.Estimate
	}
	return defaultEstimate
}

func (c *Config) boundaries() Boundaries {
	if c.Boundaries != nil {
		return c.Boundaries
	}
	return Paragraphs
}

// defaultEstimate is the rune-counted fallback token estimator (~4 chars/token).
// It counts runes, not bytes, so non-Latin scripts are not systematically
// over-counted, and it ceiling-divides to match maestro-llms's
// llms.EstimateTextTokens (so e.g. 5 runes estimate as 2 tokens, not 1) — the
// default and the standard injected estimator stay aligned.
func defaultEstimate(s string) int {
	if s == "" {
		return 0
	}
	return (utf8.RuneCountInString(s) + approxCharsPerToken - 1) / approxCharsPerToken
}

// Split segments text into semantic units (via cfg.Boundaries), then packs
// consecutive units into chunks that fit cfg.MaxTokens, with cfg.OverlapTokens
// of trailing overlap. A single unit larger than the budget is hard-split as a
// last resort. Input is trimmed first; empty or whitespace-only input yields no
// chunks (nil).
func Split(source string, cfg Config) []Chunk {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil
	}
	// Work on the trimmed view internally, but report byte offsets into the
	// caller's original source so the Chunk contract Text == source[StartByte:
	// EndByte] holds even when source has leading/trailing whitespace. off is the
	// byte length of the leading whitespace that TrimSpace removed.
	off := len(source) - len(strings.TrimLeftFunc(source, unicode.IsSpace))
	text := source[off : off+len(trimmed)]

	est := cfg.estimate()
	maxTok := cfg.maxTokens()
	overlapTok := cfg.overlapTokens()

	units := cfg.boundaries()(text)
	if !validUnits(text, units) {
		// Fail closed but non-panicky: a custom Boundaries that returns invalid
		// spans (out of range, mis-ordered, overlapping, or not rune-aligned)
		// falls back to one unit covering the whole text rather than slicing on
		// bad structure. This keeps Split panic-free and deterministic without
		// silently using partially-broken segmentation.
		units = []Unit{{Start: 0, End: len(text)}}
	}
	unitTok := make([]int, len(units))
	for i, u := range units {
		unitTok[i] = est(text[u.Start:u.End])
	}

	chunks := make([]Chunk, 0, len(units))
	idx := 0
	i := 0
	for i < len(units) {
		// Last resort: a single unit that alone exceeds the budget is hard-split.
		if unitTok[i] > maxTok {
			hardSplit(text, units[i].Start, units[i].End, maxTok, overlapTok, est, &chunks, &idx)
			i++
			continue
		}

		// Pack consecutive units while they fit the budget.
		j, sum := growChunk(unitTok, i, maxTok)
		start, end := units[i].Start, units[j].End
		chunks = append(chunks, Chunk{
			Text:       text[start:end],
			Index:      idx,
			StartByte:  start,
			EndByte:    end,
			TokenCount: sum,
		})
		idx++

		if j == len(units)-1 {
			break
		}
		i = overlapStart(unitTok, i, j, overlapTok)
	}

	// Shift text-relative spans back into the caller's source coordinates.
	if off > 0 {
		for k := range chunks {
			chunks[k].StartByte += off
			chunks[k].EndByte += off
		}
	}
	return chunks
}

// growChunk extends a chunk starting at unit i, returning the last included unit
// index j and the summed token count of units [i, j]. It always includes unit i,
// even if that single unit equals the budget (callers handle the oversize case
// before calling).
func growChunk(unitTok []int, i, maxTok int) (j, sum int) {
	j, sum = i, unitTok[i]
	for j+1 < len(unitTok) && sum+unitTok[j+1] <= maxTok {
		j++
		sum += unitTok[j]
	}
	return j, sum
}

// overlapStart returns the starting unit index for the chunk after one covering
// units [i, j]. With overlap enabled it walks back from j+1, re-including a
// trailing unit only while adding it keeps the overlap within budget — so a unit
// larger than the overlap budget is never duplicated into the next chunk. It
// always makes forward progress (the result is strictly greater than i).
func overlapStart(unitTok []int, i, j, overlapTok int) int {
	if overlapTok <= 0 {
		return j + 1
	}
	ov, ovSum := j+1, 0
	for ov-1 > i && ovSum+unitTok[ov-1] <= overlapTok {
		ov--
		ovSum += unitTok[ov]
	}
	return ov
}

// hardSplit splits the byte span [start, end) — a single semantic unit too large
// for the budget — into budget-fitting chunks, preferring sentence then word then
// rune boundaries, with back-prepended overlap. Token budgets are converted to
// byte windows using the span's own chars-per-token ratio under est.
func hardSplit(text string, start, end, maxTok, overlapTok int, est Estimate, chunks *[]Chunk, idx *int) {
	span := text[start:end]
	total := max(est(span), 1)
	bytesPerToken := float64(len(span)) / float64(total)
	target := max(int(float64(maxTok)*bytesPerToken), 1)
	overlap := max(int(float64(overlapTok)*bytesPerToken), 0)

	pos := start
	for pos < end {
		e := pos + target
		if e >= end {
			e = end
		} else {
			e = findBoundary(text, pos, e)
		}
		e = snapEndToRuneBoundary(text, pos, e)

		chunkText := text[pos:e]
		*chunks = append(*chunks, Chunk{
			Text:       chunkText,
			Index:      *idx,
			StartByte:  pos,
			EndByte:    e,
			TokenCount: est(chunkText),
		})
		*idx++

		if e >= end {
			break
		}
		next := e - overlap
		if next <= pos {
			next = pos + 1
		}
		pos = snapStartToRuneStart(text, next)
	}
}

// validUnits reports whether units form a valid segmentation of text: each span
// is within range with Start < End, both ends land on UTF-8 rune boundaries, and
// the units are strictly ascending and non-overlapping. An empty slice is
// invalid (Split substitutes a whole-text unit). Gaps between units are
// tolerated — units need not be perfectly contiguous — so a segmenter that drops
// separators is still accepted.
func validUnits(text string, units []Unit) bool {
	if len(units) == 0 {
		return false
	}
	prevEnd := 0
	for _, u := range units {
		if u.Start < 0 || u.Start >= u.End || u.End > len(text) {
			return false
		}
		if u.Start < prevEnd {
			return false // overlapping or out of order
		}
		if !runeAligned(text, u.Start) || !runeAligned(text, u.End) {
			return false
		}
		prevEnd = u.End
	}
	return true
}

// runeAligned reports whether byte offset i is a UTF-8 rune boundary in text.
// The ends of the string are boundaries.
func runeAligned(text string, i int) bool {
	return i == 0 || i == len(text) || utf8.RuneStart(text[i])
}

// Paragraphs segments text into paragraph units, splitting on blank-line runs. A
// blank-line separator is a run of two or more newlines, where each newline may
// be "\n" or "\r\n" — so "\n\n", "\r\n\r\n", and mixed line endings all split.
// Each unit is a contiguous byte span that includes its trailing separator, so
// the units cover the whole text without gaps. It is the default Boundaries.
func Paragraphs(text string) []Unit {
	if text == "" {
		return nil
	}
	var units []Unit
	start := 0
	for start < len(text) {
		sep, runEnd := nextBlankLine(text, start)
		if sep < 0 {
			units = append(units, Unit{Start: start, End: len(text)})
			break
		}
		units = append(units, Unit{Start: start, End: runEnd})
		start = runEnd
	}
	return units
}

// nextBlankLine finds the next blank-line separator in text at or after pos. A
// separator is a run of two or more consecutive newlines, where each newline is
// "\n" optionally preceded by "\r". It returns the byte index where the run
// begins (sep) and where it ends (runEnd, exclusive), or (-1, -1) if none. The
// run is greedy: it absorbs every consecutive line break so the whole blank gap
// stays with the preceding paragraph.
func nextBlankLine(text string, pos int) (sep, runEnd int) {
	for i := pos; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		// Count consecutive newlines starting at i, tolerating a "\r" before each.
		end := i
		count := 0
		for end < len(text) {
			j := end
			if j < len(text) && text[j] == '\r' {
				j++
			}
			if j < len(text) && text[j] == '\n' {
				count++
				end = j + 1
				continue
			}
			break
		}
		if count >= 2 {
			// sep is the start of the run: back up over a leading "\r" if present.
			s := i
			if s > pos && text[s-1] == '\r' {
				s--
			}
			return s, end
		}
		// Not a blank line; skip past this single newline run.
		i = end - 1
	}
	return -1, -1
}

// snapEndToRuneBoundary returns an end in (start, idealEnd] on a UTF-8 rune
// boundary. It walks backward to a rune-start byte; if that would empty the
// chunk, it extends forward to include the one full rune beginning at start.
func snapEndToRuneBoundary(text string, start, idealEnd int) int {
	if idealEnd > len(text) {
		idealEnd = len(text)
	}
	end := idealEnd
	for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	if end > start {
		return end
	}
	_, size := utf8.DecodeRuneInString(text[start:])
	return min(start+size, len(text))
}

// snapStartToRuneStart returns the smallest position >= idx on a UTF-8 rune
// boundary, keeping chunk starts rune-aligned and the loop's forward-progress
// invariant intact.
func snapStartToRuneStart(text string, idx int) int {
	if idx <= 0 {
		return 0
	}
	if idx >= len(text) {
		return len(text)
	}
	for idx < len(text) && !utf8.RuneStart(text[idx]) {
		idx++
	}
	return idx
}

// findBoundary returns the best chunk-ending byte index in text[start:idealEnd],
// preferring a sentence boundary then a word boundary within the last quarter of
// the window. It falls back to idealEnd (a hard cut) when neither applies. It is
// only used inside hardSplit, on a single oversize unit that by construction has
// no internal paragraph break.
func findBoundary(text string, start, idealEnd int) int {
	windowStart := max(idealEnd-(idealEnd-start)/4, start)
	window := text[windowStart:idealEnd]

	if i := lastSentenceEnd(window); i >= 0 {
		return windowStart + i + 1 // keep the punctuation, drop the trailing space
	}
	if i := lastWhitespace(window); i >= 0 {
		return windowStart + i
	}
	return idealEnd
}

// lastSentenceEnd returns the index of the last ".", "!", or "?" followed by a
// space in s, or -1.
func lastSentenceEnd(s string) int {
	last := -1
	for i := 0; i < len(s)-1; i++ {
		c := s[i]
		if (c == '.' || c == '!' || c == '?') && s[i+1] == ' ' {
			last = i
		}
	}
	return last
}

// lastWhitespace returns the index of the last whitespace rune in s, or -1.
func lastWhitespace(s string) int {
	last := -1
	for i, r := range s {
		if unicode.IsSpace(r) {
			last = i
		}
	}
	return last
}
