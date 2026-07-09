package objstore_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sbordeyne/vlbackup/pkg/objstore"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
)

const (
	minioUser     = "minioadmin"
	minioPassword = "minioadmin"
)

// startMinIO starts a MinIO container and sets the S3 backend env vars.
// Returns the endpoint (host:port).
func startMinIO(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "minio/minio:latest",
			Cmd:   []string{"server", "/data"},
			Env: map[string]string{
				"MINIO_ROOT_USER":     minioUser,
				"MINIO_ROOT_PASSWORD": minioPassword,
			},
			ExposedPorts: []string{"9000/tcp"},
			WaitingFor: wait.ForHTTP("/minio/health/ready").
				WithPort("9000/tcp").
				WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("failed to start MinIO container: %v", err)
	}
	endpoint, err := c.PortEndpoint(ctx, "9000/tcp", "")
	if err != nil {
		t.Fatalf("failed to get container endpoint: %v", err)
	}

	t.Setenv("S3_ENDPOINT", endpoint)
	t.Setenv("S3_USE_SSL", "false")
	t.Setenv("AWS_ACCESS_KEY_ID", minioUser)
	t.Setenv("AWS_SECRET_ACCESS_KEY", minioPassword)
	return endpoint
}

func createMinIOBucket(t *testing.T, endpoint, bucket string) {
	t.Helper()
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioUser, minioPassword, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}
}

func TestS3Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	endpoint := startMinIO(t)
	createMinIOBucket(t, endpoint, "test-bucket")
	ctx := context.Background()

	repo, prefix, err := objstore.Open(ctx, "s3://test-bucket/backups/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()
	if prefix != "backups" {
		t.Errorf("prefix = %q, want %q", prefix, "backups")
	}

	testCRUDRoundTrip(t, repo)

	// Streaming pipe path end to end: tar.gz a directory tree through Upload,
	// then Download + ExtractDir and compare byte-for-byte.
	t.Run("streamdir round trip", func(t *testing.T) {
		srcDir := t.TempDir()
		files := map[string][]byte{
			"parts.json":          []byte(`["part1","part2"]`),
			"datadb/part1/index":  bytes.Repeat([]byte("idx"), 1024),
			"datadb/part1/values": bytes.Repeat([]byte("val"), 4096),
		}
		for name, content := range files {
			path := filepath.Join(srcDir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		const key = "backups/20260703.tar.gz"
		pr, pw := io.Pipe()
		go func() {
			_, err := transfer.StreamDir(srcDir, pw)
			pw.CloseWithError(err)
		}()
		if err := repo.Upload(ctx, key, pr); err != nil {
			t.Fatalf("Upload: %v", err)
		}

		r, err := repo.Download(ctx, key)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		defer func() { _ = r.Close() }()
		destDir := t.TempDir()
		if _, _, err := transfer.ExtractDir(r, destDir); err != nil {
			t.Fatalf("ExtractDir: %v", err)
		}
		for name, want := range files {
			got, err := os.ReadFile(filepath.Join(destDir, name))
			if err != nil {
				t.Errorf("reading extracted %s: %v", name, err)
				continue
			}
			if !bytes.Equal(got, want) {
				t.Errorf("extracted %s differs from original (%d vs %d bytes)", name, len(got), len(want))
			}
		}
	})
}
