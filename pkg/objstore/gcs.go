package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/url"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func init() {
	Register("gs", newGCSRepository)
}

// gcsRepository is a Repository backed by Google Cloud Storage. Credentials
// come from Application Default Credentials; STORAGE_EMULATOR_HOST is honored
// natively by the SDK.
type gcsRepository struct {
	client *storage.Client
	bucket *storage.BucketHandle
}

func newGCSRepository(ctx context.Context, u *url.URL) (Repository, error) {
	// JSON reads instead of the default XML API: fake-gcs-server (used in
	// tests and the compose example) does not serve XML-API downloads.
	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		return nil, fmt.Errorf("creating GCS client: %w", err)
	}
	return &gcsRepository{
		client: client,
		bucket: client.Bucket(u.Host),
	}, nil
}

func (g *gcsRepository) Upload(ctx context.Context, key string, r io.Reader) error {
	w := g.bucket.Object(key).NewWriter(ctx)
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	// GCS commits the object on Close; a Close error means the upload failed.
	return w.Close()
}

func (g *gcsRepository) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := g.bucket.Object(key).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return r, err
}

func (g *gcsRepository) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		it := g.bucket.Objects(ctx, &storage.Query{Prefix: prefix})
		for {
			attrs, err := it.Next()
			if errors.Is(err, iterator.Done) {
				return
			}
			if err != nil {
				yield(ObjectInfo{}, err)
				return
			}
			if !yield(ObjectInfo{Key: attrs.Name, Size: attrs.Size, LastModified: attrs.Updated}, nil) {
				return
			}
		}
	}
}

func (g *gcsRepository) Delete(ctx context.Context, key string) error {
	err := g.bucket.Object(key).Delete(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return err
}

func (g *gcsRepository) Close() error {
	return g.client.Close()
}
