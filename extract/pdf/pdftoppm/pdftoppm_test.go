package pdftoppm

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/testpdf"
)

func TestPixelSize(t *testing.T) {
	cases := []struct {
		name         string
		w, h, dpi    float64
		maxPx        int
		wantW, wantH int
	}{
		{"letter at 72 dpi", 612, 792, 72, 2048, 612, 792},
		{"letter at 150 dpi", 612, 792, 150, 2048, 1275, 1650},
		{"letter at 300 dpi, capped", 612, 792, 300, 2048, 1583, 2048},
		{"landscape capped", 792, 612, 300, 1000, 1000, 773},
		{"square capped exactly", 500, 500, 1000, 100, 100, 100},
		{"tiny dpi keeps a pixel", 612, 792, 0.01, 2048, 1, 1},
		{"sliver keeps a pixel", 1, 10000, 72, 2048, 1, 2048},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := pixelSize(c.w, c.h, c.dpi, c.maxPx)
			if w != c.wantW || h != c.wantH {
				t.Errorf("pixelSize = %dx%d, want %dx%d", w, h, c.wantW, c.wantH)
			}
			if max(w, h) > c.maxPx {
				t.Errorf("longer side %d exceeds %d", max(w, h), c.maxPx)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	o, err := normalize(RenderOptions{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if o.DPI != DefaultDPI || o.MaxPixels != DefaultMaxPixels || o.Quality != DefaultQuality || o.Format != PNG {
		t.Errorf("defaults = %+v", o)
	}
	bad := []struct {
		o    RenderOptions
		want error
	}{
		{RenderOptions{Page: 0}, ErrPageOutOfRange},
		{RenderOptions{Page: -1}, ErrPageOutOfRange},
		{RenderOptions{Page: maxPage + 1}, ErrPageOutOfRange},
		{RenderOptions{Page: 1, Format: 7}, ErrInvalidOptions},
		{RenderOptions{Page: 1, DPI: -1}, ErrInvalidOptions},
		{RenderOptions{Page: 1, DPI: nan()}, ErrInvalidOptions},
		{RenderOptions{Page: 1, MaxPixels: -1}, ErrInvalidOptions},
		{RenderOptions{Page: 1, MaxPixels: MaxPixelsLimit + 1}, ErrInvalidOptions},
		{RenderOptions{Page: 1, Quality: 101}, ErrInvalidOptions},
		{RenderOptions{Page: 1, Quality: -5}, ErrInvalidOptions},
	}
	for _, b := range bad {
		if _, err := normalize(b.o); !errors.Is(err, b.want) {
			t.Errorf("normalize(%+v) = %v, want %v", b.o, err, b.want)
		}
	}
}

func nan() float64 { var z float64; return z / z }

func TestRenderArgs(t *testing.T) {
	got := renderArgs(RenderOptions{Page: 3, Format: JPEG, Quality: 70}, 100, 200)
	want := []string{"-f", "3", "-l", "3", "-cropbox", "-scale-to-x", "100", "-scale-to-y", "200",
		"-scale-dimension-before-rotation", "-jpeg", "-jpegopt", "quality=70", "-"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q", got)
	}
	if got := renderArgs(RenderOptions{Page: 1, Format: PNG}, 1, 1); !slices.Contains(got, "-png") || slices.Contains(got, "-jpegopt") {
		t.Errorf("png args = %q", got)
	}
}

func TestFormat(t *testing.T) {
	if PNG.String() != "png" || JPEG.String() != "jpeg" || Format(9).String() != "Format(9)" {
		t.Error("String")
	}
	if PNG.MediaType() != "image/png" || JPEG.MediaType() != "image/jpeg" || Format(9).MediaType() != "" {
		t.Error("MediaType")
	}
}

// ---- fake binaries ------------------------------------------------------

func fakeBin(t *testing.T, name, script string) string { return testpdf.FakeBin(t, name, script) }

func fakeInfo(t *testing.T, n int) string { return testpdf.FakeInfo(t, n) }

// pngFile writes a w x h PNG and returns its path.
func pngFile(t *testing.T, w, h int) string {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "out.png")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeRenderer(t *testing.T, pages int, script string, opts ...Option) *Renderer {
	opts = append([]Option{
		WithPdfinfoBinary(fakeInfo(t, pages)),
		WithBinary(fakeBin(t, "pdftoppm", script)),
		WithTimeout(5 * time.Second),
	}, opts...)
	return New(opts...)
}

func TestRenderFakeSuccess(t *testing.T) {
	// Letter at 150 dpi is 1275 x 1650; one pixel of rounding is tolerated.
	r := fakeRenderer(t, 2, "cat "+pngFile(t, 1276, 1649))
	img, err := r.Render(context.Background(), []byte("%PDF"), RenderOptions{Page: 2})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if img.Width != 1276 || img.Height != 1649 || img.PageWidth != 612 || img.PageHeight != 792 || img.Format != PNG {
		t.Errorf("image = %+v", img)
	}
	if img.ScaleX != 1276.0/612 || img.ScaleY != 1649.0/792 {
		t.Errorf("scale = %g, %g", img.ScaleX, img.ScaleY)
	}
}

func TestRenderFakeErrors(t *testing.T) {
	cases := []struct {
		name      string
		pages     int
		script    func(t *testing.T) string
		o         RenderOptions
		opts      []Option
		want      error
		malformed bool
	}{
		{name: "page past end", pages: 2, o: RenderOptions{Page: 3}, want: ErrPageOutOfRange},
		{name: "wrong size", pages: 1, script: func(t *testing.T) string { return "cat " + pngFile(t, 1275, 1600) }, malformed: true},
		// Within the rounding tolerance of 1275x1650, but above MaxPixels.
		{name: "above MaxPixels", pages: 1, script: func(t *testing.T) string { return "cat " + pngFile(t, 1276, 1651) }, o: RenderOptions{MaxPixels: 1650}, malformed: true},
		{name: "wrong format", pages: 1, script: func(t *testing.T) string { return "cat " + pngFile(t, 1275, 1650) }, o: RenderOptions{Format: JPEG}, malformed: true},
		{name: "not an image", script: func(*testing.T) string { return "echo hello" }, malformed: true},
		{name: "empty output", script: func(*testing.T) string { return "true" }, malformed: true},
		{name: "exit 1", script: func(*testing.T) string { return "exit 1" }, malformed: true},
		{name: "crash", script: func(*testing.T) string { return "kill -ABRT $$" }, want: pdf.ErrEngineCrashed, malformed: true},
		{name: "flood", script: func(*testing.T) string { return "exec yes" }, opts: []Option{WithMaxOutputBytes(64 << 10)}, want: ErrOutputTooLarge},
		{name: "timeout", script: func(*testing.T) string { return "exec sleep 10" }, opts: []Option{WithTimeout(200 * time.Millisecond)}, malformed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pages, script := max(tc.pages, 1), "exit 3"
			if tc.script != nil {
				script = tc.script(t)
			}
			if tc.o.Page == 0 {
				tc.o.Page = 1
			}
			start := time.Now()
			_, err := fakeRenderer(t, pages, script, tc.opts...).Render(context.Background(), []byte("%PDF"), tc.o)
			if err == nil {
				t.Fatal("Render succeeded")
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

// TestRenderPdfinfoErrors drives failures through the pdfinfo probe.
func TestRenderPdfinfoErrors(t *testing.T) {
	cases := []struct {
		name      string
		script    string
		want      error
		malformed bool
	}{
		{name: "exit 1", script: "exit 1", malformed: true},
		{name: "crash", script: "kill -ABRT $$", want: pdf.ErrEngineCrashed, malformed: true},
		{name: "no Pages line", script: "echo garbage", malformed: true},
		{name: "duplicate page", script: `printf 'Pages: 1\nPage 1 rot: 0\nPage 1 CropBox: 0 0 612 792\nPage 1 rot: 0\n'`, malformed: true},
		{name: "missing page", script: `printf 'Pages: 1\n'`, malformed: true},
		{name: "flood", script: "exec yes 'Page 1 rot: 0'", want: ErrOutputTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(WithPdfinfoBinary(fakeBin(t, "pdfinfo", tc.script)),
				WithBinary(fakeBin(t, "pdftoppm", "exit 3")), WithTimeout(10*time.Second))
			_, err := r.Render(context.Background(), []byte("%PDF"), RenderOptions{Page: 1})
			if err == nil {
				t.Fatal("Render succeeded")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if got := errors.Is(err, extract.ErrMalformedSource); got != tc.malformed {
				t.Errorf("malformed = %v, want %v (err %v)", got, tc.malformed, err)
			}
			if !strings.Contains(err.Error(), "pdfinfo") {
				t.Errorf("err = %v, want it to name pdfinfo", err)
			}
		})
	}
}

func TestRenderMissingBinaries(t *testing.T) {
	_, err := New(WithPdfinfoBinary("maestro-cms-no-such-pdfinfo")).Render(context.Background(), []byte("%PDF"), RenderOptions{Page: 1})
	if !errors.Is(err, ErrEngineUnavailable) || !strings.Contains(err.Error(), "maestro-cms-no-such-pdfinfo") {
		t.Errorf("err = %v, want ErrEngineUnavailable naming pdfinfo", err)
	}
	_, err = New(WithPdfinfoBinary(fakeInfo(t, 1)), WithBinary("maestro-cms-no-such-pdftoppm")).
		Render(context.Background(), []byte("%PDF"), RenderOptions{Page: 1})
	if !errors.Is(err, ErrEngineUnavailable) || !strings.Contains(err.Error(), "maestro-cms-no-such-pdftoppm") {
		t.Errorf("err = %v, want ErrEngineUnavailable naming pdftoppm", err)
	}
}

func TestRenderCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fakeRenderer(t, 1, "exec sleep 10").Render(ctx, []byte("%PDF"), RenderOptions{Page: 1})
	if !errors.Is(err, context.Canceled) || errors.Is(err, extract.ErrMalformedSource) {
		t.Errorf("err = %v, want context.Canceled and not malformed", err)
	}
}
