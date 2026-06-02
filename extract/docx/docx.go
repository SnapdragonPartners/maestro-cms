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

// Extractor extracts body text from DOCX sources. It is stateless and safe for
// concurrent use.
type Extractor struct{}

// New returns a DOCX Extractor.
func New() *Extractor {
	return &Extractor{}
}

// Extract reads r as a .docx archive and returns a single text/plain artifact
// derived from parentID, with paragraphs separated by blank lines. It honors ctx
// cancellation while reading. It returns:
//
//   - extract.ErrNoContent if the document parses cleanly but has no body text;
//   - an error matching extract.ErrMalformedSource if the archive is not a valid
//     zip, lacks word/document.xml, or contains unparseable XML.
func (Extractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
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

	text, err := decodeBodyText(ctx, doc)
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
func decodeBodyText(ctx context.Context, r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
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
