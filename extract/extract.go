// Package extract turns source bytes into derived content artifacts.
//
// It defines the Extractor interface and a media-type Registry, plus the
// genuinely standard-library extractors (currently plain text). Markdown,
// despite being stdlib-parseable, gets its own later extractor because its
// whitespace is semantic and demands deliberate handling rather than prose
// normalization. Format extractors that need third-party dependencies — HTML
// (golang.org/x/net/html), PDF, and so on — live in opt-in subpackages so that
// importing this package pulls only the standard library
// (see docs/adr/0006-optional-adapters-as-subpackages.md).
//
// Extractors return []content.Artifact rather than a single text blob so the
// model is multi-modal from the start: today only text artifacts are produced,
// but a future extractor may emit several artifacts (text plus images, OCR,
// and so on) from one source (see
// docs/adr/0003-content-as-single-parent-provenance-tree.md).
//
// Identity is the caller's: returned artifacts have DerivedFrom set to the
// supplied parent ID, MediaType set to what was produced, and ID left empty.
// The library never mints IDs; the caller assigns them before validating or
// persisting the artifacts.
//
// The Registry is the safe front door: it canonicalizes media types and bounds
// the input size (DefaultMaxBytes, overridable). Calling an Extractor directly
// honors context cancellation but does not bound input size — direct callers
// own that.
package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/SnapdragonPartners/maestro-cms/content"
)

// MediaTypeText is the media type of the text artifacts produced by the
// text-yielding extractors in this package.
const MediaTypeText content.MediaType = "text/plain"

// DefaultMaxBytes is the default cap the Registry applies to source bytes,
// guarding against unbounded memory use on a hostile or accidental huge input.
const DefaultMaxBytes int64 = 32 << 20 // 32 MiB

// Sentinel errors returned by extractors and the registry.
var (
	// ErrUnsupportedMediaType indicates no extractor is registered for a media
	// type.
	ErrUnsupportedMediaType = errors.New("extract: unsupported media type")
	// ErrInvalidMediaType indicates a caller passed a media type that cannot be
	// parsed. (At Register, an invalid media type panics instead — it is a
	// wiring bug.)
	ErrInvalidMediaType = errors.New("extract: invalid media type")
	// ErrNoContent indicates extraction completed but yielded no usable text.
	ErrNoContent = errors.New("extract: no extractable content")
	// ErrMissingParentID indicates Extract was called with an empty parentID.
	// Extraction's whole job is to derive artifacts from a known parent, so an
	// empty parent ID can only produce artifacts whose DerivedFrom never
	// validates; the registry rejects it up front.
	ErrMissingParentID = errors.New("extract: missing parent ID")
	// ErrSourceTooLarge indicates the source exceeded the configured byte limit.
	// It is matched with errors.Is; the concrete error carries the limit (see
	// SourceTooLargeError) for diagnostics.
	ErrSourceTooLarge = errors.New("extract: source exceeds size limit")
	// ErrMalformedSource indicates a format extractor could not parse the source
	// (corrupt PDF, unzippable DOCX, and so on). It is matched with errors.Is;
	// the concrete error carries the media type and cause (see
	// MalformedSourceError).
	ErrMalformedSource = errors.New("extract: malformed source")
)

// MalformedSourceError reports that a format extractor failed to parse a source,
// carrying the media type and underlying cause. It satisfies
// errors.Is(err, ErrMalformedSource) and unwraps to the cause. Format
// subpackages (extract/html, extract/pdf, …) return it for parser-level
// failures, distinct from ErrNoContent (a clean parse that yielded no text).
type MalformedSourceError struct {
	MediaType content.MediaType
	Err       error
}

func (e *MalformedSourceError) Error() string {
	return fmt.Sprintf("extract: malformed %s source: %v", e.MediaType, e.Err)
}

// Is reports a match against the ErrMalformedSource sentinel.
func (e *MalformedSourceError) Is(target error) bool { return target == ErrMalformedSource }

// Unwrap returns the underlying parser error.
func (e *MalformedSourceError) Unwrap() error { return e.Err }

// SourceTooLargeError reports that a source exceeded the byte limit, carrying the
// limit that was applied. It satisfies errors.Is(err, ErrSourceTooLarge).
type SourceTooLargeError struct {
	Limit int64
}

func (e *SourceTooLargeError) Error() string {
	return fmt.Sprintf("extract: source exceeds size limit of %d bytes", e.Limit)
}

