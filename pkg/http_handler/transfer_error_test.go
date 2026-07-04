package http_handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseTransferRangeBadTo(t *testing.T) {
	_, _, err := parseTransferRange(TransferRange{From: "2026-07-01T00:00:00Z", To: "not-a-time"})
	if err == nil {
		t.Error("parseTransferRange err = nil, want invalid range.to")
	}
}

func TestWriteJSONEncodeError(t *testing.T) {
	rec := httptest.NewRecorder()
	// A channel cannot be JSON-encoded, exercising the encode-error branch.
	writeJSON(rec, http.StatusOK, make(chan int))
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
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
	handler := TransferReceiveHandlerFactory(args, newTestMetrics())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/receive?partition=20260701",
		streamOf(t, makeSnapshotDir(t)))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestTransferAttachHandlerError points the handler at a VL that rejects the
// attach, exercising the 500 branch.
func TestTransferAttachHandlerError(t *testing.T) {
	vlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(vlSrv.Close)

	handler := TransferAttachHandlerFactory(testArgs(t, vlSrv.URL), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/attach?partition=20260701", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestTransferAttachHandlerSuccess covers the happy attach path against a fake VL.
func TestTransferAttachHandlerSuccess(t *testing.T) {
	vlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(vlSrv.Close)

	handler := TransferAttachHandlerFactory(testArgs(t, vlSrv.URL), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/attach?partition=20260701", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
