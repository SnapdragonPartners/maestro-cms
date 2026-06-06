package chunk

import "strings"

// Headings is a Boundaries function that segments Markdown at heading boundaries.
// Each unit runs from a heading line (inclusive) up to the next heading, so the
// heading text leads its own section — which means every chunk carries its own
// section title for free, since chunk text is exactly source[Start:End]. Content
// before the first heading (preamble) is its own unit.
//
// It recognizes ATX headings ("# ", "## ", … up to "######") and setext headings
// (a paragraph line underlined by a run of "=" or "-"). Heading detection is
// fence-aware: a "#" inside a fenced code block (``` or ~~~) is code, not a
// heading, so the heavily-fenced technical Markdown this is built for does not
// false-split. Ancestry (the heading trail above a section) is deliberately not
// modeled here; that is a graph concern, not a flat field (spec ADR 0005).
//
// Granularity note: a unit is a whole heading section, not a hard chunk
// boundary. Split packs small adjacent sections together to fill the token
// budget and hard-splits an oversize section — so "one section per chunk" is not
// guaranteed (that would be a later mode). A document with no headings degrades
// to Paragraphs so it still chunks at blank-line boundaries rather than becoming
// a single mega-unit.
func Headings(text string) []Unit {
	if text == "" {
		return nil
	}

	var starts []int
	inFence := false
	var fenceChar byte
	var fenceLen int
	prevLineStart := 0
	prevIsParagraph := false

	for i := 0; i < len(text); {
		line, next := lineAt(text, i, len(text))

		if inFence {
			if ch, n, info, ok := fenceMarker(line); ok && ch == fenceChar && n >= fenceLen && !info {
				inFence = false
			}
			prevIsParagraph = false
			prevLineStart = i
			i = next
			continue
		}

		switch {
		case isFenceOpen(line):
			fenceChar, fenceLen, _, _ = fenceMarker(line)
			inFence = true
			prevIsParagraph = false
		case atxLevel(line) > 0:
			starts = appendStart(starts, i)
			prevIsParagraph = false
		case prevIsParagraph && setextUnderline(line):
			// The underline turns the preceding paragraph line into a heading;
			// the section starts at that line, not the underline.
			starts = appendStart(starts, prevLineStart)
			prevIsParagraph = false
		case strings.TrimSpace(line) == "":
			prevIsParagraph = false
		default:
			prevIsParagraph = true
		}

		prevLineStart = i
		i = next
	}

	if len(starts) == 0 {
		return Paragraphs(text)
	}
	return unitsFromStarts(starts, len(text))
}

// appendStart appends s to starts unless it duplicates the last entry, keeping
// the slice strictly ascending (it is built in ascending order, but setext can
// re-point at a prior line, so guard against a coincidental duplicate).
func appendStart(starts []int, s int) []int {
	if len(starts) > 0 && starts[len(starts)-1] == s {
		return starts
	}
	return append(starts, s)
}

// unitsFromStarts builds section units from ascending heading start offsets: an
// optional preamble unit before the first heading, then one unit per heading
// spanning to the next heading (or to total for the last).
func unitsFromStarts(starts []int, total int) []Unit {
	units := make([]Unit, 0, len(starts)+1)
	if starts[0] > 0 {
		units = append(units, Unit{Start: 0, End: starts[0]})
	}
	for k := range starts {
		end := total
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		units = append(units, Unit{Start: starts[k], End: end})
	}
	return units
}

// lineAt returns the content of the line beginning at byte i within text[:end]
// (excluding the trailing "\n" and an optional preceding "\r"), and the start of
// the next line (end if this is the last line).
func lineAt(text string, i, end int) (content string, next int) {
	nl := strings.IndexByte(text[i:end], '\n')
	if nl < 0 {
		return text[i:end], end
	}
	lineEnd := i + nl
	next = lineEnd + 1
	if lineEnd > i && text[lineEnd-1] == '\r' {
		lineEnd--
	}
	return text[i:lineEnd], next
}

// fenceMarker reports whether a line (content only, no trailing newline) is a
// Markdown code-fence delimiter: up to 3 leading spaces, then a run of 3+ "`" or
// "~". It returns the fence character, the run length, whether an info string
// follows, and ok. A backtick fence whose remainder contains a backtick is not a
// fence (CommonMark forbids backticks in a backtick fence's info string).
func fenceMarker(line string) (ch byte, n int, info, ok bool) {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if i > 3 || i >= len(line) {
		return 0, 0, false, false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return 0, 0, false, false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	if j-i < 3 {
		return 0, 0, false, false
	}
	if c == '`' && strings.ContainsRune(line[j:], '`') {
		return 0, 0, false, false
	}
	return c, j - i, strings.TrimSpace(line[j:]) != "", true
}

// isFenceOpen reports whether a line opens a fenced code block.
func isFenceOpen(line string) bool {
	_, _, _, ok := fenceMarker(line)
	return ok
}

// atxLevel returns the ATX heading level (1–6) of a line, or 0 if it is not an
// ATX heading: up to 3 leading spaces, 1–6 "#", then a space/tab or end of line.
func atxLevel(line string) int {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if i > 3 {
		return 0
	}
	h := i
	for h < len(line) && line[h] == '#' {
		h++
	}
	level := h - i
	if level < 1 || level > 6 {
		return 0
	}
	if h == len(line) || line[h] == ' ' || line[h] == '\t' {
		return level
	}
	return 0
}

// setextUnderline reports whether a line is a setext heading underline: up to 3
// leading spaces, then a run of only "=" or only "-", then optional trailing
// whitespace. Whether it actually forms a heading depends on the preceding line
// being a paragraph (the caller gates on that).
func setextUnderline(line string) bool {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if i > 3 || i >= len(line) {
		return false
	}
	c := line[i]
	if c != '=' && c != '-' {
		return false
	}
	for j := i; j < len(line); j++ {
		if line[j] != c {
			return strings.Trim(line[j:], " \t") == ""
		}
	}
	return true
}
