package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func setArgs(t *testing.T, argv ...string) {
	t.Helper()
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = append([]string{"vlbackupctl"}, argv...)
}

// muteStdout swallows os.Stdout for the duration of f (help text, command
// output) so the test log stays clean.
func muteStdout(t *testing.T, f func()) {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, r); close(done) }()
	f()
	_ = w.Close()
	os.Stdout = old
	<-done
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	const jobID = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/vlbackup/snapshot":
			w.WriteHeader(202)
		case r.URL.Path == "/v1/vlbackup/restore":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			_, _ = io.WriteString(w, `{"partition":["20240115"],"bytes_written":1}`)
		case strings.HasPrefix(r.URL.Path, "/v1/vlbackup/jobs/"):
			// Poll of an already-terminal transfer/migrate job.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"job_id":"`+jobID+`","kind":"transfer","state":"succeeded","started_at":"2024-01-15T08:00:00Z","transfer":{"transferred":[],"skipped":[],"errors":[]},"migrate":{"transferred":[],"skipped":[],"errors":[]}}`)
		default:
			// transfer/migrate start: accept and hand back a job ref.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			_, _ = io.WriteString(w, `{"job_id":"`+jobID+`","status_url":"/v1/vlbackup/jobs/`+jobID+`"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVersion(t *testing.T) {
	if v := (cli{}).Version(); !strings.Contains(v, "vlbackupctl") {
		t.Fatalf("version: %q", v)
	}
}

func TestRunDispatch(t *testing.T) {
	srv := okServer(t)

	cases := [][]string{
		{"snapshot", "--from", "now-1d/d", "--dest-url", "gs://b/x"},
		{"restore", "--partition-prefix", "20240115", "--source-url", "gs://b/x"},
		{"transfer", "--from", "now-7d/d", "--target-url", "http://t:8080"},
		{"migrate", "--from", "now-7d/d",
			"--target-vlbackup-url", "http://t:8080",
			"--target-vlinsert-url", "http://t:9428",
			"--target-vlselect-url", "http://t:9428"},
	}
	for _, argv := range cases {
		t.Run(argv[0], func(t *testing.T) {
			setArgs(t, append([]string{"--url", srv.URL}, argv...)...)
			var err error
			muteStdout(t, func() { err = run() })
			if err != nil {
				t.Fatalf("run: %v", err)
			}
		})
	}
}

func TestRunInvalidOutput(t *testing.T) {
	setArgs(t, "-o", "xml", "snapshot", "--from", "now", "--dest-url", "x")
	if err := run(); err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Fatalf("err: %v", err)
	}
}

// TestMainSuccess drives main() on a happy path so it returns without calling
// os.Exit (which would abort the test binary).
func TestMainSuccess(t *testing.T) {
	srv := okServer(t)
	setArgs(t, "--url", srv.URL, "snapshot", "--from", "now-1d/d", "--dest-url", "gs://b/x")
	muteStdout(t, main)
}

func TestRunNoSubcommand(t *testing.T) {
	setArgs(t)
	var err error
	muteStdout(t, func() { err = run() })
	if err == nil || !strings.Contains(err.Error(), "no subcommand") {
		t.Fatalf("err: %v", err)
	}
}
