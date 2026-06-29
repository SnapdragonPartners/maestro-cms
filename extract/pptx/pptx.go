// Package pptx extracts slide and speaker-notes text from PPTX (Office Open XML)
// sources.
//
// A .pptx is a ZIP archive of XML. Slide text lives in ppt/slides/slideN.xml and
// is carried by DrawingML text runs (<a:t>) inside paragraphs (<a:p>) — the same
// local element names DOCX uses, so the decode is shared in spirit with the docx
// extractor. This extractor unzips in memory, walks slides in slide-number order,
// and for each slide appends its body text followed by its speaker notes (if any).
// Notes are associated to their slide through the slide's relationships
// (ppt/slides/_rels/slideN.xml.rels → a notesSlide target), so a note stays with
// the slide it belongs to rather than being guessed by filename. The result is a
// single text/plain artifact in the same shape as the core text extractor, with
// blank lines between paragraphs so the boundary-aware chunker sees structure.
//
// Like docx, this needs no third-party dependency (stdlib archive/zip +
// encoding/xml) but stays a subpackage so all formats are uniformly opt-in
// (see docs/adr/0006-optional-adapters-as-subpackages.md):
//
//	reg.Register("application/vnd.openxmlformats-officedocument.presentationml.presentation", pptx.New())
//
// Scope is slide body text plus speaker notes. Slide order is taken from the
// slideN.xml filename number (the common case), not resolved through
// presentation.xml's slide-id list. Masters, layouts, comments, and embedded
// objects are not extracted; that can be added later by parsing more archive
// parts.
package pptx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
)

// MediaType is the media type this extractor handles.
const MediaType content.MediaType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"

const (
	// slidesDir is the archive directory holding slide parts.
	slidesDir = "ppt/slides/"
	// slidePrefix and xmlSuffix bound a slide part name: ppt/slides/slideN.xml.
	slidePrefix = slidesDir + "slide"
	xmlSuffix   = ".xml"
	// notesRelType is the relationship type linking a slide to its notes slide.
	notesRelType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide"
)

// DefaultMaxDecompressedBytes caps the total uncompressed XML this extractor
// reads across all slide, notes, and relationship parts. The Registry bounds the
// *compressed* input, but a zip bomb can expand far beyond that, so the
// uncompressed stream needs its own ceiling. 64 MiB is far larger than any
// realistic deck's text parts while still bounding memory.
const DefaultMaxDecompressedBytes int64 = 64 << 20 // 64 MiB

// errDecompressedTooLarge is returned by the capping reader when uncompressed
// input exceeds the budget. It surfaces to the caller as ErrMalformedSource.
var errDecompressedTooLarge = errors.New("pptx: decompressed parts exceed size limit (possible zip bomb)")

// Extractor extracts slide and notes text from PPTX sources. It carries only
// immutable configuration and is safe for concurrent use.
type Extractor struct {
	// MaxDecompressedBytes caps the total uncompressed size of the parts read,
	// defending against a zip bomb whose compressed form fits under the Registry's
	// input cap but expands to exhaust memory. Zero or negative means
	// DefaultMaxDecompressedBytes.
	MaxDecompressedBytes int64
}

// New returns a PPTX Extractor using DefaultMaxDecompressedBytes.
func New() *Extractor {
	return &Extractor{}
}

func (e Extractor) maxDecompressed() int64 {
	if e.MaxDecompressedBytes <= 0 {
		return DefaultMaxDecompressedBytes
	}
	return e.MaxDecompressedBytes
}

// Extract reads r as a .pptx archive and returns a single text/plain artifact
// derived from parentID: each slide's body text followed by its speaker notes,
// slides in slide-number order, paragraphs separated by blank lines. It honors
// ctx cancellation while reading. It returns:
//
//   - extract.ErrNoContent if the deck parses cleanly but has no slide/notes text;
//   - an error matching extract.ErrMalformedSource if the archive is not a valid
//     zip, contains no slides, contains unparseable XML, or whose parts
//     decompress past MaxDecompressedBytes (a zip bomb).
func (e Extractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
	data, err := extract.ReadAll(ctx, r)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}

	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[f.Name] = f
	}

	slides := sortedSlides(zr)
	if len(slides) == 0 {
		return nil, &extract.MalformedSourceError{
			MediaType: MediaType,
			Err:       errors.New("no slides (ppt/slides/slideN.xml) in archive"),
		}
	}

	pd := &partDecoder{remaining: e.maxDecompressed()}
	var b strings.Builder
	for _, s := range slides {
		if err := ctx.Err(); err != nil {
			return nil, err //nolint:wrapcheck // sentinel ctx error; surfaced to caller as-is
		}
		slideText, err := pd.decode(ctx, s.file)
		if err != nil {
			return nil, err
		}
		b.WriteString(slideText)

		notes, hasNotes, err := pd.notesFor(byName, s.num)
		if err != nil {
			return nil, err
		}
		if hasNotes {
			notesText, err := pd.decode(ctx, notes)
			if err != nil {
				return nil, err
			}
			b.WriteString(notesText)
		}
	}

	text := extract.NormalizeWhitespace(b.String())
	if text == "" {
		return nil, extract.ErrNoContent
	}
	return []content.Artifact{extract.TextArtifact(parentID, text)}, nil
}

