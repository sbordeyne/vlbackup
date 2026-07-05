package objstore_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sbordeyne/vlbackup/pkg/objstore"
)

// startFakeGCS starts a fake-gcs-server container and points the GCS SDK at it
// via STORAGE_EMULATOR_HOST. Returns the emulator endpoint (host:port).
func startFakeGCS(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "fsouza/fake-gcs-server:latest",
			Cmd:          []string{"-scheme", "http", "-port", "4443"},
			ExposedPorts: []string{"4443/tcp"},
			WaitingFor: wait.ForListeningPort("4443/tcp").
				WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("failed to start fake-gcs-server container: %v", err)
	}
	endpoint, err := c.PortEndpoint(ctx, "4443/tcp", "")
	if err != nil {
		t.Fatalf("failed to get container endpoint: %v", err)
	}

	// Resumable uploads follow the Location header fake-gcs returns, which is
	// built from its external URL; point it at the mapped host port.
	cfg := fmt.Sprintf(`{"externalUrl": "http://%s"}`, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+endpoint+"/_internal/config", bytes.NewReader([]byte(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to update fake-gcs external URL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to update fake-gcs external URL: %s", resp.Status)
	}

	t.Setenv("STORAGE_EMULATOR_HOST", endpoint)
	return endpoint
}

func TestGCSIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	startFakeGCS(t)
	ctx := context.Background()

	// Pre-create the bucket directly through the SDK (emulator-backed).
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Bucket("test-bucket").Create(ctx, "test-project", nil); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	repo, prefix, err := objstore.Open(ctx, "gs://test-bucket")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = repo.Close() }()
	if prefix != "" {
		t.Errorf("prefix = %q, want empty", prefix)
	}

	testCRUDRoundTrip(t, repo)
}
