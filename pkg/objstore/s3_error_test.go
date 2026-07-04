package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	objstore "github.com/sbordeyne/vlbackup/pkg/objstore"
)

// newS3AgainstServer builds an S3Repository pointed at a local httptest server,
// so error branches can be exercised without a live/emulated S3 backend.
func newS3AgainstServer(t *testing.T, srv *httptest.Server) objstore.Repository {
	t.Helper()
	endpoint := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("S3_ENDPOINT", endpoint)
	t.Setenv("S3_USE_SSL", "false")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	u, _ := url.Parse("s3://bucket")
	repo, err := objstore.NewS3Repository(context.Background(), u)
	if err != nil {
		t.Fatalf("NewS3Repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// s3Error writes a minimal S3-style XML error with the given HTTP status and
// error code.
func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>` +
		code + `</Code><Message>` + code + `</Message></Error>`))
}

func TestS3DownloadNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Error(w, http.StatusNotFound, "NoSuchKey")
	}))
	defer srv.Close()
	repo := newS3AgainstServer(t, srv)

	_, err := repo.Download(context.Background(), "missing.tar.gz")
	if !errors.Is(err, objstore.ErrNotFound) {
		t.Errorf("Download err = %v, want ErrNotFound", err)
	}
}

func TestS3DeleteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// AccessDenied is not a not-found code, so mapNotFound returns it raw.
		s3Error(w, http.StatusForbidden, "AccessDenied")
	}))
	defer srv.Close()
	repo := newS3AgainstServer(t, srv)

	err := repo.Delete(context.Background(), "key")
	if err == nil {
		t.Fatal("Delete err = nil, want error")
	}
	if errors.Is(err, objstore.ErrNotFound) {
		t.Errorf("Delete err = %v, want raw (non-NotFound) error", err)
	}
}

func TestS3DeleteNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Error(w, http.StatusNotFound, "NoSuchBucket")
	}))
	defer srv.Close()
	repo := newS3AgainstServer(t, srv)

	err := repo.Delete(context.Background(), "key")
	if !errors.Is(err, objstore.ErrNotFound) {
		t.Errorf("Delete err = %v, want ErrNotFound", err)
	}
}

func TestS3ListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Error(w, http.StatusInternalServerError, "InternalError")
	}))
	defer srv.Close()
	repo := newS3AgainstServer(t, srv)

	var gotErr error
	for _, err := range repo.List(context.Background(), "prefix/") {
		if err != nil {
			gotErr = err
			break
		}
	}
	if gotErr == nil {
		t.Error("List did not yield an error")
	}
}

func TestS3UploadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Error(w, http.StatusForbidden, "AccessDenied")
	}))
	defer srv.Close()
	repo := newS3AgainstServer(t, srv)

	err := repo.Upload(context.Background(), "key", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Error("Upload err = nil, want error")
	}
}
