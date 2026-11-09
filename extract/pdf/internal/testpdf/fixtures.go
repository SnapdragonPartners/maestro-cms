package testpdf

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// RotatedText is a content stream with one word, "Rotated", inside both the
// MediaBox and the CropBox used by Rotations.
const RotatedText = "BT /F1 24 Tf 100 200 Td (Rotated) Tj ET\n"

// Rotation is one rotated and/or cropped page of the 612 x 792 MediaBox and the
// page Poppler should report for it.
type Rotation struct {
	Name  string
	Extra string // page dictionary entries
	// W, H and Rot are the displayed page size and normalized rotation.
	W, H float64
	Rot  int
	// SideOn is true when the text runs vertically on the displayed page.
	SideOn bool
}

// Rotations returns cases covering every rotation, a negative rotation, and a 400 x 292 CropBox
// that contains RotatedText, with and without rotation. Both engines' real
// tests run this one matrix, so layout and rendering are checked on the same
// pages.
func Rotations() []Rotation {
	return []Rotation{
		{"rot0", "", 612, 792, 0, false},
		{"rot90", "/Rotate 90", 792, 612, 90, true},
		{"rot180", "/Rotate 180", 612, 792, 180, false},
		{"rot270", "/Rotate 270", 792, 612, 270, true},
		{"rot-90", "/Rotate -90", 792, 612, 270, true},
		{"crop", "/CropBox [50 50 450 342]", 400, 292, 0, false},
		{"crop rot90", "/Rotate 90 /CropBox [50 50 450 342]", 292, 400, 90, true},
		{"crop rot270", "/Rotate 270 /CropBox [50 50 450 342]", 292, 400, 270, true},
	}
}

// RotationPages returns one RotatedText page per entry of Rotations, in order.
func RotationPages() []Page {
	rs := Rotations()
	pages := make([]Page, len(rs))
	for i := range rs {
		pages[i] = Page{Content: RotatedText, Extra: rs[i].Extra}
	}
	return pages
}

// FakeBin writes an executable shell script that drains stdin and then runs
// script, and returns its path. It skips the test on Windows.
func FakeBin(t *testing.T, name, script string) string {
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

// FakeInfo returns a fake pdfinfo reporting n unrotated letter-size pages.
func FakeInfo(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "Pages:           %d\n", n)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "Page %4d rot:   0\nPage %4d CropBox: 0 0 612 792\n", i, i)
	}
	return FakeBin(t, "pdfinfo", "printf '%s' '"+b.String()+"'")
}
