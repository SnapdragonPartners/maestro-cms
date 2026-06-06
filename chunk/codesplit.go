package chunk

// codeSpan is an atomic region of an oversize unit during code-aware hard-split:
// either a single line or a whole fenced code block. fence reports which.
type codeSpan struct {
	start, end int
	fence      bool
}

// spanHasFence reports whether text[start:end] contains a code-fence opener. It
// is the cheap test hardSplit uses to choose the code-aware path over the prose
// path.
func spanHasFence(text string, start, end int) bool {
	for i := start; i < end; {
		line, next := lineAt(text, i, end)
		if isFenceOpen(line) {
			return true
		}
		i = next
	}
	return false
}

// splitFenced splits a fence-containing oversize span into budget-fitting chunks
// on line boundaries. It groups the span into atomic regions — each fenced block
// is one region, each non-fence line is one region — then packs consecutive
// regions while they fit the budget, with back-prepended overlap. A single
// region that alone exceeds the budget is split internally: a fenced block at
// its line boundaries (splitLines), a lone over-budget non-fence line by the
// prose splitter (hardSplitProse) as the only honest last resort.
func splitFenced(text string, start, end, maxTok, overlapTok int, est Estimate, chunks *[]Chunk, idx *int) {
	groups := fencedGroups(text, start, end)
	tok := make([]int, len(groups))
	for i, g := range groups {
		tok[i] = est(text[g.start:g.end])
	}

	i := 0
	for i < len(groups) {
		if tok[i] > maxTok {
			g := groups[i]
			if g.fence {
				splitLines(text, g.start, g.end, maxTok, overlapTok, est, chunks, idx)
			} else {
				// A single non-fence line longer than the whole budget: the only
				// options are exceed the budget or cut mid-line, so fall back to
				// the prose splitter (word/rune boundaries) for this one line.
				hardSplitProse(text, g.start, g.end, maxTok, overlapTok, est, chunks, idx)
			}
			i++
			continue
		}
		j, sum := i, tok[i]
		for j+1 < len(groups) && sum+tok[j+1] <= maxTok {
			j++
			sum += tok[j]
		}
		appendChunk(text, groups[i].start, groups[j].end, sum, chunks, idx)
		if j == len(groups)-1 {
			break
		}
		i = overlapStart(tok, i, j, overlapTok)
	}
}

// splitLines splits a single oversize fenced block at line boundaries. A lone
// line that itself exceeds the budget falls back to the prose splitter; this is
// the only place a cut can land inside a fenced block, and only when one of its
// lines cannot fit on its own.
func splitLines(text string, start, end, maxTok, overlapTok int, est Estimate, chunks *[]Chunk, idx *int) {
	var lines []codeSpan
	for i := start; i < end; {
		_, next := lineAt(text, i, end)
		lines = append(lines, codeSpan{start: i, end: next})
		i = next
	}
	tok := make([]int, len(lines))
	for k, l := range lines {
		tok[k] = est(text[l.start:l.end])
	}

	i := 0
	for i < len(lines) {
		if tok[i] > maxTok {
			hardSplitProse(text, lines[i].start, lines[i].end, maxTok, overlapTok, est, chunks, idx)
			i++
			continue
		}
		j, sum := i, tok[i]
		for j+1 < len(lines) && sum+tok[j+1] <= maxTok {
			j++
			sum += tok[j]
		}
		appendChunk(text, lines[i].start, lines[j].end, sum, chunks, idx)
		if j == len(lines)-1 {
			break
		}
		i = overlapStart(tok, i, j, overlapTok)
	}
}

// fencedGroups partitions text[start:end] into atomic regions: each fenced code
// block (from its opening fence line through its closing fence line, or to end
// if unterminated) is one region; every other line is its own region. The
// regions tile the span contiguously in ascending order.
func fencedGroups(text string, start, end int) []codeSpan {
	var groups []codeSpan
	for i := start; i < end; {
		line, next := lineAt(text, i, end)
		ch, n, _, ok := fenceMarker(line)
		if !ok {
			groups = append(groups, codeSpan{start: i, end: next})
			i = next
			continue
		}
		// Consume through the matching closing fence (or to end).
		gStart := i
		i = next
		for i < end {
			l2, n2 := lineAt(text, i, end)
			i = n2
			if c2, m2, info2, ok2 := fenceMarker(l2); ok2 && c2 == ch && m2 >= n && !info2 {
				break
			}
		}
		groups = append(groups, codeSpan{start: gStart, end: i, fence: true})
	}
	return groups
}

// appendChunk appends one chunk spanning text[start:end] with the given token
// count and advances the running index.
func appendChunk(text string, start, end, tok int, chunks *[]Chunk, idx *int) {
	*chunks = append(*chunks, Chunk{
		Text:       text[start:end],
		Index:      *idx,
		StartByte:  start,
		EndByte:    end,
		TokenCount: tok,
	})
	*idx++
}
