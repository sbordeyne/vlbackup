package victoriametrics_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

func victoriaLogsImage() string {
	if img := os.Getenv("VICTORIALOGS_IMAGE"); img != "" {
		return img
	}
	return "victoriametrics/victoria-logs:latest"
}

// startVictoriaLogs starts a VictoriaLogs container and returns its base URL.
// extraArgs are appended to the container command line.
func startVictoriaLogs(t *testing.T, extraArgs ...string) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	cmd := append([]string{
		"-storageDataPath=/data",
		// flush ingested logs to disk quickly so partitions show up fast
		"-inmemoryDataFlushInterval=1s",
	}, extraArgs...)

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        victoriaLogsImage(),
			Cmd:          cmd,
			ExposedPorts: []string{"9428/tcp"},
			WaitingFor: wait.ForHTTP("/health").
				WithPort("9428/tcp").
				WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("failed to start VictoriaLogs container: %v", err)
	}

	baseURL, err := container.PortEndpoint(ctx, "9428/tcp", "http")
	if err != nil {
		t.Fatalf("failed to get container endpoint: %v", err)
	}
	return baseURL
}

// ingestLogs pushes count log lines timestamped at day via the jsonline API.
func ingestLogs(t *testing.T, baseURL string, day time.Time, count int) {
	t.Helper()
	var buf bytes.Buffer
	for i := range count {
		fmt.Fprintf(&buf, `{"_time":%q,"_msg":"test log line %d","source":"vlbackup-test"}`+"\n",
			day.Format(time.RFC3339), i)
	}
	response, err := http.Post(baseURL+"/insert/jsonline", "application/x-ndjson", &buf)
	if err != nil {
		t.Fatalf("failed to ingest logs: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("failed to ingest logs: %s", response.Status)
	}
}

// waitForPartitions polls ListPartitions until at least want partitions exist.
func waitForPartitions(t *testing.T, client *victoriametrics.Client, authKey string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		partitions, err := client.ListPartitions(authKey)
		if err == nil && len(partitions) >= want {
			return partitions
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d partitions, last result: %v (err: %v)", want, partitions, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestNewClient(t *testing.T) {
	t.Parallel()
	if _, err := victoriametrics.NewClient(context.Background(), "http://localhost:9428"); err != nil {
		t.Errorf("NewClient with valid URL returned error: %v", err)
	}
	if _, err := victoriametrics.NewClient(context.Background(), "http://[invalid"); err == nil {
		t.Error("NewClient with invalid URL did not return an error")
	}
}

func TestClientIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	baseURL := startVictoriaLogs(t)
	client, err := victoriametrics.NewClient(context.Background(), baseURL)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	today := time.Now().UTC()
	yesterday := today.AddDate(0, 0, -1)
	todayPartition := today.Format("20060102")
	yesterdayPartition := yesterday.Format("20060102")

	ingestLogs(t, baseURL, today, 10)
	ingestLogs(t, baseURL, yesterday, 10)
	waitForPartitions(t, &client, "", 2)

	t.Run("ListPartitions", func(t *testing.T) {
		partitions, err := client.ListPartitions("")
		if err != nil {
			t.Fatalf("ListPartitions returned error: %v", err)
		}
		for _, want := range []string{todayPartition, yesterdayPartition} {
			if !slices.Contains(partitions, want) {
				t.Errorf("partition %s missing from %v", want, partitions)
			}
		}
	})

	t.Run("ListSnapshotsEmpty", func(t *testing.T) {
		snapshots, err := client.ListSnapshots("")
		if err != nil {
			t.Fatalf("ListSnapshots returned error: %v", err)
		}
		if len(snapshots) != 0 {
			t.Errorf("expected no snapshots before creation, got %v", snapshots)
		}
	})

	t.Run("CreateSnapshotAllPartitions", func(t *testing.T) {
		// "20" is a prefix of every 20xx-dated daily partition
		paths, err := client.CreateSnapshot("20", "")
		if err != nil {
			t.Fatalf("CreateSnapshot returned error: %v", err)
		}
		if len(paths) < 2 {
			t.Fatalf("expected at least 2 snapshot paths (one per partition), got %v", paths)
		}
		for _, path := range paths {
			if path == "" {
				t.Errorf("got empty snapshot path in %v", paths)
			}
		}

		snapshots, err := client.ListSnapshots("")
		if err != nil {
			t.Fatalf("ListSnapshots returned error: %v", err)
		}
		if len(snapshots) != len(paths) {
			t.Errorf("ListSnapshots returned %v, want %d snapshots matching created paths %v",
				snapshots, len(paths), paths)
		}

		for _, path := range paths {
			if err := client.DeleteSnapshot(path, ""); err != nil {
				t.Errorf("DeleteSnapshot(%s) returned error: %v", path, err)
			}
		}
		snapshots, err = client.ListSnapshots("")
		if err != nil {
			t.Fatalf("ListSnapshots after delete returned error: %v", err)
		}
		if len(snapshots) != 0 {
			t.Errorf("expected no snapshots after deletion, got %v", snapshots)
		}
	})

	t.Run("CreateSnapshotSinglePartition", func(t *testing.T) {
		paths, err := client.CreateSnapshot(yesterdayPartition, "")
		if err != nil {
			t.Fatalf("CreateSnapshot returned error: %v", err)
		}
		if len(paths) != 1 {
			t.Fatalf("expected exactly 1 snapshot path for partition %s, got %v", yesterdayPartition, paths)
		}
		if err := client.DeleteSnapshot(paths[0], ""); err != nil {
			t.Errorf("DeleteSnapshot(%s) returned error: %v", paths[0], err)
		}
	})

	t.Run("DeleteSnapshotNonExistent", func(t *testing.T) {
		if err := client.DeleteSnapshot("/does/not/exist", ""); err == nil {
			t.Error("DeleteSnapshot with non-existent path did not return an error")
		}
	})

	t.Run("DeleteStaleSnapshots", func(t *testing.T) {
		if err := client.DeleteStaleSnapshots(""); err != nil {
			t.Errorf("DeleteStaleSnapshots returned error: %v", err)
		}
	})

	t.Run("DetachAttachPartition", func(t *testing.T) {
		if err := client.DetachPartition(yesterdayPartition, ""); err != nil {
			t.Fatalf("DetachPartition returned error: %v", err)
		}
		partitions, err := client.ListPartitions("")
		if err != nil {
			t.Fatalf("ListPartitions after detach returned error: %v", err)
		}
		if slices.Contains(partitions, yesterdayPartition) {
			t.Errorf("partition %s still listed after detach: %v", yesterdayPartition, partitions)
		}

		if err := client.AttachPartition(yesterdayPartition, ""); err != nil {
			t.Fatalf("AttachPartition returned error: %v", err)
		}
		partitions, err = client.ListPartitions("")
		if err != nil {
			t.Fatalf("ListPartitions after attach returned error: %v", err)
		}
		if !slices.Contains(partitions, yesterdayPartition) {
			t.Errorf("partition %s not listed after attach: %v", yesterdayPartition, partitions)
		}
	})
}

func TestClientIntegrationWithAuthKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	const authKey = "test-auth-key"

	baseURL := startVictoriaLogs(t, "-partitionManageAuthKey="+authKey)
	client, err := victoriametrics.NewClient(context.Background(), baseURL)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	yesterdayPartition := yesterday.Format("20060102")
	ingestLogs(t, baseURL, yesterday, 10)
	waitForPartitions(t, &client, authKey, 1)

	t.Run("ListPartitionsUnauthorized", func(t *testing.T) {
		if _, err := client.ListPartitions(""); err == nil {
			t.Error("ListPartitions without auth key did not return an error")
		}
	})

	t.Run("SnapshotLifecycleWithAuthKey", func(t *testing.T) {
		paths, err := client.CreateSnapshot(yesterdayPartition, authKey)
		if err != nil {
			t.Fatalf("CreateSnapshot with auth key returned error: %v", err)
		}
		if len(paths) != 1 {
			t.Fatalf("expected exactly 1 snapshot path, got %v", paths)
		}

		snapshots, err := client.ListSnapshots(authKey)
		if err != nil {
			t.Fatalf("ListSnapshots with auth key returned error: %v", err)
		}
		if !slices.Equal(snapshots, paths) {
			t.Errorf("ListSnapshots returned %v, want %v", snapshots, paths)
		}

		if err := client.DeleteSnapshot(paths[0], authKey); err != nil {
			t.Fatalf("DeleteSnapshot with auth key returned error: %v", err)
		}
		if err := client.DeleteStaleSnapshots(authKey); err != nil {
			t.Errorf("DeleteStaleSnapshots with auth key returned error: %v", err)
		}
	})

	t.Run("DetachPartitionWrongAuthKey", func(t *testing.T) {
		if err := client.DetachPartition(yesterdayPartition, "wrong-key"); err == nil {
			t.Error("DetachPartition with wrong auth key did not return an error")
		}
	})

	t.Run("DetachAttachPartitionWithAuthKey", func(t *testing.T) {
		if err := client.DetachPartition(yesterdayPartition, authKey); err != nil {
			t.Fatalf("DetachPartition with auth key returned error: %v", err)
		}
		if err := client.AttachPartition(yesterdayPartition, authKey); err != nil {
			t.Fatalf("AttachPartition with auth key returned error: %v", err)
		}
	})
}
