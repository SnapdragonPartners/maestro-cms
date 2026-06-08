// Package preset wires the common document extractors into an extract.Registry
// in one call. It is a convenience bundle, not policy: it only registers
// format extractors (text, HTML, PDF, DOCX, Markdown) and chooses no app
// behavior.
//
// Importing this package pulls in every bundled format's dependencies (e.g.
// golang.org/x/net for HTML, dslipak/pdf for PDF) — that is the trade for the
// one-call convenience. Apps that want a leaner import tree register only the
// formats they use against extract.NewRegistry directly. A depguard rule keeps
// core packages from importing this bundle (see
// docs/adr/0006-optional-adapters-as-subpackages.md).
//
//	reg := preset.NewDocumentRegistry(extract.WithMaxBytes(maxUploadBytes))
package preset

import (
	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/docx"
	"github.com/SnapdragonPartners/maestro-cms/extract/html"
	"github.com/SnapdragonPartners/maestro-cms/extract/markdown"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

// mediaTypeHTML is the media type for HTML sources; the html subpackage exposes
// no constant, so it is named here.
const mediaTypeHTML content.MediaType = "text/html"

// RegisterDocuments registers the common document extractors on r: plain text,
// HTML, PDF, DOCX, and Markdown (both text/markdown and text/x-markdown). It
// follows extract.Registry.Register's semantics and so panics if a media type is
// already registered on r.
func RegisterDocuments(r *extract.Registry) {
	r.Register(extract.MediaTypeText, extract.NewTextExtractor())
	r.Register(mediaTypeHTML, html.New())
	r.Register(pdf.MediaType, pdf.New())
	r.Register(docx.MediaType, docx.New())
	r.Register(markdown.MediaType, markdown.New())
	r.Register(markdown.MediaTypeX, markdown.New())
}

// NewDocumentRegistry returns a new extract.Registry with the common document
// extractors registered. Options (e.g. extract.WithMaxBytes) are passed through
// to extract.NewRegistry.
func NewDocumentRegistry(opts ...extract.Option) *extract.Registry {
	r := extract.NewRegistry(opts...)
	RegisterDocuments(r)
	return r
}

// SupportedDocumentMediaTypes returns the canonical media types the bundle
// registers, sorted. It delegates to the registry so the list cannot drift from
// what RegisterDocuments actually wires up.
func SupportedDocumentMediaTypes() []content.MediaType {
	return NewDocumentRegistry().SupportedMediaTypes()
}
