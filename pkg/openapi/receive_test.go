package openapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiveSnapshotHandler(t *testing.T) {
	t.Run("valid stream extracts partition", func(t *testing.T) {
		args := testArgs(t, "")
		h := buildHandler(args, newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/receive?partition=20260701", streamOf(t, makeSnapshotDir(t)))
		rec := do(h, req)

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
		h := buildHandler(args, newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/receive?partition=20260701", streamOf(t, makeSnapshotDir(t)))
		rec := do(h, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("invalid partition param yields 400", func(t *testing.T) {
		h := buildHandler(testArgs(t, ""), newTestMetrics())
		for _, p := range []string{"", "2026", "../evil", "2026070a"} {
			req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/receive?partition="+url.QueryEscape(p), nil)
			rec := do(h, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("partition %q: status = %d, want 400", p, rec.Code)
			}
		}
	})

	t.Run("garbage body yields 400 and no partition", func(t *testing.T) {
		args := testArgs(t, "")
		h := buildHandler(args, newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/receive?partition=20260701", strings.NewReader("not gzip"))
		rec := do(h, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if _, err := os.Stat(filepath.Join(args.DataPath, "partitions", "20260701")); !os.IsNotExist(err) {
			t.Error("partition dir must not exist after failed extraction")
		}
	})
}

// TestAttachHandlerError points the handler at a VL that rejects the attach,
// exercising the 500 branch.
func TestAttachHandlerError(t *testing.T) {
	vlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(vlSrv.Close)

	h := buildHandler(testArgs(t, vlSrv.URL), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/attach?partition=20260701", nil)
	rec := do(h, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestAttachHandlerSuccess covers the happy attach path against a fake VL.
func TestAttachHandlerSuccess(t *testing.T) {
	vlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(vlSrv.Close)

	h := buildHandler(testArgs(t, vlSrv.URL), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/attach?partition=20260701", nil)
	rec := do(h, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestAttachHandlerInvalidPartition covers the 400 validation branch.
func TestAttachHandlerInvalidPartition(t *testing.T) {
	h := buildHandler(testArgs(t, ""), newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/attach?partition=bad", nil)
	rec := do(h, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
