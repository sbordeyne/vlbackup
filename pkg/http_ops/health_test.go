package http_ops_test

import (
	http_ops "github.com/sbordeyne/vlbackup/pkg/http_ops"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	http_ops.HealthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "OK" {
		t.Errorf("body = %q, want %q", got, "OK")
	}
}

func TestReadyHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	http_ops.ReadyHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "OK" {
		t.Errorf("body = %q, want %q", got, "OK")
	}
}
