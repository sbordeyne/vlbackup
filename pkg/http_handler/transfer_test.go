package http_handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
)

func newTestMetrics() *metrics.Metrics {
	return metrics.New(prometheus.NewRegistry())
}

func testArgs(t *testing.T, vlURL string) cli.Args {
	t.Helper()
	args := cli.Args{
		DataPath: t.TempDir(),
	}
	if vlURL != "" {
		parsed, err := url.Parse(vlURL)
		if err != nil {
			t.Fatal(err)
		}
		args.VictoriaLogsURL = *parsed
	}
	return args
}

// makeSnapshotDir builds a fake snapshot directory tree and returns its path.
func makeSnapshotDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for path, contents := range map[string]string{
		"datadb/parts.json":                 `["18A0AD752171BFCD"]`,
		"datadb/18A0AD752171BFCD/index.bin": "index-data",
		"indexdb/parts.json":                `[]`,
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func streamOf(t *testing.T, dir string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := transfer.StreamDir(dir, &buf); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestTransferReceiveHandler(t *testing.T) {
	t.Run("valid stream extracts partition", func(t *testing.T) {
		args := testArgs(t, "")
		handler := TransferReceiveHandlerFactory(args, newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/receive?partition=20260701", streamOf(t, makeSnapshotDir(t)))
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Partition    string `json:"partition"`
			BytesWritten int64  `json:"bytes_written"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Partition != "20260701" || resp.BytesWritten == 0 {
			t.Errorf("unexpected response: %+v", resp)
		}
		got, err := os.ReadFile(filepath.Join(args.DataPath, "partitions", "20260701", "datadb", "parts.json"))
		if err != nil || string(got) != `["18A0AD752171BFCD"]` {
			t.Errorf("extracted file mismatch: %q, err %v", got, err)
		}
		// No stray temp dirs left behind.
		entries, _ := os.ReadDir(filepath.Join(args.DataPath, "partitions"))
		if len(entries) != 1 {
			t.Errorf("expected exactly 1 entry in partitions dir, got %d", len(entries))
		}
	})

	t.Run("existing partition yields 409", func(t *testing.T) {
		args := testArgs(t, "")
		if err := os.MkdirAll(filepath.Join(args.DataPath, "partitions", "20260701"), 0o755); err != nil {
			t.Fatal(err)
		}
		handler := TransferReceiveHandlerFactory(args, newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/receive?partition=20260701", streamOf(t, makeSnapshotDir(t)))
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("invalid partition param yields 400", func(t *testing.T) {
		handler := TransferReceiveHandlerFactory(testArgs(t, ""), newTestMetrics())
		for _, p := range []string{"", "2026", "../evil", "2026070a"} {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/receive?partition="+url.QueryEscape(p), nil)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("partition %q: status = %d, want 400", p, rec.Code)
			}
		}
	})

	t.Run("garbage body yields 400 and no partition", func(t *testing.T) {
		args := testArgs(t, "")
		handler := TransferReceiveHandlerFactory(args, newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/receive?partition=20260701", strings.NewReader("not gzip"))
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if _, err := os.Stat(filepath.Join(args.DataPath, "partitions", "20260701")); !os.IsNotExist(err) {
			t.Error("partition dir must not exist after failed extraction")
		}
	})
}

func TestBearerAuth(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	t.Run("empty token disables auth", func(t *testing.T) {
		rec := httptest.NewRecorder()
		BearerAuth("")(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
	t.Run("missing or wrong token yields 401", func(t *testing.T) {
		for _, header := range []string{"", "Bearer wrong", "Basic secret"} {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()
			BearerAuth("secret")(next).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("header %q: status = %d, want 401", header, rec.Code)
			}
		}
	})
	t.Run("correct token passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		BearerAuth("secret")(next).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
}

// fakeVL is a fake VictoriaLogs /internal/partition API recording calls.
type fakeVL struct {
	mu             sync.Mutex
	snapshotDir    string
	emptyDays      map[string]bool // days with no partition
	multiDays      map[string]bool // days returning >1 snapshot path
	failCreateDays map[string]bool // create returns 500
	failDetachDays map[string]bool // detach returns 500
	failDelete     bool            // all snapshot deletes return 500
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
			fmt.Fprint(w, "[]")
			return
		}
		if f.multiDays[day] {
			json.NewEncoder(w).Encode([]string{f.snapshotDir, f.snapshotDir})
			return
		}
		json.NewEncoder(w).Encode([]string{f.snapshotDir})
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
	mux.HandleFunc("/api/v1/transfer/receive", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
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
		fmt.Fprint(w, `{"bytes_written": 42}`)
	})
	mux.HandleFunc("/api/v1/transfer/attach", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		day := r.URL.Query().Get("partition")
		if f.failAttachDays[day] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.attached = append(f.attached, day)
		fmt.Fprint(w, `{}`)
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

func doTransfer(t *testing.T, vl *fakeVL, target *fakeTarget, nDays int) (*httptest.ResponseRecorder, TransferResponse) {
	t.Helper()
	vlSrv := vl.server(t)
	targetSrv := target.server(t)
	args := testArgs(t, vlSrv.URL)
	handler := TransferHandlerFactory(args, newTestMetrics())

	from := time.Now().UTC().AddDate(0, 0, -nDays).Format(time.RFC3339)
	body, _ := json.Marshal(TransferRequestBody{
		TargetURL: targetSrv.URL,
		Range:     TransferRange{From: from},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp TransferResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshaling response %q: %v", rec.Body.String(), err)
	}
	return rec, resp
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

	t.Run("conflict on target skips day and continues", func(t *testing.T) {
		days := lastDays(3)
		vl := &fakeVL{snapshotDir: makeSnapshotDir(t)}
		target := &fakeTarget{conflictDays: map[string]bool{days[0]: true}}
		rec, resp := doTransfer(t, vl, target, 3)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		assertEqual(t, "skipped", resp.Skipped, days[:1])
		assertEqual(t, "transferred", resp.Transferred, days[1:])
		assertEqual(t, "detached", vl.detached, days[1:]) // conflict day never detached
		if len(vl.deletedSnaps) != 3 {
			t.Errorf("deleted snapshots = %d, want 3 (conflict day snapshot cleaned too)", len(vl.deletedSnaps))
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
		handler := TransferHandlerFactory(testArgs(t, "http://127.0.0.1:1"), newTestMetrics())
		for name, body := range map[string]string{
			"garbage":       "not json",
			"missing from":  `{"target_url": "http://x", "range": {}}`,
			"bad from":      `{"target_url": "http://x", "range": {"from": "yesterday"}}`,
			"bad target":    `{"target_url": "not-a-url", "range": {"from": "2026-07-01T00:00:00Z"}}`,
			"from after to": `{"target_url": "http://x", "range": {"from": "2026-07-03T00:00:00Z", "to": "2026-07-01T00:00:00Z"}}`,
		} {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", strings.NewReader(body))
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", name, rec.Code)
			}
		}
	})
}

func assertEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", label, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", label, got, want)
			return
		}
	}
}
