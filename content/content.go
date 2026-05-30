// Package content defines the core, storage-neutral content model for
// maestro-cms: a single-parent provenance tree of sources and derived
// artifacts. IDs are app-assigned and opaque; the library validates their
// presence but never mints them, and never computes or mutates a source's
// content hash. See docs/adr/0003-content-as-single-parent-provenance-tree.md.
package content

import "errors"

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
	Metadata map[string]string
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
	// Metadata is a neutral label carrier, as on Source.
	Metadata map[string]string
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

// Validate reports whether the source carries the fields the library requires:
// an app-assigned ID and a media type. Hash and Raw are caller-owned and are
// not checked here.
func (s Source) Validate() error {
	if s.ID == "" {
		return ErrMissingID
	}
	if s.MediaType == "" {
		return ErrMissingMediaType
	}
	return nil
}

// Validate reports whether the artifact carries the fields the library
// requires: an app-assigned ID, a media type, and a single-parent DerivedFrom
// link.
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
