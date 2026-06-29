package pptx_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pptx"
)

var _ extract.Extractor = pptx.Extractor{}

// slideEntry describes one slide to synthesize: its slide number, body
// paragraphs, and optional speaker-notes paragraphs (nil = no notes).
type slideEntry struct {
	num   int
	body  []string
	notes []string
}

// drawingXML builds an OOXML drawing part with the given paragraphs as <a:t>
// runs — the shape shared by slides and notes slides.
func drawingXML(paras []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><p:sld xmlns:p="urn:p" xmlns:a="urn:a"><p:cSld><p:spTree><p:sp><p:txBody>`)
	for _, p := range paras {
		b.WriteString(`<a:p><a:r><a:t>` + p + `</a:t></a:r></a:p>`)
	}
	b.WriteString(`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`)
	return b.String()
}

func relsXML(notesTarget string) string {
	return `<?xml version="1.0"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="` + "http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" + `" Target="` + notesTarget + `"/>` +
		`</Relationships>`
}

// presentationXMLDoc builds ppt/presentation.xml whose sldIdLst lists the given
// slide numbers in order, each referencing slideN.xml by relationship id rId{k}.
func presentationXMLDoc(order []int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><p:presentation xmlns:p="urn:p" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst>`)
	for k := range order {
		b.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 256+k, k+1))
	}
	b.WriteString(`</p:sldIdLst></p:presentation>`)
	return b.String()
}

