package openapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/objstore"
	"github.com/sbordeyne/vlbackup/pkg/openapi"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

const (
	minioUser     = "minioadmin"
	minioPassword = "minioadmin"
)

// startMinIO starts a MinIO container and sets the S3 backend env vars.
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

// TestTriggerIntegration runs the full snapshot→object-storage flow against a
// real VictoriaLogs container and a MinIO container. It is the regression test
// for the old zero-byte GCS upload: the stored object must be a non-empty
// tar.gz whose contents match the snapshot directory.
func TestTriggerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dataDir := t.TempDir()
	vlURL := startVictoriaLogsWithVolume(t, dataDir)
	endpoint := startMinIO(t)

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioUser, minioPassword, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := minioClient.MakeBucket(context.Background(), "backups", minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// VictoriaLogs reports snapshot paths as it sees them (/data/...);
	// rewrite them to the host-side bind mount for the in-process handler.
	transfer.SnapshotPathResolver = func(p string) string {
		return filepath.Join(dataDir, strings.TrimPrefix(p, containerDataPath))
	}
	t.Cleanup(func() { transfer.SnapshotPathResolver = func(p string) string { return p } })

	day := time.Now().UTC().AddDate(0, 0, -1)
	partition := day.Format("20060102")
	ingestLogs(t, vlURL, day, 10)
	waitForFlushedData(t, dataDir, partition)

	args := cli.Args{
		VictoriaLogsURL: mustParseURL(t, vlURL),
		DataPath:        dataDir,
	}
	handler := openapi.NewHandler(openapi.NewServer(args, metrics.New(prometheus.NewRegistry())), "")

	body, _ := json.Marshal(openapi.SnapshotRequest{
		Range:          openapi.TimeRange{From: "now-1d/d"},
		DestinationUrl: "s3://backups/logs/",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/snapshot", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("snapshot status = %d, body %s", rec.Code, rec.Body.String())
	}

	// The bucket must contain a non-empty per-partition tar.gz under the
	// trimmed prefix.
	repo, prefix, err := objstore.Open(context.Background(), "s3://backups/logs/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	wantKey := prefix + "/" + partition + ".tar.gz"
	found := false
	for info, err := range repo.List(context.Background(), prefix) {
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if info.Key == wantKey {
			found = true
			if info.Size == 0 {
				t.Errorf("object %s is empty", info.Key)
			}
		}
	}
	if !found {
		t.Fatalf("object %s not found in bucket", wantKey)
	}

	// Extract the archive and check it contains the snapshot's datadb parts
	// metadata.
	r, err := repo.Download(context.Background(), wantKey)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = r.Close() }()
	extractDir := t.TempDir()
	if _, err := transfer.ExtractDir(r, extractDir); err != nil {
		t.Fatalf("ExtractDir: %v", err)
	}
	partsFile := filepath.Join(extractDir, "datadb", "parts.json")
	contents, err := os.ReadFile(partsFile)
	if err != nil {
		t.Fatalf("extracted archive missing datadb/parts.json: %v", err)
	}
	var parts []string
	if err := json.Unmarshal(contents, &parts); err != nil || len(parts) == 0 {
		t.Errorf("extracted parts.json invalid or empty: %q (err: %v)", contents, err)
	}

	// Snapshot must be cleaned up on the VictoriaLogs side.
	vmClient, err := victoriametrics.NewClient(context.Background(), vlURL)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := vmClient.ListSnapshots("")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Errorf("snapshots not cleaned up: %v", snapshots)
	}
}
