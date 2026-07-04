package victoriametrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// vmServer returns an httptest server that replies to every request with the
// given status and body, plus a Client pointed at it.
func vmServer(t *testing.T, status int, body string) *victoriametrics.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := victoriametrics.NewClient(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &c
}

// vmDeadClient points at a closed port so every request errors.
func vmDeadClient(t *testing.T) *victoriametrics.Client {
	t.Helper()
	c, err := victoriametrics.NewClient(context.Background(), "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestNewClientBadURL(t *testing.T) {
	if _, err := victoriametrics.NewClient(context.Background(), "http://[::1"); err == nil {
		t.Error("NewClient err = nil, want parse error")
	}
}

// TestRequestBuildErrors covers the request-construction error branch of the
// methods that use NewRequestWithContext: a nil context makes it fail before
// any network call.
func TestRequestBuildErrors(t *testing.T) {
	//nolint:staticcheck // deliberately passing a nil context to force the error path
	c, err := victoriametrics.NewClient(nil, "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSnapshot("p", ""); err == nil {
		t.Error("DeleteSnapshot err = nil, want request-build error")
	}
	if err := c.DeleteStaleSnapshots(""); err == nil {
		t.Error("DeleteStaleSnapshots err = nil, want request-build error")
	}
	if err := c.DetachPartition("20240101", ""); err == nil {
		t.Error("DetachPartition err = nil, want request-build error")
	}
	if err := c.AttachPartition("20240101", ""); err == nil {
		t.Error("AttachPartition err = nil, want request-build error")
	}
}

func TestCreateSnapshotUnit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c := vmServer(t, http.StatusOK, `["/data/partitions/20240101/snapshots/x"]`)
		paths, err := c.CreateSnapshot("20240101", "key")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(paths) != 1 {
			t.Errorf("paths = %v, want 1 element", paths)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		c := vmServer(t, http.StatusInternalServerError, "boom")
		if _, err := c.CreateSnapshot("", ""); err == nil || !strings.Contains(err.Error(), "failed to create snapshot") {
			t.Errorf("err = %v, want create-snapshot failure", err)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		c := vmServer(t, http.StatusOK, "not json")
		if _, err := c.CreateSnapshot("", ""); err == nil {
			t.Error("err = nil, want decode error")
		}
	})
	t.Run("conn error", func(t *testing.T) {
		if _, err := vmDeadClient(t).CreateSnapshot("", ""); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}

func TestDeleteSnapshotUnit(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		if err := vmServer(t, http.StatusNoContent, "").DeleteSnapshot("p", "key"); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		if err := vmServer(t, http.StatusInternalServerError, "x").DeleteSnapshot("p", ""); err == nil {
			t.Error("err = nil, want failure")
		}
	})
	t.Run("conn error", func(t *testing.T) {
		if err := vmDeadClient(t).DeleteSnapshot("p", ""); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}

func TestDeleteStaleSnapshotsUnit(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		if err := vmServer(t, http.StatusOK, "").DeleteStaleSnapshots("key"); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		if err := vmServer(t, http.StatusInternalServerError, "x").DeleteStaleSnapshots(""); err == nil {
			t.Error("err = nil, want failure")
		}
	})
	t.Run("conn error", func(t *testing.T) {
		if err := vmDeadClient(t).DeleteStaleSnapshots(""); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}

func TestDetachPartitionUnit(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		if err := vmServer(t, http.StatusOK, "").DetachPartition("20240101", "key"); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		if err := vmServer(t, http.StatusInternalServerError, "x").DetachPartition("20240101", ""); err == nil {
			t.Error("err = nil, want failure")
		}
	})
	t.Run("conn error", func(t *testing.T) {
		if err := vmDeadClient(t).DetachPartition("20240101", ""); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}

func TestAttachPartitionUnit(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		if err := vmServer(t, http.StatusOK, "").AttachPartition("20240101", "key"); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		if err := vmServer(t, http.StatusInternalServerError, "x").AttachPartition("20240101", ""); err == nil {
			t.Error("err = nil, want failure")
		}
	})
	t.Run("conn error", func(t *testing.T) {
		if err := vmDeadClient(t).AttachPartition("20240101", ""); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}

func TestListPartitionsUnit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		parts, err := vmServer(t, http.StatusOK, `["20240101","20240102"]`).ListPartitions("key")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(parts) != 2 {
			t.Errorf("parts = %v, want 2", parts)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		if _, err := vmServer(t, http.StatusInternalServerError, "x").ListPartitions(""); err == nil {
			t.Error("err = nil, want failure")
		}
	})
	t.Run("bad json", func(t *testing.T) {
		if _, err := vmServer(t, http.StatusOK, "nope").ListPartitions(""); err == nil {
			t.Error("err = nil, want decode error")
		}
	})
	t.Run("conn error", func(t *testing.T) {
		if _, err := vmDeadClient(t).ListPartitions(""); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}

func TestListSnapshotsUnit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		snaps, err := vmServer(t, http.StatusOK, `["/p/a","/p/b"]`).ListSnapshots("key")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(snaps) != 2 {
			t.Errorf("snaps = %v, want 2", snaps)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		if _, err := vmServer(t, http.StatusInternalServerError, "x").ListSnapshots(""); err == nil {
			t.Error("err = nil, want failure")
		}
	})
	t.Run("bad json", func(t *testing.T) {
		if _, err := vmServer(t, http.StatusOK, "nope").ListSnapshots(""); err == nil {
			t.Error("err = nil, want decode error")
		}
	})
	t.Run("conn error", func(t *testing.T) {
		if _, err := vmDeadClient(t).ListSnapshots(""); err == nil {
			t.Error("err = nil, want connection error")
		}
	})
}
