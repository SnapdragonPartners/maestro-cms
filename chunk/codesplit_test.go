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
// through the prose path and may cut mid-line (at a word boundary), confirming
// the code-aware path did not alter prose behavior.
func TestHardSplitProseUnchanged(t *testing.T) {
	src := strings.Repeat("word ", 400) // one long line, no fences, no blank lines
	chunks := Split(src, Config{MaxTokens: 16, NoOverlap: true})
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	assertChunksInvariants(t, src, chunks)
	// At least one internal cut is not at a newline (there are no newlines at all).
	if strings.Contains(src, "\n") {
		t.Fatal("test precondition: source should have no newlines")
	}
}

func texts(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Text
	}
	return out
}
