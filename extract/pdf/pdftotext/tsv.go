package pdftotext

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

// maxTSVLine bounds one line of pdftotext -tsv output, so a single giant
// "word" can't grow the scanner's buffer without bound.
const maxTSVLine = 1 << 20

// tsvHeader is the column header pdftotext -tsv writes first.
const tsvHeader = "level\tpage_num\tpar_num\tblock_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext"

// tsvFields is the number of columns; the last (text) may contain tabs.
const tsvFields = 12

// fieldName names column i for error messages.
func fieldName(i int) string { return strings.Split(tsvHeader, "\t")[i] }

// TSV row levels.
const (
	levelPage = 1
	levelWord = 5
)

// tsvPage is one page's words from TSV, before geometry is attached.
type tsvPage struct {
	number int
	words  []pdf.Word
}

// parseTSV reads pdftotext -tsv output as it streams. It is strict: anything it
// can't trust is an error (the caller reports it as malformed output), never
// skipped. Row kinds come from the level column, never from the ###PAGE###
// style sentinel text, which a document could contain. Each row is split into
// at most 12 fields, so a tab inside word text stays in the text.
func parseTSV(r io.Reader) ([]tsvPage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxTSVLine)

	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, tsvScanErr(err)
		}
		return nil, errors.New("tsv: empty output")
	}
	if sc.Text() != tsvHeader {
		return nil, fmt.Errorf("tsv: unexpected header %q", sc.Text())
	}

	var (
		pages  []tsvPage
		cur    *tsvPage
		blocks map[[2]int]int
		lines  map[[3]int]int
		lineNo = 1
	)
	for sc.Scan() {
		lineNo++
		row, err := parseRow(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("tsv line %d: %w", lineNo, err)
		}
		switch row.level {
		case levelPage:
			finishPage(cur)
			if row.page != len(pages)+1 {
				return nil, fmt.Errorf("tsv line %d: page %d out of order (want %d)", lineNo, row.page, len(pages)+1)
			}
			pages = append(pages, tsvPage{number: row.page, words: []pdf.Word{}})
			cur = &pages[len(pages)-1]
			blocks, lines = map[[2]int]int{}, map[[3]int]int{}
		case levelWord:
			if cur == nil || row.page != cur.number {
				return nil, fmt.Errorf("tsv line %d: word on page %d outside its page", lineNo, row.page)
			}
			// TSV's block and line numbers are counters within a flow (par_num),
			// so renumber them in order of appearance to be unique per page.
			bk := [2]int{row.par, row.block}
			b, ok := blocks[bk]
			if !ok {
				b = len(blocks)
				blocks[bk] = b
			}
			lk := [3]int{row.par, row.block, row.line}
			l, ok := lines[lk]
			if !ok {
				l = len(lines)
				lines[lk] = l
			}
			cur.words = append(cur.words, pdf.Word{
				Text:  row.text,
				Box:   row.box,
				Block: b,
				Line:  l,
				Conf:  row.conf,
			})
		default:
			// Structural rows (flows, lines): ids are already on the words.
		}
	}
	if err := sc.Err(); err != nil {
		return nil, tsvScanErr(err)
	}
	finishPage(cur)
	return pages, nil
}

// finishPage copies a finished page's words into an exactly sized slice, so the
// spare capacity left by append isn't kept alive in the returned Layout.
func finishPage(p *tsvPage) {
	if p != nil && cap(p.words) > len(p.words) {
		p.words = slices.Clone(p.words)
	}
}

func tsvScanErr(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("tsv: line longer than %d bytes", maxTSVLine)
	}
	return fmt.Errorf("tsv: %w", err)
}

type tsvRow struct {
	level, page, par, block, line int
	box                           pdf.Rect
	conf                          int
	text                          string
}

func parseRow(s string) (tsvRow, error) {
	f := strings.SplitN(s, "\t", tsvFields)
	if len(f) != tsvFields {
		return tsvRow{}, fmt.Errorf("%d fields, want %d", len(f), tsvFields)
	}
	var ints [6]int
	for i := range ints {
		n, err := strconv.Atoi(f[i])
		if err != nil {
			return tsvRow{}, fmt.Errorf("bad %s %q", fieldName(i), f[i])
		}
		ints[i] = n
	}
	if ints[0] < 1 || ints[0] > 5 {
		return tsvRow{}, fmt.Errorf("unknown level %d", ints[0])
	}
	var nums [5]float64 // left, top, width, height, conf
	for i := range nums {
		n, err := strconv.ParseFloat(f[6+i], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return tsvRow{}, fmt.Errorf("bad %s %q", fieldName(6+i), f[6+i])
		}
		nums[i] = n
	}
	left, top, width, height := nums[0], nums[1], nums[2], nums[3]
	if width < 0 || height < 0 {
		return tsvRow{}, fmt.Errorf("negative size %gx%g", width, height)
	}
	box := pdf.Rect{X0: left, Y0: top, X1: left + width, Y1: top + height}
	if math.IsInf(box.X1, 0) || math.IsInf(box.Y1, 0) {
		return tsvRow{}, fmt.Errorf("box overflows: %q", s)
	}
	return tsvRow{
		level: ints[0], page: ints[1], par: ints[2], block: ints[3], line: ints[4],
		box:  box,
		conf: clampConf(nums[4]),
		text: f[11],
	}, nil
}

// clampConf maps the conf column to -1 (none reported) or 0–100.
func clampConf(c float64) int {
	switch {
	case c < 0:
		return -1
	case c > 100:
		return 100
	default:
		return int(math.Round(c))
	}
}
