package openapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	openapi "github.com/sbordeyne/vlbackup/pkg/openapi"
)

// fakeVL is a fake VictoriaLogs /internal/partition API recording calls.
type fakeVL struct {
	mu             sync.Mutex
	snapshotDir    string
	emptyDays      map[string]bool // days with no partition
	multiDays      map[string]bool // days returning >1 snapshot path
	failCreateDays map[string]bool // create returns 500
	failDetachDays map[string]bool // detach returns 500
	failDelete     bool            // all snapshot deletes return 500
	recentLines    int             // rows served by /select/logsql/query (migrate)
	queryFail      bool            // /select/logsql/query returns 500 (migrate)
	created        []string
	detached       []string
	deletedSnaps   []string
}

func (f *fakeVL) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/partition/snapshot/create", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		day := r.URL.Query().Get("partition_prefix")
		f.created = append(f.created, day)
		if f.failCreateDays[day] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if f.emptyDays[day] {
			_, _ = fmt.Fprint(w, "[]")
			return
		}
		if f.multiDays[day] {
			_ = json.NewEncoder(w).Encode([]string{f.snapshotDir, f.snapshotDir})
			return
		}
		_ = json.NewEncoder(w).Encode([]string{f.snapshotDir})
	})
	mux.HandleFunc("/internal/partition/detach", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		day := r.URL.Query().Get("name")
		if f.failDetachDays[day] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.detached = append(f.detached, day)
	})
	mux.HandleFunc("/internal/partition/snapshot/delete", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.deletedSnaps = append(f.deletedSnaps, r.URL.Query().Get("path"))
		if f.failDelete {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	// LogsQL query endpoint, used by the migrate handler to export today's
	// data and to count rows for verification.
	mux.HandleFunc("/select/logsql/query", func(w http.ResponseWriter, r *http.Request) {
		if f.queryFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.FormValue("query"), "stats count()") {
			_, _ = fmt.Fprintf(w, "{\"rows\":\"%d\"}\n", f.recentLines)
			return
		}
		for i := 0; i < f.recentLines; i++ {
			_, _ = fmt.Fprintf(w, `{"_time":"2024-01-01T00:00:00Z","_msg":"line %d"}`+"\n", i)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeTarget is a fake target vlbackup recording receive/attach calls.
type fakeTarget struct {
	mu             sync.Mutex
	conflictDays   map[string]bool // days answered with 409
	failDays       map[string]bool // receive days answered with 500
	failAttachDays map[string]bool // attach days answered with 500
	received       []string
	attached       []string
}

func (f *fakeTarget) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/vlbackup/transfer/receive", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		day := r.URL.Query().Get("partition")
		if f.conflictDays[day] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if f.failDays[day] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.received = append(f.received, day)
		_, _ = fmt.Fprint(w, `{"bytes_written": 42}`)
	})
	mux.HandleFunc("/v1/vlbackup/transfer/attach", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		day := r.URL.Query().Get("partition")
		if f.failAttachDays[day] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.attached = append(f.attached, day)
		_, _ = fmt.Fprint(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// lastDays returns the n most recent sealed days as YYYYMMDD, oldest first.
func lastDays(n int) []string {
	days := make([]string, 0, n)
	for i := n; i >= 1; i-- {
		days = append(days, time.Now().UTC().AddDate(0, 0, -i).Format("20060102"))
	}
	return days
}

// transferBody builds a TransferRequest JSON body from a target URL and a
// "from" time expression (RFC3339 or relative, e.g. "now-3d/d").
func transferBody(t *testing.T, targetURL, from string) []byte {
	t.Helper()
	body, _ := json.Marshal(openapi.TransferRequest{
		TargetUrl: targetURL,
		Range:     openapi.TimeRange{From: from},
	})
	return body
}

func doTransfer(t *testing.T, vl *fakeVL, target *fakeTarget, nDays int) (*httptest.ResponseRecorder, openapi.TransferResponse) {
	t.Helper()
	vlSrv := vl.server(t)
	targetSrv := target.server(t)
	h := buildHandler(testArgs(t, vlSrv.URL), newTestMetrics())

	from := time.Now().UTC().AddDate(0, 0, -nDays).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer", bytes.NewReader(transferBody(t, targetSrv.URL, from)))
	rec := do(h, req)

	var resp openapi.TransferResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshaling response %q: %v", rec.Body.String(), err)
	}
	return rec, resp
}

func TestParseTransferRange(t *testing.T) {
	now := time.Date(2024, 1, 17, 15, 30, 45, 0, time.UTC)
	to := "2024-01-10T00:00:00Z"

	t.Run("relative from and to resolved against now", func(t *testing.T) {
		toExpr := "now/d"
		from, gotTo, err := openapi.ParseTimeRange(openapi.TimeRange{From: "now-7d/d", To: &toExpr}, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !from.Equal(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("from = %s, want 2024-01-10T00:00:00Z", from.Format(time.RFC3339))
		}
		if !gotTo.Equal(time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("to = %s, want 2024-01-17T00:00:00Z", gotTo.Format(time.RFC3339))
		}
	})

	t.Run("missing to defaults to now", func(t *testing.T) {
		_, gotTo, err := openapi.ParseTimeRange(openapi.TimeRange{From: "now-1d"}, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !gotTo.Equal(now) {
			t.Errorf("to = %s, want now %s", gotTo.Format(time.RFC3339), now.Format(time.RFC3339))
		}
	})

	t.Run("bad order errors", func(t *testing.T) {
		if _, _, err := openapi.ParseTimeRange(openapi.TimeRange{From: "2026-07-03T00:00:00Z", To: &to}, now); err == nil {
			t.Error("err = nil, want from-after-to error")
		}
	})

	t.Run("missing from errors", func(t *testing.T) {
		if _, _, err := openapi.ParseTimeRange(openapi.TimeRange{}, now); err == nil {
			t.Error("err = nil, want range.from required")
		}
	})

	t.Run("invalid from expression errors", func(t *testing.T) {
		if _, _, err := openapi.ParseTimeRange(openapi.TimeRange{From: "yesterday"}, now); err == nil {
			t.Error("err = nil, want invalid expression error")
		}
	})
}

func TestTransferHandler(t *testing.T) {
	t.Run("happy path transfers all days in order", func(t *testing.T) {
		days := lastDays(3)
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t)}
		target := &fakeTarget{}
		rec, resp := doTransfer(t, vl, target, 3)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		assertEqual(t, "transferred", resp.Transferred, days)
		assertEqual(t, "received", target.received, days)
		assertEqual(t, "detached", vl.detached, days)
		assertEqual(t, "attached", target.attached, days)
		if len(vl.deletedSnaps) != 3 {
			t.Errorf("deleted snapshots = %d, want 3", len(vl.deletedSnaps))
		}
	})

	t.Run("conflict on target resumes attach and detach", func(t *testing.T) {
		// A 409 means a prior interrupted run already delivered the partition to
		// the target: the day must be completed (attached on the target, detached
		// on the source), not skipped, so the migration can finish.
		days := lastDays(3)
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t)}
		target := &fakeTarget{conflictDays: map[string]bool{days[0]: true}}
		rec, resp := doTransfer(t, vl, target, 3)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		assertEqual(t, "skipped", resp.Skipped, []string{})
		assertEqual(t, "transferred", resp.Transferred, days)
		assertEqual(t, "detached", vl.detached, days)     // conflict day detached too
		assertEqual(t, "attached", target.attached, days) // and attached on the target
		if len(vl.deletedSnaps) != 3 {
			t.Errorf("deleted snapshots = %d, want 3", len(vl.deletedSnaps))
		}
	})

	t.Run("day with no partition is skipped", func(t *testing.T) {
		days := lastDays(2)
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), emptyDays: map[string]bool{days[0]: true}}
		target := &fakeTarget{}
		rec, resp := doTransfer(t, vl, target, 2)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		assertEqual(t, "skipped", resp.Skipped, days[:1])
		assertEqual(t, "transferred", resp.Transferred, days[1:])
	})

	t.Run("hard error aborts remaining days", func(t *testing.T) {
		days := lastDays(3)
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t)}
		target := &fakeTarget{failDays: map[string]bool{days[1]: true}}
		rec, resp := doTransfer(t, vl, target, 3)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		assertEqual(t, "transferred", resp.Transferred, days[:1])
		if len(resp.Errors) != 1 {
			t.Errorf("errors = %v, want exactly 1", resp.Errors)
		}
		assertEqual(t, "created snapshots", vl.created, days[:2]) // third day never attempted
		assertEqual(t, "detached", vl.detached, days[:1])         // failed day never detached
	})

	t.Run("invalid body yields 400", func(t *testing.T) {
		h := buildHandler(testArgs(t, "http://127.0.0.1:1"), newTestMetrics())
		for name, body := range map[string]string{
			"garbage":       "not json",
			"missing from":  `{"target_url": "http://x", "range": {}}`,
			"bad from":      `{"target_url": "http://x", "range": {"from": "yesterday"}}`,
			"bad target":    `{"target_url": "not-a-url", "range": {"from": "2026-07-01T00:00:00Z"}}`,
			"from after to": `{"target_url": "http://x", "range": {"from": "2026-07-03T00:00:00Z", "to": "2026-07-01T00:00:00Z"}}`,
		} {
			req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer", strings.NewReader(body))
			rec := do(h, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", name, rec.Code)
			}
		}
	})
}

