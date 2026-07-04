package objstore

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestNewS3RepositoryBadSSL(t *testing.T) {
	t.Setenv("S3_USE_SSL", "not-a-bool")
	u, _ := url.Parse("s3://bucket")
	if _, err := newS3Repository(context.Background(), u); err == nil ||
		!strings.Contains(err.Error(), "parsing S3_USE_SSL") {
		t.Errorf("err = %v, want S3_USE_SSL parse error", err)
	}
}

func TestNewS3RepositoryDefaults(t *testing.T) {
	// No S3_ENDPOINT / S3_USE_SSL: endpoint defaults to s3.amazonaws.com,
	// SSL defaults to true. Client construction is lazy so this succeeds.
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("S3_USE_SSL", "")
	u, _ := url.Parse("s3://my-bucket")
	repo, err := newS3Repository(context.Background(), u)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if r := repo.(*s3Repository); r.bucket != "my-bucket" {
		t.Errorf("bucket = %q, want my-bucket", r.bucket)
	}
}

func TestNewS3RepositoryBadEndpoint(t *testing.T) {
	// A fully-qualified URL as endpoint is rejected by minio.New.
	t.Setenv("S3_ENDPOINT", "http://bad/path")
	t.Setenv("S3_USE_SSL", "")
	u, _ := url.Parse("s3://b")
	if _, err := newS3Repository(context.Background(), u); err == nil ||
		!strings.Contains(err.Error(), "creating S3 client") {
		t.Errorf("err = %v, want S3 client creation error", err)
	}
}

func TestNewS3RepositoryCustomEndpoint(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "minio.local:9000")
	t.Setenv("S3_USE_SSL", "false")
	u, _ := url.Parse("s3://b")
	repo, err := newS3Repository(context.Background(), u)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
}
