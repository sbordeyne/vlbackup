package commands

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sbordeyne/vlbackup/pkg/client"
)

// newClient spins up a test HTTP server whose handler is h and returns a client
// pointed at it, plus the server so the caller can close it early if needed.
func newClient(t *testing.T, h http.HandlerFunc) (*client.ClientWithResponses, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := client.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}
	return c, srv
}

// deadClient returns a client pointed at a server that is already closed, so
// every request fails at the transport layer.
func deadClient(t *testing.T) *client.ClientWithResponses {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c, err := client.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}
	return c
}

// capture runs f while capturing everything written to os.Stdout.
func capture(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	f()
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

func TestTimeRange(t *testing.T) {
	tr := timeRange("now-1d/d", "")
	if tr.From != "now-1d/d" || tr.To != nil {
		t.Fatalf("empty to: got %+v", tr)
	}
	tr = timeRange("now-7d/d", "now/d")
	if tr.To == nil || *tr.To != "now/d" {
		t.Fatalf("set to: got %+v", tr)
	}
}

func TestApiError(t *testing.T) {
	msg := "boom"
	code := int32(400)
	if err := apiError(400, &client.ErrorResponse{Error: &msg, Code: &code}, nil); !strings.Contains(err.Error(), "boom") {
		t.Fatalf("structured: %v", err)
	}
	if err := apiError(500, nil, []byte("raw body")); !strings.Contains(err.Error(), "raw body") {
		t.Fatalf("raw: %v", err)
	}
	if err := apiError(500, nil, nil); !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("status only: %v", err)
	}
	// ErrorResponse present but with nil Error message falls back to body.
	if err := apiError(400, &client.ErrorResponse{}, []byte("fallback")); !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("nil message fallback: %v", err)
	}
}

func TestEmitJSONError(t *testing.T) {
	// A channel cannot be marshalled to JSON, exercising the error branch.
	err := emit(Options{Output: "json"}, make(chan int), func() {})
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestSnapshotRun(t *testing.T) {
	cmd := &SnapshotCmd{From: "now-1d/d", DestUrl: "gs://b/x"}
	ctx := context.Background()

	t.Run("accepted text", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{Output: "text"}) })
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(out, "accepted") {
			t.Fatalf("out: %q", out)
		}
	})

	t.Run("accepted json", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{Output: "json"}) })
		if err != nil || !strings.Contains(out, "\"status\"") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("bad request", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 400, `{"error":"bad scheme","code":400}`)
		})
		if err := cmd.Run(ctx, c, Options{}); err == nil || !strings.Contains(err.Error(), "bad scheme") {
			t.Fatalf("err: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 500, `{"error":"kaboom","code":500}`)
		})
		if err := cmd.Run(ctx, c, Options{}); err == nil || !strings.Contains(err.Error(), "kaboom") {
			t.Fatalf("err: %v", err)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		if err := cmd.Run(ctx, deadClient(t), Options{}); err == nil {
			t.Fatal("expected transport error")
		}
	})
}

func TestTransferRun(t *testing.T) {
	cmd := &TransferCmd{From: "now-7d/d", TargetUrl: "http://t:8080"}
	ctx := context.Background()

	t.Run("ok text", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, `{"transferred":["20240113"],"skipped":["20240112"],"errors":[]}`)
		})
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{Output: "text"}) })
		if err != nil || !strings.Contains(out, "20240113") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("ok json", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, `{"transferred":[],"skipped":[],"errors":[]}`)
		})
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{Output: "json"}) })
		if err != nil || !strings.Contains(out, "transferred") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("bad request", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 400, `{"error":"bad range"}`)
		})
		if err := cmd.Run(ctx, c, Options{}); err == nil || !strings.Contains(err.Error(), "bad range") {
			t.Fatalf("err: %v", err)
		}
	})

	t.Run("errors with body", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 500, `{"transferred":[],"skipped":[],"errors":["20240112: boom"]}`)
		})
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{}) })
		if err == nil || !strings.Contains(err.Error(), "errors") {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(out, "boom") {
			t.Fatalf("out: %q", out)
		}
	})

	t.Run("errors nil body", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
		if err := cmd.Run(ctx, c, Options{}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		if err := cmd.Run(ctx, deadClient(t), Options{}); err == nil {
			t.Fatal("expected transport error")
		}
	})
}

