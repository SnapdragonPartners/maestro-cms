// Package html extracts visible text from HTML sources.
//
// It is an opt-in subpackage so the core extract package stays standard-library
// only: importing this package pulls in golang.org/x/net/html
// (see docs/adr/0006-optional-adapters-as-subpackages.md). Wire it into a
// registry with:
//
//	reg.Register("text/html", html.New())
//
// The extractor strips tags and skips script, style, and noscript content, then
// normalizes whitespace, producing a single text/plain artifact in the same
// shape as the core text extractor.
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

// collectText walks the node tree, appending text content to b. Script, style,
// and noscript subtrees are skipped entirely so their bodies never reach the
// output. A space is written after each text node so inline-element text
// (e.g. "<b>Hello</b>world") does not fuse; NormalizeWhitespace collapses the
// extra spaces.
func collectText(n *html.Node, b *strings.Builder) {
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.Script, atom.Style, atom.Noscript:
			return
		}
	}
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		b.WriteByte(' ')
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, b)
	}
}
