package openapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	openapi "github.com/sbordeyne/vlbackup/pkg/openapi"
)

// fakeTargetLogs is a fake target VictoriaLogs insert+select API. It records
// the ingested JSONLine body and answers count queries with the number of
// lines it has ingested (unless countOverride is set).
type fakeTargetLogs struct {
	mu            sync.Mutex
	ingested      bytes.Buffer
	ingestFail    bool
	selectFail    bool
	countOverride *int
}

func (f *fakeTargetLogs) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/insert/jsonline", func(w http.ResponseWriter, r *http.Request) {
		if f.ingestFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		_, _ = io.Copy(&f.ingested, r.Body)
	})
	mux.HandleFunc("/select/logsql/query", func(w http.ResponseWriter, r *http.Request) {
		if f.selectFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		n := countJSONLines(f.ingested.Bytes())
		if f.countOverride != nil {
			n = *f.countOverride
		}
		_, _ = fmt.Fprintf(w, "{\"rows\":\"%d\"}\n", n)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTargetLogs) lines() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return countJSONLines(f.ingested.Bytes())
}

func countJSONLines(b []byte) int {
	n := 0
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func migrateBody(t *testing.T, vlbackupURL, insertURL, selectURL, from string) []byte {
	t.Helper()
	body, _ := json.Marshal(openapi.MigrateRequest{
		TargetVlbackupUrl: vlbackupURL,
		TargetVlinsertUrl: insertURL,
		TargetVlselectUrl: selectURL,
		Range:             openapi.TimeRange{From: from},
	})
	return body
}

func doMigrate(t *testing.T, vl *fakeVL, target *fakeTarget, tl *fakeTargetLogs, nDays int) (*httptest.ResponseRecorder, openapi.MigrateResponse) {
	t.Helper()
	vlSrv := vl.server(t)
	targetSrv := target.server(t)
	tlSrv := tl.server(t)
	h := buildHandler(testArgs(t, vlSrv.URL), newTestMetrics())

	from := time.Now().UTC().AddDate(0, 0, -nDays).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/migrate",
		bytes.NewReader(migrateBody(t, targetSrv.URL, tlSrv.URL, tlSrv.URL, from)))
	rec := do(h, req)

	var resp openapi.MigrateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshaling response %q: %v", rec.Body.String(), err)
	}
	return rec, resp
}

func TestMigrateHandler(t *testing.T) {
	t.Run("moves sealed days and copies today's data", func(t *testing.T) {
		days := lastDays(2)
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), recentLines: 5}
		target := &fakeTarget{}
		tl := &fakeTargetLogs{}
		rec, resp := doMigrate(t, vl, target, tl, 2)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		assertEqual(t, "transferred", resp.Transferred, days)
		assertEqual(t, "detached", vl.detached, days)
		assertEqual(t, "attached", target.attached, days)
		if resp.Recent == nil {
			t.Fatal("recent = nil, want populated")
		}
		if resp.Recent.SourceCount != 5 || resp.Recent.TargetCount != 5 || !resp.Recent.Verified {
			t.Errorf("recent = %+v, want source=target=5 verified", *resp.Recent)
		}
		if resp.Recent.BytesIngested == 0 {
			t.Error("recent.bytes_ingested = 0, want >0")
		}
		if got := tl.lines(); got != 5 {
			t.Errorf("target ingested %d lines, want 5", got)
		}
		want := time.Now().UTC().Format("20060102")
		if resp.Recent.Partition != want {
			t.Errorf("recent.partition = %s, want today %s", resp.Recent.Partition, want)
		}
	})

	t.Run("no sealed days still copies today's data", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), recentLines: 3}
		target := &fakeTarget{}
		tl := &fakeTargetLogs{}
		// nDays=0 -> from is today -> DaysInRange yields no sealed days.
		rec, resp := doMigrate(t, vl, target, tl, 0)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		if len(resp.Transferred) != 0 {
			t.Errorf("transferred = %v, want none", resp.Transferred)
		}
		if resp.Recent == nil || resp.Recent.TargetCount != 3 {
			t.Errorf("recent = %+v, want target=3", resp.Recent)
		}
	})

	t.Run("source query failure yields 500 with export error", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), queryFail: true}
		rec, resp := doMigrate(t, vl, &fakeTarget{}, &fakeTargetLogs{}, 0)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "recent: export") {
			t.Errorf("errors = %v, want one recent: export", resp.Errors)
		}
	})

	t.Run("ingest failure yields 500 with ingest error", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), recentLines: 4}
		tl := &fakeTargetLogs{ingestFail: true}
		rec, resp := doMigrate(t, vl, &fakeTarget{}, tl, 0)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "recent: ingest") {
			t.Errorf("errors = %v, want one recent: ingest", resp.Errors)
		}
	})

	t.Run("count mismatch is advisory: 200 with verified false", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), recentLines: 10}
		zero := 0
		tl := &fakeTargetLogs{countOverride: &zero}
		rec, resp := doMigrate(t, vl, &fakeTarget{}, tl, 0)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (mismatch is advisory), body %s", rec.Code, rec.Body.String())
		}
		if resp.Recent == nil || resp.Recent.Verified {
			t.Errorf("recent = %+v, want not verified", resp.Recent)
		}
		if resp.Recent.SourceCount != 10 || resp.Recent.TargetCount != 0 {
			t.Errorf("recent counts = %d/%d, want source 10 target 0", resp.Recent.SourceCount, resp.Recent.TargetCount)
		}
		if len(resp.Errors) != 0 {
			t.Errorf("errors = %v, want none (mismatch is advisory)", resp.Errors)
		}
	})

	t.Run("verify query failure yields 500", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), recentLines: 4}
		tl := &fakeTargetLogs{selectFail: true}
		rec, resp := doMigrate(t, vl, &fakeTarget{}, tl, 0)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "recent: verify_target") {
			t.Errorf("errors = %v, want one recent: verify_target", resp.Errors)
		}
	})

	t.Run("invalid body and target URLs yield 400", func(t *testing.T) {
		h := buildHandler(testArgs(t, "http://127.0.0.1:1"), newTestMetrics())
		for name, body := range map[string]string{
			"garbage":          "not json",
			"missing from":     `{"target_vlbackup_url":"http://x","target_vlinsert_url":"http://x","target_vlselect_url":"http://x","range":{}}`,
			"bad vlbackup url": `{"target_vlbackup_url":"not-a-url","target_vlinsert_url":"http://x","target_vlselect_url":"http://x","range":{"from":"2026-07-01T00:00:00Z"}}`,
		} {
			req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/migrate", strings.NewReader(body))
			rec := do(h, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", name, rec.Code)
			}
		}
	})
}
