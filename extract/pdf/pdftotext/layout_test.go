package pdftotext_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/pdftotext"
)

var _ pdf.LayoutEngine = (*pdftotext.Engine)(nil)

// ---- fake binaries ------------------------------------------------------

// fakeBin writes an executable shell script and returns its path.
func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries are shell scripts")
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\n"+script+"\n"), 0o700); err != nil { //nolint:gosec // test helper must be executable
		t.Fatal(err)
	}
	return p
}

// fakeInfo returns a fake pdfinfo reporting n letter-size pages.
func fakeInfo(t *testing.T, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pages:           %d\n", n)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "Page %4d rot:   0\nPage %4d CropBox: 0 0 612 792\n", i, i)
	}
	return fakeBin(t, "pdfinfo", "printf '%s' '"+b.String()+"'")
}

const tsvHeaderLine = `level\tpage_num\tpar_num\tblock_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n`

func tsvPageLine(n int) string {
	return fmt.Sprintf(`1\t%d\t0\t0\t0\t0\t0\t0\t612\t792\t-1\t###PAGE###\n`, n)
}

const tsvWordLine = `5\t1\t0\t0\t0\t0\t72\t84\t40\t10\t100\tword\n`

func fakeLayoutEngine(t *testing.T, pages int, tsvScript string, opts ...pdftotext.Option) *pdftotext.Engine {
	opts = append([]pdftotext.Option{
		pdftotext.WithPdfinfoBinary(fakeInfo(t, pages)),
		pdftotext.WithBinary(fakeBin(t, "pdftotext", tsvScript)),
		pdftotext.WithTimeout(5 * time.Second),
	}, opts...)
	return pdftotext.New(opts...)
}

