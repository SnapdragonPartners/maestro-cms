// Package pdftoppm renders PDF pages to images by shelling out to Poppler's
// pdftoppm.
//
// Like the pdftotext engine, it runs the tool as a child process (PDF on stdin,
// image on stdout) under a timeout, an output cap and kill on cancellation, so
// a crafted PDF that hangs or crashes the renderer dies in a process the
// renderer kills. It adds no Go dependency.
//
// Every Render call first reads the page's size and rotation with Poppler's
// pdfinfo (through the same helper pdftotext's Layout uses), so an image's
// pixels and a Layout's point coordinates agree by construction: multiply a
// word box by Image.ScaleX and Image.ScaleY to get pixels. Both pdftoppm and
// pdfinfo ship in poppler-utils and must be present at runtime. Rendering also
// needs fonts and a fontconfig configuration (and poppler-data for CJK), or
// pages whose fonts aren't embedded render with missing text; see the
// deployment notes in docs/pdf-layout-and-rendering.md.
//
// Licensing: Poppler is GPL. Invoking the installed binaries as subprocesses is
// a process boundary, so this module stays MIT, but an image that bundles
// Poppler carries Poppler's obligations for that artifact.
package pdftoppm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder for image.DecodeConfig
	_ "image/png"  // register the PNG decoder for image.DecodeConfig
	"math"
	"strconv"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/geometry"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/popplerexec"
)

// DefaultBinary is the pdftoppm executable looked up on PATH by default.
const DefaultBinary = "pdftoppm"

// DefaultPdfinfoBinary is the pdfinfo executable looked up on PATH by default.
const DefaultPdfinfoBinary = geometry.DefaultBinary

// DefaultTimeout bounds each subprocess of a Render call. A 300 dpi PNG of a
// scanned letter page takes about 5 s.
const DefaultTimeout = 30 * time.Second

// DefaultMaxOutputBytes caps the encoded image pdftoppm may write.
const DefaultMaxOutputBytes int64 = 64 << 20

// Defaults for RenderOptions fields left zero.
const (
	DefaultDPI       = 150.0
	DefaultMaxPixels = 2048
	DefaultQuality   = 85
)

// MaxPixelsLimit is the largest MaxPixels accepted. pdftoppm holds the whole
// uncompressed bitmap in memory (about 800 MB at this size, in color), so the
// limit bounds the child's memory as the output cap bounds this process's.
const MaxPixelsLimit = 16384

// pdfinfo is asked for pages 1..N to learn the page count along with page N's
// size (pdfinfo rejects a first page past the end as a usage error, which would
// be indistinguishable from a malformed PDF). It writes about 400 bytes per
// page under geometry.MaxOutputBytes, so pages past this are refused up front.
const maxPage = 40_000

// ErrEngineUnavailable indicates pdftoppm or pdfinfo was not found on PATH; the
// message names the missing binary.
var ErrEngineUnavailable = errors.New("pdftoppm: binary not found on PATH")

// ErrOutputTooLarge indicates the encoded image exceeded the output cap; the
// process is killed and this is returned. It is a resource limit, distinct from
// a malformed PDF.
var ErrOutputTooLarge = errors.New("pdftoppm: output exceeds size limit")

// ErrPageOutOfRange indicates RenderOptions.Page is below 1 or past the
// document's last page.
var ErrPageOutOfRange = errors.New("pdftoppm: page out of range")

// ErrInvalidOptions indicates a RenderOptions field is out of its allowed
// range; the message names the field.
var ErrInvalidOptions = errors.New("pdftoppm: invalid render options")

// Format is an image encoding.
type Format int

const (
	// PNG is lossless and the better choice for born-digital pages (sharp text).
	PNG Format = iota
	// JPEG is several times faster to produce and about 10x smaller, and the
	// better choice for scanned pages, which are photographs already.
	JPEG
)

// String returns "png" or "jpeg".
func (f Format) String() string {
	switch f {
	case PNG:
		return "png"
	case JPEG:
		return "jpeg"
	default:
		return "Format(" + strconv.Itoa(int(f)) + ")"
	}
}

// MediaType returns the format's MIME type, "image/png" or "image/jpeg", or ""
// for an unknown format.
func (f Format) MediaType() string {
	switch f {
	case PNG:
		return "image/png"
	case JPEG:
		return "image/jpeg"
	default:
		return ""
	}
}