// Is reports a match against the ErrSourceTooLarge sentinel.
func (e *SourceTooLargeError) Is(target error) bool { return target == ErrSourceTooLarge }

// Option configures a Registry or a single Extract call.
type Option func(*options)

type options struct {
	maxBytes int64
}

// WithMaxBytes sets the maximum number of source bytes the Registry reads before
// returning ErrSourceTooLarge. On a Registry it sets the default; on an Extract
// call it overrides that default for the call. n must be positive; WithMaxBytes
// panics on n <= 0 (a wiring bug). There is intentionally no "0 means unlimited"
// — a shared library should not make unbounded reads easy to request by accident.
func WithMaxBytes(n int64) Option {
	if n <= 0 {
		panic(fmt.Sprintf("extract: WithMaxBytes requires a positive limit, got %d", n))
	}
	return func(o *options) { o.maxBytes = n }
}

// canonicalMediaType normalizes a media type for registry indexing: it lowercases
// the type/subtype and drops parameters and surrounding whitespace, so
// "Text/Plain; charset=utf-8" and "text/plain" map to the same key. It returns
// an error if the value cannot be parsed as a media type; callers decide whether
// that is a panic (Register) or a returned error (Get/Extract).
func canonicalMediaType(mt content.MediaType) (content.MediaType, error) {
	parsed, _, err := mime.ParseMediaType(string(mt))
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrInvalidMediaType, mt, err)
	}
	return content.MediaType(parsed), nil
}

// Extractor reads source bytes and returns the artifacts derived from them.
//
// Implementations must:
//   - Accept any io.Reader (file, buffer, network stream).
//   - Honor ctx cancellation while reading.
//   - Set DerivedFrom to parentID and MediaType to what they produce, and
//     leave Artifact.ID empty for the caller to assign.
//   - Be safe for concurrent use (stateless or properly synchronized).
//
// Implementations must not:
//   - Access storage, databases, or external services.
//   - Mint IDs or compute source identity (hashes).
//   - Execute embedded scripts or macros.
//   - Bound input size themselves — size is the Registry's concern (a direct
//     caller that needs a bound wraps the reader, e.g. via io.LimitReader).
type Extractor interface {
	// Extract reads from r and returns the artifacts derived from a source
	// identified by parentID. It returns ErrNoContent if extraction yields no
	// usable content.
	Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error)
}

// Registry maps media types to extractors and applies the size limit. The zero
// value is not usable; use NewRegistry. A Registry is not safe for concurrent
// registration, but is safe for concurrent Get/Extract once populated.
type Registry struct {
	extractors map[content.MediaType]Extractor
	maxBytes   int64
}

// NewRegistry returns an empty registry. By default Extract bounds input at
// DefaultMaxBytes; pass WithMaxBytes to change the default.
func NewRegistry(opts ...Option) *Registry {
	cfg := options{maxBytes: DefaultMaxBytes}
	for _, fn := range opts {
		fn(&cfg)
	}
	return &Registry{
		extractors: make(map[content.MediaType]Extractor),
		maxBytes:   cfg.maxBytes,
	}
}

// Register associates a media type with an extractor. The media type is
// canonicalized (lowercased, parameters stripped), so "text/plain" and
// "Text/Plain; charset=utf-8" register the same entry.
//
// Register is for wiring-time setup and panics on misuse rather than returning
// an error: if e is nil, if mediaType cannot be parsed, or if its canonical form
// was already registered (a duplicate, which usually signals two registrations
// that differ only by casing or parameters and would otherwise silently clobber
// each other).
func (r *Registry) Register(mediaType content.MediaType, e Extractor) {
	if e == nil {
		panic("extract: Register called with nil Extractor for " + string(mediaType))
	}
	key, err := canonicalMediaType(mediaType)
	if err != nil {
		panic("extract: Register called with " + err.Error())
	}
	if _, dup := r.extractors[key]; dup {
		panic(fmt.Sprintf("extract: Register called twice for media type %q (canonical %q)", mediaType, key))
	}
	r.extractors[key] = e
}

// Get returns the extractor registered for mediaType, and whether one was found.
// The media type is canonicalized before lookup, mirroring Register. An
// unparseable media type returns (nil, false) — Get does not distinguish invalid
// from unregistered; use Extract for a typed error.
func (r *Registry) Get(mediaType content.MediaType) (Extractor, bool) {
	key, err := canonicalMediaType(mediaType)
	if err != nil {
		return nil, false
	}
	e, ok := r.extractors[key]
	return e, ok
}