func TestLayoutFakeSuccess(t *testing.T) {
	eng := fakeLayoutEngine(t, 2, `printf '`+tsvHeaderLine+tsvPageLine(1)+tsvWordLine+tsvPageLine(2)+`'`)
	l, err := eng.Layout(context.Background(), []byte("%PDF"))
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if l.Version != pdf.LayoutVersion || l.Engine != "pdftotext" || l.Box != pdf.BoxCrop || len(l.Pages) != 2 {
		t.Fatalf("layout = %+v", l)
	}
	if len(l.Pages[0].Words) != 1 || l.Pages[0].Words[0].Text != "word" || l.Pages[1].Words == nil {
		t.Fatalf("pages = %+v", l.Pages)
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"words":[]`)) || !bytes.Contains(b, []byte(`"version":1`)) {
		t.Errorf("JSON = %s", b)
	}
}

func TestLayoutFakeErrors(t *testing.T) {
	big := `printf '` + tsvHeaderLine + tsvPageLine(1) + `'; exec yes`
	cases := []struct {
		name      string
		pages     int
		script    string
		opts      []pdftotext.Option
		want      error
		malformed bool
	}{
		// Malformed halfway through a large output: stops the child (no hang on
		// a full pipe) and returns malformed output.
		{name: "malformed mid-stream", pages: 1, script: big, malformed: true},
		{name: "cap mid-stream", pages: 1, script: big, opts: []pdftotext.Option{pdftotext.WithMaxLayoutBytes(64 << 10)}, want: pdftotext.ErrOutputTooLarge},
		{name: "page count mismatch", pages: 2, script: `printf '` + tsvHeaderLine + tsvPageLine(1) + `'`, malformed: true},
		{name: "over-long line", pages: 1, script: `printf '` + tsvHeaderLine + tsvPageLine(1) + `'; head -c 2000000 /dev/zero | tr '\0' x`, malformed: true},
		{name: "pdftotext crash", pages: 1, script: `kill -ABRT $$`, want: pdf.ErrEngineCrashed, malformed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			_, err := fakeLayoutEngine(t, tc.pages, tc.script, tc.opts...).Layout(context.Background(), []byte("%PDF"))
			if err == nil {
				t.Fatal("Layout succeeded")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if got := errors.Is(err, extract.ErrMalformedSource); got != tc.malformed {
				t.Errorf("malformed = %v, want %v (err %v)", got, tc.malformed, err)
			}
			if d := time.Since(start); d > 4*time.Second {
				t.Errorf("took %s", d)
			}
		})
	}
}

func TestLayoutMissingPdfinfo(t *testing.T) {
	eng := pdftotext.New(pdftotext.WithPdfinfoBinary("maestro-cms-no-such-pdfinfo"))
	_, err := eng.Layout(context.Background(), []byte("%PDF"))
	if !errors.Is(err, pdftotext.ErrEngineUnavailable) || !strings.Contains(err.Error(), "maestro-cms-no-such-pdfinfo") {
		t.Fatalf("err = %v, want ErrEngineUnavailable naming pdfinfo", err)
	}
}

func TestLayoutPdfinfoCrash(t *testing.T) {
	eng := pdftotext.New(pdftotext.WithPdfinfoBinary(fakeBin(t, "pdfinfo", "kill -SEGV $$")))
	_, err := eng.Layout(context.Background(), []byte("%PDF"))
	if !errors.Is(err, pdf.ErrEngineCrashed) || !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want crashed + malformed", err)
	}
}

// ---- real Poppler -------------------------------------------------------

func needPoppler(t *testing.T) {
	t.Helper()
	for _, b := range []string{"pdftotext", "pdfinfo"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not installed", b)
		}
	}
}

func TestLayoutRealStatement(t *testing.T) {
	needPoppler(t)
	l, err := pdftotext.New().Layout(context.Background(), makeStatementPDF())
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(l.Pages) != 1 || l.Pages[0].Width != 612 || l.Pages[0].Height != 792 {
		t.Fatalf("pages = %+v", l.Pages)
	}
	p := l.Pages[0]
	rows := pdf.AssemblePage(p, pdf.Rows)
	for _, r := range statementRows {
		if !rowRE(r).MatchString(rows.Text) {
			t.Errorf("Rows text lacks row %q / %q on one line:\n%s", r[0], r[1], rows.Text)
		}
	}
	ro := pdf.AssemblePage(p, pdf.ReadingOrder)
	if rowRE(statementRows[0]).MatchString(ro.Text) {
		t.Errorf("ReadingOrder unexpectedly kept the row together:\n%s", ro.Text)
	}
	for _, pt := range []pdf.PageText{rows, ro} {
		for i, s := range pt.Spans {
			if pt.Text[s.Start:s.End] != p.Words[i].Text {
				t.Fatalf("span %d mismatch", i)
			}
		}
	}
}

func TestLayoutRealBlankAndImagePages(t *testing.T) {
	needPoppler(t)
	img := "q 100 0 0 100 72 600 cm BI /W 2 /H 2 /CS /G /BPC 8 ID \x00\x80\x80\x00 EI Q\n"
	data := makePDF("BT /F1 24 Tf 72 720 Td (Text) Tj ET\n", "", img)
	l, err := pdftotext.New().Layout(context.Background(), data)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(l.Pages) != 3 {
		t.Fatalf("got %d pages, want 3", len(l.Pages))
	}
	for i, p := range l.Pages {
		if p.Number != i+1 || p.Width != 612 || p.Height != 792 {
			t.Errorf("page %d = %+v", i+1, p)
		}
	}
	if len(l.Pages[0].Words) != 1 || len(l.Pages[1].Words) != 0 || len(l.Pages[2].Words) != 0 {
		t.Errorf("word counts = %d %d %d", len(l.Pages[0].Words), len(l.Pages[1].Words), len(l.Pages[2].Words))
	}
}

func TestLayoutRealHyphenation(t *testing.T) {
	needPoppler(t)
	data := makePDF("BT /F1 12 Tf 72 700 Td (A line ending in hyphen-) Tj 0 -14 Td (ated words.) Tj ET\n")
	l, err := pdftotext.New().Layout(context.Background(), data)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	got := pdf.AssemblePage(l.Pages[0], pdf.ReadingOrder).Text
	if !strings.Contains(got, "hyphen-\nated") {
		t.Errorf("ReadingOrder = %q, want both halves kept", got)
	}
}

const rotatedText = "BT /F1 24 Tf 100 200 Td (Rotated) Tj ET\n"

// rotationCases: a 612 x 792 MediaBox, with and without a 400 x 292 CropBox
// that contains the text.
var rotationCases = []struct {
	name   string
	extra  string
	w, h   float64
	rot    int
	sideon bool // text runs vertically on the displayed page
}{
	{"rot0", "", 612, 792, 0, false},
	{"rot90", "/Rotate 90", 792, 612, 90, true},
	{"rot270", "/Rotate 270", 792, 612, 270, true},
	{"rot-90", "/Rotate -90", 792, 612, 270, true},
	{"crop", "/CropBox [50 50 450 342]", 400, 292, 0, false},
	{"crop rot90", "/Rotate 90 /CropBox [50 50 450 342]", 292, 400, 90, true},
	{"crop rot270", "/Rotate 270 /CropBox [50 50 450 342]", 292, 400, 270, true},
}

func TestLayoutRealRotation(t *testing.T) {
	needPoppler(t)
	pages := make([]fixturePage, 0, len(rotationCases)+1)
	for _, c := range rotationCases {
		pages = append(pages, fixturePage{content: rotatedText, extra: c.extra})
	}
	pages = append(pages, fixturePage{extra: "/Rotate 90"}) // blank rotated page
	data := makePDFPages(pages...)

	l, err := pdftotext.New().Layout(context.Background(), data)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(l.Pages) != len(pages) {
		t.Fatalf("got %d pages, want %d", len(l.Pages), len(pages))
	}
	for i, c := range rotationCases {
		p := l.Pages[i]
		t.Run(c.name, func(t *testing.T) {
			if p.Width != c.w || p.Height != c.h || p.Rotation != c.rot {
				t.Fatalf("page = %gx%g rot %d, want %gx%g rot %d", p.Width, p.Height, p.Rotation, c.w, c.h, c.rot)
			}
			if len(p.Words) != 1 || p.Words[0].Text != "Rotated" {
				t.Fatalf("words = %+v", p.Words)
			}
			b := p.Words[0].Box
			if b.X0 < 0 || b.Y0 < 0 || b.X1 > p.Width || b.Y1 > p.Height {
				t.Errorf("box %+v outside the %gx%g page", b, p.Width, p.Height)
			}
			if tall := b.Y1-b.Y0 > b.X1-b.X0; tall != c.sideon {
				t.Errorf("box %+v: tall=%v, want %v", b, tall, c.sideon)
			}
			checkInk(t, data, p)
		})
	}
	blank := l.Pages[len(pages)-1]
	if blank.Width != 792 || blank.Height != 612 || blank.Rotation != 90 || len(blank.Words) != 0 {
		t.Errorf("blank rotated page = %+v", blank)
	}
}

// checkInk renders the page at 72 dpi with pdftoppm -cropbox (so one pixel is
// one point) and checks the image is the page's displayed size and that the
// dark pixels lie inside the word's box, within a small tolerance for glyph
// side bearings. It skips without pdftoppm. The renderer of #25 step 3 will
// repeat this through its own sizing.
func checkInk(t *testing.T, data []byte, p pdf.PageLayout) {
	t.Helper()
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return
	}
	n := strconv.Itoa(p.Number)
	cmd := exec.Command("pdftoppm", "-gray", "-r", "72", "-cropbox", "-f", n, "-l", n, "-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pdftoppm: %v", err)
	}
	var w, h, maxv int
	if _, err := fmt.Sscanf(string(out), "P5\n%d %d\n%d\n", &w, &h, &maxv); err != nil {
		t.Fatalf("PGM header: %v", err)
	}
	hdr := len(fmt.Sprintf("P5\n%d %d\n%d\n", w, h, maxv))
	if len(out) != hdr+w*h {
		t.Fatalf("PGM: %d bytes for %dx%d", len(out), w, h)
	}
	if float64(w) != p.Width || float64(h) != p.Height {
		t.Fatalf("image %dx%d, page %gx%g", w, h, p.Width, p.Height)
	}
	pix := out[hdr:]
	x0, y0, x1, y1 := w, h, -1, -1
	for y := range h {
		for x := range w {
			if pix[y*w+x] < 128 {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
	}
	if x1 < 0 {
		t.Fatal("no ink rendered")
	}
	const tol = 3
	b := p.Words[0].Box
	if float64(x0) < b.X0-tol || float64(y0) < b.Y0-tol || float64(x1+1) > b.X1+tol || float64(y1+1) > b.Y1+tol {
		t.Errorf("ink [%d,%d]-[%d,%d] outside word box %+v", x0, y0, x1+1, y1+1, b)
	}
}
