package http_handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/http_handler"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

const containerDataPath = "/data"

func victoriaLogsImage() string {
	if img := os.Getenv("VICTORIALOGS_IMAGE"); img != "" {
		return img
	}
	return "victoriametrics/victoria-logs:latest"
}

// startVictoriaLogsWithVolume starts a VictoriaLogs container with hostDir
// bind-mounted at /data (as a sidecar would share it) and returns its base URL.
func startVictoriaLogsWithVolume(t *testing.T, hostDir string) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	// The container may run as a different uid than the test process.
	if err := os.Chmod(hostDir, 0o777); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: victoriaLogsImage(),
			Cmd: []string{
				"-storageDataPath=" + containerDataPath,
				"-inmemoryDataFlushInterval=1s",
			},
			ExposedPorts: []string{"9428/tcp"},
			// Run as the test process's uid:gid so files VictoriaLogs writes
			// into the bind-mounted /data (notably the "partitions" subdir it
			// creates) are owned by us, not root. Otherwise the in-process
			// receive handler can't MkdirTemp under partitions on Linux CI.
			ConfigModifier: func(cfg *container.Config) {
				cfg.User = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
			},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Binds = append(hc.Binds, hostDir+":"+containerDataPath)
			},
			WaitingFor: wait.ForHTTP("/health").
				WithPort("9428/tcp").
				WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("failed to start VictoriaLogs container: %v", err)
	}
	baseURL, err := c.PortEndpoint(ctx, "9428/tcp", "http")
	if err != nil {
		t.Fatalf("failed to get container endpoint: %v", err)
	}
	return baseURL
}

func ingestLogs(t *testing.T, baseURL string, day time.Time, count int) {
	t.Helper()
	var buf bytes.Buffer
	for i := range count {
		fmt.Fprintf(&buf, `{"_time":%q,"_msg":"test log line %d","source":"vlbackup-test"}`+"\n",
			day.Format(time.RFC3339), i)
	}
	resp, err := http.Post(baseURL+"/insert/jsonline", "application/x-ndjson", &buf)
	if err != nil {
		t.Fatalf("failed to ingest logs: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to ingest logs: %s", resp.Status)
	}
}