// presentationRelsDoc builds ppt/_rels/presentation.xml.rels mapping rId{k} to
// slides/slide{num}.xml, matching presentationXMLDoc's ids.
func presentationRelsDoc(order []int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for k, num := range order {
		b.WriteString(fmt.Sprintf(
			`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`,
			k+1, num))
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

// makePptx synthesizes a minimal .pptx archive from the given slides. With no
// presentation order it omits ppt/presentation.xml, exercising the filename-order
// fallback; see makePptxOrdered for the presentation-order path.
func makePptx(t *testing.T, entries ...slideEntry) []byte {
	return buildPptx(t, nil, entries)
}

// makePptxOrdered is makePptx plus a ppt/presentation.xml whose sldIdLst lists
// the given slide numbers in presentation order.
func makePptxOrdered(t *testing.T, order []int, entries ...slideEntry) []byte {
	return buildPptx(t, order, entries)
}

func buildPptx(t *testing.T, order []int, entries []slideEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	for _, e := range entries {
		write(fmt.Sprintf("ppt/slides/slide%d.xml", e.num), drawingXML(e.body))
		if e.notes != nil {
			write(fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", e.num), drawingXML(e.notes))
			write(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", e.num),
				relsXML(fmt.Sprintf("../notesSlides/notesSlide%d.xml", e.num)))
		}
	}
	if order != nil {
		write("ppt/presentation.xml", presentationXMLDoc(order))
		write("ppt/_rels/presentation.xml.rels", presentationRelsDoc(order))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func extractText(t *testing.T, data []byte) string {
	t.Helper()
	arts, err := pptx.Extractor{}.Extract(context.Background(), bytes.NewReader(data), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(arts))
	}
	a := arts[0]
	if a.MediaType != extract.MediaTypeText {
		t.Fatalf("MediaType = %q, want %q", a.MediaType, extract.MediaTypeText)
	}
	if a.DerivedFrom != "src-1" || a.ID != "" {
		t.Fatalf("artifact provenance wrong: DerivedFrom=%q ID=%q", a.DerivedFrom, a.ID)
	}
	return a.Text
}

func assertOrder(t *testing.T, got string, want ...string) {
	t.Helper()
	last := -1
	for _, s := range want {
		i := strings.Index(got, s)
		if i < 0 {
			t.Fatalf("%q missing from output %q", s, got)
		}
		if i <= last {
			t.Fatalf("%q out of order in %q (want %v)", s, got, want)
		}
		last = i
	}
}

func TestExtractFilenameOrderFallback(t *testing.T) {
	// No ppt/presentation.xml → fall back to numeric filename order. Written
	// 10, 2, 1 — must come back 1, 2, 10 (numeric, not lexical).
	data := makePptx(t,
		slideEntry{num: 10, body: []string{"ten"}},
		slideEntry{num: 2, body: []string{"two"}},
		slideEntry{num: 1, body: []string{"one"}},
	)
	assertOrder(t, extractText(t, data), "one", "two", "ten")
}

func TestExtractPresentationOrderOverridesFilenames(t *testing.T) {
	// Filenames are 1=A, 2=B, 3=C but the deck presents them 3, 1, 2 (a reordered
	// deck that kept its part filenames). Presentation order must win.
	data := makePptxOrdered(t, []int{3, 1, 2},
		slideEntry{num: 1, body: []string{"alpha"}},
		slideEntry{num: 2, body: []string{"bravo"}},
		slideEntry{num: 3, body: []string{"charlie"}},
	)
	assertOrder(t, extractText(t, data), "charlie", "alpha", "bravo")
}

func TestExtractOrphanSlidesAppended(t *testing.T) {
	// The presentation lists only slides 2 and 1; slide 3 exists but is
	// unreferenced. Referenced slides come first in deck order, then the orphan —
	// no slide is dropped.
	data := makePptxOrdered(t, []int{2, 1},
		slideEntry{num: 1, body: []string{"alpha"}},
		slideEntry{num: 2, body: []string{"bravo"}},
		slideEntry{num: 3, body: []string{"orphan"}},
	)
	assertOrder(t, extractText(t, data), "bravo", "alpha", "orphan")
}

func TestExtractParagraphsSeparated(t *testing.T) {
	data := makePptx(t, slideEntry{num: 1, body: []string{"first line", "second line"}})
	got := extractText(t, data)
	if got != "first line\n\nsecond line" {
		t.Fatalf("got %q, want paragraph-separated text", got)
	}
}

func TestExtractSpeakerNotesFollowSlide(t *testing.T) {
	data := makePptx(t,
		slideEntry{num: 1, body: []string{"slide one body"}, notes: []string{"note for one"}},
		slideEntry{num: 2, body: []string{"slide two body"}, notes: []string{"note for two"}},
	)
	got := extractText(t, data)
	// Each note must appear immediately after its own slide's body, in order.
	order := []string{"slide one body", "note for one", "slide two body", "note for two"}
	last := -1
	for _, s := range order {
		i := strings.Index(got, s)
		if i <= last {
			t.Fatalf("%q out of order in %q", s, got)
		}
		last = i
	}
}

func TestExtractSlideWithoutNotes(t *testing.T) {
	data := makePptx(t, slideEntry{num: 1, body: []string{"body only"}})
	if got := extractText(t, data); got != "body only" {
		t.Fatalf("got %q, want %q", got, "body only")
	}
}

func TestExtractNoSlidesIsMalformed(t *testing.T) {
	// A valid zip with no slide parts.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("ppt/presentation.xml")
	_, _ = w.Write([]byte("<p:presentation/>"))
	_ = zw.Close()

	_, err := pptx.Extractor{}.Extract(context.Background(), bytes.NewReader(buf.Bytes()), "src-1")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

func TestExtractNotAZipIsMalformed(t *testing.T) {
	_, err := pptx.Extractor{}.Extract(context.Background(), strings.NewReader("not a zip"), "src-1")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

func TestExtractEmptyTextIsNoContent(t *testing.T) {
	data := makePptx(t, slideEntry{num: 1, body: []string{"", "   "}})
	_, err := pptx.Extractor{}.Extract(context.Background(), bytes.NewReader(data), "src-1")
	if !errors.Is(err, extract.ErrNoContent) {
		t.Fatalf("err = %v, want ErrNoContent", err)
	}
}

func TestExtractContextCanceled(t *testing.T) {
	data := makePptx(t, slideEntry{num: 1, body: []string{"x"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pptx.Extractor{}.Extract(ctx, bytes.NewReader(data), "src-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestExtractDanglingNotesTargetIsMalformed(t *testing.T) {
	// A slide declares a notesSlide relationship, but the target part is absent —
	// a corrupt archive. It must surface as malformed, not silently drop notes.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	write("ppt/slides/slide1.xml", drawingXML([]string{"body"}))
	write("ppt/slides/_rels/slide1.xml.rels", relsXML("../notesSlides/notesSlide1.xml"))
	// Intentionally omit ppt/notesSlides/notesSlide1.xml.
	_ = zw.Close()

	_, err := pptx.Extractor{}.Extract(context.Background(), bytes.NewReader(buf.Bytes()), "src-1")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

func TestExtractZipBombBounded(t *testing.T) {
	// A slide whose decompressed text exceeds a tiny cap surfaces as malformed.
	big := strings.Repeat("A", 4096)
	data := makePptx(t, slideEntry{num: 1, body: []string{big}})
	_, err := pptx.Extractor{MaxDecompressedBytes: 256}.Extract(context.Background(), bytes.NewReader(data), "src-1")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource (decompressed cap)", err)
	}
}
