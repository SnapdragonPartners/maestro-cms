// Package markdown extracts text from Markdown sources, verbatim.
//
// It is an opt-in subpackage so the core extract package stays small and
// uniform: every non-plain-text format is registered explicitly
// (see docs/adr/0006-optional-adapters-as-subpackages.md). Despite needing no
// third-party dependency (it is standard-library only), Markdown lives here
// rather than in core because its whitespace is semantic — indented code
// blocks, nested-list indentation, hard breaks — so it must NOT pass through
// the prose whitespace normalization the core text/plain extractor applies.
//
// Wire it into a registry with both common media types:
//
//	reg.Register(markdown.MediaType, markdown.New())  // text/markdown
//	reg.Register(markdown.MediaTypeX, markdown.New()) // text/x-markdown
//
// Extraction is deliberately verbatim. The structure (headings, fenced code,
// lists) is the point: the primary consumer feeds the result to an LLM as a
// prompt or a retrieval citation, where Markdown structure is signal, not noise,
// and the boundary-aware chunker (chunk.Headings) segments on that structure.
// So this extractor preserves the body as-is; it only:
//
//   - strips a leading UTF-8 BOM,
//   - coerces invalid UTF-8 to U+FFFD,
//   - removes a leading front-matter block (see below).
//
// Front matter: only a block at the very start of the document is removed, and
// only when it both opens with a delimiter line that is exactly "---" (YAML) or
// "+++" (TOML) and has a matching closing delimiter line. A document with no
// closing delimiter is left untouched — the leading "---" is then content. A
// "---" thematic break later in the document is never treated as front matter.
package markdown

import (
	"context"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
)

// MediaType and MediaTypeX are the media types this extractor handles and the
// type it stamps on the artifact it produces (always MediaType). MediaTypeX is
// the legacy "text/x-markdown" alias many tools still emit; register both.
const (
	MediaType  content.MediaType = "text/markdown"
	MediaTypeX content.MediaType = "text/x-markdown"
)

// Extractor extracts verbatim text from Markdown sources. It is stateless and
// safe for concurrent use.
type Extractor struct{}

// New returns a Markdown Extractor.
func New() *Extractor {
	return &Extractor{}
}

// Extract reads Markdown from r and returns a single text/markdown artifact
// derived from parentID. The body is preserved verbatim apart from BOM removal,
// UTF-8 coercion, and leading front-matter stripping (see the package doc). It
// honors ctx cancellation while reading and returns extract.ErrNoContent if the
// source is empty or whitespace-only once front matter is removed.
func (Extractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
	data, err := extract.ReadAll(ctx, r)
	if err != nil {
		return nil, err
	}

	text := strings.TrimPrefix(string(data), "\ufeff")
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	text = stripFrontMatter(text)

	if strings.TrimSpace(text) == "" {
		return nil, extract.ErrNoContent
	}
	return []content.Artifact{{
		MediaType:   MediaType,
		DerivedFrom: parentID,
		Text:        text,
	}}, nil
}

// stripFrontMatter removes a leading YAML ("---") or TOML ("+++") front-matter
// block from s and returns the remaining body. It strips only when s begins with
// a delimiter line that is exactly the delimiter and a matching closing
// delimiter line exists; otherwise s is returned unchanged so a leading "---"
// that is really a thematic break or content is preserved.
func stripFrontMatter(s string) string {
	var delim string
	switch {
	case strings.HasPrefix(s, "---"):
		delim = "---"
	case strings.HasPrefix(s, "+++"):
		delim = "+++"
	default:
		return s
	}

	// The opening line must be exactly the delimiter (modulo trailing
	// whitespace): "----" or "---foo" is not front matter.
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return s // single line, no closing delimiter
	}
	if strings.TrimRight(s[:nl], " \t\r") != delim {
		return s
	}

	// Scan subsequent lines for the closing delimiter line.
	rest := s[nl+1:]
	for {
		n := strings.IndexByte(rest, '\n')
		line := rest
		if n >= 0 {
			line = rest[:n]
		}
		if strings.TrimRight(line, " \t\r") == delim {
			if n < 0 {
				return "" // closing delimiter is the last line; empty body
			}
			return rest[n+1:]
		}
		if n < 0 {
			return s // no closing delimiter found; treat the block as content
		}
		rest = rest[n+1:]
	}
}
