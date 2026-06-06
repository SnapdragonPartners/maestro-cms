package chunk

import (
	"strings"
	"testing"
)

// sections returns the text of each unit Headings produces, for readable
// assertions.
func sections(text string) []string {
	units := Headings(text)
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = text[u.Start:u.End]
	}
	return out
}

func TestHeadingsEmpty(t *testing.T) {
	if got := Headings(""); got != nil {
		t.Fatalf("Headings(\"\") = %v, want nil", got)
	}
}

func TestHeadingsATXSections(t *testing.T) {
	text := "# A\nalpha\n\n## B\nbeta\n\n# C\ngamma\n"
	got := sections(text)
	want := []string{"# A\nalpha\n\n", "## B\nbeta\n\n", "# C\ngamma\n"}
	if len(got) != len(want) {
		t.Fatalf("got %d sections %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("section %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHeadingsPreambleIsOwnUnit(t *testing.T) {
	text := "intro paragraph\nstill intro\n\n# First\nbody\n"
	got := sections(text)
	if len(got) != 2 {
		t.Fatalf("got %d sections %q, want 2", len(got), got)
	}
	if got[0] != "intro paragraph\nstill intro\n\n" {
		t.Fatalf("preamble = %q", got[0])
	}
	if !strings.HasPrefix(got[1], "# First") {
		t.Fatalf("section = %q", got[1])
	}
}

func TestHeadingsSetext(t *testing.T) {
	// "Title" underlined by "=" is a heading; the section starts at "Title".
	text := "preamble\n\nTitle\n=====\nbody text\n\n## Sub\nmore\n"
	got := sections(text)
	if len(got) != 3 {
		t.Fatalf("got %d sections %q, want 3", len(got), got)
	}
	if got[0] != "preamble\n\n" {
		t.Fatalf("preamble = %q", got[0])
	}
	if got[1] != "Title\n=====\nbody text\n\n" {
		t.Fatalf("setext section = %q", got[1])
	}
}

func TestHeadingsFenceAware(t *testing.T) {
	// The "#" lines inside the fence are code, not headings: one section only.
	text := "# Real\n\n```sh\n# not a heading\n## also not\n```\n\ntail\n"
	got := sections(text)
	if len(got) != 1 {
		t.Fatalf("got %d sections %q, want 1 (only the real heading)", len(got), got)
	}
}

func TestHeadingsTildeFenceAware(t *testing.T) {
	text := "# Real\n\n~~~\n# not a heading\n~~~\n"
	if got := Headings(text); len(got) != 1 {
		t.Fatalf("got %d units, want 1", len(got))
	}
}

func TestHeadingsNoHeadingFallsBackToParagraphs(t *testing.T) {
	text := "para one\n\npara two\n\npara three\n"
	got := Headings(text)
	want := Paragraphs(text)
	if len(got) != len(want) {
		t.Fatalf("no-heading Headings gave %d units, want Paragraphs' %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unit %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestHeadingsCRLF(t *testing.T) {
	text := "# A\r\nalpha\r\n\r\n## B\r\nbeta\r\n"
	got := Headings(text)
	if len(got) != 2 {
		t.Fatalf("got %d units, want 2", len(got))
	}
}

func TestHeadingsNotAHeading(t *testing.T) {
	// 7 hashes is not an ATX heading; no leading-space-4 heading either.
	text := "####### too many\n    # indented four spaces\nplain\n"
	if got := Headings(text); len(got) != 1 {
		// no real headings -> Paragraphs fallback (single paragraph here)
		t.Fatalf("got %d units %v, want 1", len(got), got)
	}
}

// TestHeadingsProduceValidUnits checks that every Headings result is a valid
// segmentation that Split accepts and that chunk invariants hold end to end.
func TestHeadingsProduceValidUnits(t *testing.T) {
	inputs := []string{
		"# A\nalpha\n\n## B\nbeta\n",
		"intro\n\nTitle\n---\nbody\n",
		"# Only heading\n",
		"```\nfenced only\n# inside\n```\n",
		"no headings at all here\n",
	}
	for _, in := range inputs {
		units := Headings(in)
		if !validUnits(in, units) {
			t.Fatalf("Headings(%q) produced invalid units %+v", in, units)
		}
		chunks := Split(in, Config{Boundaries: Headings})
		assertChunksInvariants(t, in, chunks)
	}
}