// TestTransferHandlerStageErrors drives each per-day failure branch of the
// source-side handler through the fake VL / target servers.
func TestTransferHandlerStageErrors(t *testing.T) {
	day := lastDays(1)[0]

	t.Run("create snapshot fails", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), failCreateDays: map[string]bool{day: true}}
		rec, resp := doTransfer(t, vl, &fakeTarget{}, 1)
		if rec.Code != http.StatusInternalServerError || len(resp.Errors) != 1 {
			t.Errorf("code = %d, errors = %v", rec.Code, resp.Errors)
		}
	})

	t.Run("multiple snapshot paths", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), multiDays: map[string]bool{day: true}}
		rec, resp := doTransfer(t, vl, &fakeTarget{}, 1)
		if rec.Code != http.StatusInternalServerError || len(resp.Errors) != 1 {
			t.Errorf("code = %d, errors = %v", rec.Code, resp.Errors)
		}
		if len(vl.deletedSnaps) != 2 {
			t.Errorf("deletedSnaps = %d, want 2 (both stray paths cleaned)", len(vl.deletedSnaps))
		}
	})

	t.Run("snapshot cleanup fails", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), failDelete: true}
		rec, resp := doTransfer(t, vl, &fakeTarget{}, 1)
		if rec.Code != http.StatusInternalServerError || len(resp.Errors) != 1 {
			t.Errorf("code = %d, errors = %v", rec.Code, resp.Errors)
		}
	})

	t.Run("detach fails", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t), failDetachDays: map[string]bool{day: true}}
		rec, resp := doTransfer(t, vl, &fakeTarget{}, 1)
		if rec.Code != http.StatusInternalServerError || len(resp.Errors) != 1 {
			t.Errorf("code = %d, errors = %v", rec.Code, resp.Errors)
		}
	})

	t.Run("attach fails", func(t *testing.T) {
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t)}
		target := &fakeTarget{failAttachDays: map[string]bool{day: true}}
		rec, resp := doTransfer(t, vl, target, 1)
		if rec.Code != http.StatusInternalServerError || len(resp.Errors) != 1 {
			t.Errorf("code = %d, errors = %v", rec.Code, resp.Errors)
		}
	})
}

// TestTransferReceiveMkdirError makes DataPath a regular file so creating the
// partitions directory fails with 500.
func TestTransferReceiveMkdirError(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(dataFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := testArgs(t, "")
	args.DataPath = dataFile
	h := buildHandler(args, newTestMetrics())

	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/receive?partition=20260701",
		streamOf(t, makeSnapshotDir(t)))
	rec := do(h, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
