package openapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/openapi"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// countTodayLines polls the target for today's rows until it sees at least
// want (ingestion visibility can lag the /insert/jsonline response).
func countTodayLines(t *testing.T, baseURL, since string, want int) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.PostForm(baseURL+"/select/logsql/query", url.Values{
			"query": {"_time:>=" + since + " AND source:vlbackup-test"},
		})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		lines := 0
		for line := range bytes.SplitSeq(body, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				lines++
			}
		}
		if lines >= want || time.Now().After(deadline) {
			return lines
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestMigrateIntegration runs the full migrate flow against two real
// VictoriaLogs containers: a sealed day is moved as a partition, and today's
// still-open data is copied at the record level via LogsQL query -> JSON stream.
func TestMigrateIntegration(t *testing.T) {
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

	transfer.SnapshotPathResolver = func(p string) string {
		return filepath.Join(sourceDir, strings.TrimPrefix(p, containerDataPath))
	}
	t.Cleanup(func() { transfer.SnapshotPathResolver = func(p string) string { return p } })

	sealedDay := time.Now().UTC().AddDate(0, 0, -1)
	sealedPartition := sealedDay.Format("20060102")
	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	const todayCount = 7

	ingestLogs(t, sourceVL, sealedDay, 10)                // sealed day -> moved as a partition
	ingestLogs(t, sourceVL, time.Now().UTC(), todayCount) // today -> copied as JSONLine
	waitForPartitions(t, &sourceClient, 2)
	waitForFlushedData(t, sourceDir, sealedPartition)

	// Target vlbackup handler (receives + attaches the sealed partition).
	targetArgs := cli.Args{
		VictoriaLogsURL: mustParseURL(t, targetVL),
		DataPath:        targetDir,
		TransferAuthKey: authToken,
	}
	targetSrv := httptest.NewServer(openapi.NewHandler(openapi.NewServer(targetArgs, metrics.New(prometheus.NewRegistry())), authToken))
	t.Cleanup(targetSrv.Close)

	// Source vlbackup migrate handler.
	sourceArgs := cli.Args{
		VictoriaLogsURL: mustParseURL(t, sourceVL),
		DataPath:        sourceDir,
		TransferAuthKey: authToken,
	}
	handler := openapi.NewHandler(openapi.NewServer(sourceArgs, metrics.New(prometheus.NewRegistry())), authToken)

	body, _ := json.Marshal(openapi.MigrateRequest{
		TargetVlbackupUrl: targetSrv.URL,
		TargetVlinsertUrl: targetVL,
		TargetVlselectUrl: targetVL,
		Range:             openapi.TimeRange{From: sealedDay.Format(time.RFC3339)},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/migrate", bytes.NewReader(body))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("migrate status = %d, body %s", rec.Code, rec.Body.String())
	}
	status := waitJob(t, handler, rec)
	if status.State != openapi.Succeeded || status.Migrate == nil {
		t.Fatalf("migrate job = %+v", status)
	}
	resp := *status.Migrate
	if !slices.Equal(resp.Transferred, []string{sealedPartition}) {
		t.Errorf("transferred = %v, want [%s]", resp.Transferred, sealedPartition)
	}
	if len(resp.Errors) > 0 {
		t.Errorf("unexpected errors: %v", resp.Errors)
	}
	if resp.Recent == nil {
		t.Fatal("recent = nil, want populated")
	}
	if resp.Recent.Partition != todayStart.Format("20060102") {
		t.Errorf("recent.partition = %s, want %s", resp.Recent.Partition, todayStart.Format("20060102"))
	}
	if resp.Recent.SourceCount != todayCount {
		t.Errorf("recent.source_count = %d, want %d", resp.Recent.SourceCount, todayCount)
	}
	if resp.Recent.BytesIngested == 0 {
		t.Error("recent.bytes_ingested = 0, want >0")
	}

	// Sealed partition must now live on the target and be gone from the source.
	targetPartitions, err := targetClient.ListPartitions("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(targetPartitions, sealedPartition) {
		t.Errorf("sealed partition %s missing on target: %v", sealedPartition, targetPartitions)
	}
	sourcePartitions, err := sourceClient.ListPartitions("")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(sourcePartitions, sealedPartition) {
		t.Errorf("sealed partition %s still attached on source: %v", sealedPartition, sourcePartitions)
	}
	// Today's data is a copy: it must remain on the source.
	todayPartition := todayStart.Format("20060102")
	if !slices.Contains(sourcePartitions, todayPartition) {
		t.Errorf("today's partition %s missing from source (should be copied, not moved): %v", todayPartition, sourcePartitions)
	}

	// Today's rows must be queryable on the target.
	since := todayStart.Format(time.RFC3339)
	if got := countTodayLines(t, targetVL, since, todayCount); got != todayCount {
		t.Errorf("target has %d of today's rows, want %d", got, todayCount)
	}
}