// slide pairs a slide part with its parsed slide number.
type slide struct {
	num  int
	file *zip.File
}

// sortedSlides returns the slide parts (ppt/slides/slideN.xml) in ascending
// slide-number order. Numeric sort matters: lexical order would put slide10
// before slide2. Names without a numeric suffix (e.g. nested _rels paths) are
// skipped.
func sortedSlides(zr *zip.Reader) []slide {
	out := make([]slide, 0, len(zr.File))
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, slidePrefix) || !strings.HasSuffix(f.Name, xmlSuffix) {
			continue
		}
		mid := f.Name[len(slidePrefix) : len(f.Name)-len(xmlSuffix)]
		n, err := strconv.Atoi(mid)
		if err != nil {
			continue // not a slideN.xml part
		}
		out = append(out, slide{num: n, file: f})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].num < out[j].num })
	return out
}

// partDecoder decodes archive parts while enforcing a single decompressed-byte
// budget shared across every part it reads.
type partDecoder struct {
	remaining int64
}

// decode stream-parses an OOXML drawing part, concatenating <a:t> run text and
// emitting a blank line after each closing <a:p>. Reading is charged against the
// shared budget; exceeding it surfaces as ErrMalformedSource.
func (pd *partDecoder) decode(ctx context.Context, f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}
	defer func() { _ = rc.Close() }()

	cr := &cappedReader{r: rc, remaining: pd.remaining}
	dec := xml.NewDecoder(cr)
	var b strings.Builder
	inText := false
	for {
		if err := ctx.Err(); err != nil {
			pd.remaining = cr.remaining
			return "", err //nolint:wrapcheck // sentinel ctx error; surfaced to caller as-is
		}
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			pd.remaining = cr.remaining
			return "", &extract.MalformedSourceError{MediaType: MediaType, Err: err}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteString("\n\n")
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	pd.remaining = cr.remaining
	return b.String(), nil
}

// relationships models the subset of a .rels part we need: each Relationship's
// type and target.
type relationships struct {
	XMLName xml.Name `xml:"Relationships"`
	Rels    []struct {
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

// notesFor returns the notes-slide part associated with slide number num, with
// found reporting whether one exists. It reads ppt/slides/_rels/slideN.xml.rels,
// finds the notesSlide relationship, and resolves its (slide-relative) target to
// an archive path. Reading the rels part is charged against the shared budget.
func (pd *partDecoder) notesFor(byName map[string]*zip.File, num int) (f *zip.File, found bool, err error) {
	relsName := slidesDir + "_rels/slide" + strconv.Itoa(num) + ".xml.rels"
	rels, ok := byName[relsName]
	if !ok {
		return nil, false, nil // no relationships for this slide → no notes
	}

	rc, err := rels.Open()
	if err != nil {
		return nil, false, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}
	defer func() { _ = rc.Close() }()

	cr := &cappedReader{r: rc, remaining: pd.remaining}
	data, err := io.ReadAll(cr)
	pd.remaining = cr.remaining
	if err != nil {
		return nil, false, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}

	var rel relationships
	if err := xml.Unmarshal(data, &rel); err != nil {
		return nil, false, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}
	for i := range rel.Rels {
		if rel.Rels[i].Type == notesRelType {
			// Targets are relative to the slide part's directory (ppt/slides/).
			target := path.Join(slidesDir, rel.Rels[i].Target)
			nf, ok := byName[target]
			if !ok {
				// The slide declares a notes slide but its target is absent (or
				// escapes the archive). A valid .pptx always ships the target, so
				// this is a corrupt archive, not a slide that simply has no notes —
				// surface it rather than silently dropping the notes.
				return nil, false, &extract.MalformedSourceError{
					MediaType: MediaType,
					Err:       fmt.Errorf("slide %d notes target %q not found in archive", num, target),
				}
			}
			return nf, true, nil
		}
	}
	return nil, false, nil // no notesSlide relationship → the slide has no notes
}

// cappedReader returns errDecompressedTooLarge once more than the permitted
// number of bytes is read, bounding the uncompressed XML the zip reader hands
// back. Exactly remaining bytes is allowed; one more trips the limit. It mirrors
// the limitReader the Registry applies to compressed input.
type cappedReader struct {
	r         io.Reader
	remaining int64
}

func (cr *cappedReader) Read(p []byte) (int, error) {
	if cr.remaining < 0 {
		return 0, errDecompressedTooLarge
	}
	if int64(len(p)) > cr.remaining+1 {
		p = p[:cr.remaining+1]
	}
	n, err := cr.r.Read(p)
	cr.remaining -= int64(n)
	if cr.remaining < 0 {
		return n, errDecompressedTooLarge
	}
	return n, err //nolint:wrapcheck // pass-through of underlying reader, incl io.EOF
}