// Extract looks up the extractor for mediaType, bounds the reader at the
// configured size limit, and runs it. It returns ErrMissingParentID if parentID
// is empty, ErrInvalidMediaType if mediaType is unparseable, ErrUnsupportedMediaType
// if no extractor is registered, and ErrSourceTooLarge if the source exceeds the
// limit. Per-call opts override the registry default (e.g. WithMaxBytes).
func (r *Registry) Extract(ctx context.Context, mediaType content.MediaType, reader io.Reader, parentID string, opts ...Option) ([]content.Artifact, error) {
	// Reject up front, before any lookup or reading: extraction must have a
	// parent to derive from, or it can only produce invalid artifacts.
	if parentID == "" {
		return nil, ErrMissingParentID
	}
	cfg := options{maxBytes: r.maxBytes}
	for _, fn := range opts {
		fn(&cfg)
	}
	key, err := canonicalMediaType(mediaType)
	if err != nil {
		return nil, err // ErrInvalidMediaType — caller input, not a panic
	}
	e, ok := r.extractors[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMediaType, key)
	}
	reader = &limitReader{r: reader, remaining: cfg.maxBytes, limit: cfg.maxBytes}
	// Pure dispatch to a same-package Extractor: the error is already ours
	// (ErrNoContent / ErrSourceTooLarge / ctx error), so re-wrapping would only
	// add a redundant layer with no extra context.
	return e.Extract(ctx, reader, parentID) //nolint:wrapcheck // pure dispatch; error is already package-local
}

// TextArtifact builds a single text/plain artifact derived from parentID, with
// ID left empty for the caller to assign. It is exported for use by extractor
// subpackages (extract/html, extract/pdf, …) so they emit artifacts in the same
// shape as the core text extractor.
func TextArtifact(parentID, text string) content.Artifact {
	return content.Artifact{
		MediaType:   MediaTypeText,
		DerivedFrom: parentID,
		Text:        text,
	}
}

// NormalizeWhitespace trims each line and collapses runs of blank lines into a
// single blank line, then trims the whole result. It preserves paragraph
// structure while removing incidental whitespace. It is exported so extractor
// subpackages produce uniformly-shaped text.
func NormalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blanks++
			if blanks <= 1 {
				out = append(out, "")
			}
			continue
		}
		blanks = 0
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// ReadAll buffers r into memory, honoring ctx cancellation. Size bounding is the
// Registry's job (it wraps r in a limitReader), so ReadAll itself imposes no cap.
// It is exported for extractor subpackages, which buffer the source before
// handing it to a format parser (HTML, PDF, DOCX all need the whole input).
func ReadAll(ctx context.Context, r io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("extract: read aborted: %w", err)
	}
	data, err := io.ReadAll(&ctxReader{ctx: ctx, r: r})
	if err != nil {
		return nil, fmt.Errorf("extract: read source: %w", err)
	}
	return data, nil
}

// ctxReader wraps an io.Reader so each Read first observes ctx cancellation.
// io.Reader has no context parameter, so this is the standard bridge for making
// a buffered read cancellable.
//
// Cancellation is cooperative: the check happens between Read calls, so a reader
// already blocked inside Read (e.g. a stalled network socket that ignores
// deadlines) is not forcibly interrupted. It aborts promptly only between reads.
//
//nolint:containedctx // intentional ctx bridge for io.Reader, which has no ctx param
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *ctxReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err //nolint:wrapcheck // sentinel ctx error; readAll adds context
	}
	return cr.r.Read(p) //nolint:wrapcheck // pass-through of underlying reader, incl io.EOF
}

// limitReader returns a *SourceTooLargeError once more than the permitted number
// of bytes is read, instead of silently truncating. Exactly limit bytes is
// allowed; one more triggers the error.
type limitReader struct {
	r         io.Reader
	remaining int64
	limit     int64
}

func (lr *limitReader) Read(p []byte) (int, error) {
	if lr.remaining < 0 {
		return 0, &SourceTooLargeError{Limit: lr.limit}
	}
	// Permit reading one byte past the allowance so an exactly-at-limit source
	// succeeds while an over-limit source is detected rather than truncated.
	if int64(len(p)) > lr.remaining+1 {
		p = p[:lr.remaining+1]
	}
	n, err := lr.r.Read(p)
	lr.remaining -= int64(n)
	if lr.remaining < 0 {
		return n, &SourceTooLargeError{Limit: lr.limit}
	}
	return n, err //nolint:wrapcheck // pass-through of underlying reader, incl io.EOF
}
