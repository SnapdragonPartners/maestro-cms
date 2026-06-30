package pdftotext

import "testing"

// splitPages is internal, so this test lives in-package. It is the page-splitting
// logic, independent of the pdftotext binary.
func TestSplitPages(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Pagelike
	}{
		{"empty", "", nil},
		{"two pages trailing ff", "alpha\fbravo\f", []Pagelike{{1, "alpha"}, {2, "bravo"}}},
		{"two pages no trailing ff", "alpha\fbravo", []Pagelike{{1, "alpha"}, {2, "bravo"}}},
		{"blank middle page keeps slot", "alpha\f\fcharlie\f", []Pagelike{{1, "alpha"}, {2, ""}, {3, "charlie"}}},
		{"single page", "only\f", []Pagelike{{1, "only"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitPages(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d pages, want %d (%v)", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if got[i].Number != w.num || got[i].Text != w.text {
					t.Fatalf("page %d = {%d,%q}, want {%d,%q}", i, got[i].Number, got[i].Text, w.num, w.text)
				}
			}
		})
	}
}

// Pagelike mirrors pdf.Page's fields for compact test expectations without
// importing the pdf package into this internal test.
type Pagelike struct {
	num  int
	text string
}
