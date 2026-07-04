package openapi_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sbordeyne/vlbackup/pkg/objstore"

	openapi "github.com/sbordeyne/vlbackup/pkg/openapi"
)

// memUploadErr controls whether the registered memtest:// backend's Upload
// fails; set per-test before invoking the handler.
var memUploadErr error

type memRepo struct{}

func (memRepo) Upload(ctx context.Context, key string, r io.Reader) error {
	_, _ = io.Copy(io.Discard, r)
	return memUploadErr
}
func (memRepo) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	return nil, objstore.ErrNotFound
}
func (memRepo) List(ctx context.Context, prefix string) iter.Seq2[objstore.ObjectInfo, error] {
	return func(yield func(objstore.ObjectInfo, error) bool) {}
}
func (memRepo) Delete(ctx context.Context, key string) error { return nil }
func (memRepo) Close() error                                 { return nil }

func init() {
	objstore.Register("memtest", func(ctx context.Context, u *url.URL) (objstore.Repository, error) {
		return memRepo{}, nil
	})
}

// vlSnapshotServer fakes the VictoriaLogs create/delete snapshot endpoints.
func vlSnapshotServer(t *testing.T, createStatus int, createBody string, deleteStatus int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/partition/snapshot/create", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(createStatus)
		_, _ = io.WriteString(w, createBody)
	})
	mux.HandleFunc("/internal/partition/snapshot/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(deleteStatus)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func triggerRequest(t *testing.T, vlURL, destURL string) *httptest.ResponseRecorder {
	t.Helper()
	h := buildHandler(testArgs(t, vlURL), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/snapshot",
		strings.NewReader(`{"range":{"from":"now-1d/d"},"destination_url":"`+destURL+`"}`))
	return do(h, req)
}

func TestSnapshotFail(t *testing.T) {
	m := newTestMetrics()
	s := openapi.NewServer(testArgs(t, ""), m)
	resp := s.SnapshotFail("20240101", errors.New("boom"), http.StatusBadRequest)
	if resp.Error == nil || *resp.Error != "boom" {
		t.Errorf("error = %v, want boom", resp.Error)
	}
	if resp.Code == nil || *resp.Code != 400 {
		t.Errorf("code = %v, want 400", resp.Code)
	}
	if got := testutil.ToFloat64(m.SnapshotCount.WithLabelValues("20240101", "false")); got != 1 {
		t.Errorf("SnapshotCount{20240101,false} = %v, want 1", got)
	}
}

func TestPartitionFromSnapshotPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "standard layout", path: "/data/partitions/20240101/snapshots/abc", want: "20240101"},
		{name: "no snapshots suffix", path: "/data/partitions/20240202", want: "20240202"},
		{name: "unexpected layout", path: "/some/other/path/20240404", want: "20240404"},
		{name: "partitions is last segment", path: "/data/partitions", want: "partitions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openapi.PartitionFromSnapshotPath(tt.path); got != tt.want {
				t.Errorf("PartitionFromSnapshotPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestTriggerHandlerUnsupportedScheme(t *testing.T) {
	m := newTestMetrics()
	h := buildHandler(testArgs(t, ""), m)
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/snapshot",
		strings.NewReader(`{"range":{"from":"now-1d/d"},"destination_url":"ftp://host/path"}`))
	rec := do(h, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestTriggerHandlerBadJSON(t *testing.T) {
	h := buildHandler(testArgs(t, ""), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/snapshot", strings.NewReader(`{bad`))
	rec := do(h, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestTriggerCreateSnapshotFails(t *testing.T) {
	vlURL := vlSnapshotServer(t, http.StatusInternalServerError, "", http.StatusOK)
	rec := triggerRequest(t, vlURL, "memtest://bucket/prefix")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestTriggerNoSnapshotPaths(t *testing.T) {
	vlURL := vlSnapshotServer(t, http.StatusOK, "[]", http.StatusOK)
	rec := triggerRequest(t, vlURL, "memtest://bucket/prefix")
	// The spec folds "no data to copy" into a 202 acknowledgement.
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}

func TestTriggerUploadFails(t *testing.T) {
	memUploadErr = errors.New("upload boom")
	t.Cleanup(func() { memUploadErr = nil })

	snapDir := makeSnapshotDir(t)
	vlURL := vlSnapshotServer(t, http.StatusOK, `["`+snapDir+`"]`, http.StatusOK)
	rec := triggerRequest(t, vlURL, "memtest://bucket/prefix")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestTriggerDeleteSnapshotFails(t *testing.T) {
	snapDir := makeSnapshotDir(t)
	vlURL := vlSnapshotServer(t, http.StatusOK, `["`+snapDir+`"]`, http.StatusInternalServerError)
	rec := triggerRequest(t, vlURL, "memtest://bucket/prefix")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestTriggerHappyPath(t *testing.T) {
	snapDir := makeSnapshotDir(t)
	vlURL := vlSnapshotServer(t, http.StatusOK, `["`+snapDir+`"]`, http.StatusOK)
	rec := triggerRequest(t, vlURL, "memtest://bucket/prefix")
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}
