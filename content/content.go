// Package content defines the core, storage-neutral content model for
// maestro-cms: a single-parent provenance tree of sources and derived
// artifacts. IDs are app-assigned and opaque; the library validates their
// presence but never mints them, and never computes or mutates a source's
// content hash. See docs/adr/0003-content-as-single-parent-provenance-tree.md.
package content

import (
	"errors"
	"maps"
)

// MediaType is an IANA media (MIME) type, for example "application/pdf",
// "text/plain", or "image/png". It is the hinge that lets the model carry
// non-text content as naturally as text.
type MediaType string

// StoreHandle is an opaque reference to bytes held in an object store. The
// content package never interprets Key; only the matching store adapter, named
// by Backend, resolves it.
type StoreHandle struct {
	// Backend names the store adapter that can resolve Key, e.g. "gcs" or "fs".
	Backend string
	// Key is the adapter-defined locator for the bytes.
	Key string
}

// Source is the original content an application knows about: an uploaded file, a
// fetched page, a recording, and so on.
type Source struct {
	// ID is the stable, opaque, app-assigned identifier for this source.
	ID string
	// MediaType is the IANA media type of the raw bytes.
	MediaType MediaType
	// Hash is a caller-owned content hash of the raw bytes. The library does not
	// compute or mutate it.
	Hash string
	// Raw locates the raw bytes in an object store.
	Raw StoreHandle
	// Metadata is a neutral label carrier. It holds no grouping or policy
	// semantics; those belong to the application.
	//
	// Metadata is a map, so although Source is otherwise value-semantic, a
	// plain copy of a Source aliases the same Metadata map and mutations are
	// shared. Use Clone for an independent copy. Metadata is caller-owned: the
	// library never mutates it.
	Metadata map[string]string
}

// Clone returns a copy of the source that shares no mutable state with the
// receiver: its Metadata is an independent map. A nil Metadata stays nil.
func (s Source) Clone() Source {
	s.Metadata = maps.Clone(s.Metadata)
	return s
}

// Artifact is something derived from a Source or another Artifact, such as
// extracted text, an OCR result, a transcript, or a thumbnail. Provenance is
// single-parent: DerivedFrom names exactly one parent.
type Artifact struct {
	// ID is the stable, opaque, app-assigned identifier for this artifact.
	ID string
	// MediaType is the IANA media type of this artifact's payload.
	MediaType MediaType
	// DerivedFrom is the ID of the single parent Source or Artifact.
	DerivedFrom string
	// Text holds the payload for textual artifacts. It is the alternative to
	// Blob.
	Text string
	// Blob locates the payload for binary artifacts. It is the alternative to
	// Text.
	Blob *StoreHandle
	// Metadata is a neutral label carrier, as on Source. The same aliasing
	// caveat applies: copy with Clone for an independent Metadata map.
	Metadata map[string]string
}

// Clone returns a copy of the artifact that shares no mutable state with the
// receiver: its Metadata is an independent map and its Blob (if set) is an
// independent pointer. A nil Metadata or Blob stays nil.
func (a Artifact) Clone() Artifact {
	a.Metadata = maps.Clone(a.Metadata)
	if a.Blob != nil {
		b := *a.Blob
		a.Blob = &b
	}
	return a
}

// Validation errors returned by Source.Validate and Artifact.Validate.
var (
	// ErrMissingID indicates a required app-assigned ID was empty.
	ErrMissingID = errors.New("content: missing ID")
	// ErrMissingMediaType indicates a required media type was empty.
	ErrMissingMediaType = errors.New("content: missing media type")
	// ErrMissingParent indicates an artifact had no DerivedFrom parent.
	ErrMissingParent = errors.New("content: artifact missing DerivedFrom parent")
)

// Validate is a structural, identity-and-provenance check only: it reports
// whether the source carries an app-assigned ID and a media type. It is
// deliberately not a payload or content check — Hash and Raw are caller-owned
// and not inspected here.
func (s Source) Validate() error {
	if s.ID == "" {
		return ErrMissingID
	}
	if s.MediaType == "" {
		return ErrMissingMediaType
	}
	return nil
}

// Validate is a structural, identity-and-provenance check only: it reports
// whether the artifact carries an app-assigned ID, a media type, and a
// single-parent DerivedFrom link. It deliberately does NOT validate the
// payload: an artifact with neither Text nor Blob, or with both, passes. The
// Text/Blob "alternatives" relationship is a documented convention, not an
// invariant enforced here, because Text cannot distinguish "unset" from
// "intentionally empty". Payload validation, if needed, belongs to the
// producer (e.g. extract) once it defines what a well-formed artifact means.
func (a Artifact) Validate() error {
	if a.ID == "" {
		return ErrMissingID
	}
	if a.MediaType == "" {
		return ErrMissingMediaType
	}
	if a.DerivedFrom == "" {
		return ErrMissingParent
	}
	return nil
}
