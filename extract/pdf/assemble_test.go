package pdf_test

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

// w builds a word at (x, y) with the given width and a 10pt height.
func w(text string, x, y, width float64, block, line int) pdf.Word {
	return pdf.Word{Text: text, Box: pdf.Rect{X0: x, Y0: y, X1: x + width, Y1: y + 10}, Block: block, Line: line, Conf: 100}
}

// checkSpans asserts the invariants every assembled page must hold.
func checkSpans(t *testing.T, p pdf.PageLayout, pt pdf.PageText) {
	t.Helper()
	if len(pt.Spans) != len(p.Words) {
		t.Fatalf("%d spans for %d words", len(pt.Spans), len(p.Words))
	}
	for i, s := range pt.Spans {
		if s.Start < 0 || s.End < s.Start || s.End > len(pt.Text) {
			t.Fatalf("word %d span %+v out of range (len %d)", i, s, len(pt.Text))
		}
		if got := pt.Text[s.Start:s.End]; got != p.Words[i].Text {
			t.Fatalf("word %d: Text[%d:%d] = %q, want %q", i, s.Start, s.End, got, p.Words[i].Text)
		}
	}
	for line := range strings.SplitSeq(pt.Text, "\n") {
		if strings.TrimSpace(line) != line {
			t.Fatalf("line %q has surrounding whitespace in %q", line, pt.Text)
		}
	}
	if strings.HasPrefix(pt.Text, "\n") || strings.HasSuffix(pt.Text, "\n") {
		t.Fatalf("leading/trailing blank line in %q", pt.Text)
	}
}

func TestAssembleStatementRows(t *testing.T) {
	// Engine order puts the label column first, then the value column, as
	// pdftotext -tsv does on two-column statements.
	p := pdf.PageLayout{Words: []pdf.Word{
		w("Beginning", 72, 100, 49, 0, 0), w("balance", 124, 100, 38.5, 0, 0),
		w("Ending", 72, 118, 33, 0, 1), w("balance", 108, 118, 38.5, 0, 1),
		w("1,000.00", 400, 100, 43, 1, 2),
		w("1,150.00", 400, 118, 43, 1, 3),
	}}
	rows := pdf.AssemblePage(p, pdf.Rows)
	checkSpans(t, p, rows)
	if want := "Beginning balance        1,000.00\nEnding balance        1,150.00"; rows.Text != want {
		t.Errorf("Rows:\n%q\nwant\n%q", rows.Text, want)
	}
	ro := pdf.AssemblePage(p, pdf.ReadingOrder)
	checkSpans(t, p, ro)
	if want := "Beginning balance\nEnding balance\n\n1,000.00\n1,150.00"; ro.Text != want {
		t.Errorf("ReadingOrder:\n%q\nwant\n%q", ro.Text, want)
	}
}

func TestAssembleTwoColumnProse(t *testing.T) {
	p := pdf.PageLayout{Words: []pdf.Word{
		w("left", 72, 100, 20, 0, 0), w("one", 96, 100, 18, 0, 0),
		w("left", 72, 112, 20, 0, 1), w("two", 96, 112, 18, 0, 1),
		w("right", 320, 100, 25, 1, 2), w("one", 349, 100, 18, 1, 2),
		w("right", 320, 112, 25, 1, 3), w("two", 349, 112, 18, 1, 3),
	}}
	ro := pdf.AssemblePage(p, pdf.ReadingOrder)
	checkSpans(t, p, ro)
	if want := "left one\nleft two\n\nright one\nright two"; ro.Text != want {
		t.Errorf("ReadingOrder = %q, want %q", ro.Text, want)
	}
	// Rows interleaves the columns: the documented limit.
	rows := pdf.AssemblePage(p, pdf.Rows)
	checkSpans(t, p, rows)
	if !strings.HasPrefix(rows.Text, "left one") || !strings.Contains(strings.SplitN(rows.Text, "\n", 2)[0], "right one") {
		t.Errorf("Rows = %q, want columns interleaved", rows.Text)
	}
}

