package extract

import (
	"context"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/SnapdragonPartners/maestro-cms/content"
)

// TextExtractor handles plain-text (text/plain) sources. Extraction is minimal:
// read the bytes, coerce to valid UTF-8, and normalize prose whitespace (trim
// lines, collapse blank-line runs). It is the canonical genuinely-stdlib
// extractor.
//
// It is deliberately NOT used for Markdown. Whitespace is semantic in Markdown
// (indented code blocks, nested-list indentation, hard breaks), so the prose
// normalization here would corrupt it. Markdown has its own extractor, the
// extract/markdown subpackage, which preserves the body verbatim (see ADR 0008).
//
// TextExtractor honors ctx cancellation but does not bound input size; route
// untrusted sources through a Registry, which applies the size limit.
type TextExtractor struct{}

// NewTextExtractor returns a TextExtractor.
func NewTextExtractor() *TextExtractor {
	return &TextExtractor{}
}

// Extract reads UTF-8 text from r and returns a single text artifact derived
// from parentID. It honors ctx cancellation. Invalid UTF-8 sequences are
// replaced with U+FFFD. It returns ErrNoContent if the input normalizes to the
// empty string.
func (TextExtractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
	data, err := ReadAll(ctx, r)
	if err != nil {
		return nil, err
	}

	text := string(data)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}

	normalized := NormalizeWhitespace(text)
	if normalized == "" {
		return nil, ErrNoContent
	}
	return []content.Artifact{TextArtifact(parentID, normalized)}, nil
}