// RenderOptions selects the page and the image's size and encoding.
type RenderOptions struct {
	// Page is the 1-based page number.
	Page int
	// Format is the encoding (PNG by default).
	Format Format
	// DPI is the target resolution; 0 means DefaultDPI. MaxPixels wins when the
	// two disagree, so read the applied scale from Image.
	DPI float64
	// MaxPixels limits the image's longer side; 0 means DefaultMaxPixels. At
	// most MaxPixelsLimit.
	MaxPixels int
	// Quality is the JPEG quality, 1 to 100; 0 means DefaultQuality. Ignored for
	// PNG.
	Quality int
}

// Image is one rendered page.
type Image struct {
	// Data is the encoded image.
	Data []byte
	// Format is Data's encoding.
	Format Format
	// Width and Height are the image's size in pixels, read from its header.
	Width, Height int
	// PageWidth and PageHeight are the page's crop box in points as displayed
	// (after rotation), the same values pdf.PageLayout reports.
	PageWidth, PageHeight float64
	// ScaleX and ScaleY are the pixels per point actually applied, Width /
	// PageWidth and Height / PageHeight. They differ from DPI/72 when MaxPixels
	// capped the size, and from each other slightly by rounding.
	ScaleX, ScaleY float64
}

// Renderer renders PDF pages with pdftoppm. It is safe for concurrent use.
type Renderer struct {
	binary     string
	infoBinary string
	timeout    time.Duration
	maxOutput  int64
}

// Option configures a Renderer.
type Option func(*Renderer)

// WithBinary overrides the pdftoppm executable name/path (default DefaultBinary).
func WithBinary(path string) Option { return func(r *Renderer) { r.binary = path } }

// WithPdfinfoBinary overrides the pdfinfo executable name/path (default
// DefaultPdfinfoBinary).
func WithPdfinfoBinary(path string) Option { return func(r *Renderer) { r.infoBinary = path } }

// WithTimeout overrides the per-subprocess timeout (default DefaultTimeout).
// Non-positive means the default.
func WithTimeout(d time.Duration) Option { return func(r *Renderer) { r.timeout = d } }

// WithMaxOutputBytes overrides the encoded-image cap (default
// DefaultMaxOutputBytes). Non-positive means the default.
func WithMaxOutputBytes(n int64) Option { return func(r *Renderer) { r.maxOutput = n } }

// New returns a Renderer.
func New(opts ...Option) *Renderer {
	r := &Renderer{}
	for _, o := range opts {
		o(r)
	}
	return r
}

func orDefault[T int64 | time.Duration | string](v, def T) T {
	var zero T
	if v <= zero {
		return def
	}
	return v
}

// spec returns the shared subprocess settings for one call on data.
func (r *Renderer) spec(binary string, data []byte) popplerexec.Spec {
	return popplerexec.Spec{
		Name:           "pdftoppm",
		Binary:         binary,
		Stdin:          data,
		Timeout:        orDefault(r.timeout, DefaultTimeout),
		ErrUnavailable: ErrEngineUnavailable,
		ErrTooLarge:    ErrOutputTooLarge,
	}
}

// Render renders one page of data. It runs two subprocesses, each under the
// renderer's timeout and the caller's ctx: pdfinfo for the page's size and
// rotation, then pdftoppm with that size scaled to an exact pixel size, the
// crop box and the requested encoding.
//
// The scale is min(DPI/72, MaxPixels / longer side in points). The image's
// header must report the requested pixel size (within a pixel of rounding) or
// the render fails as malformed output.
//
// Errors: ErrInvalidOptions for options out of range, ErrPageOutOfRange,
// ErrEngineUnavailable naming the missing binary, ErrOutputTooLarge past the
// cap, a context error on cancellation, and extract.ErrMalformedSource for a
// rejected PDF, a crash (also pdf.ErrEngineCrashed), a timeout, or output that
// isn't the requested image.
func (r *Renderer) Render(ctx context.Context, data []byte, o RenderOptions) (*Image, error) {
	o, err := normalize(o)
	if err != nil {
		return nil, err
	}
	info, err := geometry.Probe(ctx, r.spec(orDefault(r.infoBinary, DefaultPdfinfoBinary), data), 1, o.Page)
	if err != nil {
		return nil, err //nolint:wrapcheck // classified sentinel/malformed/ctx errors
	}
	if o.Page > info.Total {
		return nil, fmt.Errorf("%w: page %d of %d", ErrPageOutOfRange, o.Page, info.Total)
	}
	pg := info.Pages[o.Page-1]
	w, h := pixelSize(pg.Width, pg.Height, o.DPI, o.MaxPixels)

	s := r.spec(orDefault(r.binary, DefaultBinary), data)
	s.Args = renderArgs(o, w, h)
	s.MaxOutput = orDefault(r.maxOutput, DefaultMaxOutputBytes)
	var out bytes.Buffer
	if runErr := popplerexec.Run(ctx, s, &out); runErr != nil {
		return nil, runErr //nolint:wrapcheck // classified sentinel/malformed/ctx errors
	}
	gotW, gotH, err := checkHeader(out.Bytes(), o.Format, w, h)
	if err != nil {
		return nil, &extract.MalformedSourceError{MediaType: pdf.MediaType, Err: err}
	}
	return &Image{
		Data:       out.Bytes(),
		Format:     o.Format,
		Width:      gotW,
		Height:     gotH,
		PageWidth:  pg.Width,
		PageHeight: pg.Height,
		ScaleX:     float64(gotW) / pg.Width,
		ScaleY:     float64(gotH) / pg.Height,
	}, nil
}