func TestAssembleRowsSuperscriptJoinsRow(t *testing.T) {
	p := pdf.PageLayout{Words: []pdf.Word{
		w("Total", 72, 100, 25, 0, 0),
		{Text: "1", Box: pdf.Rect{X0: 98, Y0: 97, X1: 101, Y1: 103}}, // 6pt superscript, half inside
		w("500", 200, 100, 18, 1, 1),
	}}
	pt := pdf.AssemblePage(p, pdf.Rows)
	checkSpans(t, p, pt)
	if strings.Contains(pt.Text, "\n") {
		t.Errorf("superscript split the row: %q", pt.Text)
	}
}

func TestAssembleEmptyWordsAddNoWhitespace(t *testing.T) {
	p := pdf.PageLayout{Words: []pdf.Word{
		w("", 10, 100, 0, 0, 0), w("a", 20, 100, 5, 0, 0), w("", 30, 100, 0, 0, 0),
		w("", 10, 120, 0, 1, 1), w("b", 20, 120, 5, 1, 1), w("", 40, 120, 0, 1, 1),
	}}
	for _, s := range []pdf.Strategy{pdf.ReadingOrder, pdf.Rows} {
		pt := pdf.AssemblePage(p, s)
		checkSpans(t, p, pt)
	}
	if got := pdf.AssemblePage(p, pdf.ReadingOrder).Text; got != "a\n\nb" {
		t.Errorf("ReadingOrder = %q", got)
	}
	if got := pdf.AssemblePage(pdf.PageLayout{}, pdf.Rows); got.Text != "" || len(got.Spans) != 0 {
		t.Errorf("empty page = %+v", got)
	}
}

func TestAssembleUnknownStrategyIsReadingOrder(t *testing.T) {
	p := pdf.PageLayout{Words: []pdf.Word{w("a", 0, 0, 5, 0, 0), w("b", 0, 20, 5, 1, 1)}}
	if got, want := pdf.AssemblePage(p, pdf.Strategy(99)).Text, pdf.AssemblePage(p, pdf.ReadingOrder).Text; got != want {
		t.Errorf("unknown strategy = %q, want %q", got, want)
	}
}

// randomLayout generates words with mixed sizes, overlaps, empty and
// multi-byte texts, so the span property is checked on messy input.
func randomLayout(r *rand.Rand, n int) pdf.PageLayout {
	texts := []string{"a", "word", "1,234.56", "", "héllo", "日本", "x\ty"}
	p := pdf.PageLayout{Words: make([]pdf.Word, n)}
	for i := range p.Words {
		x, y := r.Float64()*600, r.Float64()*800
		p.Words[i] = pdf.Word{
			Text:  texts[r.IntN(len(texts))],
			Box:   pdf.Rect{X0: x, Y0: y, X1: x + r.Float64()*80, Y1: y + r.Float64()*20},
			Block: r.IntN(4), Line: r.IntN(10),
		}
	}
	return p
}

func TestAssembleSpanProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic test data
	opts := [][]pdf.AssembleOption{
		nil,
		{pdf.WithRowOverlap(0.1), pdf.WithMaxGapSpaces(1)},
		{pdf.WithRowOverlap(1), pdf.WithMaxGapSpaces(pdf.MaxGapSpacesLimit)},
	}
	for range 300 {
		p := randomLayout(r, r.IntN(60))
		for _, s := range []pdf.Strategy{pdf.ReadingOrder, pdf.Rows} {
			for _, o := range opts {
				checkSpans(t, p, pdf.AssemblePage(p, s, o...))
			}
		}
	}
}

// sizeBound is the documented bound: words' bytes + MaxGapSpacesLimit per word
// + one byte per row (at most one row per word).
func sizeBound(p pdf.PageLayout) int {
	n := 0
	for _, wd := range p.Words {
		n += len(wd.Text) + pdf.MaxGapSpacesLimit + 1
	}
	return n
}

