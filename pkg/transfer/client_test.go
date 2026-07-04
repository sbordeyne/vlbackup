package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeSnapshotDir builds a fake snapshot directory tree and returns its path.
func makeSnapshotDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for path, contents := range map[string]string{
		"datadb/parts.json":                "[]",
		"datadb/18A0AD752171BFCD/index.bin": "index-data",
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

func TestNewPeerClient(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{name: "valid", baseURL: "http://example.com:8080", wantErr: false},
		{name: "valid https", baseURL: "https://peer.internal", wantErr: false},
		{name: "empty", baseURL: "", wantErr: true},
		{name: "missing scheme", baseURL: "example.com:8080", wantErr: true},
		{name: "missing host", baseURL: "http://", wantErr: true},
		{name: "unparseable", baseURL: "http://[::1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewPeerClient(tt.baseURL, "")
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewPeerClient(%q) err = nil, want error", tt.baseURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewPeerClient(%q) err = %v, want nil", tt.baseURL, err)
			}
			if c == nil || c.http == nil {
				t.Errorf("NewPeerClient(%q) returned nil client or http", tt.baseURL)
			}
		})
	}
}

func TestNewRequest(t *testing.T) {
	t.Run("with auth key", func(t *testing.T) {
		c, err := NewPeerClient("http://example.com", "secret")
		if err != nil {
			t.Fatal(err)
		}
		req, err := c.newRequest(context.Background(), RECEIVE_PATH, "20240101", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer secret")
		}
		if got := req.URL.Query().Get("partition"); got != "20240101" {
			t.Errorf("partition = %q, want %q", got, "20240101")
		}
		if !strings.HasSuffix(req.URL.Path, RECEIVE_PATH) {
			t.Errorf("path = %q, want suffix %q", req.URL.Path, RECEIVE_PATH)
		}
		if req.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", req.Method)
		}
	})

	t.Run("without auth key", func(t *testing.T) {
		c, err := NewPeerClient("http://example.com", "")
		if err != nil {
			t.Fatal(err)
		}
		req, err := c.newRequest(context.Background(), ATTACH_PATH, "20240101", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
	})
}

func TestPeerError(t *testing.T) {
	resp := &http.Response{
		Status: "500 Internal Server Error",
		Body:   io.NopCloser(strings.NewReader("boom")),
	}
	err := peerError("test op", resp)
	msg := err.Error()
	for _, want := range []string{"test op", "500 Internal Server Error", "boom"} {
		if !strings.Contains(msg, want) {
			t.Errorf("peerError message %q missing %q", msg, want)
		}
	}
}

func TestSendPartition(t *testing.T) {
	snapshotDir := makeSnapshotDir(t)

	t.Run("success", func(t *testing.T) {
		var gotAuth, gotPartition, gotContentType string
		var gotBody int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotPartition = r.URL.Query().Get("partition")
			gotContentType = r.Header.Get("Content-Type")
			n, _ := io.Copy(io.Discard, r.Body)
			gotBody = int(n)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"bytes_written": 42}`))
		}))
		t.Cleanup(srv.Close)

		c, err := NewPeerClient(srv.URL, "tok")
		if err != nil {
			t.Fatal(err)
		}
		n, err := c.SendPartition(context.Background(), "20240101", snapshotDir)
		if err != nil {
			t.Fatalf("SendPartition err = %v", err)
		}
		if n != 42 {
			t.Errorf("bytes_written = %d, want 42", n)
		}
		if gotAuth != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok")
		}
		if gotPartition != "20240101" {
			t.Errorf("partition = %q, want 20240101", gotPartition)
		}
		if gotContentType != "application/gzip" {
			t.Errorf("Content-Type = %q, want application/gzip", gotContentType)
		}
		if gotBody == 0 {
			t.Error("server received empty body, want gzip stream")
		}
	})

	t.Run("conflict", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusConflict)
		}))
		t.Cleanup(srv.Close)

		c, _ := NewPeerClient(srv.URL, "")
		_, err := c.SendPartition(context.Background(), "20240101", snapshotDir)
		if !errors.Is(err, ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			http.Error(w, "kaboom", http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)

		c, _ := NewPeerClient(srv.URL, "")
		_, err := c.SendPartition(context.Background(), "20240101", snapshotDir)
		if err == nil {
			t.Fatal("err = nil, want error")
		}
		if !strings.Contains(err.Error(), "transfer receive failed") {
			t.Errorf("err = %v, want transfer receive failure", err)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("not json"))
		}))
		t.Cleanup(srv.Close)

		c, _ := NewPeerClient(srv.URL, "")
		_, err := c.SendPartition(context.Background(), "20240101", snapshotDir)
		if err == nil || !strings.Contains(err.Error(), "decode receive response") {
			t.Errorf("err = %v, want decode error", err)
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		c, _ := NewPeerClient("http://127.0.0.1:1", "")
		_, err := c.SendPartition(context.Background(), "20240101", snapshotDir)
		if err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}

func TestAttach(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotPartition string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPartition = r.URL.Query().Get("partition")
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		c, _ := NewPeerClient(srv.URL, "")
		if err := c.Attach(context.Background(), "20240101"); err != nil {
			t.Fatalf("Attach err = %v", err)
		}
		if gotPartition != "20240101" {
			t.Errorf("partition = %q, want 20240101", gotPartition)
		}
	})

	t.Run("error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)

		c, _ := NewPeerClient(srv.URL, "")
		err := c.Attach(context.Background(), "20240101")
		if err == nil || !strings.Contains(err.Error(), "transfer attach failed") {
			t.Errorf("err = %v, want attach failure", err)
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		c, _ := NewPeerClient("http://127.0.0.1:1", "")
		if err := c.Attach(context.Background(), "20240101"); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}
