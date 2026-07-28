package openapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	openapi "github.com/sbordeyne/vlbackup/pkg/openapi"
)

// TestGetJobNotFound checks the status endpoint answers 404 for an unknown but
// well-formed job id.
func TestGetJobNotFound(t *testing.T) {
	h := buildHandler(testArgs(t, "http://127.0.0.1:1"), newTestMetrics())
	req := httptest.NewRequest(http.MethodGet, "/v1/vlbackup/jobs/"+strings.Repeat("0", 32), nil)
	rec := do(h, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body %s", rec.Code, rec.Body.String())
	}
}

// TestTransferSingleFlight verifies that while one transfer job is running, a
// second transfer request is rejected with 409. A blocking receive server holds
// the first job in the running state until the assertions are done.
func TestTransferSingleFlight(t *testing.T) {
	// release must be closed before target.Close() (registered below) runs, or
	// Close would block on the still-in-flight receive handler. defer runs
	// before t.Cleanup, so close it here.
	release := make(chan struct{})
	defer close(release)

	blocked := make(chan struct{})
	var once sync.Once
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/transfer/receive") {
			once.Do(func() { close(blocked) })
			<-release // hold the stream open so the first job stays running
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = w.Write([]byte(`{"bytes_written": 1}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(target.Close)

	vl := &fakeVL{snapshotDir: makeSnapshotDir(t)}
	vlSrv := vl.server(t)
	h := buildHandler(testArgs(t, vlSrv.URL), newTestMetrics())

	from := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer", bytes.NewReader(transferBody(t, target.URL, from)))
		return do(h, req)
	}

	first := post()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first transfer = %d, want 202", first.Code)
	}
	<-blocked // the first job is now streaming and holds the single-flight slot

	second := post()
	if second.Code != http.StatusConflict {
		t.Fatalf("second transfer = %d, want 409, body %s", second.Code, second.Body.String())
	}
	var errResp openapi.ErrorResponse
	if err := json.Unmarshal(second.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decoding 409 body %q: %v", second.Body.String(), err)
	}
	if errResp.Error == nil || !strings.Contains(*errResp.Error, "already running") {
		t.Errorf("409 error = %v, want an 'already running' message", errResp.Error)
	}
}
