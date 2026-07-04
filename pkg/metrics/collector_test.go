package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNew(t *testing.T) {
	m := New(prometheus.NewRegistry())
	if m == nil {
		t.Fatal("New returned nil")
	}
	if m.SnapshotCount == nil || m.SnapshotDuration == nil ||
		m.TransferCount == nil || m.TransferDuration == nil || m.TransferBytes == nil {
		t.Fatal("New returned Metrics with nil vec fields")
	}

	// Exercise each vec to confirm label cardinality is wired correctly.
	m.SnapshotCount.WithLabelValues("20240101", "true").Inc()
	m.SnapshotDuration.WithLabelValues("20240101", "parse").Observe(0.1)
	m.TransferCount.WithLabelValues("20240101", "transferred").Inc()
	m.TransferDuration.WithLabelValues("20240101", "stream").Observe(1.0)
	m.TransferBytes.WithLabelValues("sent").Add(1024)
}

func TestNewDuplicateRegistrationPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	New(reg)
	defer func() {
		if recover() == nil {
			t.Error("second New on same registry did not panic")
		}
	}()
	New(reg)
}

func TestHandler(t *testing.T) {
	h := Handler()
	if h == nil {
		t.Fatal("Handler returned nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
