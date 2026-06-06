// Package docx extracts body text from DOCX (Office Open XML) sources.
//
// A .docx is a ZIP archive of XML; the document body lives at word/document.xml.
// This extractor unzips in memory, stream-decodes that part, and concatenates
// text runs (<w:t>), emitting a blank line between paragraphs (<w:p>) so the
// downstream boundary-aware chunker sees paragraph boundaries. The result is a
// single text/plain artifact in the same shape as the core text extractor.
//
// Unlike the PDF and HTML extractors, this package needs no third-party
// dependency (stdlib archive/zip + encoding/xml). It is kept a subpackage anyway
// so all formats are uniformly opt-in
// (see docs/adr/0006-optional-adapters-as-subpackages.md). Wire it into a
// registry with:
//
//	reg.Register("application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx.New())
//
// Scope is body text only: headers, footers, footnotes, endnotes, comments, and
// embedded objects are not extracted. That can be added later by parsing the
// additional archive parts; it is omitted now to keep the first version small.
package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
)

// MediaType is the media type this extractor handles.
const MediaType content.MediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// documentPart is the archive path of the main document body in a .docx.
const documentPart = "word/document.xml"

// DefaultMaxDecompressedBytes caps how much of word/document.xml the decoder will
// read. The Registry bounds the *compressed* input, but a zip bomb can expand far
// beyond that, so the uncompressed stream needs its own ceiling. 64 MiB is far
// larger than any realistic body-text document.xml while still bounding memory.
const DefaultMaxDecompressedBytes int64 = 64 << 20 // 64 MiB

// errDecompressedTooLarge is returned by the capping reader when the uncompressed
// document.xml exceeds the budget. It surfaces to the caller as ErrMalformedSource
// (a hostile or corrupt archive), matching how other parse failures are reported.
var errDecompressedTooLarge = errors.New("word/document.xml exceeds decompressed size limit (possible zip bomb)")

// Extractor extracts body text from DOCX sources. It carries only immutable
// configuration and is safe for concurrent use.
type Extractor struct {
	// MaxDecompressedBytes caps the uncompressed size of word/document.xml the
	// decoder reads, defending against a zip bomb whose compressed form fits under
	// the Registry's input cap but expands to exhaust memory. Zero or negative
	// means DefaultMaxDecompressedBytes.
	MaxDecompressedBytes int64
}

// New returns a DOCX Extractor using DefaultMaxDecompressedBytes.
func New() *Extractor {
	return &Extractor{}
}

func (e Extractor) maxDecompressed() int64 {
	if e.MaxDecompressedBytes <= 0 {
		return DefaultMaxDecompressedBytes
	}
	return e.MaxDecompressedBytes
}

// Extract reads r as a .docx archive and returns a single text/plain artifact
// derived from parentID, with paragraphs separated by blank lines. It honors ctx
// cancellation while reading. It returns:
//
//   - extract.ErrNoContent if the document parses cleanly but has no body text;
//   - an error matching extract.ErrMalformedSource if the archive is not a valid
//     zip, lacks word/document.xml, contains unparseable XML, or whose
//     word/document.xml decompresses past MaxDecompressedBytes (a zip bomb).
func (e Extractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
	data, err := extract.ReadAll(ctx, r)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
	}

	doc, err := openDocumentPart(zr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = doc.Close() }()

	text, err := decodeBodyText(ctx, doc, e.maxDecompressed())
	if err != nil {
		return nil, err
	}

	text = extract.NormalizeWhitespace(text)
	if text == "" {
		return nil, extract.ErrNoContent
	}
	return []content.Artifact{extract.TextArtifact(parentID, text)}, nil
}

// openDocumentPart finds and opens word/document.xml. Its absence means the zip
// is not a Word document (or is corrupt), which is a malformed source.
func openDocumentPart(zr *zip.Reader) (io.ReadCloser, error) {
	for _, f := range zr.File {
		if f.Name == documentPart {
			rc, err := f.Open()
			if err != nil {
				return nil, &extract.MalformedSourceError{MediaType: MediaType, Err: err}
			}
			return rc, nil
		}
	}
	return nil, &extract.MalformedSourceError{
		MediaType: MediaType,
		Err:       errors.New("no " + documentPart + " in archive"),
	}
}

// decodeBodyText stream-parses word/document.xml, concatenating <w:t> text-run
// content and emitting a blank line after each closing <w:p> so paragraph
// structure survives for the chunker. All other elements are walked and ignored.
// Reading is bounded at maxBytes of uncompressed input; exceeding it surfaces as
// ErrMalformedSource so a zip bomb cannot exhaust memory.
func decodeBodyText(ctx context.Context, r io.Reader, maxBytes int64) (string, error) {
	dec := xml.NewDecoder(&cappedReader{r: r, remaining: maxBytes})
	var b strings.Builder
	inText := false

	for {
		if err := ctx.Err(); err != nil {
			return "", err //nolint:wrapcheck // sentinel ctx error; surfaced to caller as-is
		}
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
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
	return b.String(), nil
}

// cappedReader returns errDecompressedTooLarge once more than the permitted
// number of bytes is read, instead of streaming an unbounded decompressed part.
// Exactly remaining bytes is allowed; one more trips the limit. It mirrors the
// limitReader the Registry applies to compressed input, here guarding the
// uncompressed XML the zip reader hands back.
type cappedReader struct {
	r         io.Reader
	remaining int64
}

func (cr *cappedReader) Read(p []byte) (int, error) {
	if cr.remaining < 0 {
		return 0, errDecompressedTooLarge
	}
	// Permit reading one byte past the allowance so an exactly-at-limit part
	// succeeds while an over-limit part is detected rather than truncated.
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
