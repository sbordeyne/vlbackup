package openapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRestoreSnapshotNotImplemented(t *testing.T) {
	h := buildHandler(testArgs(t, ""), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/restore",
		strings.NewReader(`{"source_url":"gs://b/p","partition_prefix":"20240101"}`))
	rec := do(h, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}