// normalize validates o and fills in defaults.
func normalize(o RenderOptions) (RenderOptions, error) {
	if o.Page < 1 || o.Page > maxPage {
		return o, fmt.Errorf("%w: page %d (supported: 1..%d)", ErrPageOutOfRange, o.Page, maxPage)
	}
	if o.Format != PNG && o.Format != JPEG {
		return o, fmt.Errorf("%w: unknown format %d", ErrInvalidOptions, int(o.Format))
	}
	if math.IsNaN(o.DPI) || math.IsInf(o.DPI, 0) || o.DPI < 0 {
		return o, fmt.Errorf("%w: DPI %v", ErrInvalidOptions, o.DPI)
	}
	if o.MaxPixels < 0 || o.MaxPixels > MaxPixelsLimit {
		return o, fmt.Errorf("%w: MaxPixels %d (allowed 1..%d, or 0 for the default)", ErrInvalidOptions, o.MaxPixels, MaxPixelsLimit)
	}
	if o.Quality < 0 || o.Quality > 100 {
		return o, fmt.Errorf("%w: Quality %d (allowed 1..100, or 0 for the default)", ErrInvalidOptions, o.Quality)
	}
	if o.DPI == 0 {
		o.DPI = DefaultDPI
	}
	if o.MaxPixels == 0 {
		o.MaxPixels = DefaultMaxPixels
	}
	if o.Quality == 0 {
		o.Quality = DefaultQuality
	}
	return o, nil
}

// pixelSize returns the image size for a page of w x h points (as displayed):
// scale = min(dpi/72, maxPixels / longer side), each side rounded and at least
// one pixel, and the longer side never above maxPixels.
func pixelSize(w, h, dpi float64, maxPixels int) (int, int) {
	scale := min(dpi/72, float64(maxPixels)/max(w, h))
	px := func(v float64) int { return min(max(int(math.Round(v*scale)), 1), maxPixels) }
	return px(w), px(h)
}

// renderArgs: page n only, crop box, exact pixel size (given as displayed;
// -scale-dimension-before-rotation makes Poppler apply it to the rotated page,
// which it otherwise doesn't), PDF on stdin, image on stdout.
func renderArgs(o RenderOptions, w, h int) []string {
	n := strconv.Itoa(o.Page)
	args := []string{
		"-f", n, "-l", n, "-cropbox",
		"-scale-to-x", strconv.Itoa(w), "-scale-to-y", strconv.Itoa(h),
		"-scale-dimension-before-rotation",
	}
	if o.Format == JPEG {
		args = append(args, "-jpeg", "-jpegopt", "quality="+strconv.Itoa(o.Quality))
	} else {
		args = append(args, "-png")
	}
	return append(args, "-q", "-")
}

// checkHeader decodes the image header and checks it is the requested format
// and, within a pixel of rounding, the requested size.
func checkHeader(b []byte, f Format, wantW, wantH int) (int, int, error) {
	cfg, name, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0, fmt.Errorf("pdftoppm output is not an image: %w", err)
	}
	if name != f.String() {
		return 0, 0, fmt.Errorf("pdftoppm wrote %s, want %s", name, f)
	}
	if absInt(cfg.Width-wantW) > 1 || absInt(cfg.Height-wantH) > 1 {
		return 0, 0, fmt.Errorf("pdftoppm wrote %dx%d, want %dx%d", cfg.Width, cfg.Height, wantW, wantH)
	}
	return cfg.Width, cfg.Height, nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
