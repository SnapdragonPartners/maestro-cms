package store

import (
	"context"
	"errors"
)

// IsNotFound reports whether err indicates a missing object, i.e. it matches
// ErrObjectNotFound. It is sugar over errors.Is for the common check.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrObjectNotFound)
}

// DeleteIfExists deletes key and treats a missing object as success. ObjectStore
// Delete is deliberately strict (it returns ErrObjectNotFound for an absent
// key); this helper is the idempotent variant for cleanup and rollback paths
// where "already gone" is the desired outcome. Any other error is returned
// unchanged.
func DeleteIfExists(ctx context.Context, s ObjectStore, key string) error {
	if err := s.Delete(ctx, key); err != nil && !errors.Is(err, ErrObjectNotFound) {
		return err //nolint:wrapcheck // pass-through of the store's own already-contextual Delete error
	}
	return nil
}
