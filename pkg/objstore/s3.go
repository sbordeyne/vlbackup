package objstore

import (
	"context"
	"fmt"
	"io"
	"iter"
	"net/url"
	"os"
	"strconv"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func init() {
	Register("s3", newS3Repository)
}

// s3UploadPartSize bounds the in-memory buffer used for streaming multipart
// uploads of unknown length (minio's default sizing is ~576MiB, too large for
// a sidecar).
const s3UploadPartSize = 64 << 20

// s3Repository is a Repository backed by any S3-compatible store. It is
// configured via environment variables: AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, AWS_REGION, plus S3_ENDPOINT
// (default s3.amazonaws.com) and S3_USE_SSL (default true).
type s3Repository struct {
	client *minio.Client
	bucket string
}

func newS3Repository(ctx context.Context, u *url.URL) (Repository, error) {
	endpoint := os.Getenv("S3_ENDPOINT")
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	useSSL := true
	if v := os.Getenv("S3_USE_SSL"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("parsing S3_USE_SSL: %w", err)
		}
		useSSL = parsed
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewEnvAWS(),
		Secure: useSSL,
		Region: os.Getenv("AWS_REGION"),
	})
	if err != nil {
		return nil, fmt.Errorf("creating S3 client: %w", err)
	}
	return &s3Repository{
		client: client,
		bucket: u.Host,
	}, nil
}

func (s *s3Repository) Upload(ctx context.Context, key string, r io.Reader) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, -1, minio.PutObjectOptions{
		ContentType: "application/gzip",
		PartSize:    s3UploadPartSize,
	})
	return err
}

func (s *s3Repository) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, s.mapNotFound(err, key)
	}
	// GetObject is lazy; Stat surfaces NoSuchKey before we hand out the reader.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, s.mapNotFound(err, key)
	}
	return obj, nil
}

func (s *s3Repository) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err != nil {
				yield(ObjectInfo{}, obj.Err)
				return
			}
			if !yield(ObjectInfo{Key: obj.Key, Size: obj.Size, LastModified: obj.LastModified}, nil) {
				return
			}
		}
	}
}

func (s *s3Repository) Delete(ctx context.Context, key string) error {
	// S3 delete is idempotent: deleting a missing object usually succeeds,
	// so ErrNotFound is best-effort here.
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return s.mapNotFound(err, key)
	}
	return nil
}

func (s *s3Repository) Close() error {
	return nil
}

func (s *s3Repository) mapNotFound(err error, key string) error {
	code := minio.ToErrorResponse(err).Code
	if code == "NoSuchKey" || code == "NoSuchBucket" {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return err
}
