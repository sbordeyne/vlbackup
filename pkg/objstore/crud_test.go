package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/sbordeyne/vlbackup/pkg/objstore"
)

// testCRUDRoundTrip exercises the full Repository contract against a live
// backend: Upload → List → Download → Delete → Download(ErrNotFound).
func testCRUDRoundTrip(t *testing.T, repo objstore.Repository) {
	t.Helper()
	ctx := context.Background()
	const key = "prefix/roundtrip.tar.gz"
	content := []byte("vlbackup objstore round-trip payload")

	if err := repo.Upload(ctx, key, bytes.NewReader(content)); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	var listed []objstore.ObjectInfo
	for info, err := range repo.List(ctx, "prefix/") {
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		listed = append(listed, info)
	}
	if len(listed) != 1 {
		t.Fatalf("List returned %d objects, want 1: %v", len(listed), listed)
	}
	if listed[0].Key != key {
		t.Errorf("List key = %q, want %q", listed[0].Key, key)
	}
	if listed[0].Size != int64(len(content)) {
		t.Errorf("List size = %d, want %d", listed[0].Size, len(content))
	}

	r, err := repo.Download(ctx, key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("reading download: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Download content = %q, want %q", got, content)
	}

	if err := repo.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.Download(ctx, key); !errors.Is(err, objstore.ErrNotFound) {
		t.Errorf("Download after Delete: got %v, want ErrNotFound", err)
	}
}