func TestAssembleSizeBoundAdversarial(t *testing.T) {
	many := func(n int, f func(i int) pdf.Word) pdf.PageLayout {
		p := pdf.PageLayout{Words: make([]pdf.Word, n)}
		for i := range p.Words {
			p.Words[i] = f(i)
		}
		return p
	}
	cases := map[string]pdf.PageLayout{
		"tiny words 1e9 apart": {Words: []pdf.Word{
			{Text: "a", Box: pdf.Rect{X0: 0, Y0: 0, X1: 0.01, Y1: 1}},
			{Text: "b", Box: pdf.Rect{X0: 1e9, Y0: 0, X1: 1e9 + 0.01, Y1: 1}},
		}},
		"huge coordinates": {Words: []pdf.Word{
			{Text: "a", Box: pdf.Rect{X0: -1e300, Y0: 0, X1: -1e300, Y1: 1}},
			{Text: "b", Box: pdf.Rect{X0: 1e300, Y0: 0, X1: 1e300, Y1: 1}},
		}},
		"zero-width words": many(500, func(i int) pdf.Word {
			return pdf.Word{Text: "z", Box: pdf.Rect{X0: float64(i) * 1e6, Y0: 0, X1: float64(i) * 1e6, Y1: 1}}
		}),
		"all on one row": many(1000, func(i int) pdf.Word { return w("w", float64(i)*1e5, 0, 1, 0, 0) }),
		"one word per row": many(1000, func(i int) pdf.Word {
			return w("w", 0, float64(i)*1e5, 1, i, i)
		}),
		"nan and inf": {Words: []pdf.Word{
			{Text: "a", Box: pdf.Rect{X0: math.NaN(), Y0: math.NaN(), X1: math.Inf(1), Y1: math.NaN()}},
			{Text: "b", Box: pdf.Rect{X0: math.Inf(1), Y0: 0, X1: math.Inf(1), Y1: 1}},
			{Text: "c", Box: pdf.Rect{X0: math.Inf(-1), Y0: 0, X1: 0, Y1: 1}},
		}},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			for _, s := range []pdf.Strategy{pdf.ReadingOrder, pdf.Rows} {
				pt := pdf.AssemblePage(p, s, pdf.WithMaxGapSpaces(1<<30)) // clamped to the ceiling
				checkSpans(t, p, pt)
				if len(pt.Text) > sizeBound(p) {
					t.Errorf("strategy %d: %d bytes > bound %d", s, len(pt.Text), sizeBound(p))
				}
			}
		})
	}
}

func TestAssembleGapSpacesOption(t *testing.T) {
	p := pdf.PageLayout{Words: []pdf.Word{w("a", 0, 0, 5, 0, 0), w("b", 1000, 0, 5, 0, 0)}}
	cases := []struct {
		opt  pdf.AssembleOption
		want int
	}{
		{nil, pdf.DefaultMaxGapSpaces},
		{pdf.WithMaxGapSpaces(3), 3},
		{pdf.WithMaxGapSpaces(0), pdf.DefaultMaxGapSpaces},
		{pdf.WithMaxGapSpaces(-5), pdf.DefaultMaxGapSpaces},
		{pdf.WithMaxGapSpaces(1000), pdf.MaxGapSpacesLimit},
	}
	for _, tc := range cases {
		var opts []pdf.AssembleOption
		if tc.opt != nil {
			opts = append(opts, tc.opt)
		}
		got := pdf.AssemblePage(p, pdf.Rows, opts...).Text
		if want := "a" + strings.Repeat(" ", tc.want) + "b"; got != want {
			t.Errorf("got %q, want %d spaces", got, tc.want)
		}
	}
	// A small gap gives proportionally fewer spaces, at least one.
	near := pdf.PageLayout{Words: []pdf.Word{w("aaaaa", 0, 0, 25, 0, 0), w("b", 26, 0, 5, 0, 0)}}
	if got := pdf.AssemblePage(near, pdf.Rows).Text; got != "aaaaa b" {
		t.Errorf("near words = %q", got)
	}
}

