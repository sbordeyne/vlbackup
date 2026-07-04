package http_handler

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

	"github.com/sbordeyne/vlbackup/pkg/objstore"
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
	handler := TriggerHandlerFactory(testArgs(t, vlURL), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/snapshot",
		strings.NewReader(`{"partition_prefix":"20240101","destination_url":"`+destURL+`"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
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
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
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
