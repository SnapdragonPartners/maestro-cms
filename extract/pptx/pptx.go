// Package pptx extracts slide and speaker-notes text from PPTX (Office Open XML)
// sources.
//
// A .pptx is a ZIP archive of XML. Slide text lives in ppt/slides/slideN.xml and
// is carried by DrawingML text runs (<a:t>) inside paragraphs (<a:p>) — the same
// local element names DOCX uses, so the decode is shared in spirit with the docx
// extractor. This extractor unzips in memory, walks slides in presentation order,
// and for each slide appends its body text followed by its speaker notes (if any).
//
// Slide order comes from ppt/presentation.xml's slide-id list resolved through
// ppt/_rels/presentation.xml.rels — the order the deck actually presents, which
// can differ from the slideN.xml filename numbers after a deck is reordered. If
// those parts are missing or unparseable, it falls back to numeric filename
// order; any slide parts not referenced by the presentation are appended after
// the ordered ones so no slide is silently dropped.
//
// Notes are associated to their slide through the slide's own relationships
// (ppt/slides/_rels/slideN.xml.rels → a notesSlide target), so a note stays with
// the slide it belongs to. The result is a single text/plain artifact in the
// same shape as the core text extractor, with blank lines between paragraphs so
// the boundary-aware chunker sees structure.
//
// Like docx, this needs no third-party dependency (stdlib archive/zip +
// encoding/xml) but stays a subpackage so all formats are uniformly opt-in
// (see docs/adr/0006-optional-adapters-as-subpackages.md):
//
//	reg.Register("application/vnd.openxmlformats-officedocument.presentationml.presentation", pptx.New())
//
// Scope is slide body text plus speaker notes. Masters, layouts, comments, and
// embedded objects are not extracted; that can be added later by parsing more
// archive parts.
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
	// presentationDir is the archive directory holding the presentation parts;
	// presentation relationship targets are relative to it.
	presentationDir = "ppt"
	// slidesDir is the archive directory holding slide parts.
	slidesDir = "ppt/slides/"
	// slidePrefix and xmlSuffix bound a slide part name: ppt/slides/slideN.xml.
	slidePrefix = slidesDir + "slide"
	xmlSuffix   = ".xml"

	presentationPart     = "ppt/presentation.xml"
	presentationRelsPart = "ppt/_rels/presentation.xml.rels"

	// notesRelType is the relationship type linking a slide to its notes slide.
	notesRelType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide"
)

