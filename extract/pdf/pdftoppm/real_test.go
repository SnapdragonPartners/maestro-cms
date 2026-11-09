package pdftoppm_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"os/exec"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/testpdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/pdftoppm"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/pdftotext"
)

func needPoppler(t *testing.T) {
	t.Helper()
	for _, b := range []string{"pdftoppm", "pdftotext", "pdfinfo"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not installed", b)
		}
	}
}

// TestRenderRealRotation renders rotated and cropped pages through Render, at a
// DPI and capped by MaxPixels, in both formats, and checks that the image is
// the Layout page size times the reported scale and that the word's box,
// scaled, contains its ink.
func TestRenderRealRotation(t *testing.T) {
	needPoppler(t)
	data := testpdf.Build(testpdf.RotationPages()...)
	l, err := pdftotext.New().Layout(context.Background(), data)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	r := pdftoppm.New()
	opts := []struct {
		name string
		o    pdftoppm.RenderOptions
	}{
		{"72dpi png", pdftoppm.RenderOptions{DPI: 72}},
		{"180dpi png", pdftoppm.RenderOptions{DPI: 180}},
		{"capped png", pdftoppm.RenderOptions{DPI: 300, MaxPixels: 500}},
		{"capped jpeg", pdftoppm.RenderOptions{DPI: 300, MaxPixels: 700, Format: pdftoppm.JPEG}},
	}
	for i, c := range testpdf.Rotations() {
		p := l.Pages[i]
		for _, oc := range opts {
			t.Run(c.Name+"/"+oc.name, func(t *testing.T) {
				o := oc.o
				o.Page = p.Number
				img, err := r.Render(context.Background(), data, o)
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				if img.PageWidth != p.Width || img.PageHeight != p.Height {
					t.Fatalf("page %gx%g, layout %gx%g", img.PageWidth, img.PageHeight, p.Width, p.Height)
				}
				if o.MaxPixels > 0 && max(img.Width, img.Height) != o.MaxPixels {
					t.Errorf("image %dx%d, want longer side %d", img.Width, img.Height, o.MaxPixels)
				}
				if o.MaxPixels == 0 {
					want := o.DPI / 72
					if d := img.ScaleX - want; d > 0.01 || d < -0.01 {
						t.Errorf("ScaleX = %g, want %g", img.ScaleX, want)
					}
				}
				checkInk(t, img, p.Words[0].Box)
			})
		}
	}
}

// checkInk decodes img and checks its dark pixels lie inside box scaled to
// pixels, within a small tolerance for glyph side bearings and JPEG ringing.
func checkInk(t *testing.T, img *pdftoppm.Image, box pdf.Rect) {
	t.Helper()
	m, name, err := image.Decode(bytes.NewReader(img.Data))
	if err != nil || name != img.Format.String() {
		t.Fatalf("decode: %v (%s)", err, name)
	}
	if b := m.Bounds(); b.Dx() != img.Width || b.Dy() != img.Height {
		t.Fatalf("decoded %v, reported %dx%d", b, img.Width, img.Height)
	}
	x0, y0, x1, y1 := img.Width, img.Height, -1, -1
	for y := range img.Height {
		for x := range img.Width {
			if gray(m, x, y) < 128 {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
	}
	if x1 < 0 {
		t.Fatal("no ink rendered")
	}
	tol := 3 * max(img.ScaleX, 1)
	bx0, by0 := box.X0*img.ScaleX, box.Y0*img.ScaleY
	bx1, by1 := box.X1*img.ScaleX, box.Y1*img.ScaleY
	if float64(x0) < bx0-tol || float64(y0) < by0-tol || float64(x1+1) > bx1+tol || float64(y1+1) > by1+tol {
		t.Errorf("ink [%d,%d]-[%d,%d] outside scaled word box [%.1f,%.1f]-[%.1f,%.1f]", x0, y0, x1+1, y1+1, bx0, by0, bx1, by1)
	}
}

// TestRenderRealPages covers a blank rotated page, an image-only page and a page
// past the end.
func TestRenderRealPages(t *testing.T) {
	needPoppler(t)
	img := "q 100 0 0 100 72 600 cm BI /W 2 /H 2 /CS /G /BPC 8 ID \x00\x80\x80\x00 EI Q\n"
	data := testpdf.Build(testpdf.Page{Extra: "/Rotate 90"}, testpdf.Page{Content: img})
	r := pdftoppm.New()

	blank, err := r.Render(context.Background(), data, pdftoppm.RenderOptions{Page: 1, DPI: 72})
	if err != nil {
		t.Fatalf("blank: %v", err)
	}
	if blank.Width != 792 || blank.Height != 612 {
		t.Errorf("blank rotated page %dx%d, want 792x612", blank.Width, blank.Height)
	}

	pic, err := r.Render(context.Background(), data, pdftoppm.RenderOptions{Page: 2, DPI: 72})
	if err != nil {
		t.Fatalf("image page: %v", err)
	}
	m, _, err := image.Decode(bytes.NewReader(pic.Data))
	if err != nil {
		t.Fatal(err)
	}
	// The 2x2 image fills (72,92)-(172,192) in top-left coordinates; its
	// top-left sample is black.
	if g := gray(m, 80, 100); g > 64 {
		t.Errorf("image pixel = %d, want dark", g)
	}

	_, err = r.Render(context.Background(), data, pdftoppm.RenderOptions{Page: 3})
	if !errors.Is(err, pdftoppm.ErrPageOutOfRange) {
		t.Errorf("page 3: err = %v, want ErrPageOutOfRange", err)
	}
}

// TestRenderRealNonEmbeddedFont checks the fixtures' non-embedded Helvetica
// renders with visible text, i.e. that fonts and fontconfig are installed. A
// consumer's image should run the same check (see the deployment notes).
func TestRenderRealNonEmbeddedFont(t *testing.T) {
	needPoppler(t)
	data := testpdf.Build(testpdf.Page{Content: "BT /F1 36 Tf 72 700 Td (Helvetica) Tj ET\n"})
	l, err := pdftotext.New().Layout(context.Background(), data)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	img, err := pdftoppm.New().Render(context.Background(), data, pdftoppm.RenderOptions{Page: 1})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	checkInk(t, img, l.Pages[0].Words[0].Box)
}

// gray returns the pixel's luminance, 0 (black) to 255.
func gray(m image.Image, x, y int) uint8 {
	g, _ := color.GrayModel.Convert(m.At(x, y)).(color.Gray)
	return g.Y
}
