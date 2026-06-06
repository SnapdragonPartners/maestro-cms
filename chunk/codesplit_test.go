package chunk

import (
	"strings"
	"testing"
)

// assertLineAligned checks that every internal chunk cut falls on a line
// boundary: each chunk after the first starts right after a newline, and each
// chunk before the last ends on a newline. The last chunk's end is exempt —
// Split trims trailing whitespace, so the final chunk can end on the last
// content byte rather than a newline.
func assertLineAligned(t *testing.T, src string, chunks []Chunk) {
	t.Helper()
	for i, c := range chunks {
		if i > 0 && src[c.StartByte-1] != '\n' {
			t.Fatalf("chunk %d start %d is mid-line", i, c.StartByte)
		}
		if i < len(chunks)-1 && src[c.EndByte-1] != '\n' {
			t.Fatalf("chunk %d end %d is mid-line", i, c.EndByte)
		}
	}
}

// TestSplitFencedInternalSplitIsLineAligned: a single fenced block that alone
// exceeds the budget is split inside, but only at line boundaries.
func TestSplitFencedInternalSplitIsLineAligned(t *testing.T) {
	var b strings.Builder
	b.WriteString("```go\n")
	for range [20]int{} {
		b.WriteString("line of code\n")
	}
	b.WriteString("```\n")
	src := b.String()

	chunks := Split(src, Config{MaxTokens: 8, NoOverlap: true})
	if len(chunks) < 2 {
		t.Fatalf("expected the oversize fence to split, got %d chunk(s)", len(chunks))
	}
	assertChunksInvariants(t, src, chunks)
	assertLineAligned(t, src, chunks)
}

// TestSplitFencedKeepsBlockWholeWhenItFits: when a fenced block fits the budget,
// hard-splitting the surrounding section never cuts the block apart.
func TestSplitFencedKeepsBlockWholeWhenItFits(t *testing.T) {
	fence := "```\nshort code\n```\n"
	src := "intro line one\nintro line two\n" + fence + "tail line\n"

	chunks := Split(src, Config{MaxTokens: 6, NoOverlap: true})
	assertChunksInvariants(t, src, chunks)

	count := 0
	for _, c := range chunks {
		if strings.Contains(c.Text, fence) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("fenced block kept whole in %d chunks, want exactly 1; chunks=%q", count, texts(chunks))
	}
}

// TestSplitFencedUnterminatedFence: an unterminated fence (no closing delimiter)
// is treated as one block running to the end and still splits line-aligned.
func TestSplitFencedUnterminatedFence(t *testing.T) {
	var b strings.Builder
	b.WriteString("```\n")
	for range [12]int{} {
		b.WriteString("dangling code line\n")
	}
	src := b.String() // no closing ```

	chunks := Split(src, Config{MaxTokens: 8, NoOverlap: true})
	assertChunksInvariants(t, src, chunks)
	assertLineAligned(t, src, chunks)
}

// TestHardSplitProseUnchanged: a fence-free oversize paragraph still goes
// through the prose path, splitting at word boundaries. It must produce multiple
// chunks, cover the source with no dropped bytes (no overlap), and cut at word
// boundaries — confirming the code-aware path did not alter prose behavior.
func TestHardSplitProseUnchanged(t *testing.T) {
	src := strings.Repeat("word ", 400) // one long line, no fences, no blank lines
	if strings.Contains(src, "\n") {
		t.Fatal("precondition: prose source must have no newlines")
	}

	chunks := Split(src, Config{MaxTokens: 16, NoOverlap: true})
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	assertChunksInvariants(t, src, chunks)

	// With no overlap, the chunks must concatenate back to the trimmed source —
	// the word-boundary cuts drop no bytes.
	if got := strings.Join(texts(chunks), ""); got != strings.TrimSpace(src) {
		t.Fatalf("chunks do not reconstruct source:\n got %q\nwant %q", got, strings.TrimSpace(src))
	}
	// Every internal cut lands on a word boundary: a chunk-ending byte that is a
	// space, or equivalently the next chunk starting on a space. This is the
	// prose behavior, distinct from the line-aligned code path.
	for i := 1; i < len(chunks); i++ {
		prevEndsAtSpace := src[chunks[i-1].EndByte-1] == ' '
		curStartsAtSpace := src[chunks[i].StartByte] == ' '
		if !prevEndsAtSpace && !curStartsAtSpace {
			t.Fatalf("cut between chunk %d and %d is not at a word boundary (…%q | %q…)",
				i-1, i, lastN(chunks[i-1].Text, 6), firstN(chunks[i].Text, 6))
		}
	}
}

// TestSplitFencedOversizeLineFallsBackToProse covers the documented exception:
// a single line inside a fenced block that itself exceeds the budget cannot be
// split on a line boundary, so it falls back to the prose splitter — producing
// at least one mid-line (non-line-aligned) cut rather than an unbounded chunk.
func TestSplitFencedOversizeLineFallsBackToProse(t *testing.T) {
	longLine := strings.Repeat("word ", 100) // ~500 bytes, one line, no newline
	src := "```\n" + longLine + "\n```\n"

	chunks := Split(src, Config{MaxTokens: 16, NoOverlap: true})
	if len(chunks) < 2 {
		t.Fatalf("expected the over-budget line to split, got %d chunk(s)", len(chunks))
	}
	assertChunksInvariants(t, src, chunks)

	// No chunk may exceed the budget: the whole point of the fallback is to
	// bound the oversize line instead of emitting it whole.
	for i, c := range chunks {
		if c.TokenCount > 16 {
			t.Fatalf("chunk %d has %d tokens, over budget 16 (%q)", i, c.TokenCount, c.Text)
		}
	}
	// At least one cut must be mid-line: a chunk (after the first) whose start is
	// not immediately preceded by a newline. That is the prose fallback firing
	// inside the fence.
	midLine := false
	for i := 1; i < len(chunks); i++ {
		if src[chunks[i].StartByte-1] != '\n' {
			midLine = true
			break
		}
	}
	if !midLine {
		t.Fatal("expected a mid-line cut from the prose fallback inside the fence, found none")
	}
}

func firstN(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

func lastN(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[len(s)-n:]
}

func texts(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Text
	}
	return out
}