func TestRestoreRun(t *testing.T) {
	cmd := &RestoreCmd{PartitionPrefix: "20240115", SourceUrl: "gs://b/x"}
	ctx := context.Background()

	t.Run("accepted text", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 202, `{"partition":["20240115"],"bytes_written":4194304}`)
		})
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{Output: "text"}) })
		if err != nil || !strings.Contains(out, "20240115") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("accepted json", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 202, `{"partition":["20240115"],"bytes_written":1}`)
		})
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{Output: "json"}) })
		if err != nil || !strings.Contains(out, "bytes_written") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	cases := []struct {
		name string
		code int
		want string
	}{
		{"bad request", 400, "bad url"},
		{"not found", 404, "missing"},
		{"conflict", 409, "exists"},
		{"server error", 500, "internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tc.code, `{"error":"`+tc.want+`"}`)
			})
			if err := cmd.Run(ctx, c, Options{}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err: %v", err)
			}
		})
	}

	t.Run("transport error", func(t *testing.T) {
		if err := cmd.Run(ctx, deadClient(t), Options{}); err == nil {
			t.Fatal("expected transport error")
		}
	})
}

func TestMigrateRun(t *testing.T) {
	ctx := context.Background()
	authKey := "secret"
	cmd := &MigrateCmd{
		From:              "now-7d/d",
		TargetVlbackupUrl: "http://t:8080",
		TargetVlinsertUrl: "http://t:9428",
		TargetVlselectUrl: "http://t:9428",
		TargetVlAuthKey:   authKey,
	}

	t.Run("ok text with recent and authkey", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), authKey) {
				t.Errorf("auth key not sent: %s", body)
			}
			writeJSON(w, 200, `{"transferred":["20240113"],"skipped":[],"errors":[],"recent":{"partition":"20240115","bytes_ingested":4194304,"source_count":1024,"target_count":1024,"verified":true}}`)
		})
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{Output: "text"}) })
		if err != nil || !strings.Contains(out, "verified=true") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("ok json no authkey", func(t *testing.T) {
		bare := &MigrateCmd{From: "now-1d/d", TargetVlbackupUrl: "u", TargetVlinsertUrl: "u", TargetVlselectUrl: "u"}
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, `{"transferred":[],"skipped":[],"errors":[]}`)
		})
		var err error
		out := capture(t, func() { err = bare.Run(ctx, c, Options{Output: "json"}) })
		if err != nil || !strings.Contains(out, "transferred") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("bad request", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 400, `{"error":"bad target"}`)
		})
		if err := cmd.Run(ctx, c, Options{}); err == nil || !strings.Contains(err.Error(), "bad target") {
			t.Fatalf("err: %v", err)
		}
	})

	t.Run("errors with body", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 500, `{"transferred":[],"skipped":[],"errors":["recent phase failed"]}`)
		})
		var err error
		out := capture(t, func() { err = cmd.Run(ctx, c, Options{}) })
		if err == nil || !strings.Contains(out, "recent phase failed") {
			t.Fatalf("err=%v out=%q", err, out)
		}
	})

	t.Run("errors nil body", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
		if err := cmd.Run(ctx, c, Options{}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		if err := cmd.Run(ctx, deadClient(t), Options{}); err == nil {
			t.Fatal("expected transport error")
		}
	})
}

// TestPrintNil guards the nil-guards in the text printers.
func TestPrintNil(t *testing.T) {
	printTransfer(nil)
	printMigrate(nil)
}
