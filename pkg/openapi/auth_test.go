package openapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sbordeyne/vlbackup/pkg/cli"
)

// authedArgs returns args with a transfer auth key and VL pointed at a server
// that answers every request 200 (so a passing auth reaches a working handler).
func authedArgs(t *testing.T, token string) cli.Args {
	t.Helper()
	vlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(vlSrv.Close)
	args := testArgs(t, vlSrv.URL)
	args.TransferAuthKey = token
	return args
}

func TestAuthSecuredOperations(t *testing.T) {
	const token = "secret"

	t.Run("missing or wrong token yields 401", func(t *testing.T) {
		h := buildHandler(authedArgs(t, token), newTestMetrics())
		for _, header := range []string{"", "Bearer wrong", "Basic secret"} {
			req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/attach?partition=20260701", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := do(h, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("header %q: status = %d, want 401", header, rec.Code)
			}
		}
	})

	t.Run("correct token passes auth", func(t *testing.T) {
		h := buildHandler(authedArgs(t, token), newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/attach?partition=20260701", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := do(h, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("status = 401, want auth to pass")
		}
	})

	t.Run("empty token disables auth", func(t *testing.T) {
		h := buildHandler(authedArgs(t, ""), newTestMetrics())
		req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/transfer/attach?partition=20260701", nil)
		rec := do(h, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("status = 401, want auth disabled with empty token")
		}
	})
}

// TestAuthUnsecuredOperations confirms non-secured operations need no token:
// a bad snapshot body must yield 400, not 401, even with an auth key set.
func TestAuthUnsecuredOperations(t *testing.T) {
	args := testArgs(t, "")
	args.TransferAuthKey = "secret"
	h := buildHandler(args, newTestMetrics())
	req := httptest.NewRequest(http.MethodPost, "/v1/vlbackup/snapshot", strings.NewReader(`{bad`))
	rec := do(h, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no auth on snapshot)", rec.Code)
	}
}
