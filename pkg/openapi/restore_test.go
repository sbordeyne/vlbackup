package openapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sbordeyne/vlbackup/pkg/objstore"
)

// restoreDownload controls what the registered restoretest:// backend's
// Download returns; set per-test before invoking the handler.
var (
	restoreDownloadBody []byte
	restoreDownloadErr  error
)

type restoreRepo struct{}

func (restoreRepo) Upload(ctx context.Context, key string, r io.Reader) error {
	_, _ = io.Copy(io.Discard, r)
	return nil
}
func (restoreRepo) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	if restoreDownloadErr != nil {
		return nil, restoreDownloadErr
	}
	return io.NopCloser(bytes.NewReader(restoreDownloadBody)), nil
}
func (restoreRepo) List(ctx context.Context, prefix string) iter.Seq2[objstore.ObjectInfo, error] {
	return func(yield func(objstore.ObjectInfo, error) bool) {}
}
func (restoreRepo) Delete(ctx context.Context, key string) error { return nil }
func (restoreRepo) Close() error                                 { return nil }

func init() {
	objstore.Register("restoretest", func(ctx context.Context, u *url.URL) (objstore.Repository, error) {
		return restoreRepo{}, nil
	})
}

// vlAttachServer fakes the VictoriaLogs attach-partition endpoint, answering
// every request with the given status.
func vlAttachServer(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// snapshotTarGz builds a fake snapshot dir and returns its tar.gz bytes.
func snapshotTarGz(t *testing.T) []byte {
	t.Helper()
	return streamOf(t, makeSnapshotDir(t)).Bytes()
}

func restoreRequest(t *testing.T, vlURL, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := buildHandler(testArgs(t, vlURL), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/restore", strings.NewReader(body))
	return do(h, req)
}

func TestRestoreHappyPath(t *testing.T) {
	restoreDownloadBody = snapshotTarGz(t)
	restoreDownloadErr = nil
	t.Cleanup(func() { restoreDownloadBody = nil })

	args := testArgs(t, vlAttachServer(t, http.StatusOK))
	h := buildHandler(args, newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/restore",
		strings.NewReader(`{"source_url":"restoretest://bucket/prefix","partition_prefix":"20260701"}`))
	rec := do(h, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Partition    []string `json:"partition"`
		BytesWritten int64    `json:"bytes_written"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Partition) != 1 || resp.Partition[0] != "20260701" || resp.BytesWritten == 0 {
		t.Errorf("unexpected response: %+v", resp)
	}
	got, err := os.ReadFile(filepath.Join(args.DataPath, "partitions", "20260701", "datadb", "parts.json"))
	if err != nil || string(got) != `["18A0AD752171BFCD"]` {
		t.Errorf("extracted file mismatch: %q, err %v", got, err)
	}
	// No stray temp dirs left behind.
	entries, _ := os.ReadDir(filepath.Join(args.DataPath, "partitions"))
	if len(entries) != 1 {
		t.Errorf("expected exactly 1 entry in partitions dir, got %d", len(entries))
	}
}

func TestRestoreInvalidPartition(t *testing.T) {
	for _, p := range []string{"", "2026", "../evil", "2026070a"} {
		rec := restoreRequest(t, "", `{"source_url":"restoretest://b/p","partition_prefix":"`+p+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("partition %q: status = %d, want 400", p, rec.Code)
		}
	}
}

func TestRestoreBadJSON(t *testing.T) {
	rec := restoreRequest(t, "", `{bad`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRestoreUnsupportedScheme(t *testing.T) {
	rec := restoreRequest(t, "", `{"source_url":"ftp://host/path","partition_prefix":"20260701"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRestoreNotFound(t *testing.T) {
	restoreDownloadErr = objstore.ErrNotFound
	t.Cleanup(func() { restoreDownloadErr = nil })

	rec := restoreRequest(t, "", `{"source_url":"restoretest://b/p","partition_prefix":"20260701"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRestoreDownloadError(t *testing.T) {
	restoreDownloadErr = errors.New("download boom")
	t.Cleanup(func() { restoreDownloadErr = nil })

	rec := restoreRequest(t, "", `{"source_url":"restoretest://b/p","partition_prefix":"20260701"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestRestoreExistingPartitionConflict(t *testing.T) {
	restoreDownloadBody = snapshotTarGz(t)
	t.Cleanup(func() { restoreDownloadBody = nil })

	args := testArgs(t, "")
	if err := os.MkdirAll(filepath.Join(args.DataPath, "partitions", "20260701"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := buildHandler(args, newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/restore",
		strings.NewReader(`{"source_url":"restoretest://b/p","partition_prefix":"20260701"}`))
	rec := do(h, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestRestoreCorruptStream(t *testing.T) {
	restoreDownloadBody = []byte("not a gzip stream")
	restoreDownloadErr = nil
	t.Cleanup(func() { restoreDownloadBody = nil })

	args := testArgs(t, "")
	h := buildHandler(args, newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/restore",
		strings.NewReader(`{"source_url":"restoretest://b/p","partition_prefix":"20260701"}`))
	rec := do(h, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	// A failed extraction must not leave the partition dir behind.
	if _, err := os.Stat(filepath.Join(args.DataPath, "partitions", "20260701")); !os.IsNotExist(err) {
		t.Error("partition dir must not exist after failed extraction")
	}
}

func TestRestoreAttachFails(t *testing.T) {
	restoreDownloadBody = snapshotTarGz(t)
	restoreDownloadErr = nil
	t.Cleanup(func() { restoreDownloadBody = nil })

	rec := restoreRequest(t, vlAttachServer(t, http.StatusInternalServerError),
		`{"source_url":"restoretest://b/p","partition_prefix":"20260701"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
