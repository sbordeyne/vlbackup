// Package objstore provides a swappable object-storage repository layer.
// Backends register themselves by URL scheme (gs://, s3://, ...) and are
// selected at runtime from a destination URL via Open.
package objstore

import (
	"context"
	"errors"
	"io"
	"iter"
	"time"
)

// ErrNotFound is returned by Download and Delete when the object does not exist.
// Note: on S3-compatible backends, deleting a missing object is idempotent and
// may not return ErrNotFound.
var ErrNotFound = errors.New("object not found")

// ErrUnsupportedScheme is returned by Open when no backend is registered for
// the destination URL scheme.
var ErrUnsupportedScheme = errors.New("unsupported destination URL scheme")

// ObjectInfo describes a stored object as returned by List.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Repository is a bucket-scoped object store. Keys are slash-separated and
// must not start with "/".
type Repository interface {
	// Upload streams r into the object at key, overwriting any existing object.
	Upload(ctx context.Context, key string, r io.Reader) error
	// Download returns the object body. Caller must Close it.
	// Returns ErrNotFound if the object does not exist.
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	// List yields objects under prefix (recursive). Iteration stops on the
	// first non-nil error.
	List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error]
	// Delete removes the object. Returns ErrNotFound if it does not exist,
	// where the backend can tell.
	Delete(ctx context.Context, key string) error
	// Close releases underlying client resources.
	Close() error
}
