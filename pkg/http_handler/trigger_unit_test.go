package http_handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHandleError(t *testing.T) {
	m := newTestMetrics()
	rec := httptest.NewRecorder()
	handleError(rec, errors.New("boom"), "20240101", m, http.StatusBadRequest)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["error"] != "boom" {
		t.Errorf("error = %q, want %q", body["error"], "boom")
	}
	if got := testutil.ToFloat64(m.SnapshotCount.WithLabelValues("20240101", "false")); got != 1 {
		t.Errorf("SnapshotCount{20240101,false} = %v, want 1", got)
	}
}

func TestParseRequestBody(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader(`{"partition_prefix":"20230101","destination_url":"s3://b/p"}`))
		parsed, err := parseRequestBody(body)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if parsed.PartitionPrefix != "20230101" {
			t.Errorf("PartitionPrefix = %q, want 20230101", parsed.PartitionPrefix)
		}
		if parsed.DestinationURL != "s3://b/p" {
			t.Errorf("DestinationURL = %q, want s3://b/p", parsed.DestinationURL)
		}
	})

	t.Run("empty body defaults to yesterday UTC", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader(`{"destination_url":"s3://b/p"}`))
		parsed, err := parseRequestBody(body)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		want := time.Now().Add(-time.Hour * 24).Format("20060102")
		if parsed.PartitionPrefix != want {
			t.Errorf("PartitionPrefix = %q, want %q", parsed.PartitionPrefix, want)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader(`{not json`))
		if _, err := parseRequestBody(body); err == nil {
			t.Error("err = nil, want decode error")
		}
	})
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
			if got := partitionFromSnapshotPath(tt.path); got != tt.want {
				t.Errorf("partitionFromSnapshotPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestTriggerHandlerUnsupportedScheme(t *testing.T) {
	m := newTestMetrics()
	handler := TriggerHandlerFactory(testArgs(t, ""), m)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/snapshot",
		strings.NewReader(`{"partition_prefix":"20240101","destination_url":"ftp://host/path"}`))
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if got := testutil.ToFloat64(m.SnapshotCount.WithLabelValues("20240101", "false")); got != 1 {
		t.Errorf("SnapshotCount{20240101,false} = %v, want 1", got)
	}
}

func TestTriggerHandlerBadJSON(t *testing.T) {
	m := newTestMetrics()
	handler := TriggerHandlerFactory(testArgs(t, ""), m)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/snapshot", strings.NewReader(`{bad`))
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
