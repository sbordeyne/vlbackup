package openapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/transfer"

	openapi "github.com/sbordeyne/vlbackup/pkg/openapi"
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

// buildHandler wires the strict server behind the real chi router + auth
// middleware, so tests exercise routing, auth, and handlers together.
func buildHandler(args cli.Args, m *metrics.Metrics) http.Handler {
	return openapi.NewHandler(openapi.NewServer(args, m), args.TransferAuthKey)
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
	if _, err := transfer.StreamDir(dir, &buf); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// do sends a request through the handler and returns the recorder.
func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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
