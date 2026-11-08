package pdftotext

import (
	"strings"
	"testing"
)

const tsvHead = "level\tpage_num\tpar_num\tblock_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"

// recordedTSV is pdftotext -tsv -cropbox output (Poppler 26.04.0) for a
// statement row on page 1, a blank page 2 and a page 3 whose text lies outside
// its crop box.
const recordedTSV = tsvHead +
	"1\t1\t0\t0\t0\t0\t0.000000\t0.000000\t612.000000\t792.000000\t-1\t###PAGE###\n" +
	"3\t1\t0\t0\t0\t0\t72.000000\t84.102000\t90.497000\t10.175000\t-1\t###FLOW###\n" +
	"4\t1\t0\t0\t0\t0\t72.000000\t84.102000\t90.497000\t10.175000\t-1\t###LINE###\n" +
	"5\t1\t0\t0\t0\t0\t72.00\t84.10\t48.92\t10.17\t100\tBeginning\n" +
	"5\t1\t0\t0\t0\t1\t123.98\t84.10\t38.52\t10.17\t100\tbalance\n" +
	"3\t1\t1\t0\t0\t0\t400.000000\t84.102000\t42.812000\t10.175000\t-1\t###FLOW###\n" +
	"4\t1\t1\t0\t0\t0\t400.000000\t84.102000\t42.812000\t10.175000\t-1\t###LINE###\n" +
	"5\t1\t1\t0\t0\t0\t400.00\t84.10\t42.81\t10.17\t100\t1,000.00\n" +
	"1\t2\t0\t0\t0\t0\t0.000000\t0.000000\t612.000000\t792.000000\t-1\t###PAGE###\n" +
	"1\t3\t0\t0\t0\t0\t0.000000\t0.000000\t400.000000\t292.000000\t-1\t###PAGE###\n"

func TestParseTSVRecorded(t *testing.T) {
	pages, err := parseTSV(strings.NewReader(recordedTSV))
	if err != nil {
		t.Fatalf("parseTSV: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("got %d pages", len(pages))
	}
	w := pages[0].words
	if len(w) != 3 {
		t.Fatalf("page 1 words = %+v", w)
	}
	if w[0].Text != "Beginning" || w[0].Box.X0 != 72 || w[0].Box.Y0 != 84.10 ||
		w[0].Box.X1 != 72+48.92 || w[0].Box.Y1 != 84.10+10.17 || w[0].Conf != 100 {
		t.Errorf("word 0 = %+v", w[0])
	}
	// Two flows, each with par/block/line counters restarting at 0, become
	// distinct blocks and lines.
	if w[0].Block != 0 || w[1].Block != 0 || w[2].Block != 1 {
		t.Errorf("blocks = %d %d %d", w[0].Block, w[1].Block, w[2].Block)
	}
	if w[0].Line != 0 || w[1].Line != 0 || w[2].Line != 1 {
		t.Errorf("lines = %d %d %d", w[0].Line, w[1].Line, w[2].Line)
	}
	for i := 1; i < 3; i++ {
		if pages[i].words == nil || len(pages[i].words) != 0 {
			t.Errorf("page %d words = %#v, want empty non-nil", i+1, pages[i].words)
		}
	}
}

func page(n string) string {
	return "1\t" + n + "\t0\t0\t0\t0\t0\t0\t612\t792\t-1\t###PAGE###\n"
}

func word(pg, text string) string {
	return "5\t" + pg + "\t0\t0\t0\t0\t10\t20\t30\t40\t100\t" + text + "\n"
}

func TestParseTSVText(t *testing.T) {
	// Sentinel text at word level is a word; a tab inside text stays in it.
	in := tsvHead + page("1") + word("1", "###PAGE###") + word("1", "a\tb") + word("1", "")
	pages, err := parseTSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseTSV: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("sentinel text started a page: %d pages", len(pages))
	}
	got := []string{pages[0].words[0].Text, pages[0].words[1].Text, pages[0].words[2].Text}
	if got[0] != "###PAGE###" || got[1] != "a\tb" || got[2] != "" {
		t.Fatalf("texts = %q", got)
	}
}

func TestParseTSVRenumbersPerPage(t *testing.T) {
	row := func(pg, par, block, line string) string {
		return "5\t" + pg + "\t" + par + "\t" + block + "\t" + line + "\t0\t0\t0\t1\t1\t100\tw\n"
	}
	in := tsvHead + page("1") +
		row("1", "0", "0", "0") + row("1", "0", "0", "1") + row("1", "0", "1", "0") + row("1", "1", "0", "0") +
		page("2") + row("2", "3", "2", "1")
	pages, err := parseTSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseTSV: %v", err)
	}
	var blocks, lines []int
	for _, w := range pages[0].words {
		blocks, lines = append(blocks, w.Block), append(lines, w.Line)
	}
	if want := []int{0, 0, 1, 2}; !equalInts(blocks, want) {
		t.Errorf("blocks = %v, want %v", blocks, want)
	}
	if want := []int{0, 1, 2, 3}; !equalInts(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	if w := pages[1].words[0]; w.Block != 0 || w.Line != 0 {
		t.Errorf("page 2 ids not restarted: %+v", w)
	}
}

func TestParseTSVConf(t *testing.T) {
	in := tsvHead + page("1") +
		"5\t1\t0\t0\t0\t0\t0\t0\t1\t1\t-1\ta\n" +
		"5\t1\t0\t0\t0\t0\t0\t0\t1\t1\t87.6\tb\n" +
		"5\t1\t0\t0\t0\t0\t0\t0\t1\t1\t1e300\tc\n"
	pages, err := parseTSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseTSV: %v", err)
	}
	w := pages[0].words
	if w[0].Conf != -1 || w[1].Conf != 88 || w[2].Conf != 100 {
		t.Fatalf("confs = %d %d %d", w[0].Conf, w[1].Conf, w[2].Conf)
	}
}

func TestParseTSVRejects(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"wrong header":       "level\tpage\n",
		"short row":          tsvHead + page("1") + "5\t1\t0\t0\t0\t0\t10\t20\n",
		"bad int":            tsvHead + page("1") + "5\tx\t0\t0\t0\t0\t10\t20\t30\t40\t100\tw\n",
		"unknown level":      tsvHead + page("1") + "7\t1\t0\t0\t0\t0\t10\t20\t30\t40\t100\tw\n",
		"nan coordinate":     tsvHead + page("1") + "5\t1\t0\t0\t0\t0\tNaN\t20\t30\t40\t100\tw\n",
		"inf coordinate":     tsvHead + page("1") + "5\t1\t0\t0\t0\t0\t10\t+Inf\t30\t40\t100\tw\n",
		"negative width":     tsvHead + page("1") + "5\t1\t0\t0\t0\t0\t10\t20\t-3\t40\t100\tw\n",
		"overflowing box":    tsvHead + page("1") + "5\t1\t0\t0\t0\t0\t1.7e308\t20\t1.7e308\t40\t100\tw\n",
		"page out of order":  tsvHead + page("1") + page("3"),
		"page repeated":      tsvHead + page("1") + page("1"),
		"word before page":   tsvHead + word("1", "w"),
		"word on wrong page": tsvHead + page("1") + word("2", "w"),
		"line too long":      tsvHead + page("1") + word("1", strings.Repeat("x", maxTSVLine+1)),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if pages, err := parseTSV(strings.NewReader(in)); err == nil {
				t.Fatalf("parseTSV accepted: %+v", pages)
			}
		})
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