// DefaultMaxDecompressedBytes caps the total uncompressed XML this extractor
// reads across all parts (presentation, slides, notes, relationships). The
// Registry bounds the *compressed* input, but a zip bomb can expand far beyond
// that, so the uncompressed stream needs its own ceiling. 64 MiB is far larger
// than any realistic deck's text parts while still bounding memory.
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
// slides in presentation order, paragraphs separated by blank lines. It honors
// ctx cancellation while reading. It returns:
//
//   - extract.ErrNoContent if the deck parses cleanly but has no slide/notes text;
//   - an error matching extract.ErrMalformedSource if the archive is not a valid
//     zip, contains no slides, contains unparseable XML, declares a notes slide
//     whose target is missing, or whose parts decompress past
//     MaxDecompressedBytes (a zip bomb).
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

	pd := &partDecoder{remaining: e.maxDecompressed()}

	ordered, err := pd.presentationOrder(byName)
	if err != nil {
		return nil, err
	}
	slides := resolveOrder(ordered, sortedSlides(zr), byName)
	if len(slides) == 0 {
		return nil, &extract.MalformedSourceError{
			MediaType: MediaType,
			Err:       errors.New("no slides (ppt/slides/slideN.xml) in archive"),
		}
	}

	var b strings.Builder
	for _, sf := range slides {
		if err := ctx.Err(); err != nil {
			return nil, err //nolint:wrapcheck // sentinel ctx error; surfaced to caller as-is
		}
		slideText, err := pd.decode(ctx, sf)
		if err != nil {
			return nil, err
		}
		b.WriteString(slideText)

		notes, hasNotes, err := pd.notesFor(byName, sf.Name)
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
// before slide2. Names without a numeric suffix are skipped. This is the
// fallback ordering and the source of orphan slides not named in the
// presentation.
func sortedSlides(zr *zip.Reader) []slide {
	out := make([]slide, 0, len(zr.File))
	for _, f := range zr.File {
		if !isSlidePart(f.Name) {
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

// isSlidePart reports whether name is a ppt/slides/slideN.xml part (and not, say,
// a _rels entry under ppt/slides/).
func isSlidePart(name string) bool {
	return strings.HasPrefix(name, slidePrefix) && strings.HasSuffix(name, xmlSuffix)
}

// resolveOrder produces the final ordered slide files: the parts named by the
// presentation (in its order), followed by any actual slide parts the
// presentation did not reference (in filename order), so a reordered deck reads
// correctly and no slide is dropped. With a nil ordered list (no usable
// presentation), it is exactly the filename order.
func resolveOrder(ordered []string, fileSlides []slide, byName map[string]*zip.File) []*zip.File {
	seen := make(map[string]bool, len(fileSlides))
	out := make([]*zip.File, 0, len(fileSlides))
	for _, name := range ordered {
		if !isSlidePart(name) || seen[name] {
			continue
		}
		if f, ok := byName[name]; ok {
			out = append(out, f)
			seen[name] = true
		}
	}
	for i := range fileSlides {
		if name := fileSlides[i].file.Name; !seen[name] {
			out = append(out, fileSlides[i].file)
			seen[name] = true
		}
	}
	return out
}

// presentationXML models the slide-id list: each sldId references a slide by its
// relationship id (the r:id attribute, in the relationships namespace).
type presentationXML struct {
	XMLName  xml.Name `xml:"presentation"`
	SlideIDs []struct {
		RID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	} `xml:"sldIdLst>sldId"`
}

// relationships models the subset of a .rels part we need.
type relationships struct {
	XMLName xml.Name `xml:"Relationships"`
	Rels    []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

// partDecoder decodes archive parts while enforcing a single decompressed-byte
// budget shared across every part it reads.
type partDecoder struct {
	remaining int64
}

// presentationOrder returns the slide part names in presentation order, or nil to
// signal "fall back to filename order" when the presentation parts are absent or
// unparseable. It returns an error only when reading trips the decompressed
// budget (a malformed/hostile archive). Reads are charged against the budget.
func (pd *partDecoder) presentationOrder(byName map[string]*zip.File) ([]string, error) {
	pres, ok := byName[presentationPart]
	if !ok {
		return nil, nil
	}
	relsF, ok := byName[presentationRelsPart]
	if !ok {
		return nil, nil
	}

	presData, err := pd.readPart(pres)
	if err != nil {
		return nil, err
	}
	relsData, err := pd.readPart(relsF)
	if err != nil {
		return nil, err
	}

	var p presentationXML
	if err := xml.Unmarshal(presData, &p); err != nil || len(p.SlideIDs) == 0 {
		return nil, nil //nolint:nilerr // unparseable/empty presentation → fall back to filename order, not a hard error (no content is lost)
	}
	var rels relationships
	if err := xml.Unmarshal(relsData, &rels); err != nil {
		return nil, nil //nolint:nilerr // unparseable presentation rels → fall back to filename order
	}
	relMap := make(map[string]string, len(rels.Rels))
	for i := range rels.Rels {
		relMap[rels.Rels[i].ID] = rels.Rels[i].Target
	}

	out := make([]string, 0, len(p.SlideIDs))
	for i := range p.SlideIDs {
		if target, ok := relMap[p.SlideIDs[i].RID]; ok {
			// Targets are relative to the presentation part's directory (ppt/).
			out = append(out, path.Join(presentationDir, target))
		}
	}
	return out, nil
}

// readPart reads an entire archive part into memory, charging it against the
// shared budget. Exceeding the budget surfaces as ErrMalformedSource.
func (pd *partDecoder) readPart(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}
	defer func() { _ = rc.Close() }()

	cr := &cappedReader{r: rc, remaining: pd.remaining}
	data, err := io.ReadAll(cr)
	pd.remaining = cr.remaining
	if err != nil {
		return nil, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}
	return data, nil
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

// notesFor returns the notes-slide part associated with the slide part named
// slideName, with found reporting whether one exists. It reads the slide's own
// relationships part, finds the notesSlide relationship, and resolves its
// (slide-relative) target. A declared notesSlide whose target is missing from the
// archive is a corrupt deck and surfaces as ErrMalformedSource rather than being
// silently dropped; a slide with no notesSlide relationship simply has no notes.
func (pd *partDecoder) notesFor(byName map[string]*zip.File, slideName string) (*zip.File, bool, error) {
	dir := path.Dir(slideName)
	relsName := dir + "/_rels/" + path.Base(slideName) + ".rels"
	rels, ok := byName[relsName]
	if !ok {
		return nil, false, nil // no relationships for this slide → no notes
	}

	data, err := pd.readPart(rels)
	if err != nil {
		return nil, false, err
	}
	var rel relationships
	if err := xml.Unmarshal(data, &rel); err != nil {
		return nil, false, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}
	for i := range rel.Rels {
		if rel.Rels[i].Type == notesRelType {
			target := path.Join(dir, rel.Rels[i].Target)
			nf, ok := byName[target]
			if !ok {
				return nil, false, &extract.MalformedSourceError{
					MediaType: MediaType,
					Err:       fmt.Errorf("notes target %q for slide %q not found in archive", target, slideName),
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
