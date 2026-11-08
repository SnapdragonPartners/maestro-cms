package pdf

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Strategy selects how AssemblePage orders a page's words into text.
type Strategy int

const (
	// ReadingOrder follows the engine's order: words in a line joined by one
	// space, lines by "\n", blocks by a blank line. It keeps the columns of
	// multi-column prose apart, but can separate a table row's label from its
	// values. It is close to, not identical to, pdftotext's default-mode text:
	// that mode joins hyphenated line ends, and AssemblePage never alters words.
	ReadingOrder Strategy = iota
	// Rows groups words into geometric rows (by vertical overlap), left to
	// right, rows top to bottom, with column gaps kept as up to MaxGapSpaces
	// spaces. It keeps a table row's label and values on one line. It
	// interleaves multi-column prose line by line, and doesn't deskew: a tilted
	// scan line can split across rows, or merge neighboring rows.
	Rows
)

// Defaults and limits for the Rows strategy's options.
const (
	// DefaultRowOverlap is the default WithRowOverlap fraction.
	DefaultRowOverlap = 0.5
	// DefaultMaxGapSpaces is the default WithMaxGapSpaces value.
	DefaultMaxGapSpaces = 8
	// MaxGapSpacesLimit is the hard ceiling on WithMaxGapSpaces, which keeps
	// assembled text within a fixed multiple of the words' bytes.
	MaxGapSpacesLimit = 32
)

// PageText is a page's assembled text and where each word landed in it.
type PageText struct {
	// Text has no leading or trailing whitespace on any line, and no leading or
	// trailing blank lines, so it needs no normalization (which would shift
	// spans).
	Text string
	// Spans has one entry per word of the PageLayout, at the same index.
	// Text[s.Start:s.End] == Words[i].Text always holds, whatever the
	// strategy; a word with empty text gets an empty span.
	Spans []Span
}

// Span is a byte range in PageText.Text; End is exclusive.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// AssembleOption configures AssemblePage.
type AssembleOption func(*assembleConfig)

type assembleConfig struct {
	overlap float64
	maxGap  int
}

// WithRowOverlap sets the fraction of the shorter height by which a word's
// vertical extent must overlap a row's to join it (Rows only). Values above 1
// are clamped to 1; zero, negative or NaN mean DefaultRowOverlap.
func WithRowOverlap(f float64) AssembleOption {
	return func(c *assembleConfig) {
		switch {
		case math.IsNaN(f) || f <= 0:
			c.overlap = DefaultRowOverlap
		case f > 1:
			c.overlap = 1
		default:
			c.overlap = f
		}
	}
}

// WithMaxGapSpaces sets the most spaces a gap between two words in a row
// becomes (Rows only). Values above MaxGapSpacesLimit are clamped to it; zero
// or negative mean DefaultMaxGapSpaces.
func WithMaxGapSpaces(n int) AssembleOption {
	return func(c *assembleConfig) {
		switch {
		case n <= 0:
			c.maxGap = DefaultMaxGapSpaces
		case n > MaxGapSpacesLimit:
			c.maxGap = MaxGapSpacesLimit
		default:
			c.maxGap = n
		}
	}
}

// AssemblePage builds a page's text from its words with strategy s (an unknown
// Strategy means ReadingOrder), recording each word's byte span. Each word's
// text is written verbatim, so spans always agree with the words. Gaps are
// bounded, never sized from raw coordinates: the text is at most the words'
// bytes plus MaxGapSpacesLimit bytes per word plus one byte per row. It is pure
// and needs no engine.
func AssemblePage(p PageLayout, s Strategy, opts ...AssembleOption) PageText {
	cfg := assembleConfig{overlap: DefaultRowOverlap, maxGap: DefaultMaxGapSpaces}
	for _, o := range opts {
		o(&cfg)
	}
	if s == Rows {
		return assembleRows(p.Words, cfg)
	}
	return assembleReadingOrder(p.Words)
}

// pageWriter appends words and separators, recording spans. A separator is
// written only between two non-empty words, so no line starts or ends with
// whitespace and empty words never add any.
type pageWriter struct {
	b       strings.Builder
	spans   []Span
	pending string // separator owed before the next non-empty word
}

func (w *pageWriter) sep(s string) {
	if w.b.Len() == 0 {
		return
	}
	// The strongest separator owed wins (more newlines, then longer), so a line
	// or block break skipped over empty words isn't downgraded to a space.
	ns, np := strings.Count(s, "\n"), strings.Count(w.pending, "\n")
	if ns > np || (ns == np && len(s) > len(w.pending)) {
		w.pending = s
	}
}

