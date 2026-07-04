package objstore

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewGCSRepositoryClientError forces storage.NewClient to fail by pointing
// Application Default Credentials at a nonexistent file (and disabling the
// emulator fallback), covering the client-creation error branch.
func TestNewGCSRepositoryClientError(t *testing.T) {
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "no-such-creds.json"))
	u, _ := url.Parse("gs://bucket")
	if _, err := newGCSRepository(context.Background(), u); err == nil ||
		!strings.Contains(err.Error(), "creating GCS client") {
		t.Errorf("err = %v, want GCS client creation error", err)
	}
}
