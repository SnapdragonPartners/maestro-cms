package extract

import (
	"errors"
	"slices"

	"github.com/SnapdragonPartners/maestro-cms/content"
)

// Errors returned by SingleTextArtifact.
var (
	// ErrNoTextArtifact indicates no text artifact was present.
	ErrNoTextArtifact = errors.New("extract: no text artifact")
	// ErrMultipleTextArtifacts indicates more than one text artifact was present,
	// so picking one is an application policy decision, not the library's.
	ErrMultipleTextArtifacts = errors.New("extract: multiple text artifacts")
)

// Supports reports whether an extractor is registered for mediaType. It applies
// the same canonicalization as Extract (lowercased type, parameters dropped), so
// "text/plain", "Text/Plain", and "text/plain; charset=utf-8" all match alike.
func (r *Registry) Supports(mediaType content.MediaType) bool {
	_, ok := r.Get(mediaType)
	return ok
}

// SupportedMediaTypes returns the canonical media types this registry can
// extract, sorted for deterministic output. The values are canonical (as stored
// by Register), so they round-trip through Get/Supports.
func (r *Registry) SupportedMediaTypes() []content.MediaType {
	out := make([]content.MediaType, 0, len(r.extractors))
	for mt := range r.extractors {
		out = append(out, mt)
	}
	slices.Sort(out)
	return out
}

// TextArtifacts returns the artifacts that carry an inline text payload — those
// with no Blob handle and non-empty Text. The test is structural (payload shape),
// not semantic: it does not inspect MediaType, so inline OCR, transcript, or
// caption text counts too. Callers that care about media type filter further.
func TextArtifacts(artifacts []content.Artifact) []content.Artifact {
	out := make([]content.Artifact, 0, len(artifacts))
	for i := range artifacts {
		a := &artifacts[i]
		if a.Blob == nil && a.Text != "" {
			out = append(out, *a)
		}
	}
	return out
}

// SingleTextArtifact returns the one inline-text artifact in artifacts. It
// returns ErrNoTextArtifact when there is none and ErrMultipleTextArtifacts when
// there is more than one — the multi-artifact case is the application's policy to
// resolve, not a choice the library makes for it. It is the convenience for the
// common "this source yields exactly one text payload" path.
func SingleTextArtifact(artifacts []content.Artifact) (content.Artifact, error) {
	texts := TextArtifacts(artifacts)
	switch len(texts) {
	case 0:
		return content.Artifact{}, ErrNoTextArtifact
	case 1:
		return texts[0], nil
	default:
		return content.Artifact{}, ErrMultipleTextArtifacts
	}
}