func (w *pageWriter) word(i int, text string) {
	if text == "" {
		pos := w.b.Len()
		w.spans[i] = Span{pos, pos}
		return
	}
	if w.b.Len() > 0 {
		w.b.WriteString(w.pending)
	}
	w.pending = ""
	start := w.b.Len()
	w.b.WriteString(text)
	w.spans[i] = Span{start, w.b.Len()}
}

func assembleReadingOrder(words []Word) PageText {
	w := &pageWriter{spans: make([]Span, len(words))}
	for i := range words {
		if i > 0 {
			switch {
			case words[i].Block != words[i-1].Block:
				w.sep("\n\n")
			case words[i].Line != words[i-1].Line:
				w.sep("\n")
			default:
				w.sep(" ")
			}
		}
		w.word(i, words[i].Text)
	}
	return PageText{Text: w.b.String(), Spans: w.spans}
}

// finite maps NaN to 0 and leaves other values alone, so sorting and overlap
// tests on caller-built layouts stay well-defined.
func finite(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return v
}

type row struct {
	top, bottom float64
	words       []int
}

func assembleRows(words []Word, cfg assembleConfig) PageText {
	order := make([]int, len(words))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return finite(words[order[a]].Box.Y0) < finite(words[order[b]].Box.Y0)
	})

	// Greedy grouping in top order: a word joins the last row when their
	// vertical extents overlap by at least cfg.overlap of the shorter height.
	rows := make([]*row, 0, 16)
	for _, i := range order {
		top, bottom := finite(words[i].Box.Y0), finite(words[i].Box.Y1)
		if bottom < top {
			top, bottom = bottom, top
		}
		if n := len(rows); n > 0 {
			r := rows[n-1]
			overlap := math.Min(bottom, r.bottom) - math.Max(top, r.top)
			shorter := math.Min(bottom-top, r.bottom-r.top)
			if overlap >= 0 && overlap >= cfg.overlap*shorter {
				r.words = append(r.words, i)
				r.top, r.bottom = math.Min(r.top, top), math.Max(r.bottom, bottom)
				continue
			}
		}
		rows = append(rows, &row{top: top, bottom: bottom, words: []int{i}})
	}

	w := &pageWriter{spans: make([]Span, len(words))}
	for _, r := range rows {
		sort.SliceStable(r.words, func(a, b int) bool {
			return finite(words[r.words[a]].Box.X0) < finite(words[r.words[b]].Box.X0)
		})
		unit := charUnit(words, r.words)
		w.sep("\n")
		prevEnd, havePrev := 0.0, false
		for _, i := range r.words {
			if words[i].Text == "" {
				w.word(i, "")
				continue
			}
			if havePrev {
				w.sep(strings.Repeat(" ", gapSpaces(finite(words[i].Box.X0)-prevEnd, unit, cfg.maxGap)))
			}
			w.word(i, words[i].Text)
			prevEnd, havePrev = finite(words[i].Box.X1), true
		}
	}
	return PageText{Text: w.b.String(), Spans: w.spans}
}

// charUnit is the row's median character width (word width ÷ rune count over
// non-empty words with positive width), floored at 1 point.
func charUnit(words []Word, idx []int) float64 {
	var widths []float64
	for _, i := range idx {
		n := utf8.RuneCountInString(words[i].Text)
		wd := finite(words[i].Box.X1) - finite(words[i].Box.X0)
		if n > 0 && wd > 0 && !math.IsInf(wd, 0) {
			widths = append(widths, wd/float64(n))
		}
	}
	if len(widths) == 0 {
		return 1
	}
	sort.Float64s(widths)
	m := widths[len(widths)/2]
	if len(widths)%2 == 0 {
		m = (widths[len(widths)/2-1] + m) / 2
	}
	return math.Max(m, 1)
}

// gapSpaces converts a horizontal gap to clamp(round(gap/unit), 1, maxGap)
// spaces; overlapping, negative or non-finite gaps clamp into the same range.
func gapSpaces(gap, unit float64, maxGap int) int {
	n := math.Round(gap / unit)
	switch {
	case math.IsNaN(n) || n < 1:
		return 1
	case n > float64(maxGap):
		return maxGap
	default:
		return int(n)
	}
}
