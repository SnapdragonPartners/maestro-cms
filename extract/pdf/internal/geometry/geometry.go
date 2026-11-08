// Package geometry reads page sizes and rotations from Poppler's pdfinfo.
//
// pdfinfo -box reports each page's crop box (unrotated) and its rotation. This
// is the only source of page count and page size for the layout call and the
// renderer, so their coordinates agree by construction: pdftotext -tsv reports
// rotated word boxes but unrotated page sizes, with no rotation field.
package geometry

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/popplerexec"
)

// DefaultBinary is the pdfinfo executable looked up on PATH by default.
const DefaultBinary = "pdfinfo"

// MaxOutputBytes caps pdfinfo's output. It needs about 400 bytes per page, so
// this allows roughly 40,000 pages.
const MaxOutputBytes int64 = 16 << 20

// LastPageAll is passed as last to probe every page; pdfinfo clamps it to the
// document's page count.
const LastPageAll = 1_000_000

// Page is one page's displayed size and declared rotation.
type Page struct {
	Number int
	// Width and Height are the crop box in points, swapped for 90 and 270
	// degree rotations so they describe the page as displayed.
	Width, Height float64
	// Rotation is normalized to 0, 90, 180 or 270.
	Rotation int
}

// Info is the document's page count and the pages that were probed.
type Info struct {
	// Total is the document's page count.
	Total int
	// Pages are the probed pages first..min(last, Total), in order; empty when
	// first > Total.
	Pages []Page
}

// Probe runs pdfinfo on spec.Stdin for pages first..last (1-based). spec
// supplies the binary, PDF, timeout and the caller's sentinel errors; Probe sets
// the name, arguments and output cap. Unparseable output is a malformed source.
func Probe(ctx context.Context, spec popplerexec.Spec, first, last int) (Info, error) {
	spec.Name = "pdfinfo"
	spec.Args = []string{"-f", strconv.Itoa(first), "-l", strconv.Itoa(last), "-box", "-"}
	spec.MaxOutput = MaxOutputBytes
	var out bytes.Buffer
	if err := popplerexec.Run(ctx, spec, &out); err != nil {
		return Info{}, err //nolint:wrapcheck // popplerexec errors are already classified and labeled
	}
	info, err := Parse(out.String(), first, last)
	if err != nil {
		return Info{}, &extract.MalformedSourceError{MediaType: pdf.MediaType, Err: err}
	}
	return info, nil
}

var (
	pagesRE   = regexp.MustCompile(`^Pages:\s+(\d+)\s*$`)
	pageRowRE = regexp.MustCompile(`^Page\s+(\d+)\s+(rot|CropBox):\s+(.*?)\s*$`)
)

type partial struct {
	rot     *int
	w, h    float64
	hasCrop bool
}

// Parse reads pdfinfo -box output for a probe of pages first..last. It is
// strict: a missing, duplicated or unparseable line is an error, not skipped.
// Document metadata (a title, say) is printed verbatim before the page lines
// and could imitate them; such a forgery shows up as a duplicate and fails
// closed.
func Parse(out string, first, last int) (Info, error) {
	total, pages, err := scan(out)
	if err != nil {
		return Info{}, err
	}
	end := min(last, total)
	want := max(end-first+1, 0)
	if len(pages) != want {
		return Info{}, fmt.Errorf("pdfinfo: got %d pages, want %d (pages %d..%d of %d)", len(pages), want, first, end, total)
	}
	info := Info{Total: total, Pages: make([]Page, 0, want)}
	for n := first; n <= end; n++ {
		p := pages[n]
		if p == nil || p.rot == nil || !p.hasCrop {
			return Info{}, fmt.Errorf("pdfinfo: page %d missing size or rotation", n)
		}
		w, h := p.w, p.h
		if *p.rot == 90 || *p.rot == 270 {
			w, h = h, w
		}
		info.Pages = append(info.Pages, Page{Number: n, Width: w, Height: h, Rotation: *p.rot})
	}
	return info, nil
}

// scan collects the page count and every page's rotation and crop box.
func scan(out string) (int, map[int]*partial, error) {
	total := -1
	pages := map[int]*partial{}
	for line := range strings.SplitSeq(out, "\n") {
		if m := pagesRE.FindStringSubmatch(line); m != nil {
			if total >= 0 {
				return 0, nil, fmt.Errorf("pdfinfo: duplicate Pages line")
			}
			n, err := strconv.Atoi(m[1])
			if err != nil {
				return 0, nil, fmt.Errorf("pdfinfo: bad page count %q", m[1])
			}
			total = n
			continue
		}
		if m := pageRowRE.FindStringSubmatch(line); m != nil {
			if err := addPageRow(pages, m[1], m[2], m[3]); err != nil {
				return 0, nil, err
			}
		}
	}
	if total < 0 {
		return 0, nil, fmt.Errorf("pdfinfo: no Pages line")
	}
	return total, pages, nil
}

// addPageRow records one "Page N rot:" or "Page N CropBox:" line.
func addPageRow(pages map[int]*partial, numStr, kind, value string) error {
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return fmt.Errorf("pdfinfo: bad page number %q", numStr)
	}
	p := pages[num]
	if p == nil {
		p = &partial{}
		pages[num] = p
	}
	if kind == "rot" {
		if p.rot != nil {
			return fmt.Errorf("pdfinfo: duplicate rotation for page %d", num)
		}
		r, rerr := parseRotation(value)
		if rerr != nil {
			return fmt.Errorf("pdfinfo: page %d: %w", num, rerr)
		}
		p.rot = &r
		return nil
	}
	if p.hasCrop {
		return fmt.Errorf("pdfinfo: duplicate crop box for page %d", num)
	}
	w, h, err := parseBox(value)
	if err != nil {
		return fmt.Errorf("pdfinfo: page %d: %w", num, err)
	}
	p.w, p.h, p.hasCrop = w, h, true
	return nil
}

// parseRotation normalizes any multiple of 90 (negative or above 360) to 0, 90,
// 180 or 270; anything else is an error.
func parseRotation(s string) (int, error) {
	r, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad rotation %q", s)
	}
	if r%90 != 0 {
		return 0, fmt.Errorf("rotation %d is not a multiple of 90", r)
	}
	return ((r % 360) + 360) % 360, nil
}

// parseBox reads "x0 y0 x1 y1" and returns the box's width and height, which
// must be finite and positive.
func parseBox(s string) (float64, float64, error) {
	f := strings.Fields(s)
	if len(f) != 4 {
		return 0, 0, fmt.Errorf("bad box %q", s)
	}
	var v [4]float64
	for i, x := range f {
		n, err := strconv.ParseFloat(x, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, 0, fmt.Errorf("bad box %q", s)
		}
		v[i] = n
	}
	w, h := math.Abs(v[2]-v[0]), math.Abs(v[3]-v[1])
	if w <= 0 || h <= 0 || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return 0, 0, fmt.Errorf("degenerate box %q", s)
	}
	return w, h, nil
}