func waitForPartitions(t *testing.T, client *victoriametrics.Client, want int) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		partitions, err := client.ListPartitions("")
		if err == nil && len(partitions) >= want {
			return partitions
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d partitions, last result: %v (err: %v)", want, partitions, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitForFlushedData polls the partition's datadb parts list on the shared
// volume until VictoriaLogs has flushed in-memory log data to disk. Snapshots
// only capture on-disk parts, so transferring before the flush would ship an
// empty datadb.
func waitForFlushedData(t *testing.T, dataDir, day string) {
	t.Helper()
	partsFile := filepath.Join(dataDir, "partitions", day, "datadb", "parts.json")
	deadline := time.Now().Add(30 * time.Second)
	for {
		var parts []string
		contents, err := os.ReadFile(partsFile)
		// The file holds "null" until the first flush.
		if err == nil && json.Unmarshal(contents, &parts) == nil && len(parts) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for flushed datadb parts in %s (last: %q, err: %v)", partsFile, contents, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func mustParseURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

// TestTransferIntegration runs the full source→target flow against two real
// VictoriaLogs containers sharing their data volumes with the test process,
// exactly as the vlbackup sidecars would in Kubernetes.
func TestTransferIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	const authToken = "integration-test-token"

	sourceDir, targetDir := t.TempDir(), t.TempDir()
	sourceVL := startVictoriaLogsWithVolume(t, sourceDir)
	targetVL := startVictoriaLogsWithVolume(t, targetDir)

	sourceClient, err := victoriametrics.NewClient(context.Background(), sourceVL)
	if err != nil {
		t.Fatal(err)
	}
	targetClient, err := victoriametrics.NewClient(context.Background(), targetVL)
	if err != nil {
		t.Fatal(err)
	}

	// VictoriaLogs reports snapshot paths as it sees them (/data/...);
	// rewrite them to the host-side bind mount for the in-process handler.
	transfer.SnapshotPathResolver = func(p string) string {
		return filepath.Join(sourceDir, strings.TrimPrefix(p, containerDataPath))
	}
	t.Cleanup(func() { transfer.SnapshotPathResolver = func(p string) string { return p } })

	days := []string{
		time.Now().UTC().AddDate(0, 0, -2).Format("20060102"),
		time.Now().UTC().AddDate(0, 0, -1).Format("20060102"),
	}
	ingestLogs(t, sourceVL, time.Now().UTC().AddDate(0, 0, -2), 10)
	ingestLogs(t, sourceVL, time.Now().UTC().AddDate(0, 0, -1), 10)
	waitForPartitions(t, &sourceClient, 2)
	for _, day := range days {
		waitForFlushedData(t, sourceDir, day)
	}

	// Target vlbackup: wired exactly like main.go's /api/v1 group.
	targetArgs := cli.Args{
		VictoriaLogsURL: mustParseURL(t, targetVL),
		DataPath:        targetDir,
		TransferAuthKey: authToken,
	}
	targetMetrics := metrics.New(prometheus.NewRegistry())
	targetRouter := chi.NewRouter()
	targetRouter.Group(func(r chi.Router) {
		r.Use(http_handler.BearerAuth(authToken))
		r.Post("/api/v1/transfer/receive", http_handler.TransferReceiveHandlerFactory(targetArgs, targetMetrics))
		r.Post("/api/v1/transfer/attach", http_handler.TransferAttachHandlerFactory(targetArgs, targetMetrics))
	})
	targetSrv := httptest.NewServer(targetRouter)
	t.Cleanup(targetSrv.Close)

	// Source vlbackup transfer handler.
	sourceArgs := cli.Args{
		VictoriaLogsURL: mustParseURL(t, sourceVL),
		DataPath:        sourceDir,
		TransferAuthKey: authToken,
	}
	handler := http_handler.TransferHandlerFactory(sourceArgs, metrics.New(prometheus.NewRegistry()))

	body, _ := json.Marshal(http_handler.TransferRequestBody{
		TargetURL: targetSrv.URL,
		Range: http_handler.TransferRange{
			From: time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339),
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("transfer status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp http_handler.TransferResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.Transferred, days) {
		t.Errorf("transferred = %v, want %v", resp.Transferred, days)
	}
	if len(resp.Errors) > 0 {
		t.Errorf("unexpected errors: %v", resp.Errors)
	}

	// Partitions must now live on the target and be gone from the source.
	targetPartitions, err := targetClient.ListPartitions("")
	if err != nil {
		t.Fatal(err)
	}
	sourcePartitions, err := sourceClient.ListPartitions("")
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range days {
		if !slices.Contains(targetPartitions, day) {
			t.Errorf("partition %s missing on target: %v", day, targetPartitions)
		}
		if slices.Contains(sourcePartitions, day) {
			t.Errorf("partition %s still attached on source: %v", day, sourcePartitions)
		}
	}

	// Snapshots must be cleaned up on the source.
	snapshots, err := sourceClient.ListSnapshots("")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Errorf("source snapshots not cleaned up: %v", snapshots)
	}

	// Transferred logs must be queryable on the target.
	queryResp, err := http.PostForm(targetVL+"/select/logsql/query", url.Values{
		"query": {`source:vlbackup-test`},
		"limit": {"100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = queryResp.Body.Close() }()
	queryBody, _ := io.ReadAll(queryResp.Body)
	lines := 0
	for line := range bytes.SplitSeq(queryBody, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines++
		}
	}
	if lines != 20 {
		t.Errorf("target returned %d log lines, want 20; body starts: %.200s", lines, queryBody)
	}

	// A day whose partition already exists on the target must be skipped and
	// left attached on the source. Ingest the same older day on both sides,
	// then run a transfer covering it.
	t.Run("existing target partition is skipped", func(t *testing.T) {
		conflictDay := time.Now().UTC().AddDate(0, 0, -3)
		conflictPartition := conflictDay.Format("20060102")
		ingestLogs(t, sourceVL, conflictDay, 5)
		ingestLogs(t, targetVL, conflictDay, 5)
		waitForFlushedData(t, sourceDir, conflictPartition)
		waitForFlushedData(t, targetDir, conflictPartition)

		body, _ := json.Marshal(http_handler.TransferRequestBody{
			TargetURL: targetSrv.URL,
			Range: http_handler.TransferRange{
				From: conflictDay.Format(time.RFC3339),
				To:   conflictDay.AddDate(0, 0, 1).Format(time.RFC3339),
			},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", bytes.NewReader(body))
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		var resp http_handler.TransferResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Transferred) != 0 {
			t.Errorf("transferred = %v, want none", resp.Transferred)
		}
		if !slices.Contains(resp.Skipped, conflictPartition) {
			t.Errorf("skipped = %v, want %s included", resp.Skipped, conflictPartition)
		}
		// Conflict day must remain attached on the source, snapshot cleaned up.
		sourcePartitions, err := sourceClient.ListPartitions("")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(sourcePartitions, conflictPartition) {
			t.Errorf("partition %s no longer attached on source after conflict skip: %v", conflictPartition, sourcePartitions)
		}
		snapshots, err := sourceClient.ListSnapshots("")
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshots) != 0 {
			t.Errorf("source snapshots not cleaned up after conflict skip: %v", snapshots)
		}
	})
}