func TestAssembleRowOverlapOption(t *testing.T) {
	// The second word overlaps the first by 3 of its 10 points (30%).
	p := pdf.PageLayout{Words: []pdf.Word{w("a", 0, 0, 5, 0, 0), w("b", 20, 7, 5, 0, 0)}}
	if got := pdf.AssemblePage(p, pdf.Rows).Text; got != "a\nb" {
		t.Errorf("default 0.5: %q, want two rows", got)
	}
	if got := pdf.AssemblePage(p, pdf.Rows, pdf.WithRowOverlap(0.25)).Text; strings.Contains(got, "\n") {
		t.Errorf("0.25: %q, want one row", got)
	}
	for _, f := range []float64{0, -1, math.NaN()} { // → default
		if got := pdf.AssemblePage(p, pdf.Rows, pdf.WithRowOverlap(f)).Text; got != "a\nb" {
			t.Errorf("WithRowOverlap(%v): %q, want default behavior", f, got)
		}
	}
	full := pdf.PageLayout{Words: []pdf.Word{w("a", 0, 0, 5, 0, 0), w("b", 20, 1, 5, 0, 0)}}
	if got := pdf.AssemblePage(full, pdf.Rows, pdf.WithRowOverlap(5)).Text; got != "a\nb" { // clamped to 1: 90% < 100%
		t.Errorf("WithRowOverlap(5): %q, want two rows", got)
	}
}

// TestPageRangesMorrisStyle joins assembled pages the way a consumer like
// Morris does (empty pages contribute nothing; each range extends to the next
// range's start, so the separator belongs to the page before it) and checks
// that every byte maps to exactly one page and each word's document offset
// (page start + span) lands on its text, including on a Rows page whose words
// are reordered relative to engine order.
func TestPageRangesMorrisStyle(t *testing.T) {
	pages := []pdf.PageLayout{
		{Number: 1, Words: []pdf.Word{
			w("label", 72, 100, 25, 0, 0), w("next", 72, 120, 20, 0, 1),
			w("9.99", 400, 100, 20, 1, 2), // engine order: value after both labels
		}},
		{Number: 2, Words: []pdf.Word{}}, // blank
		{Number: 3, Words: []pdf.Word{w("third", 72, 100, 25, 0, 0)}},
		{Number: 4, Words: []pdf.Word{w("", 0, 0, 0, 0, 0)}}, // only an empty word
		{Number: 5, Words: []pdf.Word{w("fifth", 72, 100, 25, 0, 0), w("page", 100, 100, 20, 0, 0)}},
	}
	type rng struct{ page, start, end int }
	var (
		doc    strings.Builder
		ranges []rng
		texts  []pdf.PageText
		starts []int
	)
	for _, p := range pages {
		pt := pdf.AssemblePage(p, pdf.Rows)
		texts = append(texts, pt)
		starts = append(starts, -1)
		if pt.Text == "" {
			continue
		}
		if doc.Len() > 0 {
			doc.WriteString("\n\n")
			ranges[len(ranges)-1].end = doc.Len() // separator belongs to the page before
		}
		starts[len(starts)-1] = doc.Len()
		doc.WriteString(pt.Text)
		ranges = append(ranges, rng{p.Number, doc.Len() - len(pt.Text), doc.Len()})
	}
	text := doc.String()

	if got := []int{ranges[0].page, ranges[1].page, ranges[2].page}; len(ranges) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 5 {
		t.Fatalf("ranges = %+v", ranges)
	}
	if !strings.HasPrefix(texts[0].Text, "label") || !strings.Contains(strings.Split(texts[0].Text, "\n")[0], "9.99") {
		t.Fatalf("page 1 not reordered into rows: %q", texts[0].Text)
	}
	for b := range len(text) {
		n := 0
		for _, r := range ranges {
			if b >= r.start && b < r.end {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("byte %d maps to %d pages", b, n)
		}
	}
	for i, p := range pages {
		for j, wd := range p.Words {
			if wd.Text == "" {
				continue
			}
			s := texts[i].Spans[j]
			if got := text[starts[i]+s.Start : starts[i]+s.End]; got != wd.Text {
				t.Errorf("page %d word %d at doc offset: %q, want %q", p.Number, j, got, wd.Text)
			}
		}
	}
}
