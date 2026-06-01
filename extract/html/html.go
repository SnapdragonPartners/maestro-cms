// Package html extracts visible text from HTML sources.
//
// It is an opt-in subpackage so the core extract package stays standard-library
// only: importing this package pulls in golang.org/x/net/html
// (see docs/adr/0006-optional-adapters-as-subpackages.md). Wire it into a
// registry with:
//
//	reg.Register("text/html", html.New())
//
// The extractor returns visible page text: it skips non-rendered subtrees (head,
// script, style, noscript, template) so titles, metadata, CSS, and scripts are
// excluded, and it inserts paragraph breaks around block-level elements so the
// downstream boundary-aware chunker sees real structure rather than one flat
// line. The result is a single text/plain artifact in the same shape as the core
// text extractor.
package html

import (
	"context"
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
)

// Extractor extracts visible text from text/html sources. It is stateless and
// safe for concurrent use.
type Extractor struct{}

// New returns an HTML Extractor.
func New() *Extractor {
	return &Extractor{}
}

// Extract parses r as HTML and returns a single text/plain artifact derived from
// parentID. The HTML parser is permissive (browser-like), so malformed markup is
// best-effort rather than an error. It honors ctx cancellation while reading and
// returns extract.ErrNoContent if the page yields no visible text.
func (Extractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
	data, err := extract.ReadAll(ctx, r)
	if err != nil {
		return nil, err
	}

	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		// html.Parse rarely hard-errors (it tolerates most input); treat a real
		// failure as malformed source rather than no content.
		return nil, &extract.MalformedSourceError{MediaType: "text/html", Err: err}
	}

	var b strings.Builder
	collectText(doc, &b)

	text := extract.NormalizeWhitespace(b.String())
	if text == "" {
		return nil, extract.ErrNoContent
	}
	return []content.Artifact{extract.TextArtifact(parentID, text)}, nil
}

// collectText walks the node tree, appending visible text content to b.
//
//   - Non-rendered subtrees (head, script, style, noscript, template) are
//     skipped entirely, so document titles, metadata, CSS, and inline scripts
//     never reach the output.
//   - Block-level elements (headings, p, li, div, …) are wrapped in blank lines
//     so the downstream boundary-aware chunker sees paragraph boundaries instead
//     of one flat line; <br> emits a single line break. NormalizeWhitespace
//     collapses the resulting blank-line runs.
//   - A space follows each text node so inline-element text ("<b>Hello</b>world")
//     does not fuse.
func collectText(n *html.Node, b *strings.Builder) {
	if n.Type == html.ElementNode {
		switch {
		case skipSubtree(n.DataAtom):
			return
		case n.DataAtom == atom.Br:
			b.WriteByte('\n')
			return
		}
	}
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		b.WriteByte(' ')
	}
	block := n.Type == html.ElementNode && isBlock(n.DataAtom)
	if block {
		b.WriteString("\n\n")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, b)
	}
	if block {
		b.WriteString("\n\n")
	}
}

// skipSubtree reports whether an element and its descendants should be omitted
// because they are not rendered as visible page text.
func skipSubtree(a atom.Atom) bool {
	switch a {
	case atom.Head, atom.Script, atom.Style, atom.Noscript, atom.Template:
		return true
	default:
		return false
	}
}

// isBlock reports whether an element is block-level for text purposes, i.e. its
// content should be separated from surrounding content by a paragraph break.
//
//nolint:cyclop // a flat classification switch over HTML atoms; not real branching complexity
func isBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer,
		atom.Main, atom.Aside, atom.Nav, atom.Figure, atom.Figcaption,
		atom.Blockquote, atom.Pre, atom.Hr,
		atom.Ul, atom.Ol, atom.Li, atom.Dl, atom.Dt, atom.Dd,
		atom.Table, atom.Caption, atom.Tr,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		return true
	default:
		return false
	}
}
