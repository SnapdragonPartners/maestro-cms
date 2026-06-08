package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// SHA-256 hex helpers. These are content-addressing primitives only: they
// compute a stable, lowercase-hex SHA-256 digest of some bytes. They carry no
// opinion about what the digest identifies — raw source identity (Source.Hash),
// an extracted artifact's identity, a chunk's identity, or a cache key are all
// application decisions. The library never computes or mutates Source.Hash on
// the app's behalf; these helpers just remove the boilerplate when the app does.

// SHA256HexBytes returns the lowercase-hex SHA-256 digest of b.
func SHA256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SHA256HexString returns the lowercase-hex SHA-256 digest of s.
func SHA256HexString(s string) string {
	return SHA256HexBytes([]byte(s))
}

// SHA256HexReader streams r through a SHA-256 hash without buffering it all in
// memory, returning the lowercase-hex digest and the number of bytes read. It
// honors ctx cancellation between reads (a reader already blocked inside Read is
// not interrupted). It does not bound input size; the caller wraps r (e.g. with
// io.LimitReader) if a bound is needed.
func SHA256HexReader(ctx context.Context, r io.Reader) (hexDigest string, n int64, err error) {
	if cerr := ctx.Err(); cerr != nil {
		return "", 0, fmt.Errorf("content: hash aborted: %w", cerr)
	}
	h := sha256.New()
	n, err = io.Copy(h, &ctxReader{ctx: ctx, r: r})
	if err != nil {
		return "", n, fmt.Errorf("content: hash read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ctxReader makes a streaming read cancellable: each Read first observes ctx.
// Cancellation is cooperative (checked between reads), the standard bridge for
// an io.Reader that has no context parameter.
//
//nolint:containedctx // intentional ctx bridge for io.Reader, which has no ctx param
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *ctxReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err //nolint:wrapcheck // sentinel ctx error; SHA256HexReader adds context
	}
	return cr.r.Read(p) //nolint:wrapcheck // pass-through of underlying reader, incl io.EOF
}
