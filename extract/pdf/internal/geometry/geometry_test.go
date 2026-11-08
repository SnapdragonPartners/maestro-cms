package geometry

import (
	"fmt"
	"strings"
	"testing"
)

// recorded is pdfinfo -f 1 -l 1000000 -box output (Poppler 26.04.0) for three
// 612 x 792 pages, the third with /Rotate 90 and a 400 x 292 CropBox.
const recorded = `Custom Metadata: no
Metadata Stream: no
Tagged:          no
UserProperties:  no
Suspects:        no
Form:            none
JavaScript:      no
Pages:           3
Encrypted:       no
Page    1 size:  612 x 792 pts (letter)
Page    1 rot:   0
Page    2 size:  612 x 792 pts (letter)
Page    2 rot:   0
Page    3 size:  400 x 292 pts
Page    3 rot:   90
Page    1 MediaBox:      0.00     0.00   612.00   792.00
Page    1 CropBox:       0.00     0.00   612.00   792.00
Page    1 BleedBox:      0.00     0.00   612.00   792.00
Page    2 MediaBox:      0.00     0.00   612.00   792.00
Page    2 CropBox:       0.00     0.00   612.00   792.00
Page    2 BleedBox:      0.00     0.00   612.00   792.00
Page    3 MediaBox:      0.00     0.00   612.00   792.00
Page    3 CropBox:      50.00    50.00   450.00   342.00
Page    3 BleedBox:     50.00    50.00   450.00   342.00
File size:       0 bytes
Optimized:       no
PDF version:     1.4
`

func TestParseRecorded(t *testing.T) {
	info, err := Parse(recorded, 1, LastPageAll)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Page{
		{Number: 1, Width: 612, Height: 792, Rotation: 0},
		{Number: 2, Width: 612, Height: 792, Rotation: 0},
		{Number: 3, Width: 292, Height: 400, Rotation: 90}, // crop box, swapped
	}
	if info.Total != 3 || len(info.Pages) != 3 {
		t.Fatalf("info = %+v", info)
	}
	for i, w := range want {
		if info.Pages[i] != w {
			t.Errorf("page %d = %+v, want %+v", i+1, info.Pages[i], w)
		}
	}
}

// onePage builds pdfinfo output for a single probed page n of total.
func onePage(total, n int, rot string, box string) string {
	return fmt.Sprintf("Pages:           %d\nPage %4d rot:   %s\nPage %4d CropBox: %s\n", total, n, rot, n, box)
}

func TestParseRotationNormalized(t *testing.T) {
	cases := map[string]int{"0": 0, "90": 90, "-90": 270, "450": 90, "-180": 180, "720": 0, "270": 270}
	for in, want := range cases {
		info, err := Parse(onePage(1, 1, in, "0 0 100 200"), 1, 1)
		if err != nil {
			t.Fatalf("rot %s: %v", in, err)
		}
		p := info.Pages[0]
		if p.Rotation != want {
			t.Errorf("rot %s → %d, want %d", in, p.Rotation, want)
		}
		swapped := want == 90 || want == 270
		if (p.Width == 200) != swapped {
			t.Errorf("rot %s: size %gx%g, swapped=%v", in, p.Width, p.Height, swapped)
		}
	}
}

func TestParseSubsetAndOutOfRange(t *testing.T) {
	info, err := Parse(onePage(5, 4, "0", "0 0 10 20"), 4, 4)
	if err != nil || info.Total != 5 || len(info.Pages) != 1 || info.Pages[0].Number != 4 {
		t.Fatalf("subset: %+v, %v", info, err)
	}
	info, err = Parse("Pages:           5\n", 9, 9)
	if err != nil || info.Total != 5 || len(info.Pages) != 0 {
		t.Fatalf("out of range: %+v, %v", info, err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"no pages line":      "Page    1 rot:   0\nPage    1 CropBox: 0 0 1 1\n",
		"missing page":       "Pages: 2\n" + strings.Join(strings.Split(onePage(2, 1, "0", "0 0 1 1"), "\n")[1:], "\n"),
		"missing rotation":   "Pages: 1\nPage    1 CropBox: 0 0 1 1\n",
		"missing crop box":   "Pages: 1\nPage    1 rot:   0\n",
		"odd rotation":       onePage(1, 1, "45", "0 0 1 1"),
		"bad rotation":       onePage(1, 1, "x", "0 0 1 1"),
		"degenerate box":     onePage(1, 1, "0", "5 5 5 9"),
		"short box":          onePage(1, 1, "0", "0 0 1"),
		"nan box":            onePage(1, 1, "0", "0 0 NaN 1"),
		"inf box":            onePage(1, 1, "0", "0 0 1e400 1"),
		"forged title":       "Title:          x\nPage    1 rot:   0\n" + onePage(1, 1, "0", "0 0 1 1"),
		"duplicate pages":    "Pages: 1\n" + onePage(1, 1, "0", "0 0 1 1"),
		"page outside probe": onePage(3, 1, "0", "0 0 1 1") + "Page    3 rot:   0\n",
		"duplicate crop box": onePage(1, 1, "0", "0 0 1 1") + "Page    1 CropBox: 0 0 1 1\n",
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			first, last := 1, LastPageAll
			if name == "page outside probe" {
				last = 1
			}
			if info, err := Parse(out, first, last); err == nil {
				t.Fatalf("Parse accepted: %+v", info)
			}
		})
	}
}
